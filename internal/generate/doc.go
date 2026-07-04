// Package generate turns the article store into the complete set of static
// artifacts a CDN serves: pre-rendered Tier-A listing and article HTML,
// month-bucketed JSON shards for client-side Tier-B refinement, a per-page
// embedded manifest, a sitemap of Tier-A URLs, and RSS/Atom/JSON feeds for
// /latest/.
//
// It is a pure reader of store.Repository (only Find/Count, never a write) and
// it Slugifies every facet value itself (via domain.Slugify) before building any
// Filter, URL, or shard path, so server-rendered membership and client-shard
// membership cannot disagree. The store contract is not changed by this package.
//
// Two ordering axes are kept distinct. Page and shard-part SEAL boundaries are
// cut on the append-only insertion axis (CreatedAt ASC, id ASC), which is
// monotonic because id is an autoincrement primary key, so a sealed page is
// byte-immutable even under a back-dated insert. DISPLAY order within a page or
// shard part is (PublishedAt DESC, id DESC). The same displayLess comparator
// orders HTML listings and shard parts, so listing order and shard order match
// by construction.
//
// Generation is deterministic (identical store state plus identical Options
// yields byte-identical public artifacts and an empty purge) and incremental
// (only changed artifacts are written, removed ones are deleted, and a purge
// manifest lists every changed or removed public URL). PublishedAt and CreatedAt
// are truncated to whole seconds when an article enters the index (SQLite stores
// RFC3339 text at second precision), so the same logical data always produces
// identical bytes and ordering.
package generate
