// Tests for initTopicScroller (shipped in /assets/app.js): a wrapping topic /
// chip row is turned into a single horizontal line flanked by prev/next arrows.
import { afterEach, describe, expect, test } from "vitest";
import { initTopicScroller } from "../internal/generate/templates/assets/app.js";

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
});

const TOPICS = `
<ul class="topics" data-topics>
  <li><a class="topic-link" href="/topic/go/">go</a></li>
  <li><a class="topic-link" href="/topic/iac/">iac</a></li>
  <li><a class="topic-link" href="/topic/ia/">ia</a></li>
</ul>`;

describe("initTopicScroller", () => {
  test("wraps a topic row with a scroll-row and two arrows", () => {
    mount(TOPICS);
    initTopicScroller(container);

    const wrap = container.querySelector(".scroll-row");
    expect(wrap).not.toBeNull();
    const ul = wrap.querySelector(".topics");
    expect(ul).not.toBeNull();
    const arrows = wrap.querySelectorAll(".scroll-arrow");
    expect(arrows.length).toBe(2);
    // Order is prev, row, next.
    expect(wrap.children[0]).toBe(arrows[0]);
    expect(wrap.children[1]).toBe(ul);
    expect(wrap.children[2]).toBe(arrows[1]);
    // Arrows are real buttons with accessible labels.
    expect(arrows[0].tagName).toBe("BUTTON");
    expect(arrows[0].getAttribute("aria-label")).toMatch(/izquierda/);
    expect(arrows[1].getAttribute("aria-label")).toMatch(/derecha/);
  });

  test("is idempotent (no double-wrapping on a second call)", () => {
    mount(TOPICS);
    initTopicScroller(container);
    initTopicScroller(container);
    expect(container.querySelectorAll(".scroll-row").length).toBe(1);
    expect(container.querySelectorAll(".scroll-arrow").length).toBe(2);
  });

  test("enhances the portada facet topic chips", () => {
    mount(`
      <div class="facet-group" data-facet-group="topic">
        <ul class="facet-chips">
          <li><button class="facet-chip">go</button></li>
          <li><button class="facet-chip">ia</button></li>
        </ul>
      </div>`);
    initTopicScroller(container);
    const wrap = container.querySelector(".scroll-row");
    expect(wrap).not.toBeNull();
    expect(wrap.querySelector(".facet-chips")).not.toBeNull();
    expect(wrap.querySelectorAll(".scroll-arrow").length).toBe(2);
  });

  test("leaves non-topic facet groups (author/section) untouched", () => {
    mount(`
      <div class="facet-group" data-facet-group="author">
        <ul class="facet-chips"><li><button class="facet-chip">ada</button></li></ul>
      </div>`);
    initTopicScroller(container);
    expect(container.querySelector(".scroll-row")).toBeNull();
  });
});
