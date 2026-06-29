// Fixture data mirroring real generator output (verified by emitting the
// matrix fixture and the shard-cap fixture from the Go layer). The shard
// entries use the exact frozen 9-field projection; the DOM builders reproduce
// the frozen Phase-4A listing markup the refiner enhances.
import { http, HttpResponse } from "msw";
import { server } from "./server.js";

// ---- shard entries (newest-published-first within each month) --------------

export const E = {
  techAdaJune: {
    slug: "tech-ada-june",
    url: "/a/tech-ada-june-95ed0f28/",
    title: "Tech Ada June",
    author: "ada",
    author_label: "ada",
    section: "tech",
    topics: ["ai"],
    published_at: "2026-06-08T09:00:00Z",
    ts: 1780909200,
  },
  scienceAdaJune: {
    slug: "science-ada-june",
    url: "/a/science-ada-june-e69fef93/",
    title: "Science Ada June",
    author: "ada",
    author_label: "ada",
    section: "science",
    topics: ["bio", "space"],
    published_at: "2026-06-05T09:00:00Z",
    ts: 1780650000,
  },
  techLinJune: {
    slug: "tech-lin-june",
    url: "/a/tech-lin-june-a52adb52/",
    title: "Tech Lin June",
    author: "lin",
    author_label: "lin",
    section: "tech",
    topics: ["go"],
    published_at: "2026-06-02T09:00:00Z",
    ts: 1780390800,
  },
  scienceLinMay: {
    slug: "science-lin-may",
    url: "/a/science-lin-may-b969c926/",
    title: "Science Lin May",
    author: "lin",
    author_label: "lin",
    section: "science",
    topics: ["space"],
    published_at: "2026-05-20T09:00:00Z",
    ts: 1779267600,
  },
  scienceBobMay: {
    slug: "science-bob-may",
    url: "/a/science-bob-may-dc7ba389/",
    title: "Science Bob May",
    author: "bob",
    author_label: "bob",
    section: "science",
    topics: ["bio"],
    published_at: "2026-05-15T09:00:00Z",
    ts: 1778835600,
  },
  techAdaMay: {
    slug: "tech-ada-may",
    url: "/a/tech-ada-may-83562330/",
    title: "Tech Ada May",
    author: "ada",
    author_label: "ada",
    section: "tech",
    topics: ["ai", "go"],
    published_at: "2026-05-10T09:00:00Z",
    ts: 1778403600,
  },
};

// The full /latest/ stream, newest-published-first (June then May), exactly as
// the server page renders it.
export const LATEST_STREAM = [
  E.techAdaJune,
  E.scienceAdaJune,
  E.techLinJune,
  E.scienceLinMay,
  E.scienceBobMay,
  E.techAdaMay,
];

// ---- manifests + shard files for the /latest/ scope ------------------------

export const LATEST_MANIFEST = {
  scope: "/latest/",
  axis: {},
  shards: [
    { url: "/shards/latest/2026/06.json", month: "2026-06", count: 3 },
    { url: "/shards/latest/2026/05.json", month: "2026-05", count: 3 },
  ],
  total: 6,
  cap: { entries: 500, bytes: 204800 },
};

export const LATEST_SHARDS = {
  "/shards/latest/2026/06.json": [E.techAdaJune, E.scienceAdaJune, E.techLinJune],
  "/shards/latest/2026/05.json": [E.scienceLinMay, E.scienceBobMay, E.techAdaMay],
};

// Pre-built /author/ada/ scope shards: the server membership the client filter
// must reproduce exactly (set + order) when Author:ada is selected.
export const AUTHOR_ADA_SHARDS = {
  "/shards/author/ada/2026/06.json": [E.techAdaJune, E.scienceAdaJune],
  "/shards/author/ada/2026/05.json": [E.techAdaMay],
};
export const AUTHOR_ADA_STREAM = [E.techAdaJune, E.scienceAdaJune, E.techAdaMay];

// ---- /section/tech/ scope (deep page; manifest is fetched, not inline) ------

export const TECH_MANIFEST = {
  scope: "/section/tech/",
  axis: { section: "tech" },
  shards: [
    { url: "/shards/section/tech/2026/06.json", month: "2026-06", count: 2 },
    { url: "/shards/section/tech/2026/05.json", month: "2026-05", count: 1 },
  ],
  total: 3,
  cap: { entries: 500, bytes: 204800 },
};
export const TECH_SHARDS = {
  "/shards/section/tech/2026/06.json": [E.techAdaJune, E.techLinJune],
  "/shards/section/tech/2026/05.json": [E.techAdaMay],
};
export const TECH_STREAM = [E.techAdaJune, E.techLinJune, E.techAdaMay];

// ---- multi-part month: /section/caps/ (7 entries, 3-entry cap) --------------
//
// part 1 ("06.json") seals first with the OLDEST entries; higher parts hold
// newer entries; the manifest lists parts newest-part-first. Concatenating
// shards then parts in manifest array order yields the global newest-first
// stream G,F,E,D,C,B,A, exactly the server page order.

