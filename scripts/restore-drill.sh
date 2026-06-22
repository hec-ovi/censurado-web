#!/usr/bin/env bash
#
# restore-drill.sh -- the automated Litestream restore drill for censurado-web.
#
# This is the "proven backups" gate. It does not check that backups exist; it
# proves that a RESTORED database is actually good, end to end:
#
#   1. Seed a fresh WAL-mode source DB (or use a provided copy of a real one).
#   2. Replicate it with Litestream 0.3.14 to a local FILE replica (a daemon),
#      and wait for the initial snapshot to land.
#   3. Make a post-snapshot write so WAL replication is exercised, and wait for
#      the new frames to reach the replica.
#   4. Restore from the replica into a scratch DB, timing it (the RTO).
#   5. Run restorecheck to assert integrity + row counts + latest slug match.
#
# It prints "RESTORE DRILL: PASS (RTO=Ns)" and exits 0 only when every check
# passes. On any failure it prints "RESTORE DRILL: FAIL (sev1)" and exits nonzero,
# which turns the CI job red.
#
# Runs the same locally (Docker only) and in CI. Litestream always runs via the
# digest-pinned image. restorecheck is built once: natively when `go` is on PATH
# (CI sets up Go), otherwise inside the golang image (Docker-only hosts). The file
# replica needs no credentials, so nothing secret is involved.
#
# Environment overrides:
#   LITESTREAM_IMAGE   digest-pinned litestream image (default below)
#   LITESTREAM_CONFIG  config file to drive replicate/restore (default deploy/litestream.yml)
#   RESTORECHECK_BIN   prebuilt restorecheck binary to use instead of building
#   GO_IMAGE           golang image for the Docker-only build (default golang:1)
#   DRILL_SEED_DB      use a COPY of this existing DB as the source instead of
#                      seeding (point the drill at a real production DB safely;
#                      the file itself is never written, only copied)
#   DRILL_APPEND       1 (default) makes the post-snapshot WAL write; 0 skips it
#   DRILL_BREAK        teeth-proof hook, deliberately break the restore to prove
#                      the drill FAILS: "mismatch" (diverge live after restore),
#                      "garbage" (corrupt the scratch DB), "truncate" (empty it)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"

LITESTREAM_IMAGE="${LITESTREAM_IMAGE:-litestream/litestream:0.3.14@sha256:c5a1e1b01916b3a110f6600820ef176d048d9b9c2411ef0a680de0caa68934a5}"
LITESTREAM_CONFIG="${LITESTREAM_CONFIG:-$REPO/deploy/litestream.yml}"
GO_IMAGE="${GO_IMAGE:-golang:1}"
DRILL_APPEND="${DRILL_APPEND:-1}"
DRILL_BREAK="${DRILL_BREAK:-}"

UID_GID="$(id -u):$(id -g)"
CONTAINER="censurado-restore-drill-$$"
WORK="$(mktemp -d)"
mkdir -p "$WORK/data" "$WORK/replica" "$WORK/scratch"

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
	echo "RESTORE DRILL: FAIL (sev1)"
	echo "  reason: $*"
	exit 1
}

log() { echo ">> $*"; }

SRC="$WORK/data/censurado.db"
SCRATCH="$WORK/scratch/restored.db"

# --- build (or locate) the restorecheck binary ------------------------------
if [ -n "${RESTORECHECK_BIN:-}" ]; then
	BIN="$RESTORECHECK_BIN"
	log "using prebuilt restorecheck: $BIN"
elif command -v go >/dev/null 2>&1; then
	log "building restorecheck natively (go on PATH)"
	( cd "$REPO" && CGO_ENABLED=0 go build -o "$WORK/restorecheck" ./cmd/censurado/restorecheck )
	BIN="$WORK/restorecheck"
else
	log "building restorecheck in $GO_IMAGE (no go on PATH)"
	docker run --rm -u "$UID_GID" \
		-e HOME=/tmp -e GOCACHE=/app/.gocache -e GOMODCACHE=/app/.gomodcache -e CGO_ENABLED=0 \
		-v "$REPO":/app -v "$WORK":/work -w /app "$GO_IMAGE" \
		go build -o /work/restorecheck ./cmd/censurado/restorecheck
	BIN="$WORK/restorecheck"
fi

