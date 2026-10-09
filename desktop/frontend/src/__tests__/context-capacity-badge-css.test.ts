// Run: tsx src/__tests__/context-capacity-badge-css.test.ts
//
// Contract (任务665): the capacity card's status badge —「即将压缩」and its
// translations — must never be clipped. The old rule capped the badge at
// max-width:58% with overflow:hidden, which hard-cut CJK labels mid-glyph in
// narrow cards (text-overflow does not render on an inline-flex box, so not
// even an ellipsis appeared). Now the badge always sizes to its full label;
// in a narrow card the row wraps and the right-aligned used/window figure
// yields instead. JSDOM has no layout engine, so these are static stylesheet
// assertions in the composer-autosize-scrollbar-css.test.ts mold.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function matchingBlocks(selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(styles)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

function finalDeclaration(selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const block of matchingBlocks(selector)) {
    const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
    let match: RegExpExecArray | null;
    while ((match = declaration.exec(block)) !== null) {
      value = match[1].trim();
    }
  }
  return value;
}

function hasDeclaration(selector: string, property: string): boolean {
  return finalDeclaration(selector, property) !== undefined;
}

console.log("\ncontext capacity badge css (任务665)");

// The status badge renders its label whole — no width cap, no clipping.
ok(finalDeclaration(".context-panel__capacity-status", "max-width") === "100%",
  "status badge has no percentage width cap (max-width is the 100% overflow guard only)");
ok(!hasDeclaration(".context-panel__capacity-status", "overflow"),
  "status badge does not clip (no overflow rule)");
ok(!hasDeclaration(".context-panel__capacity-status", "text-overflow"),
  "status badge has no ellipsis policy (labels render in full)");
ok(finalDeclaration(".context-panel__capacity-status", "white-space") === "nowrap",
  "status badge keeps its label on one line");
ok(finalDeclaration(".context-panel__capacity-status", "flex") === "0 0 auto",
  "status badge never shrinks below its label");

// The projection-stale badge sits beside it and had the same clip pattern.
ok(finalDeclaration(".context-panel__projection-stale", "flex") === "0 0 auto",
  "projection-stale badge never shrinks below its label");
ok(!hasDeclaration(".context-panel__projection-stale", "overflow"),
  "projection-stale badge does not clip");
ok(finalDeclaration(".context-panel__projection-stale", "white-space") === "nowrap",
  "projection-stale badge keeps its label on one line");

// The flag group holds the badges without letting the row squeeze it.
ok(finalDeclaration(".context-panel__capacity-flags", "flex") === "0 0 auto",
  "flag group is not flex-squeezed by the figure");
ok(finalDeclaration(".context-panel__capacity-flags", "flex-wrap") === "wrap",
  "flag group stacks badges instead of clipping them");

// Narrow-card escape hatch: the row wraps and the figure yields.
ok(finalDeclaration(".context-panel__capacity-top", "flex-wrap") === "wrap",
  "capacity row wraps in narrow cards instead of clipping badges");
ok(finalDeclaration(".context-panel__capacity-top strong", "margin-left") === "auto",
  "used/window figure stays right-aligned when it wraps below the badges");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
