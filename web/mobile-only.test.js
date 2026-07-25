// Two corrections that are MOBILE ONLY: a card title that reads ragged when it wraps to a
// second line on a narrow screen, and a markdown table that renders as a grid of boxed cells
// and reads as raw markup on a phone. The desktop layout keeps the centered title and the
// bordered table, so the risk here is not "does it look right" but "did it leak upward".
//
// This asserts the containment: the rules exist, they live inside a max-width block, and the
// base (shared) rules still carry the desktop treatment.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, test } from "vitest";

const CSS = readFileSync(
  resolve(process.cwd(), "internal/generate/templates/assets/style.css"),
  "utf8",
);

/** The body of every `@media (max-width: ...)` block, concatenated. */
function mobileOnlyCSS() {
  const out = [];
  const re = /@media\s*\(max-width:[^)]*\)\s*\{/g;
  let m;
  while ((m = re.exec(CSS)) !== null) {
    let depth = 1;
    let i = re.lastIndex;
    while (i < CSS.length && depth > 0) {
      if (CSS[i] === "{") depth += 1;
      else if (CSS[i] === "}") depth -= 1;
      i += 1;
    }
    out.push(CSS.slice(re.lastIndex, i - 1));
  }
  return out.join("\n");
}

/** The sheet with every max-width block removed: what a wide viewport actually applies. */
function withoutMobileOnly() {
  let out = CSS;
  for (const block of mobileOnlyCSS().split("\n\n")) {
    if (block.trim()) out = out.replace(block, "");
  }
  return out;
}

const MOBILE = mobileOnlyCSS();

describe("card titles on a narrow screen", () => {
  test("are left aligned", () => {
    expect(MOBILE).toMatch(/\.card-title\s*\{[^}]*text-align:\s*left/);
  });

  test("stay centered everywhere else", () => {
    // The shared rule is what the desktop layout reads; it must be untouched.
    expect(CSS).toMatch(/\.card-title\s*\{[^}]*text-align:\s*center/);
  });
});

describe("article tables on a narrow screen", () => {
  test("drop the cell grid for row rules", () => {
    const cells = MOBILE.match(/\.article-body th,\s*\n\s*\.article-body td\s*\{[^}]*\}/);
    expect(cells).not.toBeNull();
    expect(cells[0]).toMatch(/border:\s*0/);
    expect(cells[0]).toMatch(/border-bottom:\s*1px solid/);
  });

  test("mark the header row as a header rather than another cell", () => {
    expect(MOBILE).toMatch(/\.article-body th\s*\{[^}]*text-transform:\s*uppercase/);
  });

  test("keep the boxed cells on wider screens", () => {
    expect(CSS).toMatch(/\.article-body th,\s*\n\s*\.article-body td\s*\{[^}]*border:\s*1px solid/);
  });

  test("stay scrollable instead of squeezing the columns", () => {
    // The base rule already scrolls; the mobile rule must not undo it by forcing a width.
    expect(CSS).toMatch(/\.article-body table\s*\{[^}]*overflow-x:\s*auto/);
    expect(MOBILE).toMatch(/\.article-body table\s*\{[^}]*min-width:\s*100%/);
  });
});

describe("containment", () => {
  test("neither correction reaches the shared rules", () => {
    const shared = withoutMobileOnly();
    expect(shared).not.toMatch(/\.card-title\s*\{[^}]*text-align:\s*left/);
    expect(shared).not.toMatch(/\.article-body th\s*\{[^}]*text-transform:\s*uppercase/);
  });
});
