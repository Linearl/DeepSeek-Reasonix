// Run: tsx src/__tests__/tool-recovery-panel-width-css.test.ts
//
// Contract (任务690): the interrupted-tool-call recovery strip, the composer
// and the transcript body must share ONE content width. Before the fix the
// strip used `inset-inline: 24px` with no max-width cap, so its edges never
// matched the composer (footer 32px inset + .composer-wrap capped at
// var(--maxw)) or the transcript rows (var(--transcript-inline-pad) inset +
// children capped at var(--maxw)). JSDOM has no layout engine, so these are
// static stylesheet assertions in the context-capacity-badge-css.test.ts
// mold: lock the declarations, not pixel output.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
function loadStyles(path: string): string {
  return readFileSync(resolve(testDir, path), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
}
const panelStyles = loadStyles("../components/ToolRecoveryPanel.css");
const shellStyles = loadStyles("../styles.css");

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

function matchingBlocks(source: string, selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(source)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

function finalDeclaration(source: string, selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const block of matchingBlocks(source, selector)) {
    const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
    let match: RegExpExecArray | null;
    while ((match = declaration.exec(block)) !== null) {
      value = match[1].trim();
    }
  }
  return value;
}

/** Extract the balanced body of a top-level `@media <query>` block. */
function mediaBlocks(source: string, query: string): string[] {
  const blocks: string[] = [];
  const marker = `@media ${query}`;
  let index = source.indexOf(marker);
  while (index !== -1) {
    const open = source.indexOf("{", index);
    let depth = 0;
    let end = open;
    for (let i = open; i < source.length; i += 1) {
      if (source[i] === "{") depth += 1;
      if (source[i] === "}") {
        depth -= 1;
        if (depth === 0) {
          end = i;
          break;
        }
      }
    }
    blocks.push(source.slice(open + 1, end));
    index = source.indexOf(marker, end);
  }
  return blocks;
}

console.log("\ntool recovery panel width css (任务690)");

// ── 1. The recovery strip itself (ToolRecoveryPanel.css) ────────────────
ok(
  finalDeclaration(panelStyles, ".tool-recovery-panel", "max-width") === "var(--maxw)",
  "recovery strip is capped at var(--maxw), the same cap as composer and body",
);
ok(
  finalDeclaration(panelStyles, ".tool-recovery-panel", "margin-inline") === "auto",
  "recovery strip centers via auto inline margins (composer-wrap mold)",
);
ok(
  (finalDeclaration(panelStyles, ".tool-recovery-panel", "width") ?? "").includes(
    "var(--transcript-inline-pad",
  ),
  "recovery strip width derives from the shared --transcript-inline-pad inset",
);
ok(
  finalDeclaration(panelStyles, ".tool-recovery-panel", "inset-inline") === "0",
  "old bare inset-inline: 24px (uncapped, off-grid inset) is gone",
);

// ── 2. The other two widths stay on the shared tokens ───────────────────
ok(
  finalDeclaration(shellStyles, ".composer-wrap", "max-width") === "var(--maxw)",
  "composer wrap stays capped at var(--maxw)",
);
ok(
  finalDeclaration(shellStyles, ".transcript__row > *", "max-width") === "var(--maxw)",
  "transcript row children stay capped at var(--maxw)",
);

// ── 3. The strip's containing block exposes the shared inset variable ───
// The strip is a sibling of .transcript inside .transcript-shell, so the
// variable must be defined on the shell for the strip to see it. (The 16px
// media-query override is asserted separately below; this checks the base
// rule.)
ok(
  matchingBlocks(shellStyles, ".transcript-shell").some((block) =>
    /(?:^|;)\s*--transcript-inline-pad:\s*32px/.test(block),
  ),
  "transcript-shell defines --transcript-inline-pad: 32px for the strip",
);

// ── 4. Narrow viewport keeps all three in lockstep at 16px ──────────────
const narrowBlocks = mediaBlocks(shellStyles, "(max-width: 820px)");
const narrowTranscriptRule = narrowBlocks.some((block) =>
  /\.transcript,\s*\.transcript-shell\s*\{[^}]*--transcript-inline-pad:\s*16px/.test(block),
);
ok(
  narrowTranscriptRule,
  "at max-width: 820px the shell inherits the 16px inset override together with .transcript",
);
const narrowFooterRule = narrowBlocks.some((block) =>
  /\.footer\s*\{[^}]*padding:\s*10px 16px 8px/.test(block),
);
ok(
  narrowFooterRule,
  "at max-width: 820px the footer keeps its 16px horizontal padding (the inset the strip now matches)",
);

// ── 5. Regressions this test must never allow again ─────────────────────
ok(
  !/inset-inline:\s*24px/.test(panelStyles),
  "ToolRecoveryPanel.css contains no 24px inset anywhere",
);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
