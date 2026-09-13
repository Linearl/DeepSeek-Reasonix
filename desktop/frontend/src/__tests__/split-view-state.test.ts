// Run: tsx src/__tests__/split-view-state.test.ts
//
// Split view (task 70) is built to be invisible while it is off: CLOSED_SPLIT mounts a
// single pane and the inactive pane collapses under `display: contents`. That promise
// only holds if a stale stored value and a vanished tab both resolve back to
// CLOSED_SPLIT — a stored key that mounts an empty second pane on startup is exactly the
// regression this guards.

import { CLOSED_SPLIT, normalizeSplitState, reconcileSplitState, splitIsActive } from "../lib/splitView";

let passed = 0;
let failed = 0;

function check(name: string, condition: boolean, detail?: string): void {
  if (condition) {
    passed += 1;
    return;
  }
  failed += 1;
  console.error(`FAIL ${name}${detail ? ` — ${detail}` : ""}`);
}

// splitIsActive is the mount decision: one pane unless a secondary tab is set.
check("closed split reports inactive", splitIsActive(CLOSED_SPLIT) === false);
check(
  "a secondary tab reports active",
  splitIsActive({ secondaryTabId: "t2", focusedPane: "primary" }) === true,
);

// normalizeSplitState has to survive anything a previous version may have written.
check("null normalizes to closed", normalizeSplitState(null).secondaryTabId === null);
check("a non-object normalizes to closed", normalizeSplitState("nonsense").secondaryTabId === null);
check("a blank tab id normalizes to closed", normalizeSplitState({ secondaryTabId: "   " }).secondaryTabId === null);
check(
  "a missing tab id normalizes to closed",
  normalizeSplitState({ focusedPane: "secondary" }).secondaryTabId === null,
);
check(
  "an unknown pane falls back to primary",
  normalizeSplitState({ secondaryTabId: "t2", focusedPane: "bogus" }).focusedPane === "primary",
);
check(
  "a well-formed value passes through",
  normalizeSplitState({ secondaryTabId: "t2", focusedPane: "secondary" }).secondaryTabId === "t2",
);

// reconcileSplitState runs when tabs close: it drops a split whose tab is gone and
// keeps the focused pane, because closing a tab says nothing about focus.
check("a closed split is left alone", reconcileSplitState(CLOSED_SPLIT, ["t1"]) === CLOSED_SPLIT);
const live = { secondaryTabId: "t2", focusedPane: "secondary" } as const;
check("a live secondary tab is left alone", reconcileSplitState(live, ["t1", "t2"]) === live);
const orphaned = reconcileSplitState(live, ["t1"]);
check("an orphaned secondary tab closes the split", orphaned.secondaryTabId === null);
check("closing the split preserves the focused pane", orphaned.focusedPane === "secondary");

console.log(`${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
