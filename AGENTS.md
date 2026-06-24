# AGENTS.md

A map of censurado-web for agents (and people) working in this repo. It explains what
each part does, how it works, and what it expects, then points to the files that hold
the real contract. It is a guide, not a second copy of the code: when this file and the
code disagree, the code wins, so fix the code reference here too.

censurado-web is a single-writer static news platform written in Go. Articles enter
through one authenticated HTTP API, are stored in SQLite (a Postgres adapter exists as
a proven alternative), and are rendered into a complete static site that an external web
server delivers. A private operator console browses the archive and can publish or
rebuild. The articles themselves are produced by a separate agentic system,
[censurado-web-brain](../censurado-web-brain), which talks to this platform over exactly
one seam: the publish contract.

## Big picture

```
producer (agent or operator)
        |  POST /articles  (+ optional POST /media)
        v
   publish service  --writes-->  SQLite (single writer)
        |                              |
        | payload archive              | read
        v                              v
   replay (DR)                    generate (static site)  -->  site-data + media-data
                                       |                              |
                                       | purge.json                   v
                                       v                       external web server (public)
                                   CDN purge                          |
                                                                      v
                                                                   readers
   admin console  --reads--> SQLite ; --POSTs--> publish (it never writes the DB itself)
```

Two reads define the whole system:

- **Identity is the content hash.** `ContentHash` = SHA-256 over the normalized title,
  body, author, and section. It is the dedup and idempotency key. Media, topics, slug,
  and timestamps are not part of it, so attaching an image never changes an article's
  identity.
- **One writer.** Only the publish service writes the database. The admin console and the
  generator are readers; the admin publishes by calling the publish API like any other
  client.

## Parts

### 1. Article contract and domain model

The shared shape of an article and the rules that turn an untrusted submission into a
canonical record. `domain.NewArticle` trims and checks the four required fields, normalizes
topics (trim, drop blanks, case-insensitive dedup, keep first-seen order), derives the slug
(explicit, else from the title, else a content-hash prefix), and computes `ContentHash`.
The open `metadata` map carries the display tail, including the media keys the generator
reads. The JSON Schema is the hand-authored contract artifact; the running server does not
load it. It enforces the same shape in code: a strict JSON decode with
`DisallowUnknownFields` (this is what makes `additionalProperties:false` real) plus the
`NewArticle` checks. The schema's `maxLength` bounds are input hygiene mirrored by the CLI
and the schema, not enforced by `NewArticle` (which only requires non-empty after trim).

- `internal/domain/article.go` :: `Article`, `PublishInput`, `NewArticle`, `ContentHash`, `Slugify`, `normalizeTopics` (the canonical model; depends on nothing else).
- `contracts/article.schema.json` :: the single cross-layer, cross-repo contract artifact. Do not edit casually: the brain vendors a byte-identical copy guarded by a drift test.
- `testdata/golden_article.json` :: the sample submission used as a build fixture.

### 2. Data layer (store)

The source of truth, behind a domain-only interface so the engine can be swapped. The
default adapter is pure-Go SQLite in WAL mode; a Postgres adapter implements the identical
behavior and a single conformance suite runs against both to prove the swap. Articles,
topics (a normalized join table), and a submission/idempotency log are explicit indexed
columns; everything else rides a `metadata` JSON/JSONB column. `Upsert` dedups on the
unique content hash. There is no migration framework: schema is embedded DDL applied with
`CREATE ... IF NOT EXISTS` on open (and a small additive `ALTER` step where present).

- `internal/store/store.go` :: the `Repository` and `SubmissionLog` interfaces (`Upsert`/`BySlug`/`Find`/`Count`/`Facets`/`Close`), `Filter`, `Facets`, `Submission`.
- `internal/store/sqlite/sqlite.go` + `internal/store/sqlite/schema.sql` :: the default adapter and its STRICT-table schema.
- `internal/store/postgres/postgres.go` + `internal/store/postgres/schema.sql` :: the mirror adapter (not wired into compose; proves the interface).
- `internal/store/storetest/contract.go` :: the cross-engine conformance suite both adapters run (Postgres only when `CENSURADO_TEST_POSTGRES_DSN` is set).

### 3. Publish API (the only write path)

