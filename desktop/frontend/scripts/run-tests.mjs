#!/usr/bin/env node
// Discovery-based test runner: every src/__tests__/*.test.ts{,x} runs as its
// own tsx process (suites install their own jsdom/globals and exit non-zero
// on failure), so adding a test file needs no package.json registration.
// Suites owned by a dedicated pnpm script are excluded here, named with their
// owner, and their absence fails the run so a rename cannot silently drop
// coverage. Fail-fast by default; --keep-going runs everything and summarizes.
import { spawnSync } from "node:child_process";
import { readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { suiteArgv } from "./tsx-test.mjs";

const TESTS_DIR = "src/__tests__";

const OWNED_ELSEWHERE = new Map(Object.entries({
  "terminal-events.test.ts": "test:terminal",
  "terminal-store.test.ts": "test:terminal",
  "terminal-output.test.ts": "test:terminal",
  "terminal-theme.test.ts": "test:terminal",
  "task-monitor-navigation.test.ts": "test:task-monitor",
  "workspace-refresh-store.test.ts": "test:workspace",
  "workspace-selection-isolation.test.tsx": "test:workspace",
  "workspace-changes-errors.test.tsx": "test:workspace",
  "workspace-preview-css.test.ts": "test:workspace",
  "workspace-context-menu.test.tsx": "test:workspace",
  "workspace-resize-interaction.test.tsx": "test:workspace",
  "rich-composer-selection.test.tsx": "pretest",
  "context-center-contract.test.ts": "pretest",
  "provider-model-cache.test.ts": "pretest",
  "format-tokens.test.ts": "pretest",
  "usage-stats-format.test.ts": "test:usage-stats",
  "usage-stats-panel.test.tsx": "test:usage-stats",
  "settings-responsive-layout.test.ts": "test:settings-responsive",
  "raf-batch.test.ts": "test:stream",
  "stream-delta-batch.test.ts": "test:stream",
  "use-controller-stream-progress.test.ts": "test:stream",
  "transcript-kernel.test.ts": "test:transcript",
  "transcript-kernel-races.test.ts": "test:transcript",
  "transcript-timeline.test.ts": "test:transcript",
  "transcript-viewport.test.tsx": "test:transcript",
  "transcript-question-jump.test.ts": "test:transcript",
  "nested-scroll-handoff.test.ts": "test:transcript",
  "markdown-table-virtual.test.tsx": "test:transcript",
  "markdown-table-export.test.tsx": "test:transcript",
  "typography-overflow-contract.test.ts": "test:transcript",
  "transcript-selection-retention.test.tsx": "test:transcript",
  "composer-menu-viewport.test.ts": "test:composer-menu-viewport",
  "virtual-menu-identity.test.tsx": "test:composer-menu-viewport",
  "remote-workspace-launch.test.ts": "test:remote",
  "remote-store.test.ts": "test:remote",
  "remote-error-ux.test.tsx": "test:remote",
  "remote-hosts-page.test.tsx": "test:remote",
  "remote-connect-wizard.test.tsx": "test:remote (needs the css stub register)",
  "add-project-entries.test.ts": "test:remote",
  "remote-secret-dialog.test.tsx": "test:remote",
  "remote-server-panel.test.tsx": "test:remote (needs the svg stub register)",
  "remote-session-surface.test.tsx": "test:remote (needs the svg stub register)",
  "remote-running-reconcile.test.ts": "test:remote",
  "remote-project-tree.test.tsx": "test:remote",
  "statusbar-workspace.test.tsx": "test:remote",
  "updater-shared-state.test.tsx": "test:updater",
  "window-state-ordering.test.ts": "test:window-state",
}));

const keepGoing = process.argv.includes("--keep-going");
// Subset dispatch/verification entry (tl1788): --filter=SUBSTR runs only the
// discovered suites whose file name contains SUBSTR. The OWNED_ELSEWHERE
// absence guard below always checks the FULL discovered list, so a filtered
// run can never mask a rename that dropped coverage.
const filterArg = process.argv.find(arg => arg.startsWith("--filter="));
const filter = filterArg ? filterArg.slice("--filter=".length) : null;

// Per-suite wall-clock ceiling. Some suites print all their assertions and
// then never exit (timer/jsdom handles surviving under multi-layer output
// redirection), which would block this sequential runner on spawnSync
// forever. This is the same 10-minute ceiling run-ci-tests.mjs applies, and
// the approach was proven on exactly this failure mode by 634's resumer
// (hang suites get ETIMEDOUT/SIGTERM-killed instead of stalling the run).
// Set REASONIX_TEST_SUITE_TIMEOUT_MS to override; 0 disables the ceiling.
const suiteTimeoutMs = Number(process.env.REASONIX_TEST_SUITE_TIMEOUT_MS ?? 10 * 60 * 1000);
if (!Number.isFinite(suiteTimeoutMs) || suiteTimeoutMs < 0) {
  console.error("run-tests: REASONIX_TEST_SUITE_TIMEOUT_MS must be a non-negative number of milliseconds (0 disables)");
  process.exit(1);
}
const files = readdirSync(TESTS_DIR)
  .filter((name) => /\.test\.tsx?$/.test(name))
  .sort();

for (const [name, owner] of OWNED_ELSEWHERE) {
  if (!files.includes(name)) {
    console.error(`run-tests: excluded suite ${name} (owner: ${owner}) no longer exists — update OWNED_ELSEWHERE`);
    process.exit(1);
  }
}

// CSS/SVG/PNG browser assets, the tsx-runner tsconfig (768b ③) and the
// locale pin now live in ONE canonical face: suiteArgv() from tsx-test.mjs
// (tl1788). Discovery previously built its own copy of that face; the two
// copies drifting apart is exactly how loader holes (ERR_UNKNOWN_FILE_
// EXTENSION `.css`/`.svg` crash family) reopened in dispatch paths.

const discovered = files.filter((name) => !OWNED_ELSEWHERE.has(name));
const suites = filter ? discovered.filter((name) => name.includes(filter)) : discovered;
console.log(`run-tests: ${suites.length} discovered suites (${OWNED_ELSEWHERE.size} owned by dedicated scripts)${filter ? `; filter "${filter}" matched ${suites.length}` : ""}`);

// tl1788 preflight: catch a broken pnpm resolution chain before the loop, so
// a poisoned environment aborts with one actionable error instead of
// mass-red noise (tl1776: 230/287 suites red with ERR_MODULE_NOT_FOUND
// pointing into the linked store while every suite passed when run alone).
try {
  createRequire(import.meta.url).resolve("jsdom");
} catch {
  console.error("run-tests: preflight failed — jsdom does not resolve from desktop/frontend/node_modules.");
  console.error("The pnpm link structure backing this checkout is broken; fix node_modules (reinstall deps in the real checkout this worktree links to) and re-run. Aborting instead of mass-red.");
  process.exit(1);
}

// tl1788 env-noise triage switch (same face as run-ci-tests.mjs): a failed
// suite gets exactly one immediate single re-run with identical argv. Green
// there means environment noise (ENV-FLAKY, recorded and reported, does not
// abort fail-fast); a second red is a real failure and follows the original
// fail-fast/keep-going semantics. REASONIX_TEST_REVERIFY=0 restores
// first-failure-is-final.
const reverify = process.env.REASONIX_TEST_REVERIFY !== "0";
if (!reverify) console.log("run-tests: env-noise re-verify disabled (REASONIX_TEST_REVERIFY=0)");
const envFlaky = [];

const failures = [];
for (const name of suites) {
  const path = join(TESTS_DIR, name);
  console.log(`\n▶ ${path}`);
  // English locale mirrors the Go convention (LANG=en_US.UTF-8 go test):
  // Node's built-in navigator.language follows the machine's ICU locale, and
  // suites assert English UI strings.
  const env = { ...process.env, LANG: "en_US.UTF-8", LC_ALL: "en_US.UTF-8" };
  const spawnOpts = {
    stdio: "inherit",
    env,
    timeout: suiteTimeoutMs > 0 ? suiteTimeoutMs : undefined,
  };
  let result = spawnSync(process.execPath, suiteArgv(path), spawnOpts);
  if (result.error) console.error(`run-tests: spawn failed for ${path}: ${result.error.message}`);
  // spawnSync sets error code ETIMEDOUT only for its own timeout kill, so
  // external kills are not miscounted as suite timeouts.
  const timedOut = result.error?.code === "ETIMEDOUT";
  if (timedOut) console.error(`run-tests: ${path} exceeded ${suiteTimeoutMs}ms and was killed (process did not exit)`);
  // Triage only genuine suite failures (a spawn error would repeat on re-run).
  if (result.status !== 0 && !result.error && reverify) {
    console.error(`run-tests: ${path} failed once — re-running alone with identical argv (env-noise triage)`);
    const second = spawnSync(process.execPath, suiteArgv(path), spawnOpts);
    if (second.status === 0 && !second.error) {
      envFlaky.push(path);
      console.log(`ENV-FLAKY ${path} (failed once, green on single re-run — environment noise, NOT red)`);
      continue;
    }
    result = second;
  }
  if (result.status !== 0) {
    const secondTimedOut = result.error?.code === "ETIMEDOUT";
    if (!keepGoing) {
      console.error(`\nrun-tests: ${secondTimedOut ? "TIMED OUT" : "FAILED"} at ${path}`);
      process.exit(result.status ?? 1);
    }
    failures.push({ name, timedOut: secondTimedOut });
  }
}

if (envFlaky.length > 0) {
  console.log(`\nrun-tests: ${envFlaky.length}/${suites.length} ENV-FLAKY (failed once, green on single re-run — environment noise, NOT red):`);
  for (const path of envFlaky) console.log(`  ENV-FLAKY ${path}`);
}
if (failures.length > 0) {
  const timedOutCount = failures.filter((failure) => failure.timedOut).length;
  console.error(`\nrun-tests: ${failures.length}/${suites.length} suites failed (${timedOutCount} timed out):`);
  for (const { name, timedOut } of failures) console.error(`  ${timedOut ? "TIMEOUT" : "FAIL"} ${name}`);
  process.exit(1);
}
console.log(`\nrun-tests: all ${suites.length} suites passed${envFlaky.length > 0 ? ` (${envFlaky.length} env-flaky re-verified green, see list above)` : ""}`);
