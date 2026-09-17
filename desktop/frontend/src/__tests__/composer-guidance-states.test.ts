// Task 159: the inbox state set is written twice — Go `InboxState`
// (internal/sessioninbox/types.go:31-39) and this TypeScript whitelist — and the
// two had drifted: `running` and `steer_consumed` were missing, so the shelf
// silently fell back to "unknown state" and greyed the row's actions out while
// the user was still reading "it has not taken effect yet". Cover all six.
import {
  guidanceHasKnownPendingState,
  guidanceIsDelivering,
  guidanceIsEditable,
  guidanceIsInFlight,
  guidanceNeedsRetry,
} from "../lib/composerGuidance";

let passed = 0;
function check(cond: boolean, label: string): void {
  if (!cond) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

// The six states the backend can report.
const BACKEND_STATES = ["queued", "steer_accepted", "steer_consumed", "running", "blocked", "uncertain"];

for (const state of BACKEND_STATES) {
  check(guidanceHasKnownPendingState(state), `${state} is recognised as a known pending state`);
}

// A local optimistic entry has no state yet; it must not read as unknown either.
check(guidanceHasKnownPendingState(undefined), "absent state is accepted");
check(guidanceHasKnownPendingState(""), "empty state is accepted");

// Anything outside the backend set still takes the unknown branch, which the
// shelf renders with an explicit reason rather than a silent grey-out.
check(!guidanceHasKnownPendingState("mystery"), "unrecognised state is rejected");

// `running` (picked up, not yet handed over) and `steer_consumed` (handed over)
// are both past the cancellable boundary the backend exposes, so the shelf keeps
// their actions disabled but says why.
check(guidanceIsDelivering("running"), "running counts as delivering");
check(guidanceIsDelivering("steer_consumed"), "steer_consumed counts as delivering");
for (const state of ["queued", "steer_accepted", "blocked", "uncertain", undefined, ""]) {
  check(!guidanceIsDelivering(state), `${state === undefined ? "absent" : state === "" ? "empty" : state} is not delivering`);
}

// Regression guard for the neighbouring helpers: adding the two states to the
// whitelist must not widen what the shelf lets the user act on.
check(guidanceIsInFlight("steer_accepted"), "steer_accepted still reads as in flight");
check(!guidanceIsInFlight("running"), "running is not reported as in flight");
check(guidanceNeedsRetry("blocked") && guidanceNeedsRetry("uncertain"), "blocked/uncertain still offer retry");
check(!guidanceNeedsRetry("running") && !guidanceNeedsRetry("steer_consumed"), "delivering states never offer retry");
check(guidanceIsEditable({ id: "i1", state: "queued" }), "a queued item is editable");
check(!guidanceIsEditable({ id: "i1", state: "running" }), "a running item is not editable");
check(!guidanceIsEditable({ id: "i1", state: "steer_consumed" }), "a consumed item is not editable");
check(!guidanceIsEditable({ id: "local-2", state: "queued" }), "local draft ids stay read-only");

console.log(`\n${passed} checks passed`);
