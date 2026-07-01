# AGENTS.md

Guidance for coding agents working in `censurado-web`. This repo is the static-site
GENERATOR plus the public frontend. The data model, store, publish API, and admin
console live in `censurado-web-backend`; the agentic producer lives in
`censurado-web-brain`. Keep changes here scoped to generation and the frontend.

## Big picture

The backend writes a sqlite database of published articles. This repo reads it at
build time and renders the complete static site for a CDN: immutable HTML pages, JSON
shards for client-side filtering, feeds, sitemaps, and a purge manifest. Nothing here
runs at read time; the reading path is fully static.

Author and topic pages are not driven by a backend authors/topics table. The author
pages (`/author/<slug>/`), the topic pages (`/topic/<slug>/`), and the `/about/`
roster are derived from each article's metadata (`author_name`, `author_bio`,
`author_avatar`) and its topic tags, recorded from the earliest-inserted carrier per
slug (`enumerate.go`, `BuildIndex`). The roster order is most-published author first
(`orderedAuthorSlugs` in `render.go`). An optional operator author/topic registry
(`store.AuthorStore`/`store.TopicStore`, applied by `overlayRegistry`) can override an
existing slug's label/bio/avatar but never creates a page for a slug with no articles.

The generator imports the backend module's public libraries (`domain`, `store`,
`content`, `media`) through a `replace` directive in `go.mod`. Both repos must be
checked out side by side; the dockerized toolchain mounts both so the replace
resolves.

## Parts of this repo

1. `internal/generate` :: the generator. `Generate(ctx, repo, opts)` scans the corpus
   once (`enumerate.go`), builds every scope's pages, renders (`render.go`,
   `collectors.go`), diffs against prior state, writes only changed artifacts, and
   emits `purge.json`. Deterministic and incremental.
2. `internal/generate/templates` :: the public frontend, embedded in the binary: HTML
   templates, `style.css`, `app.js` (the client-side shard refiner), favicons, video.
3. `internal/purge` + `cmd/censurado/purge` :: CDN cache invalidation. Parses the
   generator's `purge.json` and calls a vendor-neutral purge endpoint (dry run by
   default).
4. `cmd/censurado/generate` :: the generator binary. One-shot, or `-watch` to stay
   resident and regenerate plus purge whenever the backend writes new content (this is
   the freshness worker that used to live in the publish service before the split).
5. `web/` :: the vitest suite for `app.js`.

## Contracts to honor

- `contracts/shard.schema.json`, `contracts/manifest.schema.json`,
  `contracts/version.schema.json` :: the client-facing data contracts (refiner shards,
  page manifest, live-refresh sentinel). Frozen so old pages and polling clients keep
  working.
- `purge.json` (emitted to the state dir) :: the generate-to-purge handoff.
- The content hash (`domain.ContentHash`, now in the backend) is the page-identity
  and dedup key; an article's permalink carries an 8-hex prefix of it. Changing its
  inputs is a cross-repo break (the brain ports it byte-for-byte). The publish payload
  contract (`article.schema.json`) also lives in the backend now.

## Conventions and invariants

- The generator is deterministic and incremental: same database plus same options
  writes nothing; sealed article pages are byte-immutable; only changed URLs are
  purged. Golden tests pin this; do not break byte-stability casually.
- The generator only READS the store (the backend's publish service is the only
  writer; WAL mode lets them run together).
- Media rides the article `metadata` object (`image`, `image_alt`, `youtube`,
  `subtitle`, `description`); it never changes the article contract or the content
  hash.
- The cache policy is a serving-layer concern; the generator emits no HTTP headers on
  its files. HTML and JSON are served `no-store` so readers always fetch fresh bytes.
  The version sentinel is byte-stable, so its `v` only changes when the top of the site
  changes.
- The site has no reader search box by design; discovery is faceted chip filters over
  pre-built scope pages and the client-side shard refiner.
- Never cap LLM or any output length anywhere; article bodies are never truncated.

## Build, test, run

No host Go toolchain; the toolchain is dockerized (Go 1.26, pinned). The container
mounts BOTH repos so the replace resolves; the `Makefile` `DOCKER_GO` recipe shows the
exact layout. Run `make test` / `make vet` (or the wrapped `docker run`), and
`npm test` for the frontend. To generate the site or run `-watch`, see
`deploy/README.md`.
