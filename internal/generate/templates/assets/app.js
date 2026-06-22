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
// The module exports initRefine(root) so the exact shipped bytes are unit
// tested against a DOM fragment, and also auto-initializes on the real page.

const FACET_TYPES = ["author", "section", "topic", "month"];
const FACET_LABEL = { author: "Author", section: "Section", topic: "Topic", month: "Month" };

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
      const authorLink = li.querySelector(".author-link");
      const sectionLink = li.querySelector(".section-link");
      if (author && authorLink && !(author in this.labelMaps.author)) {
        this.labelMaps.author[author] = {
          label: authorLink.textContent.trim(),
          url: authorLink.getAttribute("href"),
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
    panel.setAttribute("aria-label", "Refine by facet");

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
    this.clearBtn.textContent = "Clear filter";
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
      this.announce("No articles match " + FACET_LABEL[type] + " " + value + ".");
      this.focusHeading();
      return;
    }
    this.renderEntries(filtered);
    this.setPressed(chip);
    this.activeFacet = { type, value };
    this.showClear(true);
    if (push && href) this.pushState(href);
    this.announce(
      "Showing " +
        filtered.length +
        " article" +
        (filtered.length === 1 ? "" : "s") +
        " for " +
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
    this.setPressed(null);
    this.activeFacet = null;
    this.showClear(false);
    if (push) this.pushState(this.basePath);
    this.announce("Filter cleared. Showing all articles.");
    this.focusHeading();
  }

  renderEntries(entries) {
    const frag = document.createDocumentFragment();
    for (const e of entries) frag.appendChild(this.entryToItem(e));
    this.list.replaceChildren(frag);
  }

  // entryToItem rebuilds the frozen .article-item/.card structure (so the CSS
  // applies) with the same slug-form data-* hooks the server emits.
  entryToItem(e) {
    const li = el("li", { class: "article-item" });
    li.dataset.author = e.author;
    li.dataset.section = e.section;
    li.dataset.topics = (e.topics || []).join(" ");
    li.dataset.month = monthOf(e);

    const card = el("article", { class: "card" });

    const h2 = el("h2", { class: "card-title" });
    const cardLink = el("a", { class: "card-link", href: e.url });
    cardLink.textContent = e.title;
    h2.appendChild(cardLink);

    const byline = el("p", { class: "byline" });
    byline.append("By ");
    const authorLink = el("a", {
      class: "author-link",
      href: this.facetURL("author", e.author),
      "data-author": "",
    });
    authorLink.textContent = this.labelFor("author", e.author, e.author_label);
    byline.append(authorLink, " in ");
    const sectionLink = el("a", {
      class: "section-link",
      href: this.facetURL("section", e.section),
      "data-section": "",
    });
    sectionLink.textContent = this.labelFor("section", e.section, e.section);
    byline.appendChild(sectionLink);

    const time = el("time", { class: "published", datetime: e.published_at });
    time.textContent = (e.published_at || "").slice(0, 10);

    card.append(h2, byline, time);

    if (e.topics && e.topics.length) {
      const ul = el("ul", { class: "topics", "data-topics": "" });
      for (const t of e.topics) {
        const liT = el("li");
        const tl = el("a", { class: "topic-link", href: this.facetURL("topic", t) });
        tl.textContent = this.labelFor("topic", t, t);
        liT.appendChild(tl);
        ul.appendChild(liT);
      }
      card.appendChild(ul);
    }

    li.appendChild(card);
    return li;
  }

  renderEmpty(type, value) {
    const li = el("li", { class: "empty-state", role: "note" });
    li.textContent =
      'No articles match ' + FACET_LABEL[type] + ' "' + this.labelFor(type, value, value) + '".';
    this.list.replaceChildren(li);
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

// Auto-initialize on the real page. Module scripts are deferred, so the DOM is
// usually parsed; guard for the rare loading state. No-ops off a listing page.
if (typeof document !== "undefined") {
  const boot = () => {
    initRefine(document);
  };
  if (document.readyState !== "loading") boot();
  else document.addEventListener("DOMContentLoaded", boot);
}