The authenticated `POST /articles` service. It authenticates a bearer key, binds the
article to the key's author (unless the key holds the privileged publish-any scope),
strict-decodes the body, runs the Markdown safety gate, checks the idempotency ledger,
upserts (content-hash dedup), and appends a submission record. A new publish is also
appended to an optional payload archive for replay-based recovery. A per-key token bucket
rate-limits before auth.

- `internal/publish/publish.go` :: `Handler.ServeHTTP` (the route), `authenticateWrite`, `ScopeWrite` (`articles:write`), `ScopePublishAny` (`articles:publish-any`), the author-binding rule.
- `internal/publish/apply.go` :: `Apply`, the post-auth accept core (NewArticle, the `RenderMarkdown` gate, idempotency, upsert, audit). Shared by the replay tool so recovery takes the same exactly-once path.
- `internal/publish/auth.go` :: `StaticKeyAuth`: `<prefix>.<secret>` keys, only the secret's SHA-256 is stored, constant-time compare.
- `internal/publish/{server.go,ratelimit.go,archive.go}` :: route assembly, the per-prefix limiter, the append-only payload archive.
- `cmd/censurado/publish/main.go` :: the always-running server; flags/env, the keys file, `-gen-key`, `-payload-archive`, `-media-dir`.

### 4. Media (self-hosted image store / CDN)

A content-addressed image store mounted on the publish service. An upload is validated
(magic-byte sniff, a JPEG/PNG/GIF/WebP allowlist, a size cap), hashed, and written as
`<sha256>.<ext>`, so identical bytes map to one immutable URL. `POST /media` (the same
`articles:write` scope) returns `/media/<sha256>.<ext>`; `GET /media/{name}` serves it
publicly with a one-year immutable cache. Media attaches to an article through the open
`metadata` object (`image`, `image_alt`, `youtube`), so the article contract is unchanged
and the generator already renders it. The store has no garbage collection by design;
orphaned bytes are bounded (size-capped and deduplicated).

- `internal/media/media.go` :: the `Store` (validate, hash, atomic write, traversal-safe serve) and the shared `SafeMediaURL` / `YouTubeEmbedURL` reference helpers (one source of truth for the generator, admin, and store).
- `internal/publish/media.go` :: `MediaHandler` (authenticated upload, public immutable serve).
- `cmd/censurado/admin/main.go` (`buildUploadMedia`) + `internal/adminweb/adminweb.go` (`uploadFormImage`) :: the admin upload proxy: the console POSTs the bytes to publish, so it stays a non-writer.
- `deploy/README.md` "Media" section :: the operator-facing description (store dir, volume, backup).

### 5. Static site generator

Turns the article store into the full set of static artifacts and does it incrementally.
It scans the corpus once, enumerates a bounded set of scopes (latest, section, author,
topic, month, and section-anchored combinations), and for each emits paginated listing
HTML, per-article permalinks, month-bucketed JSON shards for the client refiner, a shard
manifest, the `/latest/` feeds, the sitemap, robots.txt, and the embedded assets. It
content-hashes every artifact against the previous run's state, writes only what changed,
deletes orphans, and emits `purge.json` listing exactly the URLs to invalidate. Pages are
sealed on insertion order (so a sealed page is byte-immutable even under a back-dated
insert) but display newest-first. Bodies are rendered once per content hash through the
shared Markdown gate.

- `internal/generate/generate.go` :: `Options`, `Result`, and `Generate` (the orchestrator).
- `internal/generate/{enumerate.go,scope.go,page.go,url.go}` :: the single scan, scope and URL algebra, insertion-order sealing.
- `internal/generate/{collectors.go,shard.go,shardcap.go,manifest.go}` :: the six collectors, the shard projection and cap-splitting, the page manifest.
- `internal/generate/render.go` :: html/template execution, the view models, SEO/JSON-LD/OG, and `mediaForArticle` (precedence youtube, youtube_id, video, image).
- `internal/generate/{state.go,materialize.go,feeds.go,sitemap.go}` :: incremental diff and `purge.json`, atomic writes, feeds, sitemap.
- `internal/content/render.go` :: `RenderMarkdown` (goldmark without unsafe, then bluemonday UGCPolicy). The same gate runs at publish (to reject) and at generate (to emit).
- `contracts/shard.schema.json`, `contracts/manifest.schema.json` :: the frozen client-facing artifact contracts.
- `cmd/censurado/generate/main.go` :: the one-shot CLI.

