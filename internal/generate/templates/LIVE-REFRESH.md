# Frontend contract: live refresh (the version sentinel)

This is the contract for the client-side live-refresh feature: how a reader who is
already viewing the portal learns that new stories landed and updates the page without
a full reload. Both halves are built: the three static endpoints below are frozen, and
the client poll loop plus the refresh banner are implemented in
`templates/assets/app.js` (the `LiveRefresh` class). Nothing here requires
server-side rendering or per-user work: every endpoint is a plain static file.

See also `README.md` in this folder for the DOM hooks and theming contract, and
`../../../contracts/` for the JSON Schemas referenced below.

## What the backend provides

Three static endpoints, all regenerated and CDN-purged after a publish. The generator
running with `-watch` (see `deploy/README.md`) re-runs generation on a fixed interval
and purges exactly the URLs that changed; the backend's publish service only writes the
database.

1. **The version sentinel** `GET /latest/version.json`
   - Schema: `contracts/version.schema.json`.
   - Shape: `{"v": "<hex sha256>", "latest_ids": ["<slug>", ...]}`.
   - `v` is a content fingerprint over the newest articles in display order. It is
     byte-stable while nothing changed and changes the instant the top of the site does
     (a new top story, an edit in the window, or a reorder). `latest_ids` are the newest
     articles' slugs, newest first.
   - Serving: `no-store`, like the rest of the site's HTML and JSON, so a reader always
     gets fresh bytes (this is the iOS-friendly policy the site ships). The sentinel is
     tiny, so the client refetches it in full on every poll.

2. **The latest manifest** `GET /manifest/latest/index.json`
   - Schema: `contracts/manifest.schema.json`.
   - Lists the latest scope's month shards newest first: `shards[0].url` is the newest
     month's shard URL (for example `/shards/latest/2026/06.json`), with a `count` and,
     when a month is split, a `parts` array.

3. **The month shards** `GET /shards/latest/<YYYY>/<MM>.json`
   - Schema: `contracts/shard.schema.json`.
   - A JSON array of entries (one per article: url, title, section, author, topics,
     published time, and the media keys), in the same display order the listing HTML
     uses. This is the data the banner renders new cards from.

## The poll protocol

`app.js` implements this in the `LiveRefresh` class:

1. On load, fetch `/latest/version.json` once and record its `v` (and its `ETag`, if
   the serving layer sends one) as the page's current state.
2. Poll the sentinel every 60 seconds. Each fetch is `cache: "no-store"` with a unique
   cache-bust query (`?t=<now>`), because iOS WebKit does not reliably revalidate
   `no-store` on its own; the distinct query forces a fresh trip past its disk cache.
   The client also sends `If-None-Match` when it holds a recorded `ETag` and treats a
   `304` as "nothing changed".
3. On a response whose `v` differs from what is on screen, content has changed:
   - Fetch `/manifest/latest/index.json`, take `shards[0].url`, and fetch that shard
     (both `no-store`, both cache-busted).
   - Diff the shard entries against the articles currently rendered (by `url` or slug).
     The new entries are the ones not already on the page.
   - If there are new articles, show an unobtrusive banner (`app.js` shows a count-free
     "Actualizar nuevos artículos" call to action). On click, prepend the new cards
     using the page's existing card renderer, then record the new `v`.
   - If `v` changed but no new article appeared (an existing one was edited or removed),
     show an "Actualizar" reload banner instead. Nothing auto-refreshes under the reader;
     the reader taps when ready.

## Client behavior (as shipped in app.js)

- Polling pauses when `document.hidden` (the Page Visibility API) and resumes on
  `visibilitychange`, so backgrounded tabs cost nothing.
- The poll interval is fixed at 60 seconds, with one in-flight check at a time.
- On a fetch error the client backs off exponentially from the 60-second base, capped at
  5 minutes, and resets to the base on the next success.
- Server-Sent Events is a possible future upgrade for sub-poll-interval push, but it
  needs a stateful fan-out tier that defeats the cheap, mirrorable static edge, so it is
  deliberately out of scope here.

## Freshness timing

After a publish, the regenerate updates the sentinel, the manifest, the latest shard,
and the new article page, then the purge invalidates exactly that changed set.
Content-hash permalinks (`/a/<slug>-<hash>/`) are not in the purge set, because a changed
article is a new URL, not an edit of an old one. An active client then sees the banner
within its poll interval plus the CDN's purge propagation.

## What the backend will not change without a contract bump

The sentinel shape (`contracts/version.schema.json`), the manifest shape
(`contracts/manifest.schema.json`), and the shard shape (`contracts/shard.schema.json`)
are frozen. Build against them. If the client needs a field that is not there, that is a
contract change to raise here first, not a guess.