const capsEntry = (letter, hour, hash) => ({
  slug: "caps-" + letter.toLowerCase(),
  url: "/a/caps-" + letter.toLowerCase() + "-" + hash + "/",
  title: "Caps " + letter,
  author: "ada",
  author_label: "ada",
  section: "caps",
  topics: [],
  published_at: "2026-06-01T0" + hour + ":00:00Z",
  ts: 1779000000 + hour * 3600,
});

export const CAPS = {
  A: capsEntry("A", 3, "f6e00e8c"),
  B: capsEntry("B", 4, "2b51979a"),
  C: capsEntry("C", 5, "947af21d"),
  D: capsEntry("D", 6, "03cd0918"),
  E: capsEntry("E", 7, "c0bbb07b"),
  F: capsEntry("F", 8, "7244c8f1"),
  G: capsEntry("G", 9, "0eade39c"),
};
// Hours encode published order: G newest (09:00) ... A oldest (03:00), so the
// global display order is G..A and each part file is genuinely newest-first
// (06.json=[C,B,A] has ts C>B>A, etc.), matching real generator output.
const capsByAge = [CAPS.G, CAPS.F, CAPS.E, CAPS.D, CAPS.C, CAPS.B, CAPS.A];

export const CAPS_MANIFEST = {
  scope: "/section/caps/",
  axis: { section: "caps" },
  shards: [
    {
      url: "/shards/section/caps/2026/06.json",
      month: "2026-06",
      count: 7,
      parts: [
        "/shards/section/caps/2026/06.3.json",
        "/shards/section/caps/2026/06.2.json",
        "/shards/section/caps/2026/06.json",
      ],
    },
  ],
  total: 7,
  cap: { entries: 3, bytes: 4096 },
};
export const CAPS_SHARDS = {
  // part 1: oldest run (C,B,A), each file internally newest-first
  "/shards/section/caps/2026/06.json": [CAPS.C, CAPS.B, CAPS.A],
  "/shards/section/caps/2026/06.2.json": [CAPS.F, CAPS.E, CAPS.D],
  "/shards/section/caps/2026/06.3.json": [CAPS.G],
};
export const CAPS_STREAM = capsByAge; // G,F,E,D,C,B,A

// ---- back-dated insert across parts: /section/pp/ (2-entry cap) ------------
//
// The hard ORDER-parity case the whole-part concatenation gets wrong. Articles
// are INSERTED A,B,C (published Jun 2, 4, 6) and then a back-dated D (published
// Jun 3) is inserted LAST. The generator cuts parts on INSERTION order, so at a
// 2-entry cap part 1 = [A,B] and part 2 = [C,D]; each part file is then sorted
// internally by display order, giving file 06.json = [B,A] and 06.2.json =
// [C,D]. Concatenating whole parts newest-part-first yields C,D,B,A, but the
// server page renders the GLOBAL display order C,B,D,A (D published before B).
// The refiner must reproduce C,B,D,A, which only the ts-merge (not whole-part
// concatenation) does. ts is derived from published_at so the two never drift.

const ppEntry = (letter, iso, hash) => ({
  slug: "pp-" + letter.toLowerCase(),
  url: "/a/pp-" + letter.toLowerCase() + "-" + hash + "/",
  title: "PP " + letter,
  author: "pp",
  author_label: "pp",
  section: "pp",
  topics: [],
  published_at: iso,
  ts: Math.floor(Date.parse(iso) / 1000),
});

export const PP = {
  A: ppEntry("A", "2026-06-02T09:00:00Z", "aaaaaaaa"),
  B: ppEntry("B", "2026-06-04T09:00:00Z", "bbbbbbbb"),
  C: ppEntry("C", "2026-06-06T09:00:00Z", "cccccccc"),
  D: ppEntry("D", "2026-06-03T09:00:00Z", "dddddddd"),
};

export const BACKDATE_MANIFEST = {
  scope: "/section/pp/",
  axis: { section: "pp" },
  shards: [
    {
      url: "/shards/section/pp/2026/06.json",
      month: "2026-06",
      count: 4,
      parts: [
        "/shards/section/pp/2026/06.2.json",
        "/shards/section/pp/2026/06.json",
      ],
    },
  ],
  total: 4,
  cap: { entries: 2, bytes: 4096 },
};

export const BACKDATE_SHARDS = {
  // part 1 (06.json): earliest-inserted run [A,B], display-sorted -> [B,A]
  "/shards/section/pp/2026/06.json": [PP.B, PP.A],
  // part 2 (06.2.json): next run [C,D] (D back-dated), display-sorted -> [C,D]
  "/shards/section/pp/2026/06.2.json": [PP.C, PP.D],
};
// The server page's global display order (newest published first): the
// back-dated D slots between B and A by publication time.
export const BACKDATE_STREAM = [PP.C, PP.B, PP.D, PP.A];

// ---- body-video poster card: /section/world/ ------------------------------
//
// videoEntry has no metadata.image, so its `image` is the first body video's
// deterministic YouTube poster and `video` is true; the client must rebuild the
// card-has-video class + .card-media-play badge. imageEntry is a plain image card
// (video false) to prove the badge appears only for video entries.

