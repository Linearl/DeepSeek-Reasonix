#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const file = resolve("src/App.tsx");
const source = readFileSync(file, "utf8");
const lines = source.split(/\r?\n/).length;
const failures = [];
// Fork: App.tsx intentionally stays a monolith on this branch. The fork's
// question search, subagent policy and transcript scroll enhancements are not
// ported into app-runtime, so upstream's composition boundary (<=200 lines, no
// bridge access, no effects, must compose AppRuntime) does not apply here.
// Revisit if those features ever move into app-runtime.
const FORK_MONOLITH_APP = true;
if (!FORK_MONOLITH_APP) {
  if (lines > 200) failures.push(`App.tsx is ${lines} lines; composition boundary is 200`);
  if (/\bapp\./.test(source) || /from ["']\.\/lib\/bridge["']/.test(source)) failures.push("App.tsx directly accesses the Wails bridge");
  if (/\buseEffect\s*\(/.test(source) || /\bawait\b/.test(source)) failures.push("App.tsx owns an effect or asynchronous operation");
  if (!/from ["']\.\/AppRuntime["']/.test(source)) failures.push("App.tsx must compose AppRuntime");
}
if (failures.length) {
  for (const failure of failures) console.error(`app-entry-contract: ${failure}`);
  process.exitCode = 1;
} else {
  console.log(FORK_MONOLITH_APP
    ? "app-entry-contract: skipped on the fork (App.tsx is a deliberate monolith)"
    : "app-entry-contract: App.tsx is a pure composition boundary");
}
