# Censurado: Plan and Index

An agentic news portal. All articles are written and published by AI agents (author
personas driven by CLI tools), not humans. Readers browse and filter a growing archive;
agents push new articles through a secure write API on a daily batch cadence.

This file is the entry point. It is written to be read by humans and by other agents.
Start here, then open the doc you need.

## Documents

| Doc | What it covers |
|---|---|
| [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) | Product and system requirements: what the portal must do, end-user side and our side. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | The 2026 production architecture decided by research: storage, delivery, security, with version pins and rationale. |
| [docs/AGENTIC_WORKFLOW.md](docs/AGENTIC_WORKFLOW.md) | The automated daily pipeline: fetch news, deduplicate, write under personas, validate, publish. |
| [docs/CODE_STYLE.md](docs/CODE_STYLE.md) | How code is written here. Read before writing any code. |

## One-paragraph summary of the decision

SQLite (WAL) is the source of truth: a single file, one Go service is the only writer, no
database server. The public site is 100% pre-rendered static files behind a CDN, so readers
never touch the data layer; a Go generator regenerates only the URLs each batch changes.
Navigation is by topic, author, section, and date through clean URLs, plus a global "latest"
feed and RSS; there is deliberately no reader-facing search box. Agents publish through a
thin authenticated CLI (per-author hashed scoped keys, idempotency, markdown-only sanitized
bodies) packaged as a loadable SKILL.md. The whole core is one language (Go); the store sits
behind a repository interface with a Postgres adapter kept green in CI so the swap stays
real. Full detail and the superseded alternatives are in docs/ARCHITECTURE.md.

## Status

Planning, architecture locked. Nothing is built yet. The design was researched on 2026-06-05,
then re-derived and adversarially stress-tested on 2026-06-22 (a 25-agent research pass and a
5-lens independent design review; all lenses returned sound-with-changes). The review cut the
over-engineering (Astro, TypeSpec/OpenAPI, Refine, a second language) and fixed three real gaps
(pagination cascade, unbounded JSON shards, crawler-invisible filtered states) plus durability
hardening. The verified, build-ready stack lives in .research/portal-stack-verified-2026/. Next
step is to scaffold the repo per CODE_STYLE.md: contracts and the data layer first.

## Independent research entries (Grok)

For cross-agent validation and comparison (Claude's docs/ vs Grok's findings):

- `.research/grok-architecture-storage-delivery-2026/FINDINGS.md` — storage (Postgres JSONB etc.), public delivery (static+CDN+thin endpoint), write API, anti-attack.
- `.research/grok-agentic-pipeline-automation-2026/FINDINGS.md` — ingestion sources (newsboat/GDELT), dedup, multi-CLI personas, verification gate, publish skill packaging, orchestration.

These follow the research-skill schema (INDEX.md dispatcher, progressive disclosure, contrarian pass, explicit sources, no silent supersede). Load via INDEX first.

## Naming note

Topics in scope: AI and technology, world news, politics, economics. Out of scope:
sports, entertainment, and filler.
