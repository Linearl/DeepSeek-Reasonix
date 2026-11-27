#!/usr/bin/env node
// check-bridge-signatures: prove the three layers of the Wails method surface
// agree - the Go App methods (authority), the gitignored wailsjs bindings
// generated from them, and the hand-written AppBindings mirror in bridge.ts.
//
// tl1831-d3 measured all three gates (tsc / drift check / bridge-json-tags)
// green on a stale wailsjs while `DeletePreRepairBackup(dir string, force bool)`
// and its generated binding disagreed: 983 bridge methods - the largest segment
// of the contract - had no gate at all. This is that gate, shaped like CI's
// existing fresh-generate step but runnable locally before it (tl1842, G1+G2).
//
// Three comparisons:
//   Go vs wailsjs   a bound App method missing from (or disagreeing in arity
//                   with) App.d.ts means the gitignored bindings are stale:
//                   run `wails generate module` in desktop/. Build-tag-gated
//                   files (windows/darwin/linux variants) are exempt from the
//                   must-appear direction because CI generates on one platform
//                   only; everything else must round-trip.
//   wailsjs vs Go   a binding with no App method behind it means the method
//                   was retired from Go and the bindings are stale.
//   wailsjs vs bridge.ts
//                   every AppBindings entry must have a generated counterpart
//                   (dead-contract detection - the reverse direction the
//                   in-tsc drift check never had), and every declared entry
//                   must agree on arity with the generated binding. Generated
//                   methods without a declaration are the existing
//                   runtime-delegation segment: call sites drive declarations
//                   (tsc forces one the first time a component calls the
//                   method), so they are counted, not failed.
//
// Usage: node scripts/check-bridge-signatures.mjs [repoRoot]
// Exits non-zero on drift, so it can gate `pnpm typecheck` (pretypecheck) and
// the build chain.

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";

const repoRoot = resolve(process.argv[2] ?? ".");
const desktopDir = join(repoRoot, "desktop");
const generatedPath = join(repoRoot, "desktop/frontend/wailsjs/go/main/App.d.ts");
const bridgePath = join(repoRoot, "desktop/frontend/src/lib/bridge.ts");

/** AppBindings entries whose Go counterpart is retired (tl1842 挂账): the method
 *  exists in Go only as an UNBOUND package-level function, so it never reaches
 *  the wailsjs bindings, yet frontend code still calls it. Each entry here keeps
 *  the gate green on a documented debt; a NEW manual-only name still fails, and
 *  a listed name that starts resolving (App method bound) must be removed. */
const KNOWN_MANUAL_ONLY = {
  HeapBreakdownPath:
    "Go side is desktop/perf_read_app.go:268, a package-level func Wails does not bind; PerfMemorySection.tsx:94 still calls it, so the export-path read fails at runtime. Fixing needs an App-method binding (behavior change) - follow-up task, not a checker exception to grow.",
};

/** Top-level comma count of a parameter list string (arity). Depth-aware so
 *  `a func(int, error) string, b int` counts 2, not 3, and TS generics
 *  (`Record<string, any>`) do not split. */
function paramArity(inner) {
  let trimmed = inner.trim();
  if (trimmed === "") return 0;
  if (trimmed.endsWith(",")) trimmed = trimmed.slice(0, -1).trimEnd(); // trailing comma
  let depth = 0;
  let count = 1;
  for (const ch of trimmed) {
    if (ch === "(" || ch === "[" || ch === "{" || ch === "<") depth += 1;
    else if (ch === ")" || ch === "]" || ch === "}" || ch === ">") depth -= 1;
    else if (ch === "," && depth === 0) count += 1;
  }
  return count;
}

