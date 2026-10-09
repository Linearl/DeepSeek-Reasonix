// Run: tsx src/__tests__/history-auto-retry-contract.test.ts
// 任务676 wiring contract: the hydrate path must run the history read through
// the bounded silent auto-retry BEFORE the manual-retry banner, and the
// banner/retry-button paths must remain byte-for-byte the same escape hatch
// when the retry budget is exhausted.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const controller = readFileSync(join(root, "lib/useController.ts"), "utf8");
const retryLib = readFileSync(join(root, "lib/historyAutoRetry.ts"), "utf8");

// ── 1. The retry primitive is bounded by constants (禁无界) ──────────────────
assert.match(retryLib, /export const HISTORY_AUTO_RETRY_LIMIT = 3/, "retry budget is a module constant = 3");
assert.match(retryLib, /export const HISTORY_AUTO_RETRY_DELAY_MS = 500/, "backoff is a module constant = 500ms");
assert.match(retryLib, /options\.retries \?\? HISTORY_AUTO_RETRY_LIMIT/, "callers cannot raise the budget beyond the constant default");

// ── 2. Controller wires the history read through the auto-retry ─────────────
assert.match(controller, /import \{ HISTORY_AUTO_RETRY_LIMIT, runBoundedAutoRetry \} from "\.\/historyAutoRetry"/,
  "controller imports the bounded retry primitive");
assert.match(
  controller,
  /const attemptHistoryLoad = \(\) =>\s*\n\s*loadTimed\("history"/,
  "each retry attempt reuses loadTimed (per-attempt breadcrumbs + stage timing preserved)",
);
assert.match(
  controller,
  /projection = await runBoundedAutoRetry\(attemptHistoryLoad, \{\s*\n\s*shouldContinue: stillCurrent,/,
  "auto-retry aborts when the hydrate is no longer current (tab switched / session changed)",
);
assert.match(controller, /history auto-retry \$\{n\}\/\$\{HISTORY_AUTO_RETRY_LIMIT\}/,
  "each automatic retry leaves a breadcrumb with its budget position");

// ── 3. Banner sits strictly AFTER the retry budget is exhausted ─────────────
const retryIndex = controller.indexOf("runBoundedAutoRetry(attemptHistoryLoad");
const bannerIndex = controller.indexOf('type: "hydrate_error"', retryIndex);
assert.ok(retryIndex > 0, "auto-retry call site exists");
assert.ok(bannerIndex > retryIndex, "manual-retry banner only after the retry budget is exhausted");
assert.match(
  controller,
  /if \(!skipHistory && projection === undefined\) \{[\s\S]*?runBoundedAutoRetry\(attemptHistoryLoad[\s\S]*?dispatchTo\(tabId, \{ type: "hydrate_error", reason, error: errText \}\);/,
  "hydrate_error still guarded by a failed (post-retry) projection",
);
assert.match(controller, /local_notice", level: "warn", text: t\("history\.failedLoadHistory"\), preserveRuntime: true/,
  "transcript notice path unchanged (task 400 contract)");
assert.match(controller, /hydrateFailureDetail\(\s*\n\s*t\("history\.failedLoadHistory"\),\s*\n\s*historyLoadCause,/,
  "banner detail still carries the reader's own cause (last attempt)");

// ── 4. Manual retry path untouched (重试按钮不回归) ──────────────────────────
assert.match(controller, /retrySessionHistory/, "retry path is still exported");
assert.match(controller, /loadSessionDataForTab\(id, false, "startup", \{ sessionPath: m\?\.sessionPath, sessionRevision: m\?\.sessionRevision, sessionDigest: m\?\.sessionDigest, preserveCachedHistory: false \}\)/,
  "retrySessionHistory re-runs the hydrate (which now auto-retries on top)");
assert.match(readFileSync(join(root, "app-shell/AppRuntimeView.tsx"), "utf8"), /retrySessionHistory/,
  "App still wires the history retry control");

// ── 5. hydrate_start is not re-dispatched by retries (surface stays calm) ───
const retryBlock = controller.slice(retryIndex - 200, controller.indexOf("hydrate:read", retryIndex));
assert.ok(!retryBlock.includes("hydrate_start"), "auto-retry loop never re-dispatches hydrate_start");

console.log("  PASS  history auto-retry contract (任务676)");
