#!/usr/bin/env node
// tl1800 — bare color value ratchet for CSS use sites.
//
// Why: the theme token system is complete on the definition side (14+ families,
// ~800 definition lines) but use sites still carry bare literals (#fff,
// #2f80ff, rgba(...)). The 2026-11-24 audit (docs/硬编码与魔法数字审计-样式与
// 代码抽样-20261124.md, face A) measured 246 true bare uses in styles.css after
// three strips. This gate pins the total and only lets it go down.
//
// Division of labor with the two existing gates (no double coverage):
//   - check-class-contract.mjs assert3 blocks NEW bare colors per-site via a
//     whitelist snapshot (only-new-fail; stale whitelist entries are ignored).
//   - check-theme-token-contract.mjs validates var() reference legality
//     (undefined/retired tokens), never "should have used a token".
//   - THIS ratchet pins the TOTAL stock (count <= BASELINE). A remove-one
//     add-one swap passes here but fails assert3; pure additions fail both.
//
// Counting criteria (same as the audit's three strips):
//   1. custom-property definitions (`--x: #fff`) are the legitimate source —
//      their values are excluded;
//   2. comments are stripped before scanning;
//   3. var() fallback payloads (`var(--x, #fff)`) are the token mechanism's
//      legal form — excluded, same as assert3's `value.includes("var(")` rule.
// Scope: every .css under src/ (same file set check-theme-token-contract scans).
//
// Usage (from desktop/frontend):
//   node scripts/check-css-color-ratchet.mjs             # count <= BASELINE, exit 1 otherwise
//   node scripts/check-css-color-ratchet.mjs --report    # frequency table + file:line list
//   node scripts/check-css-color-ratchet.mjs --self-test # negative tests: must be counted

import fs from "node:fs";
import path from "node:path";
import { tmpdir } from "node:os";
import { mkdtempSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");
const sourceRoot = path.join(frontendRoot, "src");

// Ratchet ceiling, total bare color uses across src/**/*.css. History:
//   322: tl1800 gate introduction (three-strip count on the f158412e4a tree,
//        all src css; the audit doc measured 246 with a line-deduped awk on
//        styles.css alone — same mechanism, narrower sampling, older tree).
//   197: tl1800 batch 1 (125 same-value conversions: whites -> text-on-color
//        /surface-white/highlight-color, blacks -> shadow/scrim/shade
//        color-mix forms, IM brand colors -> brand-*).
// Lower this number in the same commit that removes uses. Never raise it.
const BASELINE = 197;

function listCSSFiles(directory) {
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...listCSSFiles(target));
    else if (entry.isFile() && target.endsWith(".css")) files.push(target);
  }
  return files.sort();
}

// Blank comments (keep newlines) and honor strings, so a comment marker inside
// a content string cannot eat half the sheet.
function stripComments(source) {
  let out = "";
  let state = "normal";
  for (let i = 0; i < source.length; i += 1) {
    const char = source[i];
    const next = source[i + 1];
    if (state === "comment") {
      if (char === "*" && next === "/") {
        out += "  ";
        i += 1;
        state = "normal";
      } else {
        out += char === "\n" ? "\n" : " ";
      }
      continue;
    }
    if (state === '"' || state === "'") {
      out += char;
      if (char === "\\") {
        out += next ?? "";
        i += 1;
      } else if (char === state) {
        state = "normal";
      }
      continue;
    }
    if (char === "/" && next === "*") {
      out += "  ";
      i += 1;
      state = "comment";
      continue;
    }
    if (char === '"' || char === "'") {
      state = char;
      out += char;
      continue;
    }
    out += char;
  }
  return out;
}

function isBoundaryChar(char) {
  return char === "{" || char === "}" || char === ";";
}

// Blank the parts of custom-property declarations that hold the value
// (`--x: #fff` -> `--x:     `) and var() fallback payloads
// (`var(--x, #fff)` -> `var(--x,     )`). Offsets stay intact.
function blankNonUseSites(source) {
  const chars = source.split("");
  // 1. custom-property declaration values. A match only counts at declaration
  //    position (previous non-space char is one of {}; ) so `.foo--bar:hover`
  //    in a selector never trips it.
  const declRe = /--[A-Za-z0-9_-]+[ \t]*:/g;
  let m;
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
      else if (c === ")" || c === "]") {
        if (depth === 0) break;
        depth -= 1;
      } else if (depth === 0 && (c === ";" || c === "}")) break;
    }
    for (let k = colon + 1; k < j; k += 1) {
      if (chars[k] !== "\n") chars[k] = " ";
    }
  }
  // 2. var() fallback payloads: blank after the first top-level comma.
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
    if (comma >= 0) {
      for (let k = comma + 1; k < j - 1; k += 1) {
        if (chars[k] !== "\n") chars[k] = " ";
      }
    }
  }
  return chars.join("");
}