/** Index of the ')' closing the '(' at `open`, or end of text. */
function matchingParenFrom(text, open) {
  let depth = 0;
  for (let i = open; i < text.length; i += 1) {
    if (text[i] === "(") depth += 1;
    else if (text[i] === ")") {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return text.length;
}

/** Bound App methods: `func (r *App) Name(params)` in desktop/ root .go files
 *  (non-test). Wails binds exported App methods only; a context.Context first
 *  parameter makes wails skip the method, so those are excluded too. Root level
 *  only - desktop/cmd etc. are separate binaries, not the bound surface. Each
 *  method records whether its file carries a //go:build constraint. */
function parseGoAppMethods() {
  const methods = new Map();
  let entries;
  try {
    entries = readdirSync(desktopDir, { withFileTypes: true });
  } catch {
    return methods;
  }
  for (const entry of entries) {
    if (!entry.isFile() || !entry.name.endsWith(".go") || entry.name.endsWith("_test.go")) continue;
    const source = readFileSync(join(desktopDir, entry.name), "utf8");
    const constrained = /^\/\/go:build /m.test(source.slice(0, source.indexOf("\npackage ")));
    const re = /^func \(\w+ \*?App\) ([A-Z]\w*)\(/gm;
    let match;
    while ((match = re.exec(source)) !== null) {
      const name = match[1];
      // match[0] ends at the '(' opening the parameter list - measure there,
      // never at the receiver parens.
      const open = match.index + match[0].length - 1;
      const close = matchingParenFrom(source, open);
      const paramText = source.slice(open + 1, close);
      if (/context\.Context/.test(paramText.split(",")[0] ?? "")) continue;
      methods.set(name, { arity: paramArity(paramText), constrained, file: `desktop/${entry.name}` });
    }
  }
  return methods;
}

/** Generated bindings: `export function Name(arg1: T, arg2: U): ...;` */
function parseGeneratedBindings() {
  const methods = new Map();
  if (!existsSync(generatedPath)) return methods;
  const source = readFileSync(generatedPath, "utf8");
  const re = /^export function ([A-Za-z0-9_]+)\(([^)]*)\)/gm;
  let match;
  while ((match = re.exec(source)) !== null) {
    methods.set(match[1], { arity: paramArity(match[2]) });
  }
  return methods;
}

/** All `interface Name(extends ...)? { ... }` blocks of one file, brace-matched,
 *  each with its extends clause split into names. */
function parseInterfaceBlocks(source) {
  const blocks = new Map();
  const re = /^(?:export )?interface (\w+)(?:\s+extends ([^{]+))?\s*\{/gm;
  let match;
  while ((match = re.exec(source)) !== null) {
    const name = match[1];
    let depth = 1;
    let i = re.lastIndex;
    while (i < source.length && depth > 0) {
      if (source[i] === "{") depth += 1;
      else if (source[i] === "}") depth -= 1;
      i += 1;
    }
    const extendsNames = match[2]
      ? match[2].split(",").map((s) => s.trim().split(" ")[0]).filter(Boolean)
      : [];
    // Comments first, so prose like "the UI (see settings)" cannot register a
    // phantom method.
    const segment = source
      .slice(re.lastIndex, i - 1)
      .replace(/\/\/[^\n]*/g, "")
      .replace(/\/\*[\s\S]*?\*\//g, "");
    blocks.set(name, { segment, extendsNames, end: i });
  }
  return blocks;
}

/** The full hand-written AppBindings surface: the AppBlocks declaration blocks
 *  (interface merging may split it) plus every interface on its `extends` chain,
 *  following `import type {...} from "./x"` hops into src/lib. Both declaration
 *  forms are collected - signature `Name(params): Ret` and property
 *  `Name: typeof GeneratedApp.Name;`. */
function parseBridgeAppBindings() {
  const bridge = readFileSync(bridgePath, "utf8");
  const libDir = join(repoRoot, "desktop/frontend/src/lib");
  const files = new Map([[".", parseInterfaceBlocks(bridge)]]);
  const importIndex = new Map(); // interface name -> module specifier
  for (const m of bridge.matchAll(/import type \{([^}]+)\} from "\.\/([^"]+)"/g)) {
    for (const name of m[1].split(",").map((s) => s.trim().split(" ")[0]).filter(Boolean)) {
      importIndex.set(name, m[2]);
    }
  }

  const segments = [];
  const visited = new Set();
  const queue = ["AppBindings"];
  while (queue.length > 0) {
    const name = queue.shift();
    if (visited.has(name)) continue;
    visited.add(name);
    const specifier = importIndex.get(name) ?? ".";
    if (!files.has(specifier)) {
      files.set(specifier, parseInterfaceBlocks(readFileSync(join(libDir, `${specifier}.ts`), "utf8")));
    }
    const block = files.get(specifier).get(name);
    if (!block) continue; // a type the chain references but that carries no members here
    segments.push(block.segment);
    for (const parent of block.extendsNames) queue.push(parent);
  }

  const methods = new Map();
  for (const segment of segments) {
    // Signature form: Name(params): Ret; params may wrap lines and contain
    // callback parens - arity comes from the balanced param list.
    const sigRe = /([A-Z][A-Za-z0-9]*)\s*\(/g;
    let match;
    while ((match = sigRe.exec(segment)) !== null) {
      const open = match.index + match[0].length - 1;
      const close = matchingParenFrom(segment, open);
      const paramText = segment.slice(open + 1, close);
      if (!methods.has(match[1])) {
        methods.set(match[1], { arity: paramArity(paramText), form: "signature" });
      }
    }
    // Property form: Name: typeof GeneratedApp.Name; (arity rides on wailsjs).
    for (const m of segment.matchAll(/([A-Z][A-Za-z0-9]*)\s*:\s*typeof GeneratedApp\./g)) {
      if (!methods.has(m[1])) methods.set(m[1], { arity: null, form: "property" });
    }
  }
  return methods;
}

const goMethods = parseGoAppMethods();
const generated = parseGeneratedBindings();
const bridge = parseBridgeAppBindings();

const problems = [];

if (generated.size === 0) {
  console.error("check-bridge-signatures: FAIL");
  console.error(
    "  - desktop/frontend/wailsjs/go/main/App.d.ts is missing or has no bindings.\n" +
      "    The gitignored wailsjs is part of the contract chain: generate it before\n" +
      "    typechecking - run `wails generate module` in desktop/ (the pinned CLI is\n" +
      "    installed by `make wails-install`).",
  );
  process.exit(1);
}

// Go vs wailsjs: the freshness probe (content-based, not mtime-based).
for (const [name, go] of goMethods) {
  const gen = generated.get(name);
  if (!gen) {
    if (go.constrained) continue; // another platform's binding; may be absent
    problems.push(
      `${name}: bound App method (${go.file}) is missing from wailsjs App.d.ts - wailsjs is stale; run \`wails generate module\` in desktop/`,
    );
  } else if (gen.arity !== go.arity) {
    problems.push(
      `${name}: Go signature has ${go.arity} param(s) but wailsjs binding has ${gen.arity} - wailsjs is stale; run \`wails generate module\` in desktop/`,
    );
  }
}
for (const name of generated.keys()) {
  if (!goMethods.has(name)) {
    problems.push(
      `${name}: wailsjs binds it but no App method in desktop/ matches - the method was retired from Go; run \`wails generate module\` in desktop/`,
    );
  }
}

// wailsjs vs bridge.ts: entries the hand-written contract DOES declare must
// track the generated signature. Generated methods without a declaration are
// the existing runtime-delegation segment (call sites drive declarations, and
// several call sites feature-probe via `typeof app.X === "function"`), so they
// are counted in the summary rather than failed - declaring a method stays the
// developer's move the first time a component calls it.
let undeclared = 0;
for (const name of generated.keys()) {
  if (!bridge.has(name)) undeclared += 1;
}
for (const [name, entry] of bridge) {
  const gen = generated.get(name);
  if (gen) {
    if (entry.arity !== null && entry.arity !== gen.arity) {
      problems.push(
        `${name}: AppBindings declares ${entry.arity} param(s) but the generated binding has ${gen.arity} - mirror the current signature`,
      );
    }
    continue;
  }
  const go = goMethods.get(name);
  if (go?.constrained) continue; // another platform's binding; not in this d.ts
  if (KNOWN_MANUAL_ONLY[name]) continue; // documented debt, counted in the summary
  problems.push(
    `${name}: declared in AppBindings but absent from wailsjs App.d.ts` +
      (go ? ` (Go binding surface has it only behind a build tag; not verifiable here)` : " - dead contract, remove it or restore the Go method"),
  );
}

// Registry hygiene: a KNOWN_MANUAL_ONLY name that started resolving must leave
// the registry (upper-bound + new-is-red discipline).
for (const name of Object.keys(KNOWN_MANUAL_ONLY)) {
  if (generated.has(name)) {
    problems.push(
      `${name}: KNOWN_MANUAL_ONLY entry is stale - the method now resolves in wailsjs; remove the registry entry`,
    );
  }
}

if (problems.length > 0) {
  console.error("check-bridge-signatures: FAIL");
  for (const problem of problems) console.error(`  - ${problem}`);
  console.error(
    `\n${problems.length} drift item(s) across ${goMethods.size} Go App method(s), ` +
      `${generated.size} generated binding(s), ${bridge.size} AppBindings entries.`,
  );
  process.exit(1);
}
console.log(
  `check-bridge-signatures: OK (${goMethods.size} Go App method(s), ${generated.size} generated binding(s), ` +
    `${bridge.size} AppBindings entries, ${undeclared} generated-but-undeclared (runtime delegation), ` +
    `${Object.keys(KNOWN_MANUAL_ONLY).length} documented manual-only debt: ` +
    `${Object.keys(KNOWN_MANUAL_ONLY).join(", ")})`,
);
