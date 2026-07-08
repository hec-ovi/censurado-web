// The client refiner reads runtime UI strings from window.__CNZ_I18N__ (the blob
// the generator injects for the render language) and falls back to its baked-in
// Spanish when the blob is absent. app.js captures the blob at module-eval time, so
// each case sets window.__CNZ_I18N__ and re-imports the module with a reset registry.
import { afterEach, expect, test, vi } from "vitest";
import { within, waitFor } from "@testing-library/dom";
import userEvent from "@testing-library/user-event";

import { landingHTML, serveJSON } from "./fixtures.js";

const ZETA = {
  slug: "texto-z", url: "/a/texto-z-11111111/", title: "Texto Z",
  subtitle: "Dek", description: "Resumen.", image: "", author: "zeta",
  author_label: "zeta", avatar: "", section: "tech", topics: [],
  published_at: "2026-06-08T09:00:00Z", ts: 1780909200, cts: 1780909200,
};
const OTRO = {
  slug: "otro-y", url: "/a/otro-y-33333333/", title: "Otro Y",
  subtitle: "Dek", description: "Resumen.", image: "", author: "otro",
  author_label: "otro", avatar: "", section: "tech", topics: [],
  published_at: "2026-06-03T09:00:00Z", ts: 1780477200, cts: 1780477200,
};
const MANIFEST = {
  scope: "/", axis: {},
  shards: [{ url: "/shards/latest/2026/06.json", month: "2026-06", count: 2 }],
  total: 2, cap: { entries: 500, bytes: 204800 },
};
const SHARDS = { "/shards/latest/2026/06.json": [ZETA, OTRO] };

let container;
let refiner;

function mount(html) {
  container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  return container;
}

afterEach(() => {
  if (refiner && refiner.destroy) refiner.destroy();
  if (container) container.remove();
  refiner = null;
  container = null;
  delete window.__CNZ_I18N__;
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

// loadApp re-imports app.js with a fresh module registry so it re-reads
// window.__CNZ_I18N__ (captured at module top level).
async function loadApp() {
  vi.resetModules();
  return import("../internal/generate/templates/assets/app.js");
}

// firstShareX returns the X share link's aria-label from the first rebuilt card.
async function firstShareX(initRefine, authorButtonName) {
  serveJSON(SHARDS);
  mount(landingHTML({ heading: "Portada", items: [ZETA, OTRO], months: ["2026-06"], manifest: MANIFEST }));
  refiner = await initRefine(container);
  const user = userEvent.setup();
  await user.click(within(container).getByRole("button", { name: authorButtonName }));
  await waitFor(() =>
    expect(container.querySelectorAll("[data-articles] .article-item").length).toBeGreaterThan(0)
  );
  return container.querySelector("[data-articles] .card-share-link.share-x").getAttribute("aria-label");
}

test("rebuilt cards use the injected window.__CNZ_I18N__ values", async () => {
  window.__CNZ_I18N__ = { "filter.facet_author": "Author", "share.x_aria": "Share on X" };
  const { initRefine } = await loadApp();
  // The facet chip label follows the injected facet vocabulary ("Author zeta"),
  // and the rebuilt card's share link reads the injected aria-label.
  expect(await firstShareX(initRefine, "Author zeta")).toBe("Share on X");
});

test("falls back to the baked-in Spanish when no blob is injected", async () => {
  delete window.__CNZ_I18N__;
  const { initRefine } = await loadApp();
  expect(await firstShareX(initRefine, "Autor zeta")).toBe("Compartir en X");
});

test("the per-card stamp is 24-hour, with no AM/PM, regardless of language", async () => {
  window.__CNZ_I18N__ = { "share.x_aria": "Share on X" };
  const { initRefine } = await loadApp();
  serveJSON(SHARDS);
  mount(landingHTML({ heading: "Portada", items: [ZETA, OTRO], months: ["2026-06"], manifest: MANIFEST }));
  refiner = await initRefine(container);
  const user = userEvent.setup();
  // No facet-label override here, so the chip keeps its Spanish "Autor zeta" name.
  await user.click(within(container).getByRole("button", { name: "Autor zeta" }));
  await waitFor(() =>
    expect(container.querySelectorAll("[data-articles] .article-item").length).toBeGreaterThan(0)
  );
  const stamp = container.querySelector("[data-articles] .card-date").textContent;
  // ZETA cts 2026-06-08T09:00Z = 06:00 AR, in 24h form with no AM/PM.
  expect(stamp).toBe("8 de junio de 2026, 06:00");
  expect(stamp).not.toMatch(/AM|PM/);
});