export const videoEntry = {
  slug: "glorieta-clip",
  url: "/a/glorieta-clip-12345678/",
  title: "Glorieta Clip",
  subtitle: "",
  description: "",
  image: "https://i.ytimg.com/vi/RdhnMD40q3Y/hqdefault.jpg",
  video: true,
  author: "glorieta-sadeta",
  author_label: "Glorieta Sadeta",
  avatar: "",
  section: "world",
  topics: [],
  published_at: "2026-06-09T09:00:00Z",
  ts: 1780995600,
};
export const imageEntry = {
  slug: "plain-image",
  url: "/a/plain-image-87654321/",
  title: "Plain Image",
  subtitle: "",
  description: "",
  image: "/media/" + "a".repeat(64) + ".png",
  video: false,
  author: "glorieta-sadeta",
  author_label: "Glorieta Sadeta",
  avatar: "",
  section: "world",
  topics: [],
  published_at: "2026-06-08T09:00:00Z",
  ts: 1780909200,
};
export const VIDEO_MANIFEST = {
  scope: "/section/world/",
  axis: { section: "world" },
  shards: [{ url: "/shards/section/world/2026/06.json", month: "2026-06", count: 2 }],
  total: 2,
  cap: { entries: 500, bytes: 204800 },
};
export const VIDEO_SHARDS = {
  "/shards/section/world/2026/06.json": [videoEntry, imageEntry],
};
export const VIDEO_STREAM = [videoEntry, imageEntry];

// ---- DOM builders ----------------------------------------------------------

function itemHTML(e) {
  const month = e.published_at.slice(0, 7);
  const day = e.published_at.slice(0, 10);
  const topicsAttr = (e.topics || []).join(" ");
  const topicsList = (e.topics || [])
    .map((t) => `<li><a class="topic-link" href="/topic/${t}/">${t}</a></li>`)
    .join("");
  const topicsUL = topicsList
    ? `<ul class="topics" data-topics>${topicsList}</ul>`
    : "";
  return `
<li class="article-item" data-author="${e.author}" data-section="${e.section}" data-topics="${topicsAttr}" data-month="${month}">
<article class="card">
<h2 class="card-title"><a class="card-link" href="${e.url}">${e.title}</a></h2>
<p class="byline">By <a class="author-link" href="/author/${e.author}/" data-author>${e.author_label}</a> in <a class="section-link" href="/section/${e.section}/" data-section>${e.section}</a></p>
<time class="published" datetime="${e.published_at}">${day}</time>
${topicsUL}
</article>
</li>`;
}

function monthsNavHTML(months, base) {
  if (!months || months.length === 0) return "";
  const links = months
    .map(
      (m) =>
        `<a class="month-link" href="${base}${m.replace("-", "/")}/" data-month="${m}">${m}</a>`
    )
    .join("\n");
  return `<nav class="months" data-months aria-label="Months">\n${links}\n</nav>`;
}

// landingHTML builds a landing fragment with the inline #cnz-manifest fast path.
export function landingHTML({ heading, items, months, monthBase = "/", manifest }) {
  return `
<h1 class="listing-heading">${heading}</h1>
<section class="facet-controls" data-facets aria-label="Refine">
<ol class="article-list" data-articles>
${items.map(itemHTML).join("\n")}
</ol>
${monthsNavHTML(months, monthBase)}
</section>
<script type="application/json" id="cnz-manifest">${JSON.stringify(manifest)}</script>`;
}

// deepHTML builds a deep/sealed fragment: no inline manifest, only the
// <link rel="manifest"> the refiner must fetch.
export function deepHTML({ heading, items, manifestURL }) {
  return `
<link rel="manifest" href="${manifestURL}">
<h1 class="listing-heading">${heading}</h1>
<section class="facet-controls" data-facets aria-label="Refine">
<ol class="article-list" data-articles>
${items.map(itemHTML).join("\n")}
</ol>
</section>`;
}

// articleHTML builds a permalink fragment (no facet controls): the refiner must
// do nothing here.
export function articleHTML(e) {
  return `
<article class="article" data-article>
<h1 class="article-title">${e.title}</h1>
<p class="byline">By <a class="author-link" href="/author/${e.author}/" rel="author" data-author>${e.author_label}</a> in <a class="section-link" href="/section/${e.section}/" data-section>${e.section}</a></p>
<time class="published" datetime="${e.published_at}">${e.published_at.slice(0, 10)}</time>
<div class="body article-body"><p>Body.</p></div>
</article>`;
}

// ---- MSW helpers -----------------------------------------------------------

// serveJSON registers GET handlers for a {pathSuffix: data} map. The returned
// counter records how many times each path was requested, so a test can assert
// a fetch actually happened (or did not).
export function serveJSON(map) {
  const hits = {};
  const handlers = Object.entries(map).map(([path, data]) => {
    hits[path] = 0;
    return http.get("*" + path, () => {
      hits[path] += 1;
      return HttpResponse.json(data);
    });
  });
  server.use(...handlers);
  return hits;
}
