// P17: a degraded submit renders only volatile tab state (optimistic user row
// + ↪ bubble) while the durable copy sits in the session inbox. Any surface
// replace fed from a history page / transcript-store snapshot taken before the
// inbox drain erased both, and nothing re-rendered them — the reported
// "message reached the backend but never rendered". The hydrate-time reconcile
// re-renders still-durable rows as ↪ bubbles; these checks pin its selection
// rules (which states, which dedup, which text shape).
import { missingQueuedGuidanceBubbles } from "../lib/inboxSurfaceReconcile";
import { STEER_NOTICE_PREFIX } from "../lib/useController";

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

const SURFACE_EMPTY: ReadonlyArray<{ kind: string; inboxItemId?: string }> = [];

check(missingQueuedGuidanceBubbles(undefined, SURFACE_EMPTY).length === 0, "absent snapshot yields no bubbles");
check(missingQueuedGuidanceBubbles({ items: [] }, SURFACE_EMPTY).length === 0, "empty snapshot yields no bubbles");

const snapshot = {
  items: [
    { id: "q1", state: "queued", intent: "steer", preview: "启动变快的反馈" },
    { id: "q2", state: "steer_accepted", intent: "steer", preview: "in-flight steer" },
    { id: "q3", state: "steer_consumed", intent: "steer", preview: "already consumed" },
    { id: "q4", state: "running", intent: "followup", preview: "being injected" },
    { id: "q5", state: "queued", intent: "followup", preview: "  " },
    { id: "q6", state: "blocked", intent: "followup", preview: "blocked row" },
  ],
};
const bubbles = missingQueuedGuidanceBubbles(snapshot, SURFACE_EMPTY);
check(bubbles.length === 2, `only queued/steer_accepted rows with a preview reconcile (got ${bubbles.length})`);
check(bubbles.some((b) => b.inboxItemId === "q1"), "queued steer row reconciles");
check(bubbles.some((b) => b.inboxItemId === "q2"), "steer_accepted row reconciles (durable, not yet in transcript)");
check(!bubbles.some((b) => b.inboxItemId === "q3"), "steer_consumed row does not reconcile (transcript carries it)");
check(!bubbles.some((b) => b.inboxItemId === "q4"), "running row does not reconcile (injection in flight)");
check(!bubbles.some((b) => b.inboxItemId === "q5"), "blank-preview row does not reconcile");
check(!bubbles.some((b) => b.inboxItemId === "q6"), "blocked row does not reconcile");
check(bubbles[0].text === `${STEER_NOTICE_PREFIX}启动变快的反馈`, "bubble text carries the steer notice prefix plus preview");

// Rows already represented on the surface (post-wipe survivor, or a consume-time
// Steer bubble that landed between the snapshot read and the dispatch) stay out.
const surface = [{ kind: "notice", inboxItemId: "q1" }];
const filtered = missingQueuedGuidanceBubbles(snapshot, surface);
check(filtered.length === 1 && filtered[0].inboxItemId === "q2", "items already on the surface dedupe out");

// A surface row without an inboxItemId (plain notice) must not shadow anything.
const unrelated = missingQueuedGuidanceBubbles(snapshot, [{ kind: "notice" }, { kind: "user" }]);
check(unrelated.length === 2, "surface rows without inboxItemId do not suppress reconciliation");

console.log(`\n${passed} assertions passed${process.exitCode ? " (with failures)" : ""}`);
