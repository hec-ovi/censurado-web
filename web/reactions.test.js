// Tests for initReactions (shipped in /assets/app.js): the article's like/dislike
// bar. Server-rendered hidden; unhidden only after GET /api/reactions answers, so
// a stack without the API keeps it invisible. Votes POST and repaint from the
// server's answer (one vote per IP, server-enforced); clicking the active thumb
// withdraws the vote. The counts ride in each button's aria-label too, so screen
// readers hear them (the label overrides the button content in the accessible name).
import { afterEach, describe, expect, test } from "vitest";
import { http, HttpResponse } from "msw";
import { getByRole, waitFor } from "@testing-library/dom";
import userEvent from "@testing-library/user-event";
import { initReactions } from "../internal/generate/templates/assets/app.js";
import { server } from "./server.js";

const SLUG = "ada-reacciones-2026";

let container;
function mount(slug = SLUG) {
  container = document.createElement("div");
  // Mirrors the article.tmpl markup (icons elided: behavior lives on the buttons).
  container.innerHTML = `
  <div class="reactions" data-reactions data-slug="${slug}" hidden>
    <p class="reactions-question">¿Qué te pareció esta nota?</p>
    <div class="reactions-buttons">
      <button type="button" class="reaction-btn" data-vote="up" aria-pressed="false" aria-label="Me gusta">
        <span class="reaction-count" data-count="up">0</span>
      </button>
      <button type="button" class="reaction-btn" data-vote="down" aria-pressed="false" aria-label="No me gusta">
        <span class="reaction-count" data-count="down">0</span>
      </button>
    </div>
  </div>`;
  document.body.appendChild(container);
  return container;
}

afterEach(() => {
  if (container) container.remove();
  container = null;
});

const bar = () => container.querySelector("[data-reactions]");
const like = () => getByRole(container, "button", { name: /^Me gusta/ });
const dislike = () => getByRole(container, "button", { name: /^No me gusta/ });
const counts = () => [
  like().querySelector("[data-count]").textContent,
  dislike().querySelector("[data-count]").textContent,
];

function serveGet(state) {
  server.use(
    http.get("/api/reactions", ({ request }) => {
      const url = new URL(request.url);
      if (url.searchParams.get("slug") !== SLUG) return new HttpResponse(null, { status: 400 });
      return HttpResponse.json({ slug: SLUG, up: 0, down: 0, vote: null, ...state });
    })
  );
}

describe("initReactions", () => {
  test("unhides with the server counts, marks the own vote, and labels the counts for AT", async () => {
    mount();
    serveGet({ up: 3, down: 1, vote: "down" });
    await initReactions(container);
    expect(bar().hidden).toBe(false);
    expect(counts()).toEqual(["3", "1"]);
    expect(like().getAttribute("aria-pressed")).toBe("false");
    expect(dislike().getAttribute("aria-pressed")).toBe("true");
    expect(like().getAttribute("aria-label")).toBe("Me gusta (3)");
    expect(dislike().getAttribute("aria-label")).toBe("No me gusta (1)");
  });

  test("stays hidden when the API errors (local stack, no D1)", async () => {
    mount();
    server.use(http.get("/api/reactions", () => new HttpResponse(null, { status: 503 })));
    await initReactions(container);
    expect(bar().hidden).toBe(true);
  });

  test("stays hidden when the API is unreachable", async () => {
    mount();
    server.use(http.get("/api/reactions", () => HttpResponse.error()));
    await initReactions(container);
    expect(bar().hidden).toBe(true);
  });

  test("does nothing without a slug (no request at all)", async () => {
    mount("");
    await initReactions(container); // onUnhandledRequest:error would fail on any fetch
    expect(bar().hidden).toBe(true);
  });

  test("a second init is a no-op: one GET, one listener, no double votes", async () => {
    mount();
    let gets = 0;
    server.use(
      http.get("/api/reactions", () => {
        gets += 1;
        return HttpResponse.json({ slug: SLUG, up: 1, down: 0, vote: null });
      })
    );
    await initReactions(container);
    await initReactions(container); // must not fetch again nor rebind the click handler
    expect(gets).toBe(1);
    let posts = 0;
    server.use(
      http.post("/api/reactions", () => {
        posts += 1;
        return HttpResponse.json({ slug: SLUG, up: 2, down: 0, vote: "up" });
      })
    );
    await userEvent.setup().click(like());
    await waitFor(() => expect(counts()).toEqual(["2", "0"]));
    expect(posts).toBe(1);
  });

  test("a click votes and repaints counts, pressed state, and labels from the server", async () => {
    mount();
    serveGet({ up: 3 });
    let posted;
    server.use(
      http.post("/api/reactions", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ slug: SLUG, up: 4, down: 0, vote: "up" });
      })
    );
    await initReactions(container);
    await userEvent.setup().click(like());
    await waitFor(() => expect(counts()).toEqual(["4", "0"]));
    expect(posted).toEqual({ slug: SLUG, vote: "up" });
    expect(like().getAttribute("aria-pressed")).toBe("true");
    expect(like().getAttribute("aria-label")).toBe("Me gusta (4)");
  });

  test("clicking the active thumb withdraws the vote", async () => {
    mount();
    serveGet({ up: 4, vote: "up" });
    let posted;
    server.use(
      http.post("/api/reactions", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ slug: SLUG, up: 3, down: 0, vote: null });
      })
    );
    await initReactions(container);
    await userEvent.setup().click(like());
    await waitFor(() => expect(posted).toEqual({ slug: SLUG, vote: "none" }));
    await waitFor(() => expect(counts()).toEqual(["3", "0"]));
    expect(like().getAttribute("aria-pressed")).toBe("false");
  });

  test("switching thumbs sends the new vote and repaints both counts", async () => {
    mount();
    serveGet({ up: 4, down: 2, vote: "up" });
    let posted;
    server.use(
      http.post("/api/reactions", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ slug: SLUG, up: 3, down: 3, vote: "down" });
      })
    );
    await initReactions(container);
    await userEvent.setup().click(dislike());
    await waitFor(() => expect(counts()).toEqual(["3", "3"]));
    expect(posted).toEqual({ slug: SLUG, vote: "down" });
    expect(like().getAttribute("aria-pressed")).toBe("false");
    expect(dislike().getAttribute("aria-pressed")).toBe("true");
  });

  test("keeps the painted state when the vote POST fails", async () => {
    mount();
    serveGet({ up: 3, vote: null });
    server.use(http.post("/api/reactions", () => new HttpResponse(null, { status: 503 })));
    await initReactions(container);
    await userEvent.setup().click(like());
    // The bar must not lie: counts stay at the last server-confirmed state.
    await waitFor(() => expect(counts()).toEqual(["3", "0"]));
    expect(like().getAttribute("aria-pressed")).toBe("false");
  });
});
