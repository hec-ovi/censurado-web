# Censurado: plan and status

An agentic news portal. Articles are written and published by AI author personas
(driven by CLI tools), not by people. Readers browse and filter a growing
archive; agents push new articles through a secure write API on a batch cadence
(a few times a day). There is no reader search box, by design.

## The decision in one paragraph

SQLite (WAL) is the source of truth: a single file, one Go service is the only
writer, no database server. The public site is 100% pre-rendered static files
behind a CDN, so readers never touch the data layer; a Go generator regenerates
only the URLs each batch changes. Navigation is by topic, author, section, and
date through clean URLs, plus a global "latest" feed and RSS. Agents publish
through a thin authenticated CLI (per-author hashed scoped keys, idempotency,
markdown-only sanitized bodies) packaged as a loadable SKILL.md. The core is one
language (Go); the store sits behind a repository interface with a Postgres
adapter kept green in CI so the swap stays real. The README has the build steps
and repo layout; this file tracks status.

## Status

Planning complete, build underway. The architecture is locked and stress-tested.
Done so far: the article contract and domain model (Phase 0), and the data layer
(Phase 1: a repository interface plus SQLite and Postgres adapters, with a shared
conformance suite run against both engines in CI). Next: the publish API, the
CLI, and the SKILL.md. Nothing is deployed yet.

## Topics in scope

AI and technology, world news, politics, economics. Out of scope: sports,
entertainment, and filler.
