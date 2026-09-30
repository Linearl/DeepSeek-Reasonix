#!/usr/bin/env node
// check-experimental-wiring: scan the full wiring matrix of every `experimental_*`
// switch (A6 guard, signal report only — it never mutates anything).
//
// An experimental switch is only real if a user can actually reach it. Over time
// switches accreted at different depths: some are config-defined but never
// rendered, some render but have no settings toggle, some crossed the Wails
// boundary without a TypeScript field. Each hop compiles fine on its own, so
// nothing fails loudly — the wiring just quietly ends halfway. This script makes
// the half-wired state visible by walking every switch through the chain it
// needs to be user-reachable:
//
//   config 定义 → setter → 渲染表 → bridge → types → UI → locales
//
// Stages and where each one lives (repo shapes verified 2026-09-30):
//   def     toml:"experimental_*" struct field in internal/config/*.go (non-test)
//   setter  func (c *Config) Set<Field> in internal/config/*.go, and/or
//           func (a *App) Set<Field> in desktop/*.go
//   render  the tag literal in internal/config/render.go (the render table)
//   bridge  json:"experimental<...>" tag on the desktop settings view (Go side
//           of the Wails contract, desktop/*.go)
//   types   matching camelCase field in desktop/frontend/src/lib/settingsViewTypes.ts
//   ui      camelCase usage in desktop/frontend/src components/runtime code
//   locales "settings.<id>" key derived from the settings toggle row, or a
//           fallback literal match, in each of en.ts / zh.ts / zh-TW.ts
//
// Scope notes (统计口径):
//   - The switch inventory is the set of toml tags in internal/config (the
//     authority). Tokens referenced elsewhere but never config-defined are
//     reported separately as ghost tokens (comment text is stripped first so
//     prose mentions do not count).
//   - A switch missing UI/bridge/types/locales is not automatically a defect:
//     engine-only switches (agent-internal gates) legitimately stop after
//     render. The matrix is the signal; triage is yours.
//   - Both Desktop and Agent structs may define the same tag (settings-view
//     mirrors). Every def site is recorded; the first one is the evidence.
//
// Usage:   node scripts/check-experimental-wiring.mjs [repoRoot] [--strict] [--no-color]
// Exit:    0 = report printed (default; signal only), 1 = --strict and any miss.

import { readFileSync, readdirSync } from "node:fs";
import { join, relative, sep } from "node:path";

const repoRoot = resolveRepoRoot(process.argv[2] ?? ".");
const strict = process.argv.includes("--strict");
const noColor = process.argv.includes("--no-color") || Boolean(process.env.NO_COLOR);
const useColor = process.stdout.isTTY === true && !noColor;

function resolveRepoRoot(root) {
  // Accept either the repo root or a nested path; walk up until go.mod shows up.
  let dir = root;
  for (let i = 0; i < 6; i += 1) {
    try {
      if (readFileSync(join(dir, "go.mod"), "utf8").includes("module ")) return dir;
    } catch {
      /* keep walking */
    }
    const parent = join(dir, "..");
    if (parent === dir) break;
    dir = parent;
  }
  return root;
}

const paint = {
  red: (s) => (useColor ? `\x1b[31m${s}\x1b[0m` : s),
  green: (s) => (useColor ? `\x1b[32m${s}\x1b[0m` : s),
  yellow: (s) => (useColor ? `\x1b[33m${s}\x1b[0m` : s),
  dim: (s) => (useColor ? `\x1b[2m${s}\x1b[0m` : s),
  bold: (s) => (useColor ? `\x1b[1m${s}\x1b[0m` : s),
};

/** Recursive file walk; skips trees that never carry wiring. */
function walk(dir, out = []) {
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (
        entry.name === "node_modules" ||
        entry.name === ".git" ||
        entry.name === "dist" ||
        entry.name === "testdata" ||
        entry.name === "__tests__" ||
        entry.name === "__fixtures__"
      ) {
        continue;
      }
      walk(full, out);
    } else {
      out.push(full);
    }
  }
  return out;
}

