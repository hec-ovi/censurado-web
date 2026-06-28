// Censurado client refiner (Tier-B), shipped at the stable URL /assets/app.js.
//
// Progressive enhancement over the frozen Phase-4A listing DOM: with JS off,
// every facet is a real <a href> to a pre-built scope page (the source of
// truth). With JS on, this module adds a tactile chip panel that filters the
// list in place and history.pushState()s to that same pre-built URL, so reload
// and back/forward always land on a real page. On any fetch/parse failure it
// degrades to letting the original links navigate. There is NO free-text input
// anywhere: refinement is discrete facet toggles only.
//
// Data flow on a selection:
//   manifest (inline #cnz-manifest on a landing, else fetched from
//   <link rel="manifest">)  ->  fetch the scope's month shards and concatenate
//   them into one newest-published-first stream  ->  filter by exact slug
//   equality on author/section/topic/month  ->  rewrite [data-articles] with
//   the same .article-item/.card markup  ->  pushState to the facet's pre-built
//   URL (read from the matching server-rendered link, never synthesized).
//
// The module exports initRefine(root) and initLiveRefresh(root) so the exact
// shipped bytes are unit tested against DOM fragments, and also
// auto-initializes on the real page.

// "topic" is intentionally absent: the Tema filter bar is removed from the
// listings (topics live on the author profile's own Temas nav instead).
const FACET_TYPES = ["author", "section", "month"];
const FACET_LABEL = { author: "Autor", section: "Sección", topic: "Tema", month: "Mes" };

// absURL resolves a root-relative URL against the document base. Browsers do
// this implicitly for fetch(); resolving explicitly also keeps the same code
// working under a Node test runtime whose fetch rejects relative URLs.
function absURL(u) {
  try {
    return new URL(u, document.baseURI).href;
  } catch {
    return u;
  }
}

// monthOf derives a "YYYY-MM" bucket from a shard entry's published_at.
function monthOf(entry) {
  return (entry.published_at || "").slice(0, 7);
}

function normalizedPath(u) {
  try {
    return new URL(u, document.baseURI).pathname;
  } catch {
    return u || "";
  }
}

// entryMatches mirrors the server membership test exactly: slug equality on the
// routing axes, array membership for topics, published-month for month.
function entryMatches(entry, type, value) {
  switch (type) {
    case "author":
      return entry.author === value;
    case "section":
      return entry.section === value;
    case "topic":
      return Array.isArray(entry.topics) && entry.topics.includes(value);
    case "month":
      return monthOf(entry) === value;
    default:
      return false;
  }
}

// readInlineManifest parses the landing fast-path <script id="cnz-manifest">.
function readInlineManifest(root) {
  const el = root.querySelector("#cnz-manifest");
  if (!el) return null;
  try {
    return JSON.parse(el.textContent);
  } catch {
    return null;
  }
}

// loadScopeEntries fetches every shard for a scope and MERGES them into a single
// newest-published-first stream that byte-matches the server page's global
// display order, regardless of how the generator cut shard parts.
//
// Why a merge and not a plain whole-part concatenation: the generator seals
// shard PARTS on INSERTION order (the seal axis, so low parts stay byte-stable)
// and sorts each part's file INTERNALLY by display order (ts DESC, id DESC).
// When insertion order equals publication order, concatenating shards (newest
// month first) then parts (newest part first) already reproduces the page order.
// But a BACK-DATED insert (inserted late, published early) pushes a
// publication-older entry into a higher/newer part, so part boundaries cross
// publication time and whole-part concatenation interleaves wrong (e.g. server
// renders C,B,D,A but concat yields C,D,B,A). So we collect every entry across
// all shards/parts and STABLE-sort the full stream by ts DESC.
//
// The server's id tie-break for equal ts is not in the serialized projection,
// and does not need to be: within a part, equal-ts entries are already in
// id-DESC order (the file is display-sorted); across parts, we collect newest
// part first, and because parts are cut on insertion order (i.e. id order) a
// higher part holds the larger ids, so equal-ts entries are collected id-DESC
// there too. A stable sort preserves that collected pre-order for equal ts, so
// the result equals the server's (ts DESC, id DESC) order exactly.
async function loadScopeEntries(manifest) {
  const out = [];
  for (const ref of manifest.shards || []) {
    const urls = ref.parts && ref.parts.length ? ref.parts : [ref.url];
    for (const url of urls) {
      const res = await fetch(absURL(url));
      if (!res.ok) throw new Error("shard fetch failed: " + url);
      const part = await res.json();
      for (const entry of part) out.push(entry);
    }
  }
  // Array.prototype.sort has been stable since ES2019 (V8/Node included), so
  // equal-ts entries keep the id-DESC pre-order described above.
  out.sort((a, b) => (b.ts || 0) - (a.ts || 0));
  return out;
}

// el is a tiny element builder; class/attribute pairs, no innerHTML.
function el(tag, attrs) {
  const node = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (k === "class") node.className = v;
      else node.setAttribute(k, v);
    }
  }
  return node;
}

function fallbackFacetURL(type, slug) {
  if (type === "author") return "/author/" + slug + "/";
  if (type === "section") return "/section/" + slug + "/";
  if (type === "topic") return "/topic/" + slug + "/";
  return null;
}

