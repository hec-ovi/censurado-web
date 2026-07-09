# censurado-web

The static-site generator for Censurado, a news site. It reads the published corpus
from the backend's database and renders the complete static site: immutable HTML
pages, JSON shards for client-side filtering, feeds, sitemaps, and a CDN purge
manifest. The public reading path is fully static, so nothing here runs when a reader
loads the site; the generator runs at build time and a CDN serves the output.

<h2 align="center">
  <a href="https://elcensuradoweb.com">🌐 elcensuradoweb.com →</a>
</h2>

<p align="center">
  <a href="https://elcensuradoweb.com"><img src="https://img.shields.io/badge/live-elcensuradoweb.com-e01842?style=for-the-badge&logo=cloudflare&logoColor=white" alt="Live site" /></a>
</p>

The system is three code repos plus an image backend:

- **censurado-web** (this): the generator + the public frontend (templates, CSS, JS).
- **censurado-web-backend**: the publish API, the sqlite store (the source of truth
  for the published articles and for authors, sources, topics, and the portada), the
  JSON read API, and the operator admin panel. This repo imports its public `domain`,
  `store`, `content`, and `media` libraries.
- **censurado-web-brain**: the agentic layer (the CLI, the SKILL, and the editorial
  prompts) a CLI agent walks to write and publish articles. It holds no data and runs
  no model, and it carries the one Docker Compose that wires the whole stack together.
- **comfyui-strix-docker**: ComfyUI on ROCm, the image backend for hero art.

## How it works

`generate.Generate` takes an already-open read-only handle to the backend's corpus (a
`store.Repository`; the backend's publish service is the only writer, and WAL mode lets
them run together), scans the whole
corpus once, and materializes every page across every scope (latest, per section,
author, topic, month, and the section-anchored matrix). It writes only artifacts
whose bytes changed since the last run and records a purge manifest of exactly the
URLs that changed, so a rebuild is incremental and a CDN invalidation is surgical.

Page identity is content-addressed: an article's permalink carries an 8-hex prefix of
its content hash, so a changed article is a new URL and permalinks never need purging.
HTML and JSON are served `no-store`, and readers stay current through the app.js
version-sentinel poll (see `internal/generate/templates/LIVE-REFRESH.md`), not through
cache TTLs. The client-side refiner (in
`internal/generate/templates/assets/app.js`, tested under `web/`) fetches the JSON shards to
filter by author, date, and section without any backend call.

There is no authors or topics table the generator depends on. The author pages
(`/author/<slug>/`), the topic pages (`/topic/<slug>/`), and the Nosotros roster
(`/about/`) are all derived from per-article metadata and tags: an article's
`metadata.author_*` fields supply each author's display name, bio, and avatar, and an
article's topics supply the topic facets. The Nosotros roster is ordered by the
article index, most-published author first, tie-broken by each author's earliest
article in insertion order, then slug; no author is hardcoded, so an empty corpus
yields zero authors. When the
backend store exposes an operator author/topic registry, those rows can override an
existing author's or topic's label/bio/avatar, but the registry never manufactures a
page for an author or topic that has no published articles.

## What's in here

- `internal/generate` and its embedded `templates/` (the generator + the public
  frontend: HTML, `style.css`, `app.js`, favicons, the four masthead clips).
- `internal/purge` and `cmd/censurado/purge` (CDN cache invalidation).
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

The container writes Go's module and build caches into repo-local `.gomodcache/` and
`.gocache/` (both gitignored, a few hundred MB each). Treat `.gomodcache/` like a Go
`node_modules`: it is vendored dependency code, not ours. Skip it when grepping, auditing,
or counting tests; it is full of third-party `*_test.go` files that are not part of this repo.

## Generating the site

One-shot, or `-watch` to stay resident and regenerate plus purge whenever the backend
writes a new article. See `deploy/README.md` for the exact commands and the Cloudflare
upload step.

## License

See `LICENSE`.
