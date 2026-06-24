# Frontend contract: live refresh (the version sentinel)

This is the contract for the client-side live-refresh feature: how a reader who is
already viewing the portal learns that new stories landed and updates the page
without a reload. The backend half is built and frozen; the client (the poll loop
and the "N new stories" banner) is the frontend's to implement against this
contract. Nothing here requires server-side rendering or per-user work: every
endpoint is a static file behind the CDN.

See also `README.md` in this folder for the DOM hooks and theming contract, and
`../../../contracts/` for the JSON Schemas referenced below.

## What the backend provides

Three static endpoints, all regenerated and CDN-purged automatically after a
publish (the publish service runs a debounced regenerate + purge; see
`deploy/CACHING.md`):

1. **The version sentinel** `GET /latest/version.json`
   - Schema: `contracts/version.schema.json`.
   - Shape: `{"v": "<hex sha256>", "latest_ids": ["<slug>", ...]}`.
   - `v` is a content fingerprint over the newest articles in display order. It is
     byte-stable while nothing changed and changes the instant the top of the site
     does (a new top story, an edit in the window, or a reorder). `latest_ids` are
     the newest articles' slugs, newest first.
   - Cache class: `public, max-age=10, stale-while-revalidate=60`. It is tiny and
     edge-cached, so polling it is essentially free at the origin.

2. **The latest manifest** `GET /manifest/latest/index.json`
   - Schema: `contracts/manifest.schema.json`.
   - Lists the latest scope's month shards newest first: `shards[0].url` is the
     newest month's shard URL (for example `/shards/latest/2026/06.json`), with a
     `count` and, when a month is split, a `parts` array.

3. **The month shards** `GET /shards/latest/<YYYY>/<MM>.json`
   - Schema: `contracts/shard.schema.json`.
   - A JSON array of entries (one per article: url, title, section, author, topics,
     published time, and the media keys), in the same display order the listing HTML
     uses. This is the data the banner renders new cards from.

## The poll protocol

1. On load, record the sentinel's `ETag` (or its `v`) for the page's current state.
2. Poll `GET /latest/version.json` every 30 to 60 seconds with
   `If-None-Match: <last ETag>`. The edge answers `304 Not Modified` (no body) while
   nothing changed, so millions of pollers barely touch the origin.
3. On a `200` whose `v` differs from what is on screen, new content has landed:
   - Fetch `/manifest/latest/index.json`, take `shards[0].url`, and fetch that shard.
   - Diff the shard entries against the articles currently rendered (by `url` or
     slug). The new entries are the ones not already on the page.
   - Show an unobtrusive "N new stories" banner. On click (or per the chosen UX),
     prepend the new cards using the page's existing card renderer, then update the
     recorded `v`.

## Client best practices (recommended, not enforced by the backend)

- Full-jitter the poll interval so clients do not synchronize into a thundering herd.
- Pause polling when `document.hidden` (the Page Visibility API) and resume on
  `visibilitychange`, so backgrounded tabs cost nothing.
- Back off with a cap on errors or a `429`, and never poll faster than the sentinel's
  `max-age`.
- Default to polling. Server-Sent Events is a possible future upgrade for
  sub-poll-interval push, but it needs a stateful fan-out tier that defeats the cheap,
  mirrorable static edge, so it is deliberately out of scope here.

## Freshness timing

After a publish, the regenerate updates the sentinel, the manifest, the latest shard,
and the new article page, then the purge invalidates exactly that mutable set
(content-hash permalinks are immutable and never purged). An active client then sees
the banner within its poll interval plus the CDN's purge propagation.

## What the backend will not change without a contract bump

The sentinel shape (`contracts/version.schema.json`), the manifest shape
(`contracts/manifest.schema.json`), and the shard shape (`contracts/shard.schema.json`)
are frozen. Build against them. If the client needs a field that is not there, that is
a contract change to raise here first, not a guess.
