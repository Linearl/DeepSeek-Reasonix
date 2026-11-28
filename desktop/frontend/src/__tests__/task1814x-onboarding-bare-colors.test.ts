// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1814x-onboarding-bare-colors.test.ts
// tl1814x legacy item 3 — onboarding bare-color count pin.
//
// Why: tl1814 (六组梳理施工图) found the onboarding panel mixing var() tokens
// with literal colors in the SAME roles — e.g. the submit button hover flips
// to a literal #e08868 while the idle state uses var(--accent)-derived
// values, and --accent-fg stays a token while its hover counterpart is a
// literal #1a0e08. Under a non-baseline theme that mix is a contrast defect
// (blue accent + dark-brown literal fg), registered as tl1814 fix item B5.
// B5 is NOT executed yet (ratchet still 197 on the tl1814x baseline), so
// nothing here is normalized today — this pin freezes the CURRENT stock so
// the B5 window cannot silently grow it.
//
// Counting criteria: identical to scripts/check-css-color-ratchet.mjs
// (strip comments; custom-property declaration values are definitions, not
// uses; var() fallback payloads are the token mechanism's legal form) applied
// to the onboarding section of src/styles.css only. The section runs from the
// `.onboarding {` rule to the next section comment ("Memory filter input").
//
// Pin semantics: count <= PIN with equality expected. ANY new literal in the
// section fails this test — route it through the tl1814 plan (B5 semantic
// alignment, or a registered exemption in the tl1814 豁免清单) instead of
// hardcoding another value. If a B5 batch legitimately LOWERS the count,
// tighten PIN in the same commit. Never raise it.
//
// Current stock, 9 uses (tl1800 batch 2 tightened from 12: the error row's
// #ff8b7a / rgba(255,70,70,.08) / rgba(255,70,70,.25) moved to
// var(--onboarding-error-text) / color-mix(var(--onboarding-error-tint)) —
// tl1814 plan B4; the remaining 9 are the B5 declaration-microdiff stock):
//   scrim/glow   rgba(217,119,87,.06) / rgba(7,8,10,.92) x2 / rgba(217,119,87,.04)
//   focus ring   rgba(217,119,87,.18)
//   submit hover #1a0e08 / #e08868
//   spinner      rgba(26,14,8,.3) / #1a0e08
//   link/foot    #e08868

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const stylesPath = `${here}../styles.css`;

// Ratchet-identical counting, inlined so this pin stays decoupled from the
// ratchet script's internals (which other lines actively evolve).
function stripComments(source: string): string {
  let out = "";
  let state: "normal" | "comment" | '"' | "'" = "normal";
  for (let i = 0; i < source.length; i += 1) {
    const char = source[i];
    const next = source[i + 1];
    if (state === "comment") {
      if (char === "*" && next === "/") { out += "  "; i += 1; state = "normal"; }
      else out += char === "\n" ? "\n" : " ";
      continue;
    }
    if (state === '"' || state === "'") {
      out += char;
      if (char === "\\") { out += next ?? ""; i += 1; }
      else if (char === state) state = "normal";
      continue;
    }
    if (char === "/" && next === "*") { out += "  "; i += 1; state = "comment"; continue; }
    if (char === '"' || char === "'") { state = char; out += char; continue; }
    out += char;
  }
  return out;
}

function isBoundaryChar(char: string): boolean {
  return char === "{" || char === "}" || char === ";";
}

function blankNonUseSites(source: string): string {
  const chars = source.split("");
  const declRe = /--[A-Za-z0-9_-]+[ \t]*:/g;
  let m: RegExpExecArray | null;
  while ((m = declRe.exec(source)) !== null) {
    let p = m.index - 1;
    while (p >= 0 && /\s/.test(source[p])) p -= 1;
    if (p >= 0 && !isBoundaryChar(source[p])) continue;
    const colon = source.indexOf(":", m.index + m[0].length - 1);
    let depth = 0;
    let j = colon + 1;
    for (; j < chars.length; j += 1) {
      const c = chars[j];
      if (c === "(" || c === "[") depth += 1;
      else if (c === ")" || c === "]") { if (depth === 0) break; depth -= 1; }
      else if (depth === 0 && (c === ";" || c === "}")) break;
    }
    for (let k = colon + 1; k < j; k += 1) if (chars[k] !== "\n") chars[k] = " ";
  }
  const text = chars.join("");
  const varRe = /var\(/g;
  while ((m = varRe.exec(text)) !== null) {
    let depth = 1;
    let j = m.index + m[0].length;
    let comma = -1;
    for (; j < chars.length && depth > 0; j += 1) {
      const c = chars[j];
      if (c === "(") depth += 1;
      else if (c === ")") depth -= 1;
      else if (c === "," && depth === 1 && comma < 0) comma = j;
    }
    if (comma >= 0) for (let k = comma + 1; k < j - 1; k += 1) if (chars[k] !== "\n") chars[k] = " ";
  }
  return chars.join("");
}

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntl1814x onboarding bare-color count pin (B5 regression guard)");

const styles = readFileSync(stylesPath, "utf8");
const lines = styles.split("\n");

const start = lines.findIndex((l) => l.startsWith(".onboarding {"));
const end = lines.findIndex((l) => l.startsWith("/* Memory filter input"));
ok(start > 0 && end > start, `onboarding section located (start line ${start + 1}, end anchor ${end + 1}) — if the anchors moved, re-pin deliberately`);

const section = lines.slice(start, end).join("\n");
const stripped = blankNonUseSites(stripComments(section));
const hits: Array<{ line: number; literal: string }> = [];
const COLOR_VALUE = /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/g;
let m: RegExpExecArray | null;
while ((m = COLOR_VALUE.exec(stripped)) !== null) {
  const line = stripped.slice(0, m.index).split("\n").length;
  hits.push({ line: start + line, literal: m[0].toLowerCase() });
}

// tl1814 报告 B5 项 + tl1814x 实测（5cfce0caa6）12 处；tl1800 批二（B4
// 错误族收口）同 commit 下调至 9 处。收口（B5 落地）时继续同 commit 下调；
// 新增任何字面色值即红——走 tl1814 批次语义对齐或豁免清单登记，禁止静默
// 加第三个混用位。
const PIN = 9;
ok(hits.length === PIN, `bare color stock pinned at ${PIN} (found ${hits.length})${hits.length !== PIN ? `:\n${hits.map((h) => `    styles.css:${h.line}  ${h.literal}`).join("\n")}` : ""}`);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
