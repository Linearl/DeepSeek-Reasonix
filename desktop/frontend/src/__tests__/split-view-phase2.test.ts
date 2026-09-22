// Run: npx tsx src/__tests__/split-view-phase2.test.ts
//
// Task 70 二期: cross-group drag intent, divider ratio clamping and ratio
// memory. All pure functions — the pointer/JSX wiring compiles under tsc and
// behaves through the clamped values asserted here.

import assert from "node:assert/strict";
import {
  SPLIT_RATIO_MAX,
  SPLIT_RATIO_MIN,
  clampedSplitRatio,
  crossGroupDropIntent,
  loadSplitRatio,
  normalizeSplitRatio,
  persistSplitRatio,
} from "../lib/splitView";

let passed = 0;
let failed = 0;
function check(condition: boolean, label: string): void {
  if (condition) {
    passed += 1;
    console.log(`  PASS  ${label}`);
  } else {
    failed += 1;
    process.exitCode = 1;
    console.log(`  FAIL  ${label}`);
  }
}

// ── cross-group drag (拖拽跨组) ───────────────────────────────────────────────
check(
  crossGroupDropIntent("tab-a", "tab-b", "tab-b") === "replace-secondary",
  "a primary tab dropped on the secondary tab replaces the secondary",
);
check(
  crossGroupDropIntent("tab-b", "tab-a", "tab-b") === "return-primary",
  "the secondary tab dropped on a primary tab returns to the primary group",
);
check(
  crossGroupDropIntent("tab-a", "tab-c", "tab-b") === null,
  "a drop inside the primary group is an ordinary reorder (null)",
);
check(
  crossGroupDropIntent("tab-b", "tab-b", "tab-b") === null,
  "dropping a tab on itself never crosses groups",
);
check(
  crossGroupDropIntent("tab-a", "tab-c", null) === null,
  "no split open: every drop is an ordinary reorder",
);
check(
  crossGroupDropIntent("", "tab-a", "tab-b") === null,
  "an empty dragged id never crosses groups",
);

// ── ratio clamping (最小宽度保护) ────────────────────────────────────────────
check(clampedSplitRatio(0.5, 1600) === 0.5, "a mid ratio passes through untouched");
// 2000px: the 360px floor (0.18) sits under the static bounds, so 0.2/0.8 rule.
check(clampedSplitRatio(0.05, 2000) === SPLIT_RATIO_MIN, "below the floor clamps to SPLIT_RATIO_MIN");
check(clampedSplitRatio(0.95, 2000) === SPLIT_RATIO_MAX, "above the ceiling clamps to SPLIT_RATIO_MAX");
// 360px floor: at 1000px wide the legal lo is 0.36, so 0.2 (the static floor)
// is too greedy for this container and must be lifted.
check(clampedSplitRatio(0.05, 1000) === 0.36, "the per-pane px floor lifts the clamp in a narrow container");
check(clampedSplitRatio(0.99, 1000) === 0.64, "the px floor clamps both sides (1 - 360/1000)");
// Too narrow for two legal panes (700px < 360*2): both rules disagree → 0.5.
check(clampedSplitRatio(0.7, 700) === 0.5, "a container too narrow for two legal panes collapses to 0.5");
check(clampedSplitRatio(Number.NaN, 1600) === 0.5, "NaN reads as 'no opinion' → 0.5");
check(clampedSplitRatio(0.7, 0) === 0.7, "zero width falls back to the static bounds (0.7 passes through)");

// ── normalization + memory (比例记忆) ────────────────────────────────────────
check(normalizeSplitRatio(0.9) === SPLIT_RATIO_MAX, "normalize clamps a stored overshoot");
check(normalizeSplitRatio("garbage") === 0.5, "an unparseable stored value reads 0.5");
check(normalizeSplitRatio(null) === 0.5, "a null stored value reads 0.5");

const store = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
  setItem: (key: string, value: string) => void store.set(key, value),
  removeItem: (key: string) => void store.delete(key),
};
check(loadSplitRatio() === 0.5, "no stored ratio loads the 50/50 default");
persistSplitRatio(0.62);
check(loadSplitRatio() === 0.62, "a dragged ratio survives persist → load");
store.set("desktop:splitRatio", "not-json");
check(loadSplitRatio() === 0.5, "a corrupt stored ratio falls back to 0.5 (old/foreign build safe)");
persistSplitRatio(3);
check(loadSplitRatio() === SPLIT_RATIO_MAX, "persist normalizes before storing");
delete (globalThis as { localStorage?: unknown }).localStorage;

console.log(`\n${passed} checks passed, ${failed} failed`);
if (failed > 0) process.exit(1);
