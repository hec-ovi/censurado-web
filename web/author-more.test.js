// Tests for initAuthorMore (shipped in /assets/app.js): the permalink's "Más de
// este autor" rail is filled client-side from the author scope manifest with the
// author's full list (newest first), replacing the server-rendered fallback.
import { afterEach, describe, expect, test } from "vitest";
import { http, HttpResponse } from "msw";
import { initAuthorMore } from "../internal/generate/templates/assets/app.js";
import { serveJSON } from "./fixtures.js";
import { server } from "./server.js";

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
  window.history.replaceState({}, "", "/");
});

const MANIFEST = { scope: "/author/lara/", shards: [{ url: "/shards/author/lara/2026/06.json", month: "2026-06" }] };
const SHARD = [
  { slug: "l2", url: "/a/lara-dos-00000002/", title: "Lara Dos", section: "politics", author: "lara", ts: 200 },
  { slug: "l1", url: "/a/lara-uno-00000001/", title: "Lara Uno", section: "politics", author: "lara", ts: 100 },
];

function aside(self) {
  return `
  <aside class="article-rail author-more" data-author-more="lara" data-self="${self}" aria-label="Más de este autor">
    <h2 class="rail-heading">Más de este autor</h2>
    <ol class="author-more-list" data-author-more-list>
      <li class="author-more-item"><a class="author-more-link" href="/a/fallback-00000000/"><strong>fallback</strong></a></li>
    </ol>
  </aside>`;
}

function links() {
  return [...container.querySelectorAll(".author-more-link")].map((a) => a.getAttribute("href"));
}

describe("initAuthorMore", () => {
  test("replaces the server fallback with the author's full list (newest first)", async () => {
    mount(aside("/a/lara-tres-00000003/"));
    const hits = serveJSON({
      "/manifest/author/lara/index.json": MANIFEST,
      "/shards/author/lara/2026/06.json": SHARD,
    });
    const res = await initAuthorMore(container);
    expect(res).not.toBeNull();
    expect(links()).toEqual(["/a/lara-dos-00000002/", "/a/lara-uno-00000001/"]);
    expect(hits["/manifest/author/lara/index.json"]).toBe(1);
  });

  test("excludes the article being read", async () => {
    mount(aside("/a/lara-dos-00000002/"));
    serveJSON({
      "/manifest/author/lara/index.json": MANIFEST,
      "/shards/author/lara/2026/06.json": SHARD,
    });
    await initAuthorMore(container);
    expect(links()).toEqual(["/a/lara-uno-00000001/"]);
  });

  test("keeps the server fallback when the manifest 404s", async () => {
    mount(aside("/a/lara-tres-00000003/"));
    server.use(http.get("*/manifest/author/lara/index.json", () => new HttpResponse(null, { status: 404 })));
    const res = await initAuthorMore(container);
    expect(res).toBeNull();
    expect(links()).toEqual(["/a/fallback-00000000/"]);
  });

  test("no-ops off an article page (no author rail)", async () => {
    mount(`<main><h1>Lo último</h1></main>`);
    expect(await initAuthorMore(container)).toBeNull();
  });
});
