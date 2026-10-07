// Run: npx tsx src/__tests__/split-view-phase2.test.ts
//
// Task 70 二期: cross-group drag intent. Task 247 reworks the width memory:
// the stored fraction is the file preview's INTRUSION into the session area
// (preview width = container width × tier) across three fixed tiers, with a
// one-time migration from the old primary-fraction key. All pure functions —
// the pointer/JSX wiring compiles under tsc and behaves through the tier
// values asserted here.

import assert from "node:assert/strict";
import {
  SPLIT_PREVIEW_TIERS,
  SPLIT_PREVIEW_TIER_DEFAULT,
  crossGroupDropIntent,
  effectiveSplitTier,
  loadSplitPreviewTier,
  migrateLegacySplitRatio,
  normalizeSplitTier,
  persistSplitPreviewTier,
  splitPreviewTierFromPointer,
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

// ── preview tiers (task 247: 侵入语义三档) ───────────────────────────────────
assert.deepEqual([...SPLIT_PREVIEW_TIERS], [0.4, 0.5, 0.6], "the fixed tier set is 40/50/60%");
check(SPLIT_PREVIEW_TIER_DEFAULT === 0.5, "the default tier is the 50/50 midpoint");

// Acceptance math (task 247): preview width = container width × tier. A
// 1000px session area (the container before the split opened) at each tier:
for (const tier of SPLIT_PREVIEW_TIERS) {
  const containerWidth = 1000;
  const applied = effectiveSplitTier(tier, containerWidth);
  check(applied === tier, `tier ${tier * 100}% passes through in a 1000px container`);
  check(
    Math.round(applied * containerWidth) === Math.round(tier * containerWidth),
    `tier ${tier * 100}%: preview width = ${tier * 100}% of a 1000px container (session keeps the rest)`,
  );
  check(
    containerWidth - applied * containerWidth === (1 - tier) * containerWidth,
    `tier ${tier * 100}%: session pane width is exactly the complement`,
  );
}

// The per-pane floor (task 70's 360px) still rules: at 800px the 40% tier
// parks the preview at 320px and the 60% tier the session at 320px — both
// collapse to the only legal tier, 50% (400/400).
check(effectiveSplitTier(0.4, 800) === 0.5, "a tier starving the preview under 360px collapses to 50/50");
check(effectiveSplitTier(0.6, 800) === 0.5, "a tier starving the session under 360px collapses to 50/50");
check(effectiveSplitTier(0.5, 800) === 0.5, "the 50/50 tier stands whenever two 360px panes fit");
// Too narrow for two legal panes (700px < 360*2): everything is 50/50.
check(effectiveSplitTier(0.6, 700) === 0.5, "a container too narrow for two legal panes collapses to 0.5");
check(effectiveSplitTier(Number.NaN, 1000) === 0.5, "NaN reads as 'no opinion' → 0.5");
check(effectiveSplitTier(0.6, 0) === 0.5, "zero width reads as 'no opinion' → 0.5");

// ── snapping + migration (档位吸附与旧格式迁移) ──────────────────────────────
check(normalizeSplitTier(0.44) === 0.4, "0.44 snaps to the nearest tier 40%");
check(normalizeSplitTier(0.55) === 0.5, "0.55 snaps to the nearest tier 50%");
check(normalizeSplitTier(0.59) === 0.6, "0.59 snaps to the nearest tier 60%");
check(normalizeSplitTier(1) === 0.6, "a stored 100% clamps to the widest tier");
check(normalizeSplitTier("garbage") === 0.5, "an unparseable stored value reads 0.5");
check(normalizeSplitTier(null) === 0.5, "a null stored value reads 0.5");
check(normalizeSplitTier(undefined) === 0.5, "an undefined stored value reads 0.5");

// The old key held the PRIMARY pane's fraction; the new fraction is the
// complement. Direction is preserved: an old 80%-preview layout (0.2) lands
// on the widest tier, an old 20%-preview layout (0.8) on the narrowest.
check(migrateLegacySplitRatio(0.5) === 0.5, "the old 50/50 default migrates to the 50% tier");
check(migrateLegacySplitRatio(0.2) === 0.6, "old primary 0.2 (80% preview) migrates to the 60% tier");
check(migrateLegacySplitRatio(0.8) === 0.4, "old primary 0.8 (20% preview) migrates to the 40% tier");
check(migrateLegacySplitRatio(0.35) === 0.6, "old primary 0.35 (65% preview) snaps to the nearest tier");
check(migrateLegacySplitRatio("garbage") === 0.5, "an unparseable legacy value migrates to the default");

// ── pointer snapping (拖拽吸附) ──────────────────────────────────────────────
// The preview sits on the RIGHT: the distance to the container's right edge
// is the preview width. A pointer at x=300 in a 1000px container asks for a
// 700px preview → snaps to the 60% tier.
check(splitPreviewTierFromPointer(300, 0, 1000) === 0.6, "a drag parking 700px right of the pointer snaps to 60%");
check(splitPreviewTierFromPointer(700, 0, 1000) === 0.4, "a drag parking 300px right of the pointer snaps to 40%");
check(splitPreviewTierFromPointer(500, 0, 1000) === 0.5, "a drag parking 500px right of the pointer snaps to 50%");
check(splitPreviewTierFromPointer(100, 0, 0) === 0.5, "a zero-width container never invents a tier");

const store = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
  setItem: (key: string, value: string) => void store.set(key, value),
  removeItem: (key: string) => void store.delete(key),
};
check(loadSplitPreviewTier() === 0.5, "no stored tier loads the 50/50 default");
persistSplitPreviewTier(0.4);
check(loadSplitPreviewTier() === 0.4, "a chosen tier survives persist → load (restart memory)");
store.set("desktop:splitPreviewTier", "not-json");
check(loadSplitPreviewTier() === 0.5, "a corrupt stored tier falls back to 0.5 (old/foreign build safe)");
persistSplitPreviewTier(7);
check(loadSplitPreviewTier() === 0.6, "persist snaps to the nearest tier before storing");

// One-time migration: the legacy key feeds the first load, then the new key
// takes over and the legacy key is gone.
store.clear();
store.set("desktop:splitRatio", JSON.stringify(0.2));
check(loadSplitPreviewTier() === 0.6, "a legacy primary-fraction value migrates to the complement tier");
check(store.get("desktop:splitPreviewTier") === JSON.stringify(0.6), "the migrated tier is persisted under the new key");
check(!store.has("desktop:splitRatio"), "the legacy key is removed after migration");
check(loadSplitPreviewTier() === 0.6, "the next load reads the migrated tier (migration runs once)");
delete (globalThis as { localStorage?: unknown }).localStorage;

console.log(`\n${passed} checks passed, ${failed} failed`);
if (failed > 0) process.exit(1);
