# CDN caching and purge

The static site is built to be cached hard at the edge and corrected by an exact
purge after each generate batch. This is architecture section 7. Two URL classes,
two policies:

- Article permalinks are content-hash URLs. The hash is part of the path, so a new
  version is a new URL. Cache them with a long TTL (a year, immutable). They are
  never in the purge manifest and this tool never purges them. A changed article
  publishes under a fresh hashed URL; the old one simply ages out.
- Listing pages and JSON shards (`/latest/`, `/topic/ai/`, `/topic/ai/2026/06.json`,
  `/index.json`, and the rest) are mutable. Give them a short edge TTL plus
  `stale-while-revalidate`, so a stale copy keeps serving while the edge refetches
  in the background. If a purge is ever missed, the short TTL self-heals it by the
  next batch instead of pinning stale content.

The generator emits `<out>/.generated/purge.json` after every batch: version, timestamp, and
the exact root-relative paths that changed or were removed. This tool reads that
file and invalidates exactly those URLs, one single-file purge per URL (or one
batch carrying the list), never a wildcard. Purging exactly the changed set keeps
the edge consistent without dumping the whole cache.

Suggested response headers at the origin or CDN:

```
# article permalinks (content-hash URLs)
Cache-Control: public, max-age=31536000, immutable

# listings and JSON shards
Cache-Control: public, max-age=60, stale-while-revalidate=86400
```

## Usage

Run a generate batch, then purge exactly what it changed:

```
go run ./cmd/censurado/generate -out ./public
go run ./cmd/censurado/purge -provider http \
  -file ./public/.generated/purge.json \
  -endpoint "$CENSURADO_PURGE_ENDPOINT" \
  -base-url "$CENSURADO_BASE_URL"
```

The generator writes the manifest under `<out>/.generated/` (its state dir), so the
purge `-file` default is `./public/.generated/purge.json`. When the generate batch
runs in Docker (`docker compose run --rm generate`, which writes `-out /site`), the
manifest lands at `/site/.generated/purge.json` on the site-data volume; point
`-file` (or `CENSURADO_PURGE_FILE`) there.

The default provider is `dryrun`, which lists the URLs it would purge and makes no
network calls. Drop `-provider http` to preview a batch safely. The auth secret is
read only from the `CENSURADO_PURGE_TOKEN` environment variable (never a flag, so it
stays out of shell history and `ps`) and is never printed. An empty manifest is a
successful no-op, and the tool exits nonzero if any purge fails so a deploy step can
gate on it.