// articleItemFromEntry rebuilds the server card structure from a body-free shard
// entry: optional hero <figure> only when the entry carries an image (no
// placeholder box), a .card-body wrapping kicker/title/subtitle/signature, and
// NO per-card topic list (topics filter via the data-topics attribute only). The
// .article-item / .card-link / data-author / .author-link hooks are preserved
// because the refiner and its tests read them.
function articleItemFromEntry(e, helpers) {
  const labelFor = helpers.labelFor;
  const facetURL = helpers.facetURL;
  const avatarFor = helpers.avatarFor;

  const li = el("li", { class: "article-item" });
  li.dataset.author = e.author;
  li.dataset.authorAvatar = avatarFor(e.author);
  li.dataset.section = e.section;
  li.dataset.topics = (e.topics || []).join(" ");
  li.dataset.month = monthOf(e);

  const hasImage = !!e.image;
  const card = el("article", { class: "card " + (hasImage ? "card-has-media" : "card-textonly") });

  if (hasImage) {
    const figure = el("figure", { class: "card-media" });
    const img = el("img", { src: e.image, alt: e.title || "", loading: "lazy", decoding: "async" });
    figure.appendChild(img);
    card.appendChild(figure);
  }

  const body = el("div", { class: "card-body" });

  const kicker = el("div", { class: "card-kicker" });
  const sectionLink = el("a", {
    class: "section-link",
    href: facetURL("section", e.section),
    "data-section": "",
  });
  sectionLink.textContent = labelFor("section", e.section, e.section);
  const time = el("time", { class: "published", datetime: e.published_at });
  time.textContent = (e.published_at || "").slice(0, 10);
  kicker.append(sectionLink, time);

  const h2 = el("h2", { class: "card-title" });
  const cardLink = el("a", { class: "card-link", href: e.url });
  cardLink.textContent = e.title;
  h2.appendChild(cardLink);

  body.append(kicker, h2);

  if (e.subtitle) {
    const sub = el("p", { class: "card-subtitle" });
    sub.textContent = e.subtitle;
    body.appendChild(sub);
  }

  // The standfirst/summary. Present on every card to match the server markup;
  // desktop CSS hides it on image cards and turns it into the fill-to-row clamp on
  // text cards (see initCardFill). Mobile shows it in full.
  if (e.description) {
    const desc = el("p", { class: "card-description" });
    desc.textContent = e.description;
    body.appendChild(desc);
  }

  const meta = el("div", { class: "card-meta" });
  const avatar = el("span", { class: "author-avatar", "aria-hidden": "true" });
  // Prefer the avatar carried by the shard entry (works for any author, even one
  // absent from the page's first batch); fall back to the DOM-derived map.
  const avatarSrc = e.avatar || avatarFor(e.author);
  if (avatarSrc) {
    const img = el("img", { src: avatarSrc, alt: "", loading: "lazy", decoding: "async" });
    avatar.appendChild(img);
  } else {
    const fallback = el("span");
    fallback.textContent = initialFor(labelFor("author", e.author, e.author_label));
    avatar.appendChild(fallback);
  }

  const byline = el("p", { class: "byline" });
  const authorLink = el("a", {
    class: "author-link",
    href: facetURL("author", e.author),
    "data-author": "",
  });
  authorLink.textContent = labelFor("author", e.author, e.author_label);
  byline.appendChild(authorLink);
  meta.append(avatar, byline);
  body.appendChild(meta);

  card.appendChild(body);
  li.appendChild(card);
  return li;
}

class Refiner {
  constructor(root, facets, list) {
    this.root = root;
    this.facets = facets;
    this.list = list;
    this.manifest = null;
    this.basePath = "/";
    this.entries = null; // full scope stream once loaded
    this.entriesLoaded = false;
    this.activeFacet = null; // { type, value }
    this.labelMaps = { author: {}, section: {}, topic: {}, month: {} };
    this.chips = [];
    this.heading = root.querySelector(".listing-heading");
    this.originalListHTML = list.innerHTML; // server-rendered base view
    this._onPop = () => this.onPopState();
  }

  // init resolves the manifest, derives label maps, builds the chip panel, and
  // wires history. Returns the controller, or null when it cannot enhance.
  async init() {
    if (this.facets.hasAttribute("data-refine-init")) return null;

    let manifest = readInlineManifest(this.root);
    if (!manifest) {
      const link =
        this.root.querySelector('link[rel="manifest"]') ||
        document.querySelector('link[rel="manifest"]');
      if (!link) return null;
      try {
        const res = await fetch(absURL(link.getAttribute("href")));
        if (!res.ok) throw new Error("manifest fetch failed");
        manifest = await res.json();
      } catch {
        return null; // degrade: server links keep working
      }
    }
    this.manifest = manifest;
    this.basePath = manifest.scope || "/";

    this.buildLabelMaps();
    this.buildPanel();
    this.facets.setAttribute("data-refine-init", "");
    if (this.heading && !this.heading.hasAttribute("tabindex")) {
      this.heading.setAttribute("tabindex", "-1");
    }
    window.addEventListener("popstate", this._onPop);
    return this;
  }

  // buildLabelMaps records, per facet value, the display label and the exact
  // pre-built href from the server-rendered links. These hrefs are the only
  // source of truth for pushState targets.
  buildLabelMaps() {
    const items = this.list.querySelectorAll(".article-item");
    for (const li of items) {
      const author = li.dataset.author;
      const section = li.dataset.section;
      const avatar = li.dataset.authorAvatar || "";
      const authorLink = li.querySelector(".author-link");
      const sectionLink = li.querySelector(".section-link");
      if (author && authorLink && !(author in this.labelMaps.author)) {
        this.labelMaps.author[author] = {
          label: authorLink.textContent.trim(),
          url: authorLink.getAttribute("href"),
          avatar,
        };
      }
      if (section && sectionLink && !(section in this.labelMaps.section)) {
        this.labelMaps.section[section] = {
          label: sectionLink.textContent.trim(),
          url: sectionLink.getAttribute("href"),
        };
      }
      const topicSlugs = (li.dataset.topics || "").split(/\s+/).filter(Boolean);
      const topicLinks = li.querySelectorAll(".topic-link");
      topicSlugs.forEach((slug, i) => {
        const link = topicLinks[i];
        if (link && !(slug in this.labelMaps.topic)) {
          this.labelMaps.topic[slug] = {
            label: link.textContent.trim(),
            url: link.getAttribute("href"),
          };
        }
      });
    }
    // Months: the server-rendered months navigator carries data-month + href.
    const monthLinks = this.root.querySelectorAll(".month-link[data-month]");
    for (const m of monthLinks) {
      const slug = m.dataset.month;
      if (slug && !(slug in this.labelMaps.month)) {
        this.labelMaps.month[slug] = {
          label: m.textContent.trim(),
          url: m.getAttribute("href"),
        };
      }
    }
  }

  // facetValues lists a facet's values in DOM/manifest order (no duplicates).
  facetValues(type) {
    if (type === "month") {
      // Derive from the manifest shard list (newest-month-first).
      const seen = new Set();
      const out = [];
      for (const ref of this.manifest.shards || []) {
        if (ref.month && !seen.has(ref.month)) {
          seen.add(ref.month);
          out.push(ref.month);
        }
      }
      return out;
    }
    const seen = new Set();
    const out = [];
    for (const li of this.list.querySelectorAll(".article-item")) {
      let vals;
      if (type === "topic") vals = (li.dataset.topics || "").split(/\s+/).filter(Boolean);
      else vals = [li.dataset[type]].filter(Boolean);
      for (const v of vals) {
        if (!seen.has(v)) {
          seen.add(v);
          out.push(v);
        }
      }
    }
    return out;
  }