### 6. Public frontend (templates and assets)

The presentation half of the generator: Go html/templates plus one stylesheet and one ES
module, embedded and emitted at stable `/assets/` URLs. The identity is a square,
image-led editorial layout with light/dark theming through CSS custom properties. `app.js`
is a progressive-enhancement facet refiner: discrete chip toggles (there is no reader
search box), which read the page's manifest and month shards and filter the list in place,
falling back to the pre-built static scope links when JavaScript is off. The only inline
script is a small theme bootstrap in the head; there is no CSP header today.

- `internal/generate/templates/base.tmpl` :: the document shell (head meta, theme bootstrap, asset links, JSON-LD).
- `internal/generate/templates/{listing.tmpl,article.tmpl}` + `components/{chrome,article_card,media}.tmpl` :: listing and article pages and the shared chrome, card, and lead-media component.
- `internal/generate/templates/assets/{style.css,app.js,favicon.svg}` :: the single stylesheet, the facet refiner, the favicon.
- `internal/generate/templates/README.md` :: the frontend contract: the required DOM hooks, the accepted `metadata` media keys, the theming and static constraints.

### 7. Admin console

A private, single-operator, server-rendered console (Go html/template plus vendored htmx).
It browses and inspects the archive and the audit ledger read-only behind a signed-cookie
session with per-session CSRF, and offers two mutating actions: a manual New-article form
and a regenerate trigger. The form publishes through the injected publish closure and
uploads images through the publish media endpoint, so the console never writes the database
or disk itself. It is meant to run bound to localhost behind a tunnel or private network.

