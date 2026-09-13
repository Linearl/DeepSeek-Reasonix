#!/usr/bin/env node
// State-wiring audit for App.tsx (task 38).
//
// The monolith keeps state the composition layers also keep. That duplication is what
// this checks for, but only in the cases that can be proven from the file alone:
//
//   1. a useState whose setter appears nowhere else — the value can never change, so
//      anything keyed on it is dead (workspaceControllerEpoch was exactly this: read
//      into the workspace cache key, never written, stuck at 0 for the process life);
//   2. a useState whose value is never read — the write goes nowhere.
//
// Cases where a same-named setter arrives from a composition prop are NOT reported:
// the file does contain that name, so the check cannot tell the two apart. Those need
// reading, not grepping, and the batch that did it is recorded in the task notes.
//
// Report-only by design. Run it after merges to see whether the count grew.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const file = resolve("src/App.tsx");
const source = readFileSync(file, "utf8");
const lines = source.split(/\r?\n/);

const declarations = [];
lines.forEach((line, index) => {
  const match = line.match(/const \[(\w+), (\w+)\] = useState/);
  if (match) declarations.push({ value: match[1], setter: match[2], line: index + 1 });
});

const findings = [];
for (const declaration of declarations) {
  const outside = lines.filter((_, index) => index + 1 !== declaration.line);
  const setterSeen = outside.some((line) => new RegExp(`\\b${declaration.setter}\\b`).test(line));
  const valueSeen = outside.some((line) => new RegExp(`\\b${declaration.value}\\b`).test(line));
  if (!setterSeen) {
    findings.push(
      `${declaration.value} (line ${declaration.line}) is never written: its setter appears nowhere else, so it stays at its initial value`,
    );
  }
  if (!valueSeen) {
    findings.push(
      `${declaration.value} (line ${declaration.line}) is never read: only its setter is used`,
    );
  }
}

console.log(`app-state-wiring: ${declarations.length} useState declarations in App.tsx`);
if (findings.length) {
  for (const finding of findings) console.error(`app-state-wiring: ${finding}`);
} else {
  console.log("app-state-wiring: every declared state is both read and written");
}
// Report-only: this is a signal for the next batch, not a gate.
process.exit(0);
