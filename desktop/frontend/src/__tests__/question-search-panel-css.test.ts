// Run: tsx src/__tests__/question-search-panel-css.test.ts
//
// Contract (tl1244): the question search panel must stay inside the viewport.
// The panel anchors 8px below .transcript-shell's top; the shell sits under
// the topbar and ABOVE the composer (input bar), so its bottom edge is the
// composer's top. A viewport-relative cap (min(70vh, 520px)) ignored the
// topbar offset and the real composer height: on short windows / tall
// composers the panel's bottom slid under the input bar and the lowest
// history cards became unreachable (third incident on this panel after the
// 8-31 style loss and the task-566 props miss). The cap is now a percentage
// of the containing block (.transcript-shell — position:relative with a
// height:100% chain bounded by the viewport), so the whole panel always fits:
// header fixed, footer fixed, .question-search__results is the ONLY flexible
// part and scrolls internally.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const panelSource = readFileSync(resolve(testDir, "../components/QuestionSearchPanel.tsx"), "utf8");
const transcriptSource = readFileSync(resolve(testDir, "../components/Transcript.tsx"), "utf8");

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
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

function allDeclarations(selector: string, property: string): string[] {
  const values: string[] = [];
  for (const block of matchingBlocks(selector)) {
    const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
    let match: RegExpExecArray | null;
    while ((match = declaration.exec(block)) !== null) {
      values.push(match[1].trim());
    }
  }
  return values;
}

console.log("\nquestion search panel css");

// AC1 — the panel's vertical budget is bounded by its containing block (the
// viewport-bounded .transcript-shell), never by a raw viewport unit: 70vh
// cannot know about the topbar above or the composer below the shell.
eq(finalDeclaration(".question-search-panel", "max-height"), "min(520px, calc(100% - 16px))", "panel max-height resolves against the shell (100% - top 8px - bottom 8px gap), 520px comfort cap kept");
eq(finalDeclaration(".question-search-panel", "position"), "absolute", "panel stays absolutely anchored inside .transcript-shell");
eq(
  allDeclarations(".question-search-panel", "max-height").filter((value) => /v[hw]\b/.test(value)).join(", "),
  "",
  "no rule on .question-search-panel may reintroduce a viewport-unit max-height (70vh-class regression goes red)",
);

// AC2 — the fixed/scroll split: header and footer never flex away, the results
// area is the only scroll container.
eq(finalDeclaration(".question-search__header", "flex"), "none", "search box row is fixed (never compressed by a tight budget)");
eq(finalDeclaration(".question-search__foot", "flex"), "none", "footer count row is fixed");
eq(finalDeclaration(".question-search__results", "flex"), "1", "results area absorbs the flexible space");
eq(finalDeclaration(".question-search__results", "min-height"), "0", "results area may shrink below its content (flex scroll precondition)");
eq(finalDeclaration(".question-search__results", "overflow-y"), "auto", "results area scrolls internally");
eq(finalDeclaration(".question-search__panel", "display"), undefined, "no stray .question-search__panel rule (selector family stays exact)");

// AC3 — no fixed height on the results area: its scrollable range must come
// from the panel's capped flex chain (single source of the vertical budget),
// not from a hardcoded 定高 that drifts out of sync with the shell.
eq(finalDeclaration(".question-search__results", "height"), undefined, "results area has no fixed height (budget comes from the panel max-height)");
eq(finalDeclaration(".question-search__results", "max-height"), undefined, "results area carries no second max-height (single viewport bound at the panel level)");

// AC4 — structural precondition: the flex column order header → results →
// footer in the component, and the panel still renders inside the
// transcript shell (the containing block the percentage cap resolves against).
const headerAt = panelSource.indexOf("question-search__header");
const resultsAt = panelSource.indexOf("question-search__results");
const footAt = panelSource.indexOf("question-search__foot");
eq(headerAt !== -1 && resultsAt !== -1 && footAt !== -1, true, "panel renders header/results/footer");
eq(headerAt < resultsAt && resultsAt < footAt, true, "column order stays header (fixed) → results (scroll) → footer (fixed)");
eq(/className=\{?`transcript-shell/.test(transcriptSource), true, "panel host .transcript-shell (position:relative, viewport-bounded) stays the mounting shell");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