  // buildPanel constructs the chip groups and the live-status region, inserted
  // ahead of the article list. It never removes the server-rendered fallbacks.
  buildPanel() {
    const panel = el("div", { class: "facet-panel", role: "group" });
    panel.setAttribute("aria-label", "Filtrar por faceta");

    for (const type of FACET_TYPES) {
      const values = this.facetValues(type).filter((v) => this.facetURL(type, v));
      if (values.length === 0) continue;

      const group = el("div", { class: "facet-group" });
      group.setAttribute("data-facet-group", type);
      const labelId = "cnz-facet-" + type;
      const groupLabel = el("p", { class: "facet-group-label", id: labelId });
      groupLabel.textContent = FACET_LABEL[type];

      const ul = el("ul", { class: "facet-chips" });
      ul.setAttribute("aria-labelledby", labelId);
      for (const value of values) {
        const li = el("li");
        const chip = el("button", {
          type: "button",
          class: "facet-chip",
          "aria-pressed": "false",
          "data-facet-type": type,
          "data-facet-value": value,
          "data-href": this.facetURL(type, value),
          "aria-label": FACET_LABEL[type] + " " + this.labelFor(type, value, value),
        });
        chip.textContent = this.labelFor(type, value, value);
        chip.addEventListener("click", () => this.onChip(chip));
        this.chips.push(chip);
        li.appendChild(chip);
        ul.appendChild(li);
      }
      group.append(groupLabel, ul);
      panel.appendChild(group);
    }

    this.clearBtn = el("button", { type: "button", class: "facet-clear", hidden: "" });
    this.clearBtn.textContent = "Quitar filtro";
    this.clearBtn.addEventListener("click", () => this.clearFilter(true));
    panel.appendChild(this.clearBtn);

    this.statusEl = el("p", { class: "refine-status", role: "status", "aria-live": "polite" });
    panel.appendChild(this.statusEl);

    this.facets.insertBefore(panel, this.list);
    this.panel = panel;
  }

  // facetURL returns the exact pre-built href for a value, preferring the
  // server-rendered link. Byline/topic links inside rebuilt cards fall back to
  // the canonical single-facet path, which the generator always pre-builds for
  // any slug that appears in shard data.
  facetURL(type, slug) {
    const m = this.labelMaps[type] && this.labelMaps[type][slug];
    if (m && m.url) return m.url;
    if (type === "author") return "/author/" + slug + "/";
    if (type === "section") return "/section/" + slug + "/";
    if (type === "topic") return "/topic/" + slug + "/";
    return null; // month with no server link: not safely pre-buildable here
  }

  labelFor(type, slug, fallback) {
    const m = this.labelMaps[type] && this.labelMaps[type][slug];
    return (m && m.label) || fallback || slug;
  }

  avatarFor(slug) {
    const m = this.labelMaps.author && this.labelMaps.author[slug];
    return (m && m.avatar) || "";
  }

  onChip(chip) {
    if (chip.disabled || chip.getAttribute("aria-disabled") === "true") return;
    const type = chip.dataset.facetType;
    const value = chip.dataset.facetValue;
    if (this.activeFacet && this.activeFacet.type === type && this.activeFacet.value === value) {
      this.clearFilter(true);
      return;
    }
    this.selectFacet(type, value, chip.dataset.href, true);
  }

  // selectFacet loads the scope stream (once), filters by exact slug equality,
  // rewrites the list, and (when push) records the pre-built URL in history.
  async selectFacet(type, value, href, push) {
    const chip = this.chipFor(type, value);
    if (!(await this.ensureEntries())) {
      if (push && href) this.degradeNav(href);
      return;
    }
    const filtered = this.entries.filter((e) => entryMatches(e, type, value));
    if (filtered.length === 0) {
      this.renderEmpty(type, value);
      if (chip) {
        chip.setAttribute("aria-disabled", "true");
        chip.disabled = true;
      }
      this.setPressed(null);
      this.activeFacet = null;
      this.showClear(true);
      this.announce("Ningún artículo coincide con " + FACET_LABEL[type] + " " + value + ".");
      this.focusHeading();
      return;
    }
    this.renderEntries(filtered);
    this.setPressed(chip);
    this.activeFacet = { type, value };
    this.showClear(true);
    if (push && href) this.pushState(href);
    this.announce(
      "Mostrando " +
        filtered.length +
        (filtered.length === 1 ? " artículo" : " artículos") +
        " de " +
        FACET_LABEL[type] +
        " " +
        value +
        "."
    );
    this.focusHeading();
  }

  // ensureEntries lazily fetches and caches the current scope's full stream.
  async ensureEntries() {
    if (this.entriesLoaded) return true;
    try {
      this.entries = await loadScopeEntries(this.manifest);
      this.entriesLoaded = true;
      return true;
    } catch {
      return false;
    }
  }

  clearFilter(push) {
    this.list.innerHTML = this.originalListHTML; // restore exact server view
    this.list.removeAttribute("data-filtered"); // let infinite scroll resume
    this.setPressed(null);
    this.activeFacet = null;
    this.showClear(false);
    if (push) this.pushState(this.basePath);
    this.announce("Filtro quitado. Mostrando todos los artículos.");
    this.focusHeading();
  }

  renderEntries(entries) {
    const frag = document.createDocumentFragment();
    for (const e of entries) frag.appendChild(this.entryToItem(e));
    this.list.replaceChildren(frag);
    this.list.setAttribute("data-filtered", ""); // pause infinite scroll
  }

  // entryToItem rebuilds the frozen .article-item/.card structure (so the CSS
  // applies) with the same slug-form data-* hooks the server emits.
  entryToItem(e) {
    return articleItemFromEntry(e, {
      labelFor: (type, slug, fallback) => this.labelFor(type, slug, fallback),
      facetURL: (type, slug) => this.facetURL(type, slug),
      avatarFor: (slug) => this.avatarFor(slug),
    });
  }

  renderEmpty(type, value) {
    const li = el("li", { class: "empty-state", role: "note" });
    li.textContent =
      'Ningún artículo coincide con ' + FACET_LABEL[type] + ' "' + this.labelFor(type, value, value) + '".';
    this.list.replaceChildren(li);
    this.list.setAttribute("data-filtered", ""); // pause infinite scroll
  }

  chipFor(type, value) {
    return this.chips.find(
      (c) => c.dataset.facetType === type && c.dataset.facetValue === value
    );
  }

  setPressed(active) {
    for (const c of this.chips) {
      c.setAttribute("aria-pressed", c === active ? "true" : "false");
    }
  }

  showClear(show) {
    if (!this.clearBtn) return;
    if (show) this.clearBtn.removeAttribute("hidden");
    else this.clearBtn.setAttribute("hidden", "");
  }

  announce(msg) {
    if (this.statusEl) this.statusEl.textContent = msg;
  }

  focusHeading() {
    if (this.heading) this.heading.focus();
  }

  pushState(href) {
    try {
      window.history.pushState({ cnzRefine: href }, "", href);
    } catch {
      /* history unavailable: filtering still works, just no URL sync */
    }
  }

  degradeNav(href) {
    try {
      window.location.assign(href);
    } catch {
      /* navigation blocked (e.g. test env): leave the page as-is */
    }
  }

  // destroy detaches the history listener (used by tests; harmless on a page
  // that is being torn down anyway).
  destroy() {
    window.removeEventListener("popstate", this._onPop);
  }

  onPopState() {
    const path = decodeURIComponent(window.location.pathname || "/");
    if (path === this.basePath) {
      this.clearFilter(false);
      return;
    }
    for (const chip of this.chips) {
      if (chip.dataset.href === path) {
        this.selectFacet(chip.dataset.facetType, chip.dataset.facetValue, chip.dataset.href, false);
        return;
      }
    }
    // Unknown path (navigated outside our own pushStates): show the base view;
    // a real reload would render the genuine pre-built page for that path.
    this.clearFilter(false);
  }
}

