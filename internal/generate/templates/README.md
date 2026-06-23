# Public Frontend

This directory is the isolated presentation layer for the reader-facing static
site. It is intentionally thin: templates and assets render the data that
`internal/generate` already prepared, but they do not query the store, mutate
articles, change shard contracts, or introduce a frontend build step.

## Boundaries

The frontend may change:

- HTML templates under `templates/*.tmpl` and `templates/components/*.tmpl`.
- Public assets embedded from `templates/assets/`.
- Presentation-only view fields in `internal/generate/render.go`.

The frontend must not change without a broader contract migration:

- `contracts/shard.schema.json` or the frozen shard field set.
- `contracts/manifest.schema.json`.
- Publish input validation, store behavior, page/shard enumeration, or purge
  semantics.
- The stable asset URLs `/assets/style.css` and `/assets/app.js`.

## Required DOM Contract

The client refiner and tests depend on these hooks:

- Listing pages include `[data-facets]` and `[data-articles]`.
- Each listing row is `.article-item` with `data-author`, `data-section`,
  `data-topics`, and `data-month`.
- Card links use `.card-link`; author, section, topic, and time use
  `.author-link`, `.section-link`, `.topic-link`, and `.published`.
- Landing pages embed `#cnz-manifest`; sealed/deep pages link to
  `<link rel="manifest" href="/manifest/.../index.json">`.
- Article pages render sanitized Markdown through `.article-body`; raw author
  HTML is never trusted.

New classes can be added freely, but these hooks must remain.

## Components

- `base.tmpl` owns the document shell and stable asset links.
- `components/chrome.tmpl` owns masthead, navigation, and footer.
- `components/article_card.tmpl` owns reusable listing cards, topic chips, and
  author avatar fallback. Listing cards prefer `metadata.image` and otherwise
  render a generic section art panel.
- `components/media.tmpl` owns article lead media.
- `listing.tmpl` composes the listing hero, article list, pager, months, and
  manifest. It keeps `[data-articles]` as a direct child of `[data-facets]`
  because the client refiner inserts its panel before that list.
- `article.tmpl` composes lead media, article header, metadata links, and body.

## Accepted Presentation Metadata

`metadata` is the article contract's open object. The frontend treats these keys
as optional strings. Invalid, blank, non-string, or unsafe values are ignored.

- `image`: root-relative, `http`, or `https` URL for article lead image and
  social image metadata.
- `image_alt` or `alt`: accessible alt text for `image`; falls back to title.
- `video`: root-relative, `http`, or `https` URL for a native `<video>` lead.
- `youtube` or `youtube_id`: YouTube ID or supported YouTube URL rendered through
  `youtube-nocookie.com/embed/...`.
- `author_avatar` or `avatar`: root-relative, `http`, or `https` author image.

Unsupported by design:

- Raw HTML embeds in article Markdown.
- Arbitrary iframe providers.
- `javascript:`, `data:`, protocol-relative, malformed, or relative media URLs
  that are not root-relative.
- Listing-card thumbnails from shard JSON; the shard schema is body-free and
  media-free on purpose.

## Theming

All visual decisions start in `templates/assets/style.css` under `@layer tokens`.
Change colors, spacing, type scale, radii, and motion there before editing
component rules. Font stacks are system-only and no remote CSS is loaded.

The current visual language is deliberately square, image-led editorial UI:
radii tokens are zero, topic labels do not get hashtag prefixes, and monospace
is reserved for actual code content rather than navigation, facets, or article
metadata.

Theme support is presentation-only. `/assets/app.js` stores `cnz-theme` as
`system`, `light`, or `dark`; CSS reads `html[data-theme]`, while the absence of
that attribute means system preference. The favicon is emitted as
`/assets/favicon.svg`.

The CSS keeps the public site static and cacheable:

- One stylesheet.
- One ES module.
- No bundler.
- No hydration framework.
- No runtime dependency beyond the browser.

## Extension Path

If future work needs richer listing media, author profile records, or card
excerpts in client-refined results, do not patch around the current shard JSON.
Add a versioned contract first, update `contracts/shard.schema.json`, update the
Go generator projection, then update `app.js` and tests together.
