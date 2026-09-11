#!/usr/bin/env node
// check-bridge-json-tags: prove the Wails boundary has a matching contract on both
// sides.
//
// The boundary is JSON, and neither language checks it for us: Go emits a struct's
// Go field names when a field has no tag, and a TypeScript `interface` is an
// assertion over whatever arrives, not a verification of it. So a renamed or
// untagged field compiles, type-checks, and silently yields undefined at runtime.
//
// That is not hypothetical. ConsolidationReport crossed the bridge without tags, so
// the frontend read `report.blockedByDivergence` as undefined for as long as the
// code existed - which turned a merge the engine had *refused* into one the panel
// reported as succeeded, and meant the confirmation offering to let a copy win never
// appeared. Nothing errored anywhere.
//
// This check treats `desktop/frontend/src/lib/bridge.ts` as the contract: every
// interface declared there names a value the frontend expects, and the Go struct of
// the same name must carry exactly the matching tags.
//
// Usage: node scripts/check-bridge-json-tags.mjs [repoRoot]
// Exits non-zero on a mismatch, so it can gate a build.

import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

const repoRoot = resolve(process.argv[2] ?? ".");
const bridgePath = join(repoRoot, "desktop/frontend/src/lib/bridge.ts");
// Bridge values are not all declared in desktop/: the agent package owns the reports
// the desktop package returns verbatim, so both trees are scanned. Scanning only
// desktop/ is what let ConsolidationReport - the struct this check exists for - go
// unnoticed.
const goRoots = [join(repoRoot, "desktop"), join(repoRoot, "internal")];

/** Interfaces declared in bridge.ts that are frontend-only conveniences rather than
 *  values returned across the bridge. Keep this empty unless a real mismatch makes
 *  an entry necessary - every name added here stops being checked. */
const FRONTEND_ONLY = new Set([]);

/** Parse `export interface Name { field: Type; ... }` from bridge.ts. */
function parseTypeScriptInterfaces(source) {
  const interfaces = new Map();
  const re = /export interface (\w+)\s*\{/g;
  let match;
  while ((match = re.exec(source)) !== null) {
    const name = match[1];
    // Walk braces so a nested object literal cannot end the body early.
    let depth = 1;
    let i = re.lastIndex;
    while (i < source.length && depth > 0) {
      if (source[i] === "{") depth += 1;
      else if (source[i] === "}") depth -= 1;
      i += 1;
    }
    // Comments first, so a `// a: b` line cannot register a field.
    const body = source
      .slice(re.lastIndex, i - 1)
      .replace(/\/\/[^\n]*/g, "")
      .replace(/\/\*[\s\S]*?\*\//g, "");
    const fields = new Map();
    const fieldRe = /^\s*(\w+)(\?)?\s*:/gm;
    let fieldMatch;
    while ((fieldMatch = fieldRe.exec(body)) !== null) {
      fields.set(fieldMatch[1], Boolean(fieldMatch[2]));
    }
    interfaces.set(name, fields);
  }
  return interfaces;
}

/** Every non-test .go file under the given roots, recursively. */
function goFilesUnder(roots) {
  const out = [];
  const visit = (dir) => {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === "node_modules" || entry.name === "testdata") continue;
        visit(full);
      } else if (entry.name.endsWith(".go") && !entry.name.endsWith("_test.go")) {
        out.push(full);
      }
    }
  };
  for (const root of roots) visit(root);
  return out;
}

/** Parse `type Name struct { ... }` from every non-test .go file under the roots. */
function parseGoStructs(roots) {
  const structs = new Map();
  for (const file of goFilesUnder(roots)) {
    const source = readFileSync(file, "utf8");
    const re = /^type (\w+) struct \{/gm;
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
      const body = source.slice(re.lastIndex, i - 1).replace(/\/\/[^\n]*/g, "");
      const tagged = new Map();
      const untagged = [];
      const seen = new Set();
      const taggedRe = /^\s*([A-Z]\w*)\s+([^\n`]+?)\s+`([^`]*)`/gm;
      let fieldMatch;
      while ((fieldMatch = taggedRe.exec(body)) !== null) {
        seen.add(fieldMatch[1]);
        const tag = /json:"([^",]*)/.exec(fieldMatch[3]);
        if (tag && tag[1] && tag[1] !== "-") tagged.set(fieldMatch[1], tag[1]);
      }
      const plainRe = /^\s*([A-Z]\w*)\s+([^\n`]+?)\s*$/gm;
      while ((fieldMatch = plainRe.exec(body)) !== null) {
        if (seen.has(fieldMatch[1])) continue;
        if (/^json:/.test(fieldMatch[2])) continue;
        untagged.push(fieldMatch[1]);
      }
      structs.set(name, { tagged, untagged, file });
    }
  }
  return structs;
}

const bridge = readFileSync(bridgePath, "utf8");
const interfaces = parseTypeScriptInterfaces(bridge);
const structs = parseGoStructs(goRoots);

const problems = [];
let checked = 0;
for (const [name, tsFields] of interfaces) {
  if (FRONTEND_ONLY.has(name)) continue;
  const go = structs.get(name);
  if (!go) continue; // not every frontend interface mirrors a desktop struct
  checked += 1;

  const tsNames = new Set(tsFields.keys());
  const goNames = new Set(go.tagged.values());

  for (const field of tsNames) {
    if (goNames.has(field)) continue;
    const asGoName = field.charAt(0).toUpperCase() + field.slice(1);
    if (go.untagged.includes(asGoName)) {
      problems.push(
        `${name}.${field}: Go field ${asGoName} carries no json tag, so the frontend reads undefined`,
      );
    } else {
      problems.push(`${name}.${field}: declared in bridge.ts but absent from ${go.file}`);
    }
  }
  for (const [field, tag] of go.tagged) {
    if (!tsNames.has(tag)) {
      problems.push(`${name}.${field}: Go tag "${tag}" is not declared in bridge.ts`);
    }
  }
}

if (problems.length > 0) {
  console.error("check-bridge-json-tags: FAIL");
  for (const problem of problems) console.error(`  - ${problem}`);
  console.error(
    `\n${problems.length} mismatch(es) across ${checked} mirrored interface(s).\n` +
      "A missing tag fails silently on both sides: Go emits the Go field name, and the\n" +
      "TypeScript declaration asserts rather than verifies, so the value arrives as\n" +
      "undefined with nothing raising. Add the tag, or correct whichever side is wrong.",
  );
  process.exit(1);
}
console.log(`check-bridge-json-tags: OK (${checked} mirrored interface(s) agree)`);
