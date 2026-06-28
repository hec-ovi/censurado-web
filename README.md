# censurado-web

The static-site generator for Censurado, a news site. It reads the published corpus
from the backend's database and renders the complete static site: immutable HTML
pages, JSON shards for client-side filtering, feeds, sitemaps, and a CDN purge
manifest. The public reading path is fully static, so nothing here runs when a reader
loads the site; the generator runs at build time and a CDN serves the output.

The system is four repos:

- **censurado-web** (this): the generator + the public frontend (templates, CSS, JS).
- **censurado-web-backend**: the publish API, the sqlite store (source of truth for
  articles/authors/topics), the JSON read API, and the operator admin. This repo
  imports its public `domain`, `store`, `content`, and `media` libraries.
- **censurado-web-brain**: the agentic newsroom that writes articles and publishes
  them to the backend.
- **censurado-web-harness**: one Docker Compose that runs all of the above together
  (plus a local LLM and ComfyUI), and carries the CLI publishing skill.

## How it works

`generate.Generate` opens the backend's sqlite database as a reader (the backend's
publish service is the only writer; WAL mode lets them run together), scans the whole
corpus once, and materializes every page across every scope (latest, per section,
author, topic, month, and the section-anchored matrix). It writes only artifacts
whose bytes changed since the last run and records a purge manifest of exactly the
URLs that changed, so a rebuild is incremental and a CDN invalidation is surgical.

Page identity is content-addressed: an article's permalink carries an 8-hex prefix of
its content hash, so published pages are immutable and safe to cache forever, while
listing pages and shards carry a short TTL. The client-side refiner (in
`internal/generate/templates/app.js`, tested under `web/`) fetches the JSON shards to
filter by author, date, topic, and section without any backend call.

## What's in here

- `internal/generate` and its embedded `templates/` (the generator + the public
  frontend: HTML, `style.css`, `app.js`, favicons, masthead video).
- `internal/purge` and `cmd/censurado/purge` (CDN cache invalidation).
- `internal/cachepolicy` (the Cache-Control each generated URL should get).
- `cmd/censurado/generate` (the generator binary: one-shot and `-watch`).
- `web/` (the frontend's vitest suite for `app.js`).

The data model, the store, and the publish/admin servers live in
`censurado-web-backend`; this repo imports the shared libraries it needs.

## Build and test

There is no host Go toolchain; everything runs in the pinned `golang:1.26.4-trixie`
container, which mounts BOTH repos so the `replace github.com/hec-ovi/censurado-web-backend`
directive resolves to the sibling checkout. With `make` available:

```sh
make test     # go test ./... (both repos mounted)
make build
make vet
npm test      # the frontend (vitest) suite
```

Without `make`, run the `docker run ...` the Makefile wraps (it shows the exact mount
layout).

## Generating the site

One-shot, or `-watch` to stay resident and regenerate plus purge whenever the backend
writes a new article. See `deploy/README.md` for the exact commands and the Cloudflare
upload step.

## License

See `LICENSE`.
