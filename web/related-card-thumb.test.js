// The related-article card ({{relacionado:slug}}) is rendered INSIDE the article body, so
// every rule under `.article-body` applies to it. `.article-body img` sets `height: auto` to
// let a body illustration keep its aspect ratio; the card's thumbnail is the opposite case, a
// fixed square the picture must fill. Both selectors used to have the same specificity, and
// the body rule comes later in the same cascade layer, so it won: the thumbnail rendered at
// its own aspect ratio inside the square and left a block of empty background under it.
//
// This pins the invariant that fixes it: whatever selector styles the thumbnail image must
// BEAT `.article-body img`, by specificity or by order. Asserted against the stylesheet
// itself, because the bug lives in the cascade, not in any behaviour jsdom can run.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, test } from "vitest";

// The suite runs from the repo root (vitest), and the sheet is compiled into the generator.
const CSS = readFileSync(
  resolve(process.cwd(), "internal/generate/templates/assets/style.css"),
  "utf8",
);

/** Specificity as [ids, classes, types], enough for the plain selectors this sheet uses. */
function specificity(selector) {
  const ids = (selector.match(/#[\w-]+/g) || []).length;
  const classes = (selector.match(/\.[\w-]+/g) || []).length +
    (selector.match(/\[[^\]]+\]/g) || []).length +
    (selector.match(/:(?!:)(?!not|is|where)[\w-]+/g) || []).length;
  const types = (selector.replace(/\.[\w-]+|#[\w-]+|\[[^\]]+\]|:[\w-()]+/g, "")
    .match(/\b[a-z][\w-]*\b/g) || []).length;
  return [ids, classes, types];
}

/** Every rule in the sheet, in source order, as {selector, index, body}. */
function rules() {
  const out = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m;
  while ((m = re.exec(CSS)) !== null) {
    const selector = m[1].split("\n").pop().trim();
    if (!selector || selector.startsWith("@")) continue;
    out.push({ selector, index: m.index, body: m[2] });
  }
  return out;
}

function wins(a, b) {
  const [ai, ac, at] = specificity(a.selector);
  const [bi, bc, bt] = specificity(b.selector);
  if (ai !== bi) return ai > bi;
  if (ac !== bc) return ac > bc;
  if (at !== bt) return at > bt;
  return a.index > b.index;
}

describe("related-article card thumbnail", () => {
  const all = rules();
  const bodyImg = all.find((r) => r.selector === ".article-body img");
  const thumbImg = all.filter((r) => /\.related-card-thumb img$/.test(r.selector));

  test("the body image rule that caused the bug is still there", () => {
    expect(bodyImg).toBeDefined();
    expect(bodyImg.body).toMatch(/height:\s*auto/);
  });

  test("a rule fills the thumbnail square", () => {
    expect(thumbImg.length).toBeGreaterThan(0);
    const filling = thumbImg.find((r) => /height:\s*100%/.test(r.body) &&
      /object-fit:\s*cover/.test(r.body));
    expect(filling).toBeDefined();
  });

  test("that rule beats .article-body img, or the picture leaves a gap", () => {
    const filling = thumbImg.find((r) => /height:\s*100%/.test(r.body));
    expect(wins(filling, bodyImg)).toBe(true);
  });

  test("the thumbnail box it fills is a fixed square", () => {
    const box = rules().find((r) => r.selector === ".related-card-thumb");
    expect(box).toBeDefined();
    const width = box.body.match(/width:\s*([\d.]+rem)/);
    const height = box.body.match(/height:\s*([\d.]+rem)/);
    expect(width && height).toBeTruthy();
    expect(width[1]).toBe(height[1]);
    expect(box.body).toMatch(/overflow:\s*hidden/);
  });
});