function initialFor(label) {
  const s = (label || "").trim();
  return s ? Array.from(s)[0].toUpperCase() : "?";
}

const LIVE_SENTINEL_URL = "/latest/version.json";
const LIVE_MANIFEST_URL = "/manifest/latest/index.json";

function latestSurface(root) {
  const manifest = readInlineManifest(root);
  if (manifest && manifest.scope) return manifest.scope === "/latest/";
  const path = normalizedPath(window.location.pathname || "/");
  return path === "/" || path === "/latest/";
}

function newestShardURL(manifest) {
  const ref = manifest && Array.isArray(manifest.shards) ? manifest.shards[0] : null;
  if (!ref) return "";
  if (Array.isArray(ref.parts) && ref.parts.length) return ref.parts[0];
  return ref.url || "";
}

function defaultLiveLabel(type, slug, fallback) {
  return fallback || slug || "";
}

class LiveRefresh {
  constructor(root, list, options) {
    this.root = root;
    this.list = list;
    this.doc = options.document || list.ownerDocument || document;
    this.win = this.doc.defaultView || window;
    const fetcher = options.fetch || this.win.fetch || globalThis.fetch;
    this.fetcher = typeof fetcher === "function" ? fetcher.bind(this.win) : null;
    this.setTimer = options.setTimeout || this.win.setTimeout.bind(this.win);
    this.clearTimer = options.clearTimeout || this.win.clearTimeout.bind(this.win);
    const interval = Number(options.intervalMs || 0);
    this.minIntervalMs = interval || Number(options.minIntervalMs || 30000);
    this.maxIntervalMs = interval || Number(options.maxIntervalMs || 60000);
    this.maxBackoffMs = Number(options.maxBackoffMs || 300000);
    this.autoStart = options.autoStart !== false;
    this.timer = null;
    this.errorCount = 0;
    this.inFlight = false;
    this.destroyed = false;
    this.etag = "";
    this.version = "";
    this.pendingEntries = [];
    this.pendingSentinel = null;
    this.banner = null;
    this._onVisibility = () => this.onVisibility();
  }

  async init() {
    if (!this.fetcher || this.list.hasAttribute("data-live-refresh-init")) return null;
    if (!latestSurface(this.root)) return null;
    try {
      const initial = await this.fetchSentinel(false);
      if (!initial || initial.notModified || !initial.v) return null;
      this.commitSentinel(initial);
    } catch {
      return null;
    }
    this.list.setAttribute("data-live-refresh-init", "");
    this.doc.addEventListener("visibilitychange", this._onVisibility);
    if (this.autoStart) this.schedule();
    return this;
  }

  async fetchSentinel(withValidator) {
    const headers = {};
    if (withValidator && this.etag) headers["If-None-Match"] = this.etag;
    const res = await this.fetcher(absURL(LIVE_SENTINEL_URL), {
      headers,
      cache: "no-cache",
    });
    if (res.status === 304) return { notModified: true };
    if (!res.ok) {
      const err = new Error("version sentinel fetch failed");
      err.status = res.status;
      throw err;
    }
    const body = await res.json();
    return {
      etag: res.headers && res.headers.get ? res.headers.get("ETag") || "" : "",
      v: body && body.v,
    };
  }

  async fetchJSON(url) {
    const res = await this.fetcher(absURL(url), { cache: "no-cache" });
    if (!res.ok) throw new Error("live refresh fetch failed: " + url);
    return res.json();
  }

  async fetchNewestEntries() {
    const manifest = await this.fetchJSON(LIVE_MANIFEST_URL);
    const shardURL = newestShardURL(manifest);
    if (!shardURL) return [];
    const entries = await this.fetchJSON(shardURL);
    return Array.isArray(entries) ? entries : [];
  }

  renderedKeys() {
    const keys = new Set();
    for (const link of this.list.querySelectorAll(".article-item .card-link[href]")) {
      keys.add(normalizedPath(link.getAttribute("href")));
    }
    return keys;
  }

  diffNewEntries(entries) {
    const rendered = this.renderedKeys();
    const seen = new Set(rendered);
    const fresh = [];
    for (const entry of entries) {
      const key = entry && entry.url ? normalizedPath(entry.url) : entry && entry.slug;
      if (!key || seen.has(key)) continue;
      seen.add(key);
      fresh.push(entry);
    }
    return fresh;
  }

  async checkNow() {
    if (this.destroyed || this.inFlight || this.pendingEntries.length || this.isHidden()) return;
    this.inFlight = true;
    try {
      const sentinel = await this.fetchSentinel(true);
      if (sentinel.notModified) {
        this.errorCount = 0;
        return;
      }
      if (!sentinel.v || sentinel.v === this.version) {
        this.commitSentinel(sentinel);
        this.errorCount = 0;
        return;
      }
      const entries = await this.fetchNewestEntries();
      const fresh = this.diffNewEntries(entries);
      if (fresh.length) this.showBanner(fresh, sentinel);
      else this.commitSentinel(sentinel);
      this.errorCount = 0;
    } catch (err) {
      this.errorCount += 1;
    } finally {
      this.inFlight = false;
      if (this.autoStart) this.schedule();
    }
  }

  showBanner(entries, sentinel) {
    this.pendingEntries = entries;
    this.pendingSentinel = sentinel;
    if (!this.banner) {
      this.banner = el("button", {
        type: "button",
        class: "live-refresh-banner",
        "data-live-refresh-banner": "",
        "aria-live": "polite",
      });
      this.banner.addEventListener("click", () => this.applyPending());
      this.list.parentNode.insertBefore(this.banner, this.list);
    }
    // A plain call to action, no count: the reader taps to pull in whatever is new.
    this.banner.textContent = "Actualizar nuevos artículos";
    this.banner.removeAttribute("hidden");
  }

  applyPending() {
    const entries = this.pendingEntries.filter(
      (entry) => entry && entry.url && !this.renderedKeys().has(normalizedPath(entry.url))
    );
    if (entries.length) {
      const frag = this.doc.createDocumentFragment();
      for (const entry of entries) {
        frag.appendChild(
          articleItemFromEntry(entry, {
            labelFor: defaultLiveLabel,
            facetURL: fallbackFacetURL,
            avatarFor: () => "",
          })
        );
      }
      this.list.prepend(frag);
    }
    if (this.pendingSentinel) this.commitSentinel(this.pendingSentinel);
    this.pendingEntries = [];
    this.pendingSentinel = null;
    if (this.banner) this.banner.setAttribute("hidden", "");
  }

  commitSentinel(sentinel) {
    if (sentinel.etag) this.etag = sentinel.etag;
    if (sentinel.v) this.version = sentinel.v;
  }

  isHidden() {
    return this.doc.visibilityState === "hidden";
  }

