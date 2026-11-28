// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1800x-status-token-pins.test.ts
// tl1800 batch 2 (tl1814 plan B2+B4) — same-value replacement pins.
//
// Why: batch 2 converted 30 bare color uses to tokens under the "definition
// value = the replaced literal" rule (tl1814 naming spec §1.4, route A/C).
// The zero-visual-regression guarantee rests on the byte identity between
// each token's :root definition and the literal it replaced. This pin
// freezes those identities so a future edit cannot silently recolor a
// "same-value" token.
//
// Two pin groups:
//   1. The 13 new B4 tokens (--status-* / --im-* / --onboarding-error-*):
//      :root definition must stay literally equal to the original bare value
//      recorded in tl1814-裸色值六组分批计划-20261126.tsv.
//   2. The B2 route-A anchors: existing tokens whose VALUE the replaced
//      literals were proven byte-equal to (--ok = rgb(116,184,122),
//      --warn = rgb(217,164,65), --highlight-color/--scrim-color/--shadow-color
//      white/black sides). Recoloring any of them changes the batch-2 use
//      sites that map onto them via color-mix.
//
// Pin semantics: equality pins — change the expected literal ONLY in the
// same commit that deliberately recolors the token AND its use sites (that
// is a design decision, not a refactor; tl1800 non-goals forbid it inside
// the收口 batches).

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const stylesPath = `${here}../styles.css`;

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntl1800 batch 2 same-value token pins (definition = replaced literal)");

const styles = readFileSync(stylesPath, "utf8");

// Base :root block only (first ":root {" in the file) — the theme-invariant
// constants live there, theme definition blocks must never override them.
const rootStart = styles.indexOf(":root {");
assert.ok(rootStart >= 0, "base :root block found");
const declStart = styles.indexOf("{", rootStart) + 1;
let depth = 1;
let declEnd = declStart;
for (let i = declStart; i < styles.length; i += 1) {
  if (styles[i] === "{") depth += 1;
  else if (styles[i] === "}") { depth -= 1; if (depth === 0) { declEnd = i; break; } }
}
const rootBlock = styles.slice(declStart, declEnd);

function rootValue(token: string): string | null {
  const m = rootBlock.match(new RegExp(`--${token}\\s*:\\s*([^;]+);`));
  return m ? m[1].trim() : null;
}

// Group 1 — the 13 new B4 tokens (tl1814 plan B4 registry values).
const newTokens: Array<[string, string]> = [
  ["status-done", "#2e9e5b"],
  ["status-blocked", "#d9534f"],
  ["status-error", "#ef4444"],
  ["status-ok-bright", "#4ade80"],
  ["status-danger-hover", "#e5534b"],
  ["im-generic", "#3b82f6"],
  ["im-generic-deep", "#2563eb"],
  ["im-feishu", "#0ea5e9"],
  ["im-feishu-deep", "#0284c7"],
  ["im-weixin", "#22c55e"],
  ["im-weixin-deep", "#16a34a"],
  ["onboarding-error-text", "#ff8b7a"],
  ["onboarding-error-tint", "rgb(255, 70, 70)"],
];
for (const [token, literal] of newTokens) {
  const actual = rootValue(token);
  ok(actual !== null && actual.toLowerCase() === literal.toLowerCase(),
    `--${token} stays ${literal} (found ${actual ?? "<missing>"})`);
}

// Group 2 — B2 route-A anchors (the tokens the replaced literals mapped onto).
const anchors: Array<[string, string]> = [
  // badge borders: rgba(116,184,122,.4)/rgba(217,164,65,.4) are byte-equal
  // color-mix compositions of these exact definitions (#74b87a = rgb(116,184,122),
  // #d9a441 = rgb(217,164,65)).
  ["ok", "#74b87a"],
  ["warn", "#d9a441"],
  // white haze / backdrop / popover shadow composition bases.
  ["highlight-color", "#fff"],
  ["scrim-color", "#000"],
  ["shadow-color", "#000"],
];
for (const [token, literal] of anchors) {
  const actual = rootValue(token);
  ok(actual !== null && actual.toLowerCase() === literal.toLowerCase(),
    `anchor --${token} stays ${literal} (found ${actual ?? "<missing>"})`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