const COLOR_VALUE = /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/g;

function scanFile(file) {
  const raw = fs.readFileSync(file, "utf8");
  const stripped = blankNonUseSites(stripComments(raw));
  const rel = path.relative(frontendRoot, file).replace(/\\/g, "/");
  const hits = [];
  COLOR_VALUE.lastIndex = 0;
  let match;
  while ((match = COLOR_VALUE.exec(stripped)) !== null) {
    const line = stripped.slice(0, match.index).split("\n").length;
    const segStart = Math.max(
      stripped.lastIndexOf(";", match.index),
      stripped.lastIndexOf("{", match.index),
      stripped.lastIndexOf("\n", match.index),
    );
    const segEndCandidates = [
      stripped.indexOf(";", match.index),
      stripped.indexOf("}", match.index),
      stripped.indexOf("\n", match.index),
    ].filter((v) => v >= 0);
    const segEnd = segEndCandidates.length ? Math.min(...segEndCandidates) : stripped.length;
    const context = stripped.slice(segStart + 1, segEnd).trim().replace(/\s+/g, " ");
    hits.push({ file: rel, line, literal: match[0].toLowerCase(), context });
  }
  return hits;
}

function scanAll() {
  return listCSSFiles(sourceRoot).flatMap((file) => scanFile(file));
}

function report(hits) {
  const byValue = new Map();
  for (const hit of hits) {
    byValue.set(hit.literal, (byValue.get(hit.literal) ?? 0) + 1);
  }
  const sorted = [...byValue.entries()].sort((a, b) => b[1] - a[1]);
  process.stdout.write(`bare color uses: ${hits.length} (baseline ${BASELINE})\n`);
  process.stdout.write("\nby value:\n");
  for (const [value, count] of sorted) {
    process.stdout.write(`  ${value.padEnd(14)} ${count}\n`);
  }
  process.stdout.write("\nsites:\n");
  for (const hit of hits) {
    process.stdout.write(`  ${hit.file}:${hit.line}  ${hit.context}\n`);
  }
}

async function selfTest() {
  const dir = mkdtempSync(path.join(tmpdir(), "tl1800-ratchet-"));
  const cases = [];
  try {
    const write = (name, content) => fs.writeFileSync(path.join(dir, name), content);
    const count = () => listCSSFiles(dir).flatMap((file) => scanFile(file)).length;

    write("legal.css", ':root { --tok: #2f80ff; }\n.a { color: var(--tok); }\n.b { color: var(--tok, #fff); }\n/* comment #123456 stays invisible */\n.c { background: var(--tok, rgba(0,0,0,.4)); }\n.d { content: "/* not a comment */"; }\n');
    cases.push(["token definitions / var fallbacks / comments / strings are NOT counted", count() === 0]);

    write("bare.css", ".e { color: #fff; }\n.f { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2); }\n.g { background: linear-gradient(145deg, #7867f2, #3b54c9); }\n");
    cases.push(["bare hex / rgba / gradient stops ARE counted", count() === 4]);

    write("mixed.css", ".h { color: color-mix(in srgb, var(--fg) 50%, #fff); }\n");
    cases.push(["literal color-mix tail without var() exemption IS counted", count() === 5]);

    write("localdef.css", ".i { --local: #ff5a3d; color: var(--local); }\n.j .k--l:hover { color: var(--local); }\n");
    cases.push(["custom property inside a rule is a definition; selector hyphens don't trip it", count() === 5]);

    let ok = true;
    for (const [label, passed] of cases) {
      process.stdout.write(`  ${passed ? "PASS" : "FAIL"}  ${label}\n`);
      if (!passed) ok = false;
    }
    return ok;
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

const mode = process.argv[2];
if (mode === "--report") {
  report(scanAll());
} else if (mode === "--self-test") {
  const ok = await selfTest();
  process.exit(ok ? 0 : 1);
} else if (mode === undefined) {
  const hits = scanAll();
  if (hits.length > BASELINE) {
    process.stdout.write(`FAIL color ratchet: ${hits.length} bare color uses > baseline ${BASELINE} (only decreases allowed):\n`);
    for (const hit of hits) process.stdout.write(`  ${hit.file}:${hit.line}  ${hit.context}\n`);
    process.exit(1);
  }
  if (hits.length < BASELINE) {
    process.stdout.write(`PASS color ratchet: ${hits.length} bare color uses <= baseline ${BASELINE} — tighten BASELINE to ${hits.length} in the same commit.\n`);
  } else {
    process.stdout.write(`PASS color ratchet: ${hits.length} bare color uses = baseline ${BASELINE}.\n`);
  }
} else {
  process.stderr.write(`unknown mode: ${mode}\n`);
  process.exit(2);
}