  nextDelay() {
    if (this.errorCount > 0) {
      const base = this.minIntervalMs * Math.pow(2, this.errorCount - 1);
      return Math.min(this.maxBackoffMs, Math.max(this.minIntervalMs, base));
    }
    if (this.maxIntervalMs <= this.minIntervalMs) return this.minIntervalMs;
    return this.minIntervalMs + Math.floor(Math.random() * (this.maxIntervalMs - this.minIntervalMs));
  }

  schedule() {
    if (this.destroyed || this.isHidden()) return;
    this.clearSchedule();
    this.timer = this.setTimer(() => {
      this.timer = null;
      this.checkNow();
    }, this.nextDelay());
  }

  clearSchedule() {
    if (this.timer) {
      this.clearTimer(this.timer);
      this.timer = null;
    }
  }

  onVisibility() {
    if (this.destroyed) return;
    if (this.isHidden()) {
      this.clearSchedule();
      return;
    }
    if (this.autoStart) this.schedule();
  }

  destroy() {
    this.destroyed = true;
    this.clearSchedule();
    this.doc.removeEventListener("visibilitychange", this._onVisibility);
    if (this.banner) this.banner.remove();
  }
}

// ---- infinite scroll (Tier-B) ---------------------------------------------
// Progressive enhancement of a listing LANDING: with JS off the static page
// shows its first batch plus a real <nav class="pager"> to older pages. With JS
// on, this hides the pager and, as the reader nears the bottom, fetches the next
// batch of older entries from the same per-month shards the refiner already uses,
// shows a brief "Cargando" beat (for personality), then appends them, inserting a
// full-width day separator whenever the date rolls back to a previous day. It
// dedupes by permalink so it never double-renders a card the server (or
// live-refresh) already placed, and it pauses while a facet filter owns the list.
// Only landings (inline #cnz-manifest) are enhanced; sealed pages keep their
// static pager.

const MONTHS_ES = [
  "enero", "febrero", "marzo", "abril", "mayo", "junio",
  "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
];

// formatDayES turns a "YYYY-MM-DD" prefix into "D de mes de YYYY" WITHOUT parsing
// a Date, so it is timezone-stable and identical under the test runtime.
function formatDayES(isoDayStr) {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(isoDayStr || "");
  if (!m) return isoDayStr || "";
  const month = MONTHS_ES[parseInt(m[2], 10) - 1] || "";
  return parseInt(m[3], 10) + " de " + month + " de " + m[1];
}

function isoDay(s) {
  return (s || "").slice(0, 10);
}

// cardDay reads the "YYYY-MM-DD" of an existing .article-item from its
// <time class="published" datetime="..."> (server and client cards both carry it).
function cardDay(li) {
  const t = li.querySelector("time.published");
  const dt = (t && (t.getAttribute("datetime") || t.textContent)) || "";
  return isoDay(dt);
}

class InfiniteScroll {
  constructor(root, list, manifest, options) {
    this.root = root;
    this.list = list;
    this.manifest = manifest;
    this.doc = options.document || list.ownerDocument || document;
    this.win = this.doc.defaultView || window;
    const fetcher = options.fetch || this.win.fetch || globalThis.fetch;
    this.fetcher = typeof fetcher === "function" ? fetcher.bind(this.win) : null;
    this.batchSize = Math.max(1, Number(options.batchSize || 6));
    this.delayMs = options.delayMs == null ? 500 : Number(options.delayMs); // brief "Cargando" beat
    this.stepMs = options.stepMs == null ? 0 : Number(options.stepMs); // 0 = reveal the batch at once (no top-to-bottom "feed" flicker)
    this.setTimer = options.setTimeout || this.win.setTimeout.bind(this.win);
    this.wait = options.wait || ((ms) => new Promise((r) => this.setTimer(r, ms)));
    this.observeFn = typeof options.observe === "function" ? options.observe : null;
    this.entries = null;
    this.entriesLoaded = false;
    this.loading = false;
    this.done = false;
    this.maps = { author: {}, section: {} };
    this.tail = null;
    this.loader = null;
    this.sentinel = null;
    this.observer = null;
  }

  // init enhances only when there is genuinely more to load AND a scroll trigger
  // is available; otherwise it returns null and leaves the static pager fallback.
  async init() {
    if (!this.fetcher) return null;
    if (this.list.hasAttribute("data-infinite-init")) return null;
    const total = Number(this.manifest && this.manifest.total) || 0;
    const shown = this.list.querySelectorAll(".article-item").length;
    if (total <= shown) return null;
    const canObserve =
      this.observeFn || this.win.IntersectionObserver || globalThis.IntersectionObserver;
    if (!canObserve) return null; // no scroll trigger: keep the static pager

    this.list.setAttribute("data-infinite-init", "");
    this.buildLabelMaps();
    this.decorateInitial();
    this.buildTail();
    this.armObserver();
    this.removePager();
    return this;
  }

  // decorateInitial inserts day separators among the server-rendered cards, so a
  // date rollover already visible on first paint reads the same as one reached by
  // scrolling. The server now renders these separators on every scope, so this is
  // idempotent: it only inserts one where the date rolls back AND no separator is
  // already sitting before the card (a no-JS / older-cache page, or the test DOM).
  // Runs after the refiner has captured its clean base view, so a facet clear
  // restores the plain server list (separators re-appear on reload).
  decorateInitial() {
    let lastDay = "";
    for (const li of [...this.list.querySelectorAll(".article-item")]) {
      const day = cardDay(li);
      if (lastDay && day && day !== lastDay) {
        const prev = li.previousElementSibling;
        const hasSep = prev && prev.classList && prev.classList.contains("day-separator");
        if (!hasSep) this.list.insertBefore(this.daySeparator(day), li);
      }
      lastDay = day;
    }
  }

  // buildLabelMaps records, per author/section, the display label, the pre-built
  // href, and the avatar from the server-rendered cards, so appended cards reuse
  // the exact same byline/section link the server emits.
  buildLabelMaps() {
    for (const li of this.list.querySelectorAll(".article-item")) {
      const a = li.dataset.author;
      const s = li.dataset.section;
      const avatar = li.dataset.authorAvatar || "";
      const authorLink = li.querySelector(".author-link");
      const sectionLink = li.querySelector(".section-link");
      if (a && authorLink && !(a in this.maps.author)) {
        this.maps.author[a] = {
          label: authorLink.textContent.trim(),
          url: authorLink.getAttribute("href"),
          avatar,
        };
      }
      if (s && sectionLink && !(s in this.maps.section)) {
        this.maps.section[s] = {
          label: sectionLink.textContent.trim(),
          url: sectionLink.getAttribute("href"),
        };
      }
    }
  }

  helpers() {
    const maps = this.maps;
    return {
      labelFor: (type, slug, fallback) =>
        (maps[type] && maps[type][slug] && maps[type][slug].label) || fallback || slug,
      facetURL: (type, slug) => {
        const m = maps[type] && maps[type][slug];
        if (m && m.url) return m.url;
        return fallbackFacetURL(type, slug);
      },
      avatarFor: (slug) => (maps.author[slug] && maps.author[slug].avatar) || "",
    };
  }

