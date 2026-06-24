// Tests for the shipped client refiner (the exact bytes embedded at
// /assets/app.js), driven Testing-Library style with MSW network mocking over a
// jsdom DOM. They prove progressive enhancement over the frozen Phase-4A
// listing markup and, crucially, client/server membership + order parity.
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { within, waitFor } from "@testing-library/dom";
import userEvent from "@testing-library/user-event";

import { initLiveRefresh, initMasthead, initRefine } from "../internal/generate/templates/assets/app.js";
import {
  E,
  LATEST_STREAM,
  LATEST_MANIFEST,
  LATEST_SHARDS,
  AUTHOR_ADA_SHARDS,
  AUTHOR_ADA_STREAM,
  TECH_STREAM,
  TECH_MANIFEST,
  TECH_SHARDS,
  CAPS_STREAM,
  CAPS_MANIFEST,
  CAPS_SHARDS,
  PP,
  BACKDATE_STREAM,
  BACKDATE_MANIFEST,
  BACKDATE_SHARDS,
  landingHTML,
  deepHTML,
  articleHTML,
  serveJSON,
} from "./fixtures.js";

let container;
let refiner;
let liveRefresh;

function mount(html) {
  container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  return container;
}

function itemURLs() {
  return [...container.querySelectorAll("[data-articles] .article-item .card-link")].map((a) =>
    a.getAttribute("href")
  );
}
function itemAuthors() {
  return [...container.querySelectorAll("[data-articles] .article-item")].map((li) => li.dataset.author);
}

beforeEach(() => {
  window.history.replaceState({}, "", "/");
});

