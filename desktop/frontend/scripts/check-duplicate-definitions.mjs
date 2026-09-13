#!/usr/bin/env node
// Duplicate-definition audit (task 38).
//
// The fork kept re-declaring things that already existed: App.tsx alone held twenty local
// copies of helpers and types that lib/ and app-runtime/ own. Those cost merge time twice
// over — they can conflict, and they can be silently missed when upstream changes the real
// one. This finds them.
//
// Two lessons from doing it by hand, both encoded here:
//
//   1. Compare a file's definitions against the exports of OTHER files. The first pass
//      included every file in its own comparison pool, which made all 87 of
//      lib/useController.ts and lib/bridge.ts's exports look duplicated. They were not.
//   2. A name match is a lead, not a verdict. sameStringList, errorMessage and baseName
//      exist on both sides with different behaviour; folding them in would change what the
//      app does. This reports; a human decides.
//
// Report-only: a signal for the next batch, not a gate.

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";

const root = resolve("src");
const EXPORTED = /^export (?:type|interface|function|const|class) ([A-Za-z_][A-Za-z0-9_]*)/gm;

function walk(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (entry === "__tests__" || entry === "node_modules") continue;
      out.push(...walk(full));
    } else if (/\.tsx?$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

const files = walk(root);
const exportsByFile = new Map();
for (const file of files) {
  const source = readFileSync(file, "utf8");
  const names = new Set();
  for (const match of source.matchAll(EXPORTED)) names.add(match[1]);
  exportsByFile.set(file, names);
}

const exportedNames = new Map();
for (const [file, names] of exportsByFile) {
  for (const name of names) {
    if (!exportedNames.has(name)) exportedNames.set(name, []);
    exportedNames.get(name).push(relative(root, file).replace(/\\/g, "/"));
  }
}

const collisions = [...exportedNames.entries()]
  .filter(([, where]) => where.length > 1)
  .sort((a, b) => a[0].localeCompare(b[0]));

console.log(`duplicate-definitions: ${files.length} files, ${exportedNames.size} exported names`);
if (collisions.length === 0) {
  console.log("duplicate-definitions: no exported name is declared twice");
} else {
  for (const [name, where] of collisions) {
    console.log(`duplicate-definitions: ${name} declared in ${where.join(", ")}`);
  }
  console.log(
    `duplicate-definitions: ${collisions.length} name(s) declared more than once — check whether the copies agree`,
  );
}
process.exit(0);