  buildTail() {
    this.tail = el("div", { class: "infinite-tail", "data-infinite-tail": "" });
    this.loader = el("div", {
      class: "infinite-loader",
      role: "status",
      "aria-live": "polite",
      hidden: "",
    });
    const spinner = el("span", { class: "infinite-spinner", "aria-hidden": "true" });
    const loaderLabel = el("span", { class: "infinite-loader-label" });
    loaderLabel.textContent = "Cargando";
    this.loader.append(spinner, loaderLabel);
    this.sentinel = el("div", {
      class: "infinite-sentinel",
      "data-infinite-sentinel": "",
      "aria-hidden": "true",
    });
    this.tail.append(this.loader, this.sentinel);
    // Live INSIDE the article list, as its last child: this keeps the loader in
    // the content column (centered under the articles) and out of the portal grid
    // entirely, so it can never disturb the "Lo más leído" rail in column 2. New
    // cards are inserted before it.
    this.list.appendChild(this.tail);
  }

  armObserver() {
    if (this.observeFn) {
      this.observeFn(this.sentinel, () => this.loadMore());
      return;
    }
    const IO = this.win.IntersectionObserver || globalThis.IntersectionObserver;
    if (!IO) return;
    this.observer = new IO(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            this.loadMore();
            break;
          }
        }
      },
      { root: null, rootMargin: "600px 0px", threshold: 0 }
    );
    this.observer.observe(this.sentinel);
  }

  // removePager deletes the page-number nav entirely: infinite scroll is the
  // sole pagination when JS is on (the `hidden` attribute alone does not hide it,
  // since `.pager { display: flex }` outranks it). The static pages still exist
  // for crawlers and no-JS, reachable via the sitemap and <head> rel=prev/next.
  removePager() {
    const pager = this.root.querySelector("[data-pager]");
    if (pager) pager.remove();
  }

  shownKeys() {
    const keys = new Set();
    for (const link of this.list.querySelectorAll(".article-item .card-link[href]")) {
      keys.add(normalizedPath(link.getAttribute("href")));
    }
    return keys;
  }

  hasMore() {
    if (!this.entries) return true;
    const shown = this.shownKeys();
    return this.entries.some((e) => e && e.url && !shown.has(normalizedPath(e.url)));
  }

  nextBatch() {
    const shown = this.shownKeys();
    const out = [];
    for (const e of this.entries) {
      if (out.length >= this.batchSize) break;
      const key = e && e.url ? normalizedPath(e.url) : null;
      if (!key || shown.has(key)) continue;
      out.push(e);
    }
    return out;
  }

  // loadMore appends the next batch of not-yet-shown entries with a visible
  // loading beat and day separators. No-op while a filter owns the list, while a
  // batch is in flight, or once the stream is exhausted.
  async loadMore() {
    if (this.loading || this.done) return;
    if (this.list.hasAttribute("data-filtered")) return;
    if (this.entriesLoaded && !this.hasMore()) {
      this.finish();
      return;
    }
    this.loading = true;
    this.showLoader(true);
    try {
      if (!this.entriesLoaded) {
        try {
          this.entries = await loadScopeEntries(this.manifest);
          this.entriesLoaded = true;
        } catch {
          return; // a later scroll may retry; keep the static content intact
        }
      }
      const next = this.nextBatch();
      if (next.length === 0) {
        this.finish();
        return;
      }
      await this.wait(this.delayMs);
      this.showLoader(false); // brief beat over: reveal the batch gradually
      await this.appendBatch(next);
      if (!this.hasMore()) this.finish();
    } finally {
      this.showLoader(false);
      this.loading = false;
    }
  }

  // appendBatch reveals one card at a time (a short step between each) so the list
  // grows gradually instead of jumping by a whole batch; each card and day
  // separator fades in via .cnz-enter.
  async appendBatch(entries) {
    const h = this.helpers();
    let lastDay = this.lastCardDay();
    for (const e of entries) {
      const day = isoDay(e.published_at);
      if (day && day !== lastDay) {
        const sep = this.daySeparator(day);
        sep.classList.add("cnz-enter");
        this.list.insertBefore(sep, this.tail);
        lastDay = day;
      }
      const card = articleItemFromEntry(e, h);
      card.classList.add("cnz-enter");
      this.list.insertBefore(card, this.tail);
      await this.wait(this.stepMs);
    }
  }

  lastCardDay() {
    const items = this.list.querySelectorAll(".article-item");
    return items.length ? cardDay(items[items.length - 1]) : "";
  }

  daySeparator(day) {
    const li = el("li", { class: "day-separator", role: "separator", "aria-label": formatDayES(day) });
    const span = el("span", { class: "day-separator-label" });
    span.textContent = formatDayES(day);
    li.appendChild(span);
    return li;
  }

  showLoader(on) {
    if (!this.loader) return;
    if (on) this.loader.removeAttribute("hidden");
    else this.loader.setAttribute("hidden", "");
  }

  finish() {
    this.done = true;
    this.showLoader(false);
    if (this.observer && this.observer.disconnect) this.observer.disconnect();
    if (this.sentinel) this.sentinel.setAttribute("hidden", "");
  }

  destroy() {
    if (this.observer && this.observer.disconnect) this.observer.disconnect();
    if (this.tail) this.tail.remove();
  }
}

function initTheme(root = document) {
  const controls = root.querySelectorAll("[data-theme-choice]");
  if (!controls.length) return;

  const stored = (() => {
    try {
      return localStorage.getItem("cnz-theme") || "system";
    } catch {
      return "system";
    }
  })();

  const apply = (choice) => {
    const mode = choice === "light" || choice === "dark" ? choice : "system";
    if (mode === "system") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.dataset.theme = mode;
    for (const control of controls) {
      control.setAttribute("aria-pressed", String(control.dataset.themeChoice === mode));
    }
    try {
      localStorage.setItem("cnz-theme", mode);
    } catch {
      /* storage unavailable: the current page still updates */
    }
  };

  for (const control of controls) {
    control.addEventListener("click", () => apply(control.dataset.themeChoice));
  }
  apply(stored);
}

function initMenu(root = document) {
  const header = root.querySelector("[data-site-header]");
  const toggle = root.querySelector("[data-menu-toggle]");
  const panel = root.querySelector("[data-menu-panel]");
  if (!header || !toggle || !panel) return;

  const setOpen = (open) => {
    header.dataset.menuOpen = String(open);
    toggle.setAttribute("aria-expanded", String(open));
  };

  header.dataset.menuReady = "true";
  markCurrentNav(panel);
  toggle.addEventListener("click", () => setOpen(header.dataset.menuOpen !== "true"));
  panel.addEventListener("click", (event) => {
    const isMobile =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(max-width: 42rem)").matches;
    if (isMobile && event.target.closest("a")) setOpen(false);
  });

  setOpen(false);
}

