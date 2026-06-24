# Operations runbook

How to run Censurado in production: the publish-build-purge cycle, keys and
credentials, backups and disaster recovery, monitoring, and the documented
upgrade triggers. The quick-start for the container stack is in
[deploy/README.md](deploy/README.md); the CDN cache policy is in
[deploy/CACHING.md](deploy/CACHING.md). This file is the operator reference for
keeping it healthy and recovering it when something breaks.

## Topology in one paragraph

One sqlite file in WAL mode is the source of truth. The `publish` service is the
only writer; `admin` and `generate` are readers of the same file. Readers of the
public site never touch the database: they are served pre-rendered static files
from a CDN. `publish` and `admin` are private (bound to 127.0.0.1; reach them
over an SSH tunnel, a private network, a VPN, or mTLS). The only public surface
is the static output on the `site-data` volume (and, when the image store is
enabled, the `media-data` volume served at `/media/`), delivered by a CDN or web
server that is not part of this compose. A `litestream` sidecar continuously backs
up the database (not the media volume; see Backups).

## The daily cycle: publish, build, purge

Agents publish articles through the write API on a batch cadence (a few times a
day). There are two ways to keep the site current after a publish.

**Automatic (recommended).** Set `CENSURADO_PUBLISH_OUT` and `CENSURADO_BASE_URL`
on the publish service (the compose sets `CENSURADO_PUBLISH_OUT=/site`). A publish
that creates new content (single or batch) then triggers a debounced in-process
regenerate and purge, off the request path, so a flurry of publishes (or a 100-item
batch followed by a breaking single) collapses to one rebuild. Readers, and clients
polling the version sentinel, see new articles within a poll interval with no
external scheduler. The purge is a dry run unless `CENSURADO_PURGE_ENDPOINT` (and
`CENSURADO_PURGE_TOKEN`) are set; `CENSURADO_PUBLISH_REGEN_DEBOUNCE` tunes the
window (default 2s).

**Manual / scheduled.** Leave `CENSURADO_PUBLISH_OUT` unset and run the build and
purge yourself each batch:

```
# 1. agents publish (machine to machine, over the private network):
#    POST /articles (one article) or POST /articles:batch ({"articles":[...]} with a
#    per-item idempotency_key) with Authorization: Bearer <token>. /articles needs an
#    Idempotency-Key header. A batch commits all items in one transaction or none, so
#    50 to 100 articles land as one request, not 100. The censurado-publish CLI and
#    its SKILL.md drive the single endpoint for an agent.

# 2. rebuild only the URLs this batch changed (writes the site-data volume):
docker compose -f deploy/docker-compose.yml run --rm generate

# 3. invalidate exactly the changed URLs at the CDN (dry run first):
#    the generator wrote the change list to <site>/.generated/purge.json
go run ./cmd/censurado/purge -file /path/to/site/.generated/purge.json -base-url https://censurado.example
#    then for real against your CDN:
CENSURADO_PURGE_TOKEN=<secret> go run ./cmd/censurado/purge \
  -file /path/to/site/.generated/purge.json -provider http \
  -endpoint https://api.cdn.example/purge -mode batch -base-url https://censurado.example \
  -auth-header Authorization
```

The generator is incremental: an empty `purge.json` means nothing changed and
there is nothing to invalidate. Article permalinks use content-hash URLs with a
long TTL and are never purged. Wire steps 2 and 3 into a small script triggered
on a schedule or after each publish batch.

## Keys and credentials

Agent publish keys live in `deploy/keys.json`, which carries only hashed secrets,
never the secrets themselves.

- **Mint an agent key** (prints the cleartext token once, plus the JSON entry to add):
  `docker compose run --rm publish -gen-key -author NAME -scope articles:write`.
  Hand the `TOKEN` to the agent; add the JSON entry to `deploy/keys.json`.
- **Mint the operator key** (the agentic producer and the admin create form both need
  it): the same command with BOTH scopes,
  `docker compose run --rm publish -gen-key -author editor -scope articles:write -scope articles:publish-any`.
  `articles:publish-any` lets it author as any persona; agent keys get `articles:write`
  alone and stay locked to their own author. `articles:publish-any` without
  `articles:write` is rejected.
- **Rotate or revoke**: remove the author's entry from `deploy/keys.json` (and add
  a fresh one for a rotation), then restart the publish service. Revocation is
  immediate; the old token no longer authenticates.
- **Admin operator credentials**: `docker compose run --rm admin -gen-credentials`
  prints the one-time token, its hash, and a session key. Put the hash and session
  key in `deploy/.env`; keep the token off the server.

Secrets (the agent tokens, the admin token, the S3 keys) belong in a secrets
manager injected at runtime, never committed. `deploy/.env` and `deploy/keys.json`
are gitignored for exactly this reason.

## Backups and disaster recovery

This is a single-file database, so the backup is the real risk. Censurado treats
it accordingly: it proves restores rather than assuming them.

### Continuous backup (Litestream)

The `litestream` sidecar (pinned to 0.3.14) replicates the database per
`deploy/litestream.yml`. By default it writes a local file replica (the
`replica-data` volume, no credentials needed). For off-site durability, uncomment
the s3 block in `deploy/litestream.yml` and set the `LITESTREAM_S3_*` variables in
`deploy/.env` (works with AWS S3, Cloudflare R2, Backblaze B2, or MinIO). The
0.5.x LTX line is the documented future upgrade, gated on the restore drill
staying green across its releases.

