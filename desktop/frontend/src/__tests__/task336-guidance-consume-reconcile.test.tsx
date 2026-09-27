// Task 336: an already-injected guidance row must not return to the
// "pending guidance" shelf after a tab round-trip. Reproduction: the backend
// snapshot still reports the row (host guidance and collab replies are
// injected without ever passing through the shelf's submitted set, and the
// backend acknowledges only on its own cadence), so the queue re-fills from
// InboxSnapshot — the shelf must re-filter it against the transcript's ↪
// injection receipts instead of trusting the one-shot keyed effect.
//
// Layers: pure (receipt extraction + retire composition) then behaviour
// (snapshot applies with rows present, receipt set lands late, tab
// round-trip re-applies the snapshot — the row never comes back, the real
// pending row never leaves).
import { act } from "react";
import { installDom, installBridgeApp, renderComposer } from "./composerInboxHarness";
import { consumedGuidanceIdsFromItems, retireSubmittedGuidance } from "../lib/composerInboxQueue";

// Warm the shelf module before the Composer's lazy() first renders it: the
// dynamic import resolves from the same ESM cache, so the Suspense fallback
// does not swallow the first snapshot's rows in a bare jsdom run.
await import("../components/ComposerGuidanceShelf");

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string): void {
  if (cond) {
    passed += 1;
    console.log(`  PASS  ${label}`);
  } else {
    failed += 1;
    console.error(`  FAIL  ${label}`);
  }
}
const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const shelfRows = () =>
  [...document.querySelectorAll(".composer-guidance-item")].map((el) => el.textContent ?? "");

console.log("\nTask 336 guidance consume reconciliation");

// --- Pure layer: which transcript rows count as injection receipts. ---
{
  const receipts = consumedGuidanceIdsFromItems([
    { kind: "notice", text: "↪ injected one", inboxItemId: "x" },
    { kind: "notice", text: "↪ injected two", inboxItemId: "y" },
    { kind: "notice", text: "plain notice", inboxItemId: "z" },
    { kind: "notice", text: "↪ missing id" },
    { kind: "compaction", summary: "no text field at all" },
  ] as never);
  ok(receipts.has("x") && receipts.has("y") && receipts.size === 2, "receipt ids collect from ↪ notices with an inboxItemId only");
  const rows = [
    { id: "x", text: "consumed row", submitText: "" },
    { id: "y", text: "pending row", submitText: "" },
  ];
  // Only x carries a receipt; y stays genuinely pending.
  const filtered = retireSubmittedGuidance(rows, new Set(["x"]));
  ok(filtered.length === 1 && filtered[0].id === "y", "retire drops the receipted row and keeps the pending one");
  ok(retireSubmittedGuidance(rows, new Set()).length === 2, "an empty receipt set is identity (zero regression)");
}

// --- Behaviour layer: snapshot applies, receipts land late, round-trip. ---
installDom();
let snapshots = 0;
installBridgeApp({
  InboxSnapshot: async () => {
    snapshots += 1;
    return {
      sessionPath: "session-a",
      // Monotonic so the applied-revision guard never rewinds.
      revision: snapshots,
      paused: false,
      items: [
        { id: "x", preview: "consumed row body", state: "steer_accepted", intent: "steer" },
        { id: "y", preview: "pending row body", state: "queued", intent: "followup" },
      ],
    };
  },
  ReadInboxItem: async () => ({ displayText: "" }),
});

const view = await renderComposer();
for (let i = 0; i < 12 && shelfRows().length < 2; i++) await act(flush);
console.log(`  debug baseline rows=${shelfRows().length} snapshots=${snapshots} :: ${JSON.stringify(shelfRows().map((r) => r.slice(0, 40)))}`);
console.log(`  debug shelf=${document.querySelectorAll(".composer-guidance-shelf").length} head=${document.querySelectorAll(".composer-guidance-head").length} list=${document.querySelectorAll(".composer-guidance-list").length} lazyText=${document.body.textContent?.includes("guidance")}`);
const baseline = shelfRows();
ok(baseline.some((row) => row.includes("consumed row body")), "baseline reproduces: the snapshot fills the shelf with the consumed row");
ok(baseline.some((row) => row.includes("pending row body")), "baseline: the real pending row is queued too");

// The transcript receipts arrive after the snapshot (hydrate order / tab
// round-trip): re-render with the consumed set and the row must disappear.
await view.rerender({ guidanceConsumedIds: new Set(["x"]) });
await act(flush);
const afterReceipts = shelfRows();
ok(!afterReceipts.some((row) => row.includes("consumed row body")), "receipt set lands late: the consumed row is filtered out");
ok(afterReceipts.some((row) => row.includes("pending row body")), "the real pending row survives the filter");

// Tab round-trip: scope switch clears the queue, the snapshot re-applies on
// the way back — the consumed row must not return.
await view.rerender({ tabId: "tab-b", sessionKey: "session-b", inboxSessionPath: "session-b" });
await act(flush);
await view.rerender({ tabId: "tab-a", sessionKey: "session-a", inboxSessionPath: "session-a" });
for (let i = 0; i < 12 && shelfRows().length === 0; i++) await act(flush);
const afterRoundTrip = shelfRows();
console.log(`  debug roundtrip rows=${afterRoundTrip.length} snapshots=${snapshots} :: ${JSON.stringify(afterRoundTrip.map((r) => r.slice(0, 40)))}`);
ok(!afterRoundTrip.some((row) => row.includes("consumed row body")), "tab round-trip: the consumed row stays off the shelf");
ok(afterRoundTrip.some((row) => row.includes("pending row body")), "tab round-trip: the real pending row is still queued");

console.log(`${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