function markCurrentNav(panel) {
  const links = Array.from(panel.querySelectorAll(".site-nav-link[href]"));
  const current = normalizedPath(window.location.pathname || "/");
  let active = null;
  let activeLen = -1;
  for (const link of links) {
    const href = normalizedPath(link.getAttribute("href"));
    const matches =
      href === current ||
      (href === "/latest/" && current === "/") ||
      (href !== "/" && current.startsWith(href));
    if (matches && href.length > activeLen) {
      active = link;
      activeLen = href.length;
    }
  }
  for (const link of links) {
    if (link === active) {
      link.setAttribute("aria-current", "page");
      link.dataset.current = "true";
    } else {
      link.removeAttribute("aria-current");
      delete link.dataset.current;
    }
  }
}

// initRefine enhances one listing root. Returns the controller, or null on an
// article page / non-listing fragment / unrecoverable manifest failure.
export async function initRefine(root = document) {
  const scope = root || document;
  const facets = scope.querySelector("[data-facets]");
  const list = scope.querySelector("[data-articles]");
  if (!facets || !list) return null; // not a listing page: do nothing
  const refiner = new Refiner(scope, facets, list);
  return refiner.init();
}

export async function initLiveRefresh(root = document, options = {}) {
  const scope = root || document;
  const list = scope.querySelector("[data-articles]");
  if (!list) return null;
  const live = new LiveRefresh(scope, list, options);
  return live.init();
}

// initInfiniteScroll enhances a listing LANDING (inline #cnz-manifest) with
// observer-driven "load more" over the scope's shards. Returns the controller,
// or null on a non-listing / sealed page / when everything already fits.
export async function initInfiniteScroll(root = document, options = {}) {
  const scope = root || document;
  const list = scope.querySelector("[data-articles]");
  if (!list) return null;
  const manifest = readInlineManifest(scope);
  if (!manifest || !manifest.scope) return null; // landings only
  const infinite = new InfiniteScroll(scope, list, manifest, options);
  return infinite.init();
}

// initMasthead cycles the background videos behind the wordmark with a crossfade,
// for a "monitor switching feeds" feel. Two stacked <video> elements ping-pong
// through the playlist (any length), so only two ever decode at once. It bails on
// reduced motion (the CSS hides the video there too) and pauses when the tab is
// hidden. With no JavaScript the first <video> simply autoplays and loops.
export function initMasthead(root = document) {
  const scope = root || document;
  const stage = scope.querySelector("[data-masthead]");
  if (!stage) return null;
  const els = Array.from(stage.querySelectorAll(".masthead-video"));
  const playlist = (stage.getAttribute("data-masthead-videos") || "").split(/\s+/).filter(Boolean);
  if (els.length < 2 || playlist.length < 2) return null;

  const win = scope.defaultView || (scope.ownerDocument && scope.ownerDocument.defaultView) || window;
  const reduce =
    win && typeof win.matchMedia === "function" && win.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (reduce) {
    els.forEach((v) => {
      try {
        v.pause();
      } catch {}
    });
    return null;
  }

  const doc = scope.ownerDocument || (scope.nodeType === 9 ? scope : document);
  const CROSSFADE = 0.7; // seconds before a clip ends to begin the fade
  let activeEl = 0; // which <video> element is showing
  let playIdx = 0; // playlist index on the active element
  let switching = false;

  els.forEach((v) => {
    v.loop = false;
    v.muted = true;
    v.playsInline = true;
  });
  const play = (v) => {
    const p = v.play && v.play();
    if (p && p.catch) p.catch(() => {});
  };
  // No panning (it read as too busy). Each clip is shown statically, either top- or
  // bottom-aligned, alternating per clip; and each full pass through the playlist
  // inverts, so a clip that was top-aligned last cycle is bottom-aligned the next.
  // With object-fit cover this shows a different slice of each clip, with no motion.
  let cycle = 0;
  const setAlign = (v, idx) => {
    v.style.objectPosition = (idx + cycle) % 2 === 0 ? "50% 0%" : "50% 100%";
  };

  play(els[0]); // the first element already holds playlist[0] via its <source>
  setAlign(els[0], 0);

  function crossfade() {
    if (switching) return;
    switching = true;
    const cur = els[activeEl];
    const nxt = els[1 - activeEl];
    playIdx = (playIdx + 1) % playlist.length;
    if (playIdx === 0) cycle = (cycle + 1) % 2; // invert top/bottom each full pass
    nxt.src = playlist[playIdx];
    try {
      nxt.currentTime = 0;
    } catch {}
    play(nxt);
    setAlign(nxt, playIdx);
    nxt.classList.add("is-active");
    cur.classList.remove("is-active");
    activeEl = 1 - activeEl;
    win.setTimeout(() => {
      try {
        cur.pause();
      } catch {}
      switching = false;
    }, 1100);
  }

  function tick(e) {
    const v = e.target;
    if (!v.classList.contains("is-active") || switching) return;
    if (v.duration && v.currentTime >= v.duration - CROSSFADE) crossfade();
  }
  els.forEach((v) => v.addEventListener("timeupdate", tick));

  doc.addEventListener("visibilitychange", () => {
    const hidden = doc.visibilityState === "hidden";
    els.forEach((v) => {
      if (hidden) {
        try {
          v.pause();
        } catch {}
      } else if (v.classList.contains("is-active")) {
        play(v);
      }
    });
  });

  return { stage, advance: crossfade };
}

// Auto-initialize on the real page. Module scripts are deferred, so the DOM is
// initTopicScroller turns a wrapping chip/topic row into a single horizontal line
// with prev/next arrows that appear only when the row overflows. Applied to the
// article-page topic list and (after the refiner builds it) the portada facet
// topic chips, so topics read as one line "< topic . topic . topic >" instead of
// wrapping. Idempotent: a row already wrapped is skipped, so it is safe to call
// again once the refiner panel exists.
export function initTopicScroller(root = document) {
  const scope = root || document;
  // The article-page topic strip and the portada topic filter both become a
  // single bounded line scrolled with arrows (and mouse drag / native swipe).
  const rows = scope.querySelectorAll(
    '.topics[data-topics], .facet-group[data-facet-group="topic"] .facet-chips'
  );
  rows.forEach(enhanceScrollRow);
}

