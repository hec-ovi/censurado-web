// Tests for the desktop card summary + fill (shipped in /assets/app.js): the
// client card builder must carry the summary (.card-description) so a filtered or
// appended card matches the server, and initCardFill must degrade safely where
// there is no layout (jsdom).
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { within, waitFor } from "@testing-library/dom";
import userEvent from "@testing-library/user-event";

import { initRefine, initCardFill } from "../internal/generate/templates/assets/app.js";
import { landingHTML, serveJSON } from "./fixtures.js";

let container;
let refiner;

function mount(html) {
  container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  return container;
}

beforeEach(() => {
  window.history.replaceState({}, "", "/");
});

afterEach(() => {
  if (refiner && refiner.destroy) refiner.destroy();
  if (container) container.remove();
  refiner = null;
  container = null;
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

const TEXT = {
  slug: "texto-z", url: "/a/texto-z-11111111/", title: "Texto Z",
  subtitle: "Dek texto", description: "El resumen del texto que llena la tarjeta.",
  image: "", author: "zeta", author_label: "zeta", avatar: "", section: "tech",
  topics: [], published_at: "2026-06-08T09:00:00Z", ts: 1780909200, cts: 1780909200,
};
const IMG = {
  slug: "imagen-z", url: "/a/imagen-z-22222222/", title: "Imagen Z",
  subtitle: "Dek imagen", description: "El resumen de la tarjeta con imagen.",
  image: "/media/" + "a".repeat(64) + ".png", author: "zeta", author_label: "zeta",
  avatar: "", section: "tech", topics: [], published_at: "2026-06-05T09:00:00Z", ts: 1780650000,
};
const OTHER = {
  slug: "otro-y", url: "/a/otro-y-33333333/", title: "Otro Y",
  subtitle: "Dek otro", description: "Resumen del otro autor.",
  image: "", author: "otro", author_label: "otro", avatar: "", section: "tech",
  topics: [], published_at: "2026-06-03T09:00:00Z", ts: 1780477200,
};

const MANIFEST = {
  scope: "/", axis: {},
  shards: [{ url: "/shards/latest/2026/06.json", month: "2026-06", count: 3 }],
  total: 3, cap: { entries: 500, bytes: 204800 },
};
const SHARDS = { "/shards/latest/2026/06.json": [TEXT, IMG, OTHER] };

describe("card summary (.card-description) + fill", () => {
  test("client card builder carries the summary and marks text vs image cards", async () => {
    serveJSON(SHARDS);
    mount(landingHTML({ heading: "Latest", items: [TEXT, IMG, OTHER], months: ["2026-06"], manifest: MANIFEST }));

    refiner = await initRefine(container);
    expect(refiner).toBeTruthy();

    const user = userEvent.setup();
    await user.click(within(container).getByRole("button", { name: "Autor zeta" }));

    await waitFor(() =>
      expect(container.querySelectorAll("[data-articles] .article-item").length).toBe(2)
    );

    const cards = [...container.querySelectorAll("[data-articles] .article-item .card")];
    const textCard = cards.find((c) => c.classList.contains("card-textonly"));
    const imgCard = cards.find((c) => c.classList.contains("card-has-media"));
    expect(textCard).toBeTruthy();
    expect(imgCard).toBeTruthy();

    // Both rebuilt cards carry the summary node (the desktop CSS, not the markup,
    // hides it on image cards), so a filtered card matches the server.
    expect(textCard.querySelector(".card-description").textContent).toBe(
      "El resumen del texto que llena la tarjeta."
    );
    expect(imgCard.querySelector(".card-description").textContent).toBe(
      "El resumen de la tarjeta con imagen."
    );
    // Only the image card has a figure.
    expect(textCard.querySelector("figure.card-media")).toBeNull();
    expect(imgCard.querySelector("figure.card-media")).toBeTruthy();

    // The rebuilt card carries the AR-local date stamp (UTC-3) from the entry's
    // created time (cts) in its own .card-date line (not in the signature row):
    // cts 2026-06-08T09:00Z = 06:00 AR.
    const stamp = textCard.querySelector(".card-date");
    expect(stamp.textContent).toBe("8 de junio de 2026, 06:00");
    // The date is a body line, not a child of the signature row.
    expect(textCard.querySelector(".card-meta .card-date")).toBeNull();
    // The signature signs Name then portrait: byline before the author-avatar.
    const meta = textCard.querySelector(".card-meta");
    expect([...meta.children].indexOf(meta.querySelector(".byline"))).toBeLessThan(
      [...meta.children].indexOf(meta.querySelector(".author-avatar"))
    );
    // Source-order field layout: the date opens the card, then title, then dek
    // (media sits between title and dek on image cards; CSS pulls it above the
    // title visually).
    const body = textCard.querySelector(".card-body");
    const kids = [...body.children];
    expect(kids.indexOf(body.querySelector(".card-date"))).toBeLessThan(
      kids.indexOf(body.querySelector(".card-title"))
    );
    expect(kids.indexOf(body.querySelector(".card-title"))).toBeLessThan(
      kids.indexOf(body.querySelector(".card-subtitle"))
    );
    // Image card: date still first; the media figure sits between the title and
    // the dek in the DOM.
    const imgBody = imgCard.querySelector(".card-body");
    const imgKids = [...imgBody.children];
    expect(imgKids.indexOf(imgBody.querySelector(".card-date"))).toBeLessThan(
      imgKids.indexOf(imgBody.querySelector(".card-title"))
    );
    expect(imgKids.indexOf(imgBody.querySelector(".card-title"))).toBeLessThan(
      imgKids.indexOf(imgBody.querySelector("figure.card-media"))
    );
    expect(imgKids.indexOf(imgBody.querySelector("figure.card-media"))).toBeLessThan(
      imgKids.indexOf(imgBody.querySelector(".card-subtitle"))
    );
  });

  test("initCardFill returns a fill API and no-ops safely without layout (jsdom)", () => {
    mount(`
      <ol class="article-list" data-articles>
        <li class="article-item"><article class="card lead-card"><p class="card-description">lead</p></article></li>
        <li class="article-item"><article class="card card-textonly"><div class="card-body"><p class="card-description">fill me</p></div></article></li>
      </ol>`);
    const api = initCardFill(container);
    expect(api && typeof api.fill).toBe("function");
    expect(() => api.fill()).not.toThrow();
    // jsdom reports clientHeight 0 (no layout), so the clamp is a safe no-op and the
    // lead card is never touched.
    const desc = container.querySelector(".card-textonly .card-description");
    expect(desc.style.webkitLineClamp || "").toBe("");
  });
});
