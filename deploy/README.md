# Censurado web: generating the static site

This repo is the static-site GENERATOR. It reads the published corpus from the
backend's sqlite database (the `censurado-web-backend` repo owns the publish API,
the store, and the database) and renders the complete static site: HTML pages, JSON
shards, feeds, sitemaps, and a purge manifest. The public reading path is fully
static, so nothing here runs at read time; the generator runs at build/publish time
and the output is served by a CDN.

## How it reads the data

The generator imports the backend module's shared libraries (`domain`, `store`,
`content`, `media`) through a `replace` directive in `go.mod`, so both repos must be
checked out side by side. It opens the same sqlite file the backend's publish
service writes (WAL mode lets the generator read while publish keeps writing).

## Run it (dockerized toolchain, both repos mounted)

There is no host Go toolchain; the pinned `golang:1.26.4-trixie` container is used
for everything. The container mounts BOTH repos so the `replace` resolves:

```sh
IMG=golang:1.26.4-trixie
docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp \
  -e GOCACHE=/work/censurado-web/.gocache -e GOMODCACHE=/work/censurado-web/.gomodcache \
  -v "$PWD/../censurado-web":/work/censurado-web \
  -v "$PWD/../censurado-web-backend":/work/censurado-web-backend \
  -w /work/censurado-web $IMG \
  go run ./cmd/censurado/generate -db /path/to/censurado.db -out ./public -base-url https://your.site
```

`-db` (or `CENSURADO_DB`) is the backend's database file; `-base-url` (or
`CENSURADO_BASE_URL`) is the absolute site origin. The run prints a one-line summary
(`written=... scopes=...`) and writes the site into `-out`.

## Keep it fresh: -watch

`-watch` keeps the generator resident and re-runs the same idempotent generation on
a fixed interval (default 2s), then invalidates exactly the URLs that changed at the
CDN. The backend writes the database; this generator turns the database into the
static site and purges the CDN.

```sh
# ... same docker run as above, but append:
  -watch -purge-endpoint "$CENSURADO_PURGE_ENDPOINT"   # purge-endpoint optional; omit for a dry-run purge
```

With no `-purge-endpoint` the purge is a dry run (no network). The purge secret is
read only from `CENSURADO_PURGE_TOKEN`, never a flag.

## Deploy

Point a CDN at the generated `-out` directory (and at the backend's media volume for
`/media/`). For Cloudflare, upload the output with `wrangler pages deploy ./public`.
The generator's purge manifest (`./public/.generated/purge.json`) lists exactly the
URLs to invalidate after each rebuild.
