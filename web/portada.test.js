// Tests for the curated per-day order on the client (shipped in /assets/app.js):
// the infinite-scroll stream is sorted by (published-day DESC, within-day ord ASC),
// so a per-day plan reorders the day even against publish time, the day's ord-0 card
// opens it as the lead, and a role:"important" card spans the full row. jsdom drives
// loadMore directly (options.observe/wait injected), as in infinite.test.js.
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

const iso = (h) => `2026-06-10T${String(h).padStart(2, "0")}:00:00Z`;
const mk = (n, h, ord, role = "") => ({
  slug: `n${n}`,
  url: `/a/n${n}/`,
  title: `Nota ${n}`,
  author: "ada",
  author_label: "Ada L.",
  section: "politics",
  topics: [],
  published_at: iso(h),
  ts: Math.floor(Date.parse(iso(h)) / 1000),
  ord,
  role,
});

// Same day. Publish time would order C(11h), B(10h), A(9h); the curated ord is
// A(0), C(1), B(2), with C marked important.
const A = mk(1, 9, 0);
const B = mk(2, 10, 2);
const C = mk(3, 11, 1, "important");
const SHARD_URL = "/shards/latest/2026/06.json";
// Served in publish-time order to prove the client re-sorts by (day, ord).
const SERVED = [C, B, A];
const MANIFEST = {
  scope: "/latest/",
  axis: {},
  shards: [{ url: SHARD_URL, month: "2026-06", count: 3 }],
  total: 3,
  cap: { entries: 500, bytes: 204800 },
};

const liFor = (href) =>
  [...container.querySelectorAll("[data-articles] .article-item")].find((li) =>
    li.querySelector(`.card-link[href="${href}"]`)
  );

describe("curated per-day order (client)", () => {
  test("within-day ord wins over publish time; ord-0 is the lead, important spans the row", async () => {
    serveJSON({ [SHARD_URL]: SERVED });
    mount(
      landingHTML({ heading: "Lo último", items: [], manifest: MANIFEST }) +
        `<nav class="pager" data-pager aria-label="Páginas"><a class="pager-link" href="/latest/page/1/">1</a></nav>`
    );
    const infinite = await initInfiniteScroll(container, {
      observe: () => {},
      wait: () => Promise.resolve(),
    });
    expect(infinite).not.toBeNull();

    await infinite.loadMore();

    const urls = [...container.querySelectorAll("[data-articles] .article-item .card-link")].map((a) =>
      a.getAttribute("href")
    );
    expect(urls).toEqual(["/a/n1/", "/a/n3/", "/a/n2/"]); // A, C, B (curated), not C, B, A (ts)

    // A (ord 0) opens the day -> the day's lead card.
    expect(liFor("/a/n1/").querySelector(".card").classList.contains("lead-card")).toBe(true);
    // C is role:important and not the lead -> its <li> spans the row.
    expect(liFor("/a/n3/").classList.contains("is-important")).toBe(true);
    // B is a normal card.
    expect(liFor("/a/n2/").classList.contains("is-important")).toBe(false);
    expect(liFor("/a/n2/").querySelector(".card").classList.contains("lead-card")).toBe(false);
  });
});