afterEach(() => {
  if (refiner && refiner.destroy) refiner.destroy();
  if (liveRefresh && liveRefresh.destroy) liveRefresh.destroy();
  if (container) container.remove();
  refiner = null;
  liveRefresh = null;
  container = null;
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

const latestFragment = () =>
  landingHTML({
    heading: "Latest",
    items: LATEST_STREAM,
    months: ["2026-06", "2026-05"],
    monthBase: "/",
    manifest: LATEST_MANIFEST,
  });

function responseJSON(data, { etag } = {}) {
  const headers = { "Content-Type": "application/json" };
  if (etag) headers.ETag = etag;
  return new Response(JSON.stringify(data), { status: 200, headers });
}

describe("Censurado refiner", () => {
  // Scenario 1
  test("landing (inline manifest): selecting an Author chip filters to that author and pushes the pre-built URL", async () => {
    const hits = serveJSON(LATEST_SHARDS);
    mount(latestFragment());
    const push = vi.spyOn(window.history, "pushState");

    refiner = await initRefine(container);
    expect(refiner).toBeTruthy();

    const user = userEvent.setup();
    const chip = within(container).getByRole("button", { name: "Autor ada" });
    expect(chip.getAttribute("aria-pressed")).toBe("false");

    await user.click(chip);

    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(3)
    );

    // The scope's own shards were fetched (the landing fast path still reads the
    // authoritative full stream, not just the visible first page).
    expect(hits["/shards/latest/2026/06.json"]).toBe(1);
    expect(hits["/shards/latest/2026/05.json"]).toBe(1);

    // Filtered to exactly ada's entries, in shard display order.
    expect(itemURLs()).toEqual(AUTHOR_ADA_STREAM.map((e) => e.url));
    expect(new Set(itemAuthors())).toEqual(new Set(["ada"]));
    expect(chip.getAttribute("aria-pressed")).toBe("true");

    // pushState to the author's pre-built URL (read from the server link).
    expect(push).toHaveBeenCalledWith(expect.anything(), "", "/author/ada/");
    expect(window.location.pathname).toBe("/author/ada/");
  });

  // Scenario 2
  test("deep/sealed page (no inline manifest): the refiner fetches the manifest, then filters correctly", async () => {
    const manifestHits = serveJSON({ "/manifest/section/tech/index.json": TECH_MANIFEST });
    serveJSON(TECH_SHARDS);
    mount(
      deepHTML({
        heading: "tech",
        items: TECH_STREAM,
        manifestURL: "/manifest/section/tech/index.json",
      })
    );

    refiner = await initRefine(container);
    expect(refiner).toBeTruthy();
    // The standalone manifest was actually fetched (deep-page path).
    expect(manifestHits["/manifest/section/tech/index.json"]).toBe(1);

    const user = userEvent.setup();
    await user.click(within(container).getByRole("button", { name: "Autor ada" }));

    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(2)
    );
    // tech stream filtered by ada, newest-first.
    expect(itemURLs()).toEqual([E.techAdaJune.url, E.techAdaMay.url]);
  });

  // Scenario 3
  test("multi-part month: parts concatenate oldest-content-last into the server newest-first order", async () => {
    const hits = serveJSON(CAPS_SHARDS);
    mount(
      landingHTML({
        heading: "caps",
        items: CAPS_STREAM,
        months: ["2026-06"],
        monthBase: "/section/caps/",
        manifest: CAPS_MANIFEST,
      })
    );

    refiner = await initRefine(container);
    const user = userEvent.setup();
    await user.click(within(container).getByRole("button", { name: "Sección caps" }));

    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(7)
    );

    // All three parts were fetched and concatenated.
    expect(hits["/shards/section/caps/2026/06.3.json"]).toBe(1);
    expect(hits["/shards/section/caps/2026/06.2.json"]).toBe(1);
    expect(hits["/shards/section/caps/2026/06.json"]).toBe(1);

    // Result is the exact server newest-first order: G,F,E,D,C,B,A.
    expect(itemURLs()).toEqual(CAPS_STREAM.map((e) => e.url));
  });

  // Scenario 3b: back-dated insert across parts (the ORDER-parity hard case).
  // Insertion order A,B,C then a back-dated D makes part boundaries cross
  // publication time, so the part files are 06.json=[B,A] and 06.2.json=[C,D].
  // Whole-part concatenation (newest part first) yields C,D,B,A, but the server
  // page renders the global display order C,B,D,A. The refiner must reproduce
  // C,B,D,A: this test FAILS on whole-part concatenation and passes on the
  // ts-merge.
  test("back-dated insert across parts: merged order is the server page order C,B,D,A, not the concat C,D,B,A", async () => {
    const hits = serveJSON(BACKDATE_SHARDS);
    mount(
      landingHTML({
        heading: "pp",
        items: BACKDATE_STREAM,
        months: ["2026-06"],
        monthBase: "/section/pp/",
        manifest: BACKDATE_MANIFEST,
      })
    );

    refiner = await initRefine(container);
    const user = userEvent.setup();
    await user.click(within(container).getByRole("button", { name: "Sección pp" }));

    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(4)
    );

    // Both parts were fetched.
    expect(hits["/shards/section/pp/2026/06.2.json"]).toBe(1);
    expect(hits["/shards/section/pp/2026/06.json"]).toBe(1);

    // Global display order, with the back-dated D between B and A.
    expect(itemURLs()).toEqual([PP.C.url, PP.B.url, PP.D.url, PP.A.url]);
    // And explicitly NOT the whole-part concatenation order C,D,B,A.
    expect(itemURLs()).not.toEqual([PP.C.url, PP.D.url, PP.B.url, PP.A.url]);
  });

  // Scenario 4
  test("empty selection: a facet value with zero matches shows the empty state and disables the chip", async () => {
    serveJSON(LATEST_SHARDS);
    const phantom = {
      ...E.techAdaJune,
      slug: "ghost-piece",
      url: "/a/ghost-piece-00000000/",
      title: "Ghost Piece",
      author: "phantom",
      author_label: "phantom",
    };
    mount(
      landingHTML({
        heading: "Latest",
        items: [...LATEST_STREAM, phantom],
        months: ["2026-06", "2026-05"],
        monthBase: "/",
        manifest: LATEST_MANIFEST,
      })
    );

    refiner = await initRefine(container);
    const user = userEvent.setup();
    const chip = within(container).getByRole("button", { name: "Autor phantom" });
    await user.click(chip);

    await waitFor(() =>
      expect(container.querySelector("[data-articles] .empty-state")).not.toBeNull()
    );
    expect(container.querySelector("[data-articles] .article-item")).toBeNull();
    expect(chip.disabled).toBe(true);
    expect(chip.getAttribute("aria-disabled")).toBe("true");
    expect(container.querySelector(".refine-status").textContent).toMatch(/ningún artículo/i);
  });

  // Scenario 5
  test("progressive enhancement: server-rendered facet links survive init and remain real <a href> links", async () => {
    serveJSON(LATEST_SHARDS);
    mount(latestFragment());

    // Before init: the byline + month navigators are real anchors with hrefs.
    const beforeAuthors = container.querySelectorAll("[data-articles] a.author-link[href]");
    expect(beforeAuthors.length).toBeGreaterThan(0);
    expect(container.querySelector('a.month-link[href="/2026/06/"]')).not.toBeNull();

    refiner = await initRefine(container);

    // After init: those exact links are still present (enhancement ADDS chips,
    // it does not remove the no-JS fallback).
    const afterAuthor = container.querySelector('[data-articles] a.author-link[href="/author/ada/"]');
    expect(afterAuthor).not.toBeNull();
    expect(afterAuthor.tagName).toBe("A");
    expect(afterAuthor.getAttribute("href")).toBe("/author/ada/");
    expect(container.querySelector('a.month-link[href="/2026/06/"]')).not.toBeNull();
    // The chip panel was added alongside, not in place of, the fallbacks.
    expect(container.querySelector(".facet-panel")).not.toBeNull();
  });

  test("article page: initRefine does nothing (no facet panel, returns null)", async () => {
    mount(articleHTML(E.techAdaJune));
    refiner = await initRefine(container);
    expect(refiner).toBeNull();
    expect(container.querySelector(".facet-panel")).toBeNull();
  });

  // Explicit client/server parity
  test("parity: Author:ada slugs + order equal the pre-built /author/ada/ scope shard order", async () => {
    serveJSON(LATEST_SHARDS);
    mount(latestFragment());

    refiner = await initRefine(container);
    const user = userEvent.setup();
    await user.click(within(container).getByRole("button", { name: "Autor ada" }));
    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(3)
    );

    const clientURLs = itemURLs();
    // Independently concatenate the pre-built /author/ada/ scope's shard JSON
    // (newest-month-first, newest-published-first within a month): this is the
    // membership + order the server-rendered /author/ada/ page would show.
    const serverURLs = [
      ...AUTHOR_ADA_SHARDS["/shards/author/ada/2026/06.json"],
      ...AUTHOR_ADA_SHARDS["/shards/author/ada/2026/05.json"],
    ].map((e) => e.url);

    expect(clientURLs).toEqual(serverURLs);
  });

  // Clearing / toggling a facet returns to the base scope view + URL.
  test("toggling a chip off clears the filter and restores the base scope URL", async () => {
    serveJSON(LATEST_SHARDS);
    mount(latestFragment());
    const push = vi.spyOn(window.history, "pushState");

    refiner = await initRefine(container);
    const user = userEvent.setup();
    const chip = within(container).getByRole("button", { name: "Autor lin" });

    await user.click(chip);
    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(2)
    );
    expect(window.location.pathname).toBe("/author/lin/");

    await user.click(chip); // toggle off
    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(6)
    );
    expect(chip.getAttribute("aria-pressed")).toBe("false");
    expect(push).toHaveBeenLastCalledWith(expect.anything(), "", "/latest/");
  });

  test("live refresh polls the sentinel, shows a new-stories banner, and prepends new cards on click", async () => {
    const breaking = {
      ...E.techAdaJune,
      slug: "breaking-story",
      url: "/a/breaking-story-11111111/",
      title: "Breaking Story",
      published_at: "2026-06-08T10:00:00Z",
      ts: E.techAdaJune.ts + 3600,
    };
    const shard = [breaking, ...LATEST_SHARDS["/shards/latest/2026/06.json"]];
    let sentinelHits = 0;
    const fetchMock = vi.fn(async (input, init = {}) => {
      const url = new URL(String(input));
      if (url.pathname === "/latest/version.json") {
        sentinelHits += 1;
        if (sentinelHits === 1) {
          return responseJSON(
            { v: "v1", latest_ids: LATEST_STREAM.map((e) => e.slug) },
            { etag: '"v1"' }
          );
        }
        return responseJSON(
          { v: "v2", latest_ids: [breaking.slug, ...LATEST_STREAM.map((e) => e.slug)] },
          { etag: '"v2"' }
        );
      }
      if (url.pathname === "/manifest/latest/index.json") return responseJSON(LATEST_MANIFEST);
      if (url.pathname === "/shards/latest/2026/06.json") return responseJSON(shard);
      throw new Error("unexpected fetch: " + url.pathname + " " + JSON.stringify(init));
    });

    mount(latestFragment());
    liveRefresh = await initLiveRefresh(container, { fetch: fetchMock, autoStart: false });
    expect(liveRefresh).toBeTruthy();

    await liveRefresh.checkNow();

    expect(fetchMock.mock.calls[1][1].headers["If-None-Match"]).toBe('"v1"');
    const banner = within(container).getByRole("button", { name: "1 historia nueva" });
    expect(banner).not.toBeNull();

    const user = userEvent.setup();
    await user.click(banner);

    expect(itemURLs()[0]).toBe(breaking.url);
    expect(container.querySelector("[data-live-refresh-banner]").hasAttribute("hidden")).toBe(true);
  });

  test("masthead cycles videos with a crossfade and alternating top/bottom alignment", () => {
    vi.useFakeTimers();
    try {
      container = document.createElement("div");
      container.innerHTML = `
        <div class="masthead-media" data-masthead data-masthead-videos="/a.mp4 /b.mp4 /c.mp4">
          <video class="masthead-video is-active"></video>
          <video class="masthead-video"></video>
        </div>`;
      document.body.appendChild(container);
      const videos = container.querySelectorAll(".masthead-video");
      // jsdom does not implement media playback; stub it so init/crossfade run.
      videos.forEach((v) => {
        v.play = () => Promise.resolve();
        v.pause = () => {};
      });

      const ctrl = initMasthead(container);
      expect(ctrl).toBeTruthy();

      // First clip: active and top-aligned.
      expect(videos[0].classList.contains("is-active")).toBe(true);
      expect(videos[0].style.objectPosition).toBe("50% 0%");

      // Advance: the second element takes the next playlist item, becomes active, and
      // is bottom-aligned (alternating).
      ctrl.advance();
      expect(videos[1].getAttribute("src")).toBe("/b.mp4");
      expect(videos[1].classList.contains("is-active")).toBe(true);
      expect(videos[0].classList.contains("is-active")).toBe(false);
      expect(videos[1].style.objectPosition).toBe("50% 100%");

      // Let the post-crossfade settle timer fire, then advance again: back to the
      // first element with the third clip, top-aligned.
      vi.advanceTimersByTime(1200);
      ctrl.advance();
      expect(videos[0].getAttribute("src")).toBe("/c.mp4");
      expect(videos[0].classList.contains("is-active")).toBe(true);
      expect(videos[0].style.objectPosition).toBe("50% 0%");
    } finally {
      vi.useRealTimers();
    }
  });
});
