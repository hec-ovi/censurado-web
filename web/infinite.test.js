// Tests for initInfiniteScroll (shipped in /assets/app.js): observer-driven
// "load more" over a listing landing's shards, with a visible loading beat, a
// full-width day separator on date change, dedupe-by-permalink, and a pause while
// a facet filter owns the list. jsdom has no IntersectionObserver, so the scroll
// trigger is injected (options.observe) and the load is driven directly; the 5s
// beat is injected (options.wait) so tests do not actually wait.
import { afterEach, describe, expect, test, vi } from "vitest";
import { initInfiniteScroll } from "../internal/generate/templates/assets/app.js";
import { landingHTML, serveJSON } from "./fixtures.js";

let container;
function mount(html) {
  container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  return container;
}

afterEach(() => {
  if (container) container.remove();
  container = null;
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

const iso = (day) => `2026-06-${day}T12:00:00Z`;
const entry = (n, day) => ({
  slug: `n${n}`,
  url: `/a/n${n}/`,
  title: `Nota ${n}`,
  author: "ada",
  author_label: "Ada L.",
  section: "politics",
  topics: [],
  published_at: iso(day),
  ts: Math.floor(Date.parse(iso(day)) / 1000),
});

// Newest-first: three on the 27th, two on the 26th (so exactly one day rollover).
const STREAM = [entry(1, 27), entry(2, 27), entry(3, 27), entry(4, 26), entry(5, 26)];
const SHARD_URL = "/shards/latest/2026/06.json";
const MANIFEST = {
  scope: "/latest/",
  axis: {},
  shards: [{ url: SHARD_URL, month: "2026-06", count: 5 }],
  total: 5,
  cap: { entries: 500, bytes: 204800 },
};

// Render a landing showing the `shown` newest cards, with the inline manifest for
// the full 5-entry scope and a static pager fallback.
function mountLanding(shown) {
  return mount(
    landingHTML({ heading: "Lo último", items: STREAM.slice(0, shown), manifest: MANIFEST }) +
      `<nav class="pager" data-pager aria-label="Páginas"><a class="pager-link" href="/latest/page/1/">1</a></nav>`
  );
}

const urls = () =>
  [...container.querySelectorAll("[data-articles] .article-item .card-link")].map((a) =>
    a.getAttribute("href")
  );

// Boundary fixtures: b2 is published at 01:00Z on 27 June, which is 22:00 on 26 June
// in Argentina local time (UTC-3). Day separators are keyed in ART, so b2 groups
// under 26 June even though its UTC day is 27 June. b1 (noon UTC) stays 27 June.
const bEntry = (n, isoStr) => ({
  slug: `b${n}`,
  url: `/a/b${n}/`,
  title: `Borde ${n}`,
  author: "ada",
  author_label: "Ada L.",
  section: "politics",
  topics: [],
  published_at: isoStr,
  ts: Math.floor(Date.parse(isoStr) / 1000),
});
const BSTREAM = [
  bEntry(1, "2026-06-27T12:00:00Z"), // ART 27 June
  bEntry(2, "2026-06-27T01:00:00Z"), // UTC 27 June, ART 26 June (the boundary)
  bEntry(3, "2026-06-25T12:00:00Z"), // ART 25 June (so there is always more to load)
];
const BSHARD_URL = "/shards/latest/2026/06.json";
const BMANIFEST = {
  scope: "/latest/",
  axis: {},
  shards: [{ url: BSHARD_URL, month: "2026-06", count: 3 }],
  total: 3,
  cap: { entries: 500, bytes: 204800 },
};
function mountB(shown) {
  return mount(
    landingHTML({ heading: "Lo último", items: BSTREAM.slice(0, shown), manifest: BMANIFEST }) +
      `<nav class="pager" data-pager aria-label="Páginas"><a class="pager-link" href="/latest/page/1/">1</a></nav>`
  );
}

describe("initInfiniteScroll", () => {
  test("appends older entries with a day separator, dedupes, hides the pager, and uses the loading beat", async () => {
    const hits = serveJSON({ [SHARD_URL]: STREAM });
    mountLanding(2); // server shows the two newest (both the 27th)
    const waits = [];
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: (ms) => {
        waits.push(ms);
        return Promise.resolve();
      },
    });

    expect(infinite).not.toBeNull();
    // The static pager is removed once the scroll enhancement is live.
    expect(container.querySelector("[data-pager]")).toBeNull();
    // Nothing loaded until the trigger fires.
    expect(urls()).toEqual(["/a/n1/", "/a/n2/"]);

    await infinite.loadMore();

    // The three remaining entries are appended once, in order, with no duplicate
    // of the two already on the page.
    expect(urls()).toEqual(["/a/n1/", "/a/n2/", "/a/n3/", "/a/n4/", "/a/n5/"]);

    // Exactly one full-width day separator, for the 26th, sitting before n4.
    const seps = container.querySelectorAll("[data-articles] .day-separator");
    expect(seps.length).toBe(1);
    expect(seps[0].textContent).toBe("26 de junio de 2026");
    expect(seps[0].nextElementSibling.querySelector(".card-link").getAttribute("href")).toBe("/a/n4/");

    // The first card of the new day (n4) opens it as that day's lead; the second
    // card of the same day (n5) stays a regular card.
    const cardFor = (href) =>
      [...container.querySelectorAll("[data-articles] .article-item")]
        .find((li) => li.querySelector(`.card-link[href="${href}"]`))
        .querySelector(".card");
    expect(cardFor("/a/n4/").classList.contains("lead-card")).toBe(true);
    expect(cardFor("/a/n5/").classList.contains("lead-card")).toBe(false);

    // The loading beat ran (brief now), and the shard was fetched exactly once.
    expect(waits.length).toBeGreaterThan(0);
    expect(hits[SHARD_URL]).toBe(1);
  });

  test("inserts a day separator among the initially rendered cards on a date rollover", async () => {
    serveJSON({ [SHARD_URL]: STREAM });
    mountLanding(4); // server shows n1,n2,n3 (the 27th) and n4 (the 26th)
    await initInfiniteScroll(container, { observe: () => {}, wait: () => Promise.resolve() });

    const seps = container.querySelectorAll("[data-articles] .day-separator");
    expect(seps.length).toBe(1);
    expect(seps[0].textContent).toBe("26 de junio de 2026");
    // It sits right before the first card of the new day (n4), not before n1.
    expect(seps[0].nextElementSibling.querySelector(".card-link").getAttribute("href")).toBe("/a/n4/");
  });

  test("appends a card across the ART-midnight boundary under its Argentina day", async () => {
    serveJSON({ [BSHARD_URL]: BSTREAM });
    mountB(1); // server shows only b1 (ART 27 June)
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: () => Promise.resolve(),
    });
    await infinite.loadMore();

    expect(urls()).toEqual(["/a/b1/", "/a/b2/", "/a/b3/"]);
    // b2 is UTC 27 June but ART 26 June, so it opens a 26-June group (not 27); b3 opens
    // 25 June. The boundary separator is 26 June, sitting before b2.
    const seps = container.querySelectorAll("[data-articles] .day-separator");
    expect(seps.length).toBe(2);
    expect(seps[0].textContent).toBe("26 de junio de 2026");
    expect(seps[0].nextElementSibling.querySelector(".card-link").getAttribute("href")).toBe("/a/b2/");

    // The appended card's kicker date text is its ART day (26 June); its datetime attr
    // stays UTC. Pins artDayFromSec on the kicker: a revert to the UTC slice would show
    // "2026-06-27".
    const b2card = [...container.querySelectorAll("[data-articles] .article-item")].find((li) =>
      li.querySelector('.card-link[href="/a/b2/"]')
    );
    expect(b2card.querySelector("time.published").textContent).toBe("2026-06-26");
  });

  test("groups the initially rendered cards by Argentina day across a UTC-midnight boundary", async () => {
    serveJSON({ [BSHARD_URL]: BSTREAM });
    mountB(2); // server shows b1 (ART 27) and b2 (01:00Z = ART 26)
    await initInfiniteScroll(container, { observe: () => {}, wait: () => Promise.resolve() });

    // decorateInitial derives each card's ART day from its datetime attr, matching the
    // server, so b2 gets a 26-June separator even though both cards are UTC 27 June.
    const seps = container.querySelectorAll("[data-articles] .day-separator");
    expect(seps.length).toBe(1);
    expect(seps[0].textContent).toBe("26 de junio de 2026");
    expect(seps[0].nextElementSibling.querySelector(".card-link").getAttribute("href")).toBe("/a/b2/");
  });

  test("pauses while a facet filter owns the list", async () => {
    serveJSON({ [SHARD_URL]: STREAM });
    mountLanding(2);
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: () => Promise.resolve(),
    });
    container.querySelector("[data-articles]").setAttribute("data-filtered", "");

    await infinite.loadMore();

    expect(urls()).toEqual(["/a/n1/", "/a/n2/"]); // unchanged: the refiner owns the list
  });

  test("does not enhance when the page already shows the whole scope", async () => {
    mountLanding(5); // server shows all five; manifest total is five
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: () => Promise.resolve(),
    });
    expect(infinite).toBeNull();
    expect(container.querySelector(".infinite-tail")).toBeNull();
    // The static pager is left intact as the fallback (not removed).
    expect(container.querySelector("[data-pager]")).not.toBeNull();
  });

  test("uses the avatar carried by the shard entry for an author not in the first batch", async () => {
    const AV = "/media/lin.png";
    const stream = [
      entry(1, 27), // ada, shown by the server
      { ...entry(2, 26), author: "lin", author_label: "Lin X.", avatar: AV, url: "/a/n2-lin/" },
    ];
    serveJSON({ [SHARD_URL]: stream });
    mount(
      landingHTML({ heading: "Lo último", items: [stream[0]], manifest: { ...MANIFEST, total: 2 } }) +
        `<nav class="pager" data-pager><a class="pager-link" href="/latest/page/1/">1</a></nav>`
    );
    const infinite = await initInfiniteScroll(container, { observe: () => {}, wait: () => Promise.resolve() });

    await infinite.loadMore();

    const linCard = [...container.querySelectorAll("[data-articles] .article-item")].find(
      (li) => li.dataset.author === "lin"
    );
    const img = linCard.querySelector(".author-avatar img");
    expect(img).not.toBeNull(); // a real avatar, not the initial-letter fallback
    expect(img.getAttribute("src")).toBe(AV);
  });

  test("finishes when the stream is exhausted and is then a no-op", async () => {
    serveJSON({ [SHARD_URL]: STREAM });
    mountLanding(2);
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: () => Promise.resolve(),
    });

    await infinite.loadMore(); // pulls the remaining three -> all five shown
    expect(infinite.done).toBe(true);
    expect(container.querySelector(".infinite-sentinel").hasAttribute("hidden")).toBe(true);

    await infinite.loadMore(); // no more to load
    expect(urls()).toEqual(["/a/n1/", "/a/n2/", "/a/n3/", "/a/n4/", "/a/n5/"]);
  });

  test("does not duplicate a day separator the server already rendered", async () => {
    serveJSON({ [SHARD_URL]: STREAM });
    mountLanding(4); // server shows n1,n2,n3 (27th) and n4 (26th)
    // Simulate the server-rendered separator sitting before the first 26th card.
    const list = container.querySelector("[data-articles]");
    const n4 = [...list.querySelectorAll(".article-item")].find(
      (li) => li.querySelector(".card-link").getAttribute("href") === "/a/n4/"
    );
    const sep = document.createElement("li");
    sep.className = "day-separator";
    sep.setAttribute("role", "separator");
    sep.innerHTML = '<span class="day-separator-label">26 de junio de 2026</span>';
    list.insertBefore(sep, n4);

    await initInfiniteScroll(container, { observe: () => {}, wait: () => Promise.resolve() });

    const seps = container.querySelectorAll("[data-articles] .day-separator");
    expect(seps.length).toBe(1); // decorateInitial is idempotent, no duplicate
    expect(seps[0].textContent).toBe("26 de junio de 2026");
  });

  test("uses a default batch size of 6", async () => {
    serveJSON({ [SHARD_URL]: STREAM });
    mountLanding(2);
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: () => Promise.resolve(),
    });
    expect(infinite.batchSize).toBe(6);
  });
});