/** Strips // line comments (best effort) so prose mentions stop counting. */
function stripLineComments(source) {
  return source
    .split("\n")
    .map((line) => line.replace(/(^|[^:"'`])\/\/.*$/, "$1"))
    .join("\n");
}

const rel = (file) => relative(repoRoot, file).split(sep).join("/");
const short = (file, line) => `${rel(file).split("/").pop()}:${line}`;

// ---------------------------------------------------------------------------
// Stage sources
// ---------------------------------------------------------------------------

const configFiles = walk(join(repoRoot, "internal", "config")).filter(
  (f) => f.endsWith(".go") && !f.endsWith("_test.go"),
);
const renderFile = join(repoRoot, "internal", "config", "render.go");
const desktopGoFiles = walk(join(repoRoot, "desktop")).filter(
  (f) => f.endsWith(".go") && !f.endsWith("_test.go"),
);
const typesFile = join(repoRoot, "desktop", "frontend", "src", "lib", "settingsViewTypes.ts");
const localesDir = join(repoRoot, "desktop", "frontend", "src", "locales");
const localeFiles = ["en.ts", "zh.ts", "zh-TW.ts"].map((name) => join(localesDir, name));
// Keep locales out of the UI stage evidence: they are their own stage, and
// settingsViewTypes.ts is the types stage.
const localesAbs = new Set(localeFiles.map((f) => f.split(sep).join("/")));
const uiSources = walk(join(repoRoot, "desktop", "frontend", "src"))
  .filter((f) => (f.endsWith(".tsx") || f.endsWith(".ts")) && !f.endsWith(".test.ts") && !f.endsWith(".test.tsx"))
  .filter((f) => !localesAbs.has(f.split(sep).join("/")))
  .filter((f) => !f.endsWith(`${sep}settingsViewTypes.ts`))
  .map((file) => ({ file, source: readFileSync(file, "utf8") }));

const linesOf = new Map();
function lines(file) {
  if (!linesOf.has(file)) linesOf.set(file, readFileSync(file, "utf8").split("\n"));
  return linesOf.get(file);
}

function findLines(file, needle) {
  const hits = [];
  lines(file).forEach((text, i) => {
    if (text.includes(needle)) hits.push(i + 1);
  });
  return hits;
}

function findLinesIn(files, needle) {
  const hits = [];
  for (const file of files) {
    for (const lineNo of findLines(file, needle)) hits.push({ file, lineNo });
  }
  return hits;
}

// ---------------------------------------------------------------------------
// Inventory: parse toml-tagged Experimental* fields out of internal/config
// ---------------------------------------------------------------------------

const defRe = /^\s*(Experimental\w+)\s+\*?bool\s+`([^`]*)`/;
const tomlRe = /toml:"(experimental_[a-z0-9_]+)"/;

/** @type {Map<string, {tag: string, field: string, sites: {file: string, lineNo: number}[]}>} */
const switches = new Map();
for (const file of configFiles) {
  lines(file).forEach((text, i) => {
    const def = defRe.exec(text);
    if (!def) return;
    const tag = tomlRe.exec(def[2])?.[1];
    if (!tag) return;
    if (!switches.has(tag)) switches.set(tag, { tag, field: def[1], sites: [] });
    const sw = switches.get(tag);
    if (sw.field !== def[1]) {
      // Same tag mapped to two field names would be a config bug; surface it.
      console.error(`check-experimental-wiring: tag ${tag} maps to both ${sw.field} and ${def[1]}`);
      process.exit(1);
    }
    sw.sites.push({ file, lineNo: i + 1 });
  });
}

const camel = (field) => field.charAt(0).toLowerCase() + field.slice(1);

// Settings toggle rows: `{ id: "dream", group: ..., label: t("settings.dream"), on: Boolean(s.experimentalDream) }`
const rowRe = /\{ id: "([A-Za-z0-9_]+)"[^{}]*?s\.(experimental[A-Za-z0-9]+)/g;
const camelToId = new Map();
for (const { source } of uiSources) {
  for (const match of source.matchAll(rowRe)) {
    if (!camelToId.has(match[2])) camelToId.set(match[2], match[1]);
  }
}

// Function index over the setter-bearing Go files. Renamed setters exist in the
// wild — the task-231 master switch is stored by `SetPreapproveManagedPaths`
// (no Experimental prefix, five arguments in one write) — so the setter stage
// falls back to "any Config method whose body assigns the field". Safe to be
// package-wide: every `.Experimental<Field> =` assignment in internal/config
// lives inside edit.go setter bodies (verified 2026-09-30); desktop App methods
// are NOT eligible for the fallback because their view builders assign fields
// when rendering, which is the bridge stage, not a setter.
const funcRe = /^func \((\w+) \*?(\w+)\) (\w+)\(/;
const funcIndex = []; // {file, recv, name, start, body}
for (const file of [...configFiles, ...desktopGoFiles]) {
  const ls = lines(file);
  let current = null;
  ls.forEach((text, i) => {
    const fn = funcRe.exec(text);
    if (fn) {
      current = { file, recv: fn[2], name: fn[3], start: i + 1, body: [] };
      funcIndex.push(current);
      return;
    }
    if (current && text.startsWith("}")) current = null;
    else if (current) current.body.push(text);
  });
}

// Ghost-token sweep: experimental_* tokens in live code (comments stripped),
// outside the config definitions, that name no known switch.
const sweepRoots = [
  ...walk(join(repoRoot, "internal")).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go")),
  ...desktopGoFiles,
  ...walk(join(repoRoot, "desktop", "frontend", "src")).filter(
    (f) => f.endsWith(".ts") || f.endsWith(".tsx"),
  ),
];
const tokenRe = /experimental_[a-z0-9_]+/g;
const ghost = new Map();
for (const file of sweepRoots) {
  const source = stripLineComments(readFileSync(file, "utf8"));
  for (const match of source.matchAll(tokenRe)) {
    const token = match[0].replace(/_+$/, "");
    if (switches.has(token)) continue;
    if (!ghost.has(token)) ghost.set(token, []);
    ghost.get(token).push(short(file, source.slice(0, match.index).split("\n").length));
  }
}

// ---------------------------------------------------------------------------
// Per-switch wiring
// ---------------------------------------------------------------------------

/** Formats a hit list as short evidence, e.g. `edit.go:562(+app settings_preferences.go:186)`. */
function evidence(hits, max = 2) {
  if (hits.length === 0) return "";
  const shown = hits.slice(0, max).map((h) => short(h.file, h.lineNo));
  const extra = hits.length > max ? `(+${hits.length - max})` : "";
  return `${shown.join(" ")}${extra}`;
}

const rows = switches.size === 0 ? [] : [...switches.values()].map((sw) => {
  const name = camel(sw.field);

  // def — always found by construction; evidence = every def site.
  const def = { status: "ok", evidence: evidence(sw.sites.map((s) => ({ file: s.file, lineNo: s.lineNo })), 3) };

  // setter — Config setter and/or App setter, by name; renamed Config setters
  // are caught by the function-body fallback described at the funcIndex build.
  const primary = funcIndex.filter(
    (f) =>
      f.name === `Set${sw.field}` &&
      ((f.recv === "Config" && f.file.startsWith(join(repoRoot, "internal", "config"))) ||
        (f.recv === "App" && f.file.startsWith(join(repoRoot, "desktop")))),
  );
  const renamed = funcIndex.filter(
    (f) =>
      f.recv === "Config" &&
      f.file.startsWith(join(repoRoot, "internal", "config")) &&
      f.name !== `Set${sw.field}` &&
      new RegExp(`\\.${sw.field}\\s*=[^=]`).test(f.body.join("\n")),
  );
  const setterHits = [...primary, ...renamed].map((f) => ({ file: f.file, lineNo: f.start }));
  const setter = {
    status: setterHits.length > 0 ? "ok" : "miss",
    evidence: evidence(setterHits),
    detail: renamed.length > 0 && primary.length === 0 ? `renamed setter: ${renamed.map((f) => f.name).join(",")}` : primary.length > 0 && renamed.length > 0 ? "named+renamed" : "",
  };

  // render — the render table in internal/config/render.go.
  const renderHits = findLines(renderFile, sw.tag).map((lineNo) => ({ file: renderFile, lineNo }));
  const render = { status: renderHits.length > 0 ? "ok" : "miss", evidence: evidence(renderHits) };

  // bridge — Go side of the Wails contract (json tag on the settings view).
  const bridgeHits = findLinesIn(desktopGoFiles, `json:"${name}"`);
  const bridge = { status: bridgeHits.length > 0 ? "ok" : "miss", evidence: evidence(bridgeHits) };

  // types — TypeScript mirror field.
  const typesHits = findLines(typesFile, name).map((lineNo) => ({ file: typesFile, lineNo }));
  const types = { status: typesHits.length > 0 ? "ok" : "miss", evidence: evidence(typesHits) };

  // ui — component/runtime usage of the camelCase field.
  const uiHits = [];
  let usageSource = null;
  for (const { file, source } of uiSources) {
    const idx = source.indexOf(name);
    if (idx === -1) continue;
    uiHits.push({ file, lineNo: source.slice(0, idx).split("\n").length });
    usageSource ??= source;
  }
  const ui = { status: uiHits.length > 0 ? "ok" : "miss", evidence: evidence(uiHits, 3) };

  // locales — settings.<id> label key (id derived from the toggle row, or from
  // the camel name minus the experimental prefix — several switches have a
  // settings page but no `{ id: ... }` toggle row), or a fallback literal match
  // of the camel name / tag in the locale files.
  const id = camelToId.get(name);
  const derivedId = name.startsWith("experimental") ? camel(name.slice("experimental".length)) : null;
  const keys = [];
  for (const candidate of [id, derivedId]) {
    if (candidate) keys.push(`"settings.${candidate}"`);
  }
  keys.push(`"${name}`, sw.tag);
  const localeHits = localeFiles.map((file) => {
    const source = readFileSync(file, "utf8");
    for (const key of keys) {
      const lineNo = findLines(file, key)[0];
      if (lineNo) return { file, lineNo, key: key.replace(/^"/, "") };
    }
    return null;
  });
  const foundLocales = localeHits.filter(Boolean);
  const localeStatus = foundLocales.length === localeFiles.length ? "ok" : foundLocales.length > 0 ? "part" : "miss";
  const locales = {
    status: localeStatus,
    evidence: foundLocales.map((h) => `${short(h.file, h.lineNo)}(${h.key.split(":")[0]})`).join(" "),
    via: id ? `settings.${id}` : derivedId ? `settings.${derivedId}~` : "fallback",
  };

  return { sw, name, def, setter, render, bridge, types, ui, locales };
});

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------

const stages = [
  ["def", (r) => r.def],
  ["setter", (r) => r.setter],
  ["render", (r) => r.render],
  ["bridge", (r) => r.bridge],
  ["types", (r) => r.types],
  ["ui", (r) => r.ui],
  ["locales", (r) => r.locales],
];

function cell(status) {
  if (status === "ok") return paint.green("ok    ");
  if (status === "part") return paint.yellow("part  ");
  return paint.red("[MISS]");
}

const nameWidth = Math.max(...rows.map((r) => r.name.length), "experimental_".length) + 2;

const today = new Date();
const datestamp = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}-${String(today.getDate()).padStart(2, "0")}`;

console.log(`check-experimental-wiring: experimental_* switch wiring matrix`);
console.log(paint.dim(`repo=${rel(repoRoot) || "."}  switches=${rows.length}  date=${datestamp}`));
console.log(paint.dim(`chain: def → setter → render → bridge → types → ui → locales (signal report; engine-only switches legitimately stop early)`));
console.log("");
console.log(paint.bold(`${"switch".padEnd(nameWidth)}def    setter render bridge types ui     locales`));
for (const row of rows) {
  console.log(`${row.name.padEnd(nameWidth)}${stages.map(([, get]) => cell(get(row).status)).join(" ")}`);
  const parts = [];
  for (const [label, get] of stages) {
    const stage = get(row);
    const ev = stage.evidence || "-";
    const note = stage.detail ? paint.dim(` [${stage.detail}]`) : "";
    const tag = stage.status === "ok" ? paint.dim(ev) : stage.status === "part" ? paint.yellow(ev) : paint.red(ev);
    parts.push(`${label}=${tag}${note}`);
  }
  console.log(paint.dim(" ".repeat(nameWidth)) + paint.dim("│ ") + parts.join(paint.dim("  ")));
}

console.log("");
console.log(paint.bold("Summary (missing per stage):"));
const summary = stages.map(([label, get]) => {
  const miss = rows.filter((r) => get(r).status === "miss").length;
  const part = rows.filter((r) => get(r).status === "part").length;
  return { label, miss, part };
});
for (const { label, miss, part } of summary) {
  const text = `  ${label.padEnd(8)} miss=${String(miss).padStart(2)}${part ? `  part=${part}` : ""}`;
  console.log(miss > 0 ? paint.red(text) : text);
}
const fullyWired = rows.filter((r) => stages.every(([, get]) => get(r).status === "ok")).length;
console.log(`  fully wired: ${fullyWired}/${rows.length}`);
const neverUi = rows.filter((r) => r.ui.status === "miss").map((r) => r.name);
if (neverUi.length > 0) {
  console.log(paint.dim(`  engine-only (no UI usage; expected for agent-internal gates): ${neverUi.join(", ")}`));
}

if (ghost.size > 0) {
  console.log("");
  console.log(paint.yellow("Ghost tokens (experimental_* referenced in live code but not config-defined):"));
  for (const [token, sites] of ghost) {
    console.log(paint.yellow(`  ${token}  ← ${sites.slice(0, 3).join(" ")}${sites.length > 3 ? ` (+${sites.length - 3})` : ""}`));
  }
}

if (strict) {
  const bad = summary.some((s) => s.miss > 0);
  if (bad) {
    console.error("\ncheck-experimental-wiring: --strict FAIL (missing wiring above)");
    process.exit(1);
  }
  console.log("\ncheck-experimental-wiring: --strict OK");
}
console.log(paint.dim("\n(signal report only — exit 0 unless --strict)"));
