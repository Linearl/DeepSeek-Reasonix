#!/usr/bin/env node
// Runs the frontend's independent contract checks concurrently.
//
// They were a `&&` chain in package.json, which meant their costs added up. Measured on this
// machine, serialised they were ~47s of a local build - and every one of them reads source
// files or the built stylesheet without writing anything, so there is no reason they cannot
// overlap. Run together the wall clock is the slowest single check instead of the sum.
//
// Not included here, because they are not independent of each other or of the build:
//   - tsc and vite build both compile the same sources; they stay sequential in `build`.
//   - check-bundle-budget reads vite's output, so it runs after vite.
//
// Output is captured per check and printed only when something fails, plus a one-line summary
// either way. Interleaving seven tools' progress output would make a failure harder to read
// than the time saved is worth.

import { spawn } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");

const checks = [
  ["lint:hooks", "pnpm", ["lint:hooks"]],
  ["check:waapi", "node", ["scripts/check-waapi-contract.mjs"]],
  ["check:scroll-writer", "node", ["scripts/check-single-scroll-writer.mjs"]],
  ["check:app-layers", "pnpm", ["check:app-layers"]],
  ["check:css-syntax", "node", [
    "scripts/check-css-syntax.mjs",
    "src/styles.css",
    "src/components/RemoteConnectWizard.css",
    "src/components/TranscriptSelectionMenu.css",
    "src/components/MCPInteractionCard.css",
    "src/components/SubagentDetails.css",
    "src/custom/features/heartbeat/heartbeat.css",
  ]],
  ["check:z-index", "node", ["scripts/check-z-index-tokens.mjs", "src/styles.css", "src/components/RemoteConnectWizard.css"]],
  ["check:theme-token", "node", ["scripts/check-theme-token-contract.mjs"]],
  ["check:bridge-json-tags", "node", ["../../scripts/check-bridge-json-tags.mjs", "../.."]],
];

function run(label, command, args) {
  return new Promise((settle) => {
    const started = Date.now();
    // spawn with shell:true and an args array is deprecated (DEP0190): the arguments are
    // concatenated into a shell line without escaping. Every argument here is a literal path
    // this file controls, so quoting the ones with spaces and passing a single line is both
    // warning-free and unambiguous.
    const commandLine = [command, ...args]
      .map((part) => (part.includes(" ") ? `"${part}"` : part))
      .join(" ");
    const child = spawn(commandLine, { cwd: root, shell: true });
    let output = "";
    child.stdout.on("data", (chunk) => { output += chunk; });
    child.stderr.on("data", (chunk) => { output += chunk; });
    child.on("error", (err) => settle({ label, ok: false, ms: Date.now() - started, output: String(err) }));
    child.on("close", (code) => settle({ label, ok: code === 0, ms: Date.now() - started, output }));
  });
}

const started = Date.now();
const results = await Promise.all(checks.map(([label, command, args]) => run(label, command, args)));
const elapsed = Date.now() - started;

for (const result of results) {
  const mark = result.ok ? "PASS" : "FAIL";
  process.stdout.write(`  ${mark}  ${result.label} (${(result.ms / 1000).toFixed(1)}s)\n`);
}

const failed = results.filter((result) => !result.ok);
if (failed.length > 0) {
  for (const result of failed) {
    process.stdout.write(`\n--- ${result.label} ---\n${result.output}\n`);
  }
  process.stdout.write(`\n${failed.length} of ${results.length} checks failed (${(elapsed / 1000).toFixed(1)}s)\n`);
  process.exit(1);
}

process.stdout.write(`  ${results.length} checks passed in ${(elapsed / 1000).toFixed(1)}s\n`);