# Litestream invocation helper. Mounts the drill's data/replica/scratch dirs and
# the config read-only. Runs as the host user so no root-owned files are left in
# the bind mounts.
ls_run() {
	docker run --rm -u "$UID_GID" \
		-v "$WORK/data":/data -v "$WORK/replica":/replica -v "$WORK/scratch":/scratch \
		-v "$LITESTREAM_CONFIG":/etc/litestream.yml:ro \
		"$LITESTREAM_IMAGE" "$@"
}

# --- 1. seed (or copy a real DB) --------------------------------------------
if [ -n "${DRILL_SEED_DB:-}" ]; then
	log "using a copy of $DRILL_SEED_DB as the drill source (the original is never written)"
	cp "$DRILL_SEED_DB" "$SRC"
else
	log "seeding a fresh source DB"
	"$BIN" -seed "$SRC"
fi

# --- 2. replicate to the file replica (daemon) ------------------------------
log "starting litestream replicate ($LITESTREAM_IMAGE)"
docker run -d --name "$CONTAINER" -u "$UID_GID" \
	-v "$WORK/data":/data -v "$WORK/replica":/replica \
	-v "$LITESTREAM_CONFIG":/etc/litestream.yml:ro \
	"$LITESTREAM_IMAGE" replicate -config /etc/litestream.yml >/dev/null

log "waiting for the initial snapshot to land in the replica"
snap_ok=0
for _ in $(seq 1 60); do
	if find "$WORK/replica" -name '*.snapshot.lz4' 2>/dev/null | grep -q .; then snap_ok=1; break; fi
	sleep 0.5
done
[ "$snap_ok" = 1 ] || fail "litestream produced no snapshot in 30s; logs:
$(docker logs "$CONTAINER" 2>&1 | tail -n 10)"

# --- 3. post-snapshot WAL write ---------------------------------------------
walstate() { find "$WORK/replica" -path '*/wal/*' -printf '%p %s\n' 2>/dev/null | sort; }
if [ "$DRILL_APPEND" = 1 ]; then
	log "making a post-snapshot write and waiting for WAL replication"
	before="$(walstate)"
	"$BIN" -append "$SRC" >/dev/null
	wal_ok=0
	for _ in $(seq 1 60); do
		if [ "$(walstate)" != "$before" ]; then wal_ok=1; break; fi
		sleep 0.5
	done
	[ "$wal_ok" = 1 ] || fail "the post-snapshot WAL write was not replicated to the file replica in 30s"
	sleep 1 # small margin for the final frames to flush
fi

# --- 4. stop the daemon (replica is now fully synced) -----------------------
docker rm -f "$CONTAINER" >/dev/null

# --- 5. restore from the replica, timing the RTO ----------------------------
log "restoring from the replica into a scratch DB"
rm -f "$WORK/scratch/"* 2>/dev/null || true
start="$(date +%s.%N)"
ls_run restore -config /etc/litestream.yml -o /scratch/restored.db /data/censurado.db
end="$(date +%s.%N)"
RTO="$(awk "BEGIN{printf \"%.1f\", $end - $start}")"
[ -s "$SCRATCH" ] || fail "litestream restore produced no database at $SCRATCH"
log "restore finished in ${RTO}s"

# --- teeth-proof hook: deliberately break the restore -----------------------
case "$DRILL_BREAK" in
	"") ;;
	mismatch)
		log "DRILL_BREAK=mismatch: appending an extra row to the LIVE db AFTER restore so it diverges from the restored copy"
		"$BIN" -append "$SRC" >/dev/null
		;;
	garbage)
		log "DRILL_BREAK=garbage: overwriting the restored scratch DB with non-database bytes"
		printf 'this is not a sqlite database\n' >"$SCRATCH"
		;;
	truncate)
		log "DRILL_BREAK=truncate: emptying the restored scratch DB"
		: >"$SCRATCH"
		;;
	*)
		fail "unknown DRILL_BREAK=$DRILL_BREAK (use mismatch, garbage, or truncate)"
		;;
esac

# --- 6. assert the restore is good ------------------------------------------
echo "=== restorecheck ==="
if "$BIN" -live "$SRC" -restored "$SCRATCH"; then
	echo "RESTORE DRILL: PASS (RTO=${RTO}s)"
	exit 0
fi
fail "restorecheck reported a bad restore (see the report above)"
