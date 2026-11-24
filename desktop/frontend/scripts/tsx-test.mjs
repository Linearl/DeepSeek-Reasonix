#!/usr/bin/env node
// Unified single-suite test entry (tl1788 patterns 2+3). Every frontend suite
// runs through this face so the runner-level environment false positives stay
// governed in one place:
//   - css/svg/yaml static-asset stubs are ALWAYS attached (css-stub-register +
//     svg-stub-register), so a suite whose component graph imports `.css` or
//     `.svg` can never die with ERR_UNKNOWN_FILE_EXTENSION (tl1774/tl1767/
//     tl1739r2 crash family: heartbeat-reuse-session, remote-session-surface);
//   - tsconfig.tsx-runner.json is always attached (768b ③: one JSX transpile
//     runtime for every discovered or dispatched suite);
//   - the given test path is auto-corrected to the file that actually exists
//     (.ts <-> .tsx), so a dispatch that guesses the extension gets the real
//     suite instead of a Node ERR_MODULE_NOT_FOUND with empty output.
// Usage: node scripts/tsx-test.mjs <test-path> [more-paths...]
// Works from any cwd; resolves and runs relative to the frontend root.
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export const FRONTEND_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// Resolve the local tsx entry directly so the runner works both under pnpm
// scripts and when invoked as plain `node scripts/tsx-test.mjs`.
const tsxCli = createRequire(import.meta.url).resolve("tsx/cli");

const stubArgs = [
  "--import", pathToFileURL(join(FRONTEND_ROOT, "scripts", "css-stub-register.mjs")).href,
  "--import", pathToFileURL(join(FRONTEND_ROOT, "scripts", "svg-stub-register.mjs")).href,
];

// Canonical face for one suite, split by consumer:
//   - suiteFlags(testPath): tsx flags + the suite path — the argv AFTER the
//     tsx command. ci-test-plan.mjs embeds this as ["tsx", ...suiteFlags(p)]
//     and run-ci-tests.mjs resolves the tsx cli itself.
//   - suiteArgv(testPath): [tsxCli, ...suiteFlags(p)] — the complete argv for
//     spawnSync(process.execPath, ...). Exported so run-tests.mjs and this
//     wrapper build the exact same face instead of drifting apart again (the
//     drift is what left loader holes in the first place).
export function suiteFlags(testPath) {
  return ["--tsconfig", join(FRONTEND_ROOT, "tsconfig.tsx-runner.json"), ...stubArgs, testPath];
}

export function suiteArgv(testPath) {
  return [tsxCli, ...suiteFlags(testPath)];
}

// Return the path corrected to the file that actually exists: exact match
// wins, then the sibling extension (.ts <-> .tsx). Returns null and prints
// the diagnosis when neither exists (a genuinely wrong path, not a guess).
export function resolveTestPath(testPath) {
  const abs = resolve(FRONTEND_ROOT, testPath);
  if (existsSync(abs)) return abs;
  const sibling = abs.replace(/\.tsx?$/, (ext) => (ext === ".ts" ? ".tsx" : ".ts"));
  if (existsSync(sibling)) {
    console.error(`tsx-test: ${testPath} does not exist — corrected to ${sibling}`);
    return sibling;
  }
  console.error(`tsx-test: no such test file (${abs}); also tried the sibling extension (${sibling})`);
  return null;
}

export function runSuite(testPath) {
  const corrected = resolveTestPath(testPath);
  if (!corrected) return 1;
  console.log(`tsx-test: node ${tsxCli} ${suiteFlags(corrected).map(String).join(" ")}`);
  // Same locale pin as run-tests.mjs: suites assert English UI strings, and
  // Node's navigator.language follows the machine ICU locale otherwise.
  const result = spawnSync(process.execPath, suiteArgv(corrected), {
    stdio: "inherit",
    cwd: FRONTEND_ROOT,
    env: { ...process.env, LANG: "en_US.UTF-8", LC_ALL: "en_US.UTF-8" },
  });
  if (result.error) console.error(`tsx-test: spawn failed for ${corrected}: ${result.error.message}`);
  return result.status ?? 1;
}

function main(argv) {
  if (argv.length === 0) {
    console.error("usage: node scripts/tsx-test.mjs <test-path> [more-paths...]");
    return 1;
  }
  let failed = 0;
  for (const testPath of argv) {
    if (runSuite(testPath) !== 0) failed += 1;
  }
  return failed === 0 ? 0 : 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