- `internal/adminweb/adminweb.go` :: `Config`, `Handler`, routes, the create-form handler and validation, the upload proxy.
- `internal/adminweb/auth.go` :: the signed-cookie session and CSRF.
- `internal/adminweb/render.go` + `templates/{layout,create,...}.tmpl` :: the view structs and pages (the create form's media fieldset is in `create.tmpl`).
- `cmd/censurado/admin/main.go` :: env to `Config`, opens SQLite as a reader, builds the regenerate/publish/upload closures.

### 8. Author CLI and agent skill

`censurado-publish` submits exactly one article. It reads JSON from stdin or `--file`,
validates it locally with the same domain code the server uses, then POSTs it with a bearer
token and an `Idempotency-Key`. Exit codes are distinct per outcome (0 ok or replay, 1
network or 5xx, 2 auth or usage, 3 validation or 4xx) so an orchestrator can branch.
`--dry-run` validates without a network call. `SKILL.md` is the prose contract that tells an
author-persona agent how to drive it.

- `cli/main.go` :: the CLI (`run` is the testable core; local validation, then the authed idempotent POST).
- `cli/skill/SKILL.md` :: the agent skill: env vars, the article JSON shape, publish/dry-run, idempotency, exit codes.

### 9. Ops, deploy, and recovery

The self-hosting topology and the durability tooling. Docker Compose runs three Go binaries
(`publish` the writer, `admin` and `generate` readers) plus a Litestream sidecar over one
shared SQLite file, each container hardened (distroless nonroot, cap-drop, read-only
rootfs, private 127.0.0.1 bindings). Litestream continuously replicates the database; a
CI-gated restore drill proves a restored database is good; the payload archive plus the
replay tool recover writes lost in the replication window after a restore; the purge tool
invalidates exactly the URLs the generator changed (content-hash permalinks are never
purged). An OpenTofu skeleton provisions only the off-site backup bucket.

- `deploy/docker-compose.yml` :: the topology and the four volumes (`db-data`, `site-data`, `media-data`, `replica-data`).
- `deploy/litestream.yml` + `scripts/restore-drill.sh` + `cmd/censurado/restorecheck` :: continuous backup and the proven-restore drill (CI job in `.github/workflows/ci.yml`).
- `cmd/censurado/replay` + `internal/publish/archive.go` :: recover post-restore writes through the same idempotent apply path.
- `cmd/censurado/purge` + `internal/purge/` :: exact CDN invalidation from `purge.json` (vendor-neutral, `dryrun` by default).
- `OPERATIONS.md`, `deploy/README.md`, `deploy/CACHING.md`, `deploy/tofu/` :: the operator runbook, the compose quick start, the cache policy, the backup-bucket skeleton.

### 10. The agentic producer (censurado-web-brain)

The articles come from a separate, separately deployable system. The two repos share no
code and meet only at the publish seam. The brain researches, drafts, evaluates, and
finalizes articles under journalist personas, then POSTs each one with an operator key that
holds both `articles:write` and `articles:publish-any` (so it can author as any persona),
optionally uploading a hero image to `POST /media` first. The contract is pinned by a drift
test in the brain that asserts its vendored copy of `article.schema.json` is byte-equal to
this repo's. The seam is one-directional; the platform expects nothing back and never learns
that personas exist.

- `../censurado-web-brain/README.md` :: the producer described end to end (its own repo; not duplicated here).
- `../censurado-web-brain/tests/test_schema_drift.py` :: the cross-repo pin (resolves the live schema via `NEWSROOM_PLATFORM_SCHEMA` or the sibling checkout).
- `internal/publish/publish.go` + `internal/publish/auth.go` :: this side of the seam (the scopes and the key format the operator key must satisfy).

## Contracts to honor

- `contracts/article.schema.json` :: the publish payload. Cross-repo: the brain vendors a byte-equal copy with a drift test, so any change here must be mirrored there (and may warrant a new contract version rather than mutating v1).
- `contracts/shard.schema.json`, `contracts/manifest.schema.json` :: the client refiner's data contracts, frozen so old pages keep working.
- `purge.json` (emitted to the state dir) :: the generate-to-purge handoff (`internal/purge` parses it).
- The content hash (`domain.ContentHash`) :: the dedup and idempotency identity, ported byte-for-byte by the brain. Changing its inputs is a cross-repo break.

## Conventions and invariants

- Single writer: only `publish` writes the DB. The admin and generator are readers; the admin publishes through the API.
- The contract is enforced in code, not by loading the JSON Schema at runtime (strict decode plus `domain.NewArticle`).
- Media rides the `metadata` object (`image`, `image_alt`, `youtube`); it never changes the article contract or the content hash.
- The publish key format is `<prefix>.<secret>`; only the SHA-256 is stored. Scopes are `articles:write` and the privileged `articles:publish-any`. There is no OAuth2 (the design docs mention it as a future option only).
- The site has no reader search box by design; discovery is faceted chip filters over pre-built scope pages.
- The generator is deterministic and incremental: same store plus same options writes nothing; sealed pages are byte-immutable; only changed URLs are purged.

## Docs map (what to trust)

Tracked, current, operator/contract facing: `README.md`, `OPERATIONS.md`, `deploy/README.md`,
`deploy/CACHING.md`, `deploy/tofu/README.md`, `cli/skill/SKILL.md`,
`internal/generate/templates/README.md`. `PLAN.md` is the tracked phase history.

The `docs/` tree (`ARCHITECTURE.md`, `GENERATOR-DESIGN.md`, `CODE_STYLE.md`, `REQUIREMENTS.md`,
`AGENTIC_WORKFLOW.md`, `NEWSROOM-BRAIN-DESIGN.md`) is gitignored internal design notes: useful
background, but not the published surface and not all current. In particular
`AGENTIC_WORKFLOW.md` is a pre-build research sketch superseded by the actual brain repo;
prefer `../censurado-web-brain` for how the producer really works. `stage_*_instructions.md`,
`grok-research/`, and `.research/` are gitignored local scratch.

## Build, test, run

There is no host Go toolchain; the toolchain is dockerized (Go 1.26, pinned in `go.mod` and
the deploy Dockerfiles). Run the suite with the pinned image:

```
docker run --rm -v "$PWD":/app -w /app golang:1.26.4-trixie sh -c "go vet ./... && go test ./..."
```

The cross-engine conformance suite also runs against real Postgres when
`CENSURADO_TEST_POSTGRES_DSN` is set. To run the platform, see `deploy/README.md`; to publish
one article by hand, see `cli/skill/SKILL.md`.