function enhanceScrollRow(row) {
  if (!row || row.dataset.scrollEnhanced) return;
  row.dataset.scrollEnhanced = "1";
  const doc = row.ownerDocument || document;
  const win = doc.defaultView || (typeof window !== "undefined" ? window : null);

  const wrap = doc.createElement("div");
  wrap.className = "scroll-row";
  row.parentNode.insertBefore(wrap, row);
  const prev = arrowButton(doc, "‹", "Desplazar a la izquierda");
  const next = arrowButton(doc, "›", "Desplazar a la derecha");
  wrap.append(prev, row, next);

  const step = () => Math.max(140, Math.round(row.clientWidth * 0.7));
  const update = () => {
    const overflow = row.scrollWidth - row.clientWidth > 2;
    wrap.dataset.overflow = overflow ? "true" : "false";
    prev.disabled = row.scrollLeft <= 1;
    next.disabled = row.scrollLeft + row.clientWidth >= row.scrollWidth - 1;
  };
  prev.addEventListener("click", () => row.scrollBy({ left: -step(), behavior: "smooth" }));
  next.addEventListener("click", () => row.scrollBy({ left: step(), behavior: "smooth" }));
  row.addEventListener("scroll", update, { passive: true });
  if (win && win.addEventListener) win.addEventListener("resize", update);

  // Mouse drag-to-scroll (touch / trackpad already scroll the overflow natively).
  let dragging = false, moved = false, startX = 0, startLeft = 0;
  row.addEventListener("pointerdown", (e) => {
    if (e.pointerType !== "mouse" || e.button !== 0) return;
    dragging = true;
    moved = false;
    startX = e.clientX;
    startLeft = row.scrollLeft;
  });
  row.addEventListener("pointermove", (e) => {
    if (!dragging) return;
    const dx = e.clientX - startX;
    if (Math.abs(dx) > 3) moved = true;
    if (moved) {
      row.scrollLeft = startLeft - dx;
      if (e.cancelable) e.preventDefault();
    }
  });
  const endDrag = () => {
    dragging = false;
  };
  row.addEventListener("pointerup", endDrag);
  row.addEventListener("pointerleave", endDrag);
  // Suppress the click that terminates a drag so it never activates a chip/link.
  row.addEventListener(
    "click",
    (e) => {
      if (moved) {
        e.preventDefault();
        e.stopPropagation();
        moved = false;
      }
    },
    true
  );

  update();
  if (win && win.requestAnimationFrame) win.requestAnimationFrame(update);
}

// initAuthorMore fills the "Más de este autor" rail on a permalink with the
// CURRENT author's other articles (all of them, newest first), fetched from the
// author scope manifest. The server pre-renders the author's older pieces as a
// byte-stable static fallback (so the permalink stays immutable and works with
// JS off); this enhancement replaces that with the full live set, so a piece
// also surfaces the author's newer work. The bar is always present, even empty.
export async function initAuthorMore(root = document) {
  const scope = root || document;
  const aside = scope.querySelector("[data-author-more]");
  if (!aside) return null;
  const list = aside.querySelector("[data-author-more-list]");
  const slug = aside.getAttribute("data-author-more");
  if (!list || !slug) return null;
  const selfPath = normalizedPath(aside.getAttribute("data-self") || (window.location && window.location.pathname) || "");

  let manifest;
  try {
    const res = await fetch(absURL("/manifest/author/" + slug + "/index.json"), { cache: "no-cache" });
    if (!res.ok) return null;
    manifest = await res.json();
  } catch {
    return null; // keep the server-rendered fallback
  }
  let entries;
  try {
    entries = await loadScopeEntries(manifest);
  } catch {
    return null;
  }
  const others = entries.filter((e) => e && e.url && normalizedPath(e.url) !== selfPath).slice(0, 8);
  const frag = document.createDocumentFragment();
  for (const e of others) {
    const li = el("li", { class: "author-more-item" });
    const a = el("a", { class: "author-more-link", href: e.url });
    const sec = el("span", { class: "author-more-section" });
    sec.textContent = e.section || "";
    const strong = el("strong");
    strong.textContent = e.title || "";
    a.append(sec, strong);
    li.appendChild(a);
    frag.appendChild(li);
  }
  list.replaceChildren(frag);
  return { aside, count: others.length };
}

function arrowButton(doc, glyph, label) {
  const b = doc.createElement("button");
  b.type = "button";
  b.className = "scroll-arrow";
  b.setAttribute("aria-label", label);
  b.textContent = glyph;
  return b;
}

// initCardFill equalizes the desktop listing cards within a row. The grid already
// stretches every card to the row's tallest; image cards drop their summary in CSS
// (the image carries the height), and here each TEXT card's summary is grown to
// fill its leftover height and clamped with an ellipsis when there is more text
// than fits. It is desktop-only (the 42rem grid breakpoint, below which cards stack
// one per row and keep their full summary) and re-runs on resize and whenever a
// list's card set changes (facet filter, infinite-scroll append, live refresh).
export function initCardFill(root = document) {
  const scope = root || document;
  const win = typeof window !== "undefined" ? window : null;
  const desktop = () =>
    !win || typeof win.matchMedia !== "function" || win.matchMedia("(min-width: 42rem)").matches;

  const fillCard = (desc) => {
    // Reset to the natural (un-clamped) box so the measure is honest.
    desc.style.removeProperty("display");
    desc.style.removeProperty("-webkit-box-orient");
    desc.style.removeProperty("-webkit-line-clamp");
    if (!desktop()) return; // mobile keeps the full summary
    const avail = desc.clientHeight; // flex:1 already gave it the row's leftover height
    const lh = win ? parseFloat(win.getComputedStyle(desc).lineHeight) : 0;
    if (!avail || !lh) return; // no layout (e.g. jsdom): leave the full summary
    const lines = Math.max(1, Math.floor(avail / lh));
    desc.style.display = "-webkit-box";
    desc.style.webkitBoxOrient = "vertical";
    desc.style.webkitLineClamp = String(lines);
  };

  const fillAll = () => {
    scope
      .querySelectorAll(".article-list .article-item:not(:first-child) .card-textonly .card-description")
      .forEach(fillCard);
  };
  const schedule = () => {
    if (win && win.requestAnimationFrame) win.requestAnimationFrame(fillAll);
    else fillAll();
  };

  schedule();
  if (win && win.addEventListener) {
    let t;
    win.addEventListener("resize", () => {
      if (t) win.clearTimeout(t);
      t = win.setTimeout(fillAll, 150);
    });
  }
  // childList-only (never subtree/attributes), so our own style writes above do not
  // re-trigger it; a re-render or appended batch does.
  if (typeof MutationObserver === "function") {
    scope.querySelectorAll(".article-list").forEach((list) => {
      new MutationObserver(schedule).observe(list, { childList: true });
    });
  }
  return { fill: fillAll };
}

// usually parsed; guard for the rare loading state. No-ops off a listing page.
if (typeof document !== "undefined") {
  const boot = () => {
    initMasthead(document);
    initMenu(document);
    initTheme(document);
    initTopicScroller(document);
    initAuthorMore(document);
    initCardFill(document);
    initRefine(document)
      .then(() => initTopicScroller(document))
      .finally(() => {
        initInfiniteScroll(document);
        initLiveRefresh(document);
      });
  };
  if (document.readyState !== "loading") boot();
  else document.addEventListener("DOMContentLoaded", boot);
}