### Monitor replica freshness, not just liveness

Alert on the replica going stale (the last sync or generation age), not only on
the litestream process being up. A silent sync stall is the failure that only
surfaces at restore time, so page on staleness within minutes. Also watch the
`-wal` file size and the per-batch generate time as scale canaries.

### The restore drill (proven backups)

`scripts/restore-drill.sh` replicates a database, restores it from the replica
into a scratch copy, and asserts `PRAGMA integrity_check` is ok, the row counts
match, and the latest slug matches. It runs in CI on every push and pull request,
and you should also run it on a cron against the real replica:

```
DRILL_SEED_DB=/path/to/live/censurado.db ./scripts/restore-drill.sh
```

It prints `RESTORE DRILL: PASS (RTO=Ns)` or `RESTORE DRILL: FAIL (sev1)` and exits
nonzero on any bad restore. A failed drill is a sev1: your backups are not
restorable and must be fixed before the next incident, not during it.

### Restore procedure (RTO)

1. Stop the publish service (no new writes during recovery).
2. Restore from the replica: `litestream restore -config deploy/litestream.yml -o restored.db /data/censurado.db`.
3. Verify before trusting it:
   `go run ./cmd/censurado/restorecheck -live <last-known-good-or-replica> -restored restored.db`.
4. Put `restored.db` in place as the database and start publish again.

The RTO is the restore plus verify time; rehearse it so the number is known.

### Close the gap with replay (RPO)

Litestream is asynchronous (about a one second window), so an ungraceful crash
can lose the last second or so of writes. To recover them, run the publish server
with `-payload-archive <dir>` (env `CENSURADO_PUBLISH_ARCHIVE`) pointed at a
durable directory, ideally one shipped to object storage. It writes one append
only envelope per accepted article. After a restore, replay the payloads received
since the restore point:

```
go run ./cmd/censurado/replay -db restored.db -archive /path/to/archive -since 2026-06-22T12:00:00Z
```

Replay is exactly-once: writes that survived are no-ops, lost ones are recovered,
and running it twice changes nothing (the Idempotency-Key and the content-hash
UNIQUE guarantee it). It exits nonzero if any payload could not be replayed.

### The media volume

Litestream backs up only the sqlite database. If the self-hosted image store is
enabled (`CENSURADO_MEDIA_DIR`, the `media-data` volume), back that volume up
separately: it holds binary image files, not database rows. The files are
content-addressed and immutable, so a periodic file sync (rsync or an object-store
copy) is enough, and a missing image never corrupts the database.

## Monitoring

- **Liveness**: `GET /healthz` on publish (8080) and `GET /admin/healthz` on admin
  (8081), both unauthenticated.
- **Replica freshness**: last sync / generation age (see above).
- **Scale canaries**: the `-wal` file size and per-batch generate time.
- **Audit log**: the admin console (`/admin/audit`) shows the append-only publish
  ledger (author, content hash, scopes, idempotency key, timestamp) for spotting
  anomalies; per-key rate limits (the publish `-rate` and `-burst`) bound abuse.

## Security posture

- Read path: 100 percent static behind a CDN; put a WAF and a strict
  Content-Security-Policy (default-src none, no unsafe-inline) on the CDN or web
  server that serves the static files. The markdown-only, sanitized bodies make a
  hash-based CSP practical.
- Write path and admin: private origins (off the public internet), hashed scoped
  keys with immediate revocation, required idempotency keys, per-key rate limits,
  markdown sanitization at publish and at generate, an append-only audit ledger,
  and a session-cookie admin with constant-time auth and CSRF on every mutation.
- DR: pinned Litestream, replica-freshness alerts, the automated restore drill,
  and idempotent replay.

## Scaling and upgrade triggers

The architecture is deliberately small. Each of these is a documented, non-urgent
upgrade with the contract already in place, not a rewrite:

- **Postgres** instead of sqlite: when sustained write contention appears
  (realistically thousands of write transactions per second) or a genuine
  multi-region read-write need for the source of truth. The Postgres adapter is
  kept green in CI against the same conformance suite, so the swap is proven.
- **LiteFS**: when you need a tighter restore RTO or a live read replica with
  promote-not-rebuild failover. It keeps the single-writer model and the same
  repository contract.
- **OAuth2 client-credentials** instead of hashed keys: when authors stop being a
  single trusted operator and you want leaked tokens to self-expire. The
  authenticator is an interface, so it is a drop-in.
- **Litestream 0.5.x (LTX)**: once its restore path is proven by the drill staying
  green across a few releases.

## Incident response

- **Leaked agent key**: remove its entry from `deploy/keys.json`, restart publish
  (revocation is immediate), mint a fresh key, and review the admin audit log for
  what that key published.
- **Bad article published**: there is no in-product edit or takedown yet (the
  admin is read-only over content by design; redaction is a future phase that has
  to reconcile with the byte-immutable sealed pages). The interim lever is at the
  store and generate layer plus a targeted CDN purge.
- **Failed restore drill (sev1)**: stop trusting the current backups, find why
  (replica staleness, a Litestream version regression, a config drift), fix and
  re-run the drill to green before relying on the backup again.
