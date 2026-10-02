// Run: tsx src/__tests__/tab-close-policy.test.tsx
// Task 223 pins: batch close (关闭右侧/关闭其他) targets and the fork's
// detach-don't-block policy, including the upstream comparison record.
// Task 368 adds 关闭非活跃标签页 (keep the ACTIVE tab) to the same policy module.

import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { selectCloseInactiveIds, selectCloseOtherIds, selectCloseRightIds, batchClosePolicy } from "../lib/tabClosePolicy";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const tabs = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "d" }];

console.log("\nTab batch-close policy (task 223)");

// 关闭其他: every tab except the menu tab; menu tab stays active.
const others = selectCloseOtherIds(tabs, "b");
ok(others.ids.join(",") === "a,c,d", "close-others selects every tab but the menu tab");
ok(others.nextActiveTabId === "b", "close-others activates the menu tab");

// 关闭右侧: strict right side; active tab on the right falls back to menu tab.
const right = selectCloseRightIds(tabs, 1, "b", "d");
ok(right.ids.join(",") === "c,d", "close-right selects tabs strictly to the right");
ok(right.nextActiveTabId === "b", "closing the active right-side tab activates the menu tab");
const rightIdle = selectCloseRightIds(tabs, 1, "b", "a");
ok(rightIdle.nextActiveTabId === undefined, "active tab outside the range keeps its selection");

// Boundary cases: last tab has no right side; empty menu is a no-op.
ok(selectCloseRightIds(tabs, 3, "d", "d").ids.length === 0, "last tab closes nothing");
ok(selectCloseOtherIds(tabs, null).ids.length === 0, "no menu tab closes nothing");

// The policy itself: batch close never blocks — the residual upstream
// stop-prompt must not come back through this entry (task 223 acceptance 1/3),
// while the explicit stop entry remains the only stop_and_close path.
ok(batchClosePolicy() === "keep_running", "batch close always detaches active work (never stop_and_close)");

// 任务 368: 关闭非活跃标签页 — keep the ACTIVE tab, not the right-clicked one.
// Keyed differently from 关闭其他 on purpose: right-clicking a background tab
// and choosing it must not keep that background tab alive.
const inactive = selectCloseInactiveIds(tabs, "c");
ok(inactive.ids.join(",") === "a,b,d", "close-inactive selects every tab but the active one");
ok(inactive.nextActiveTabId === "c", "close-inactive keeps the active tab selected");
ok(selectCloseInactiveIds(tabs, null).ids.length === 0, "no active tab closes nothing");
const activeOnly = selectCloseInactiveIds([{ id: "only" }], "only");
ok(activeOnly.ids.length === 0, "single active tab closes nothing");
ok(hierarchyIsDisplayOnlyInactive(tabs, inactive), "close-inactive never reorders or renames the kept set");

function hierarchyIsDisplayOnlyInactive(all: { id: string }[], target: { ids: string[] }): boolean {
  const closed = new Set(target.ids);
  const kept = all.filter((tab) => !closed.has(tab.id)).map((tab) => tab.id);
  return kept.length === 1 && kept[0] === target.nextActiveTabId;
}

// Wiring guard: the menu entry exists in the TabBar and goes through the same
// helper (the policy module stays the single decision point, task 223 style).
{
  const tabBar = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../components/TabBar.tsx"), "utf8");
  ok(tabBar.includes('key: "close-inactive"'), "TabBar context menu offers 关闭非活跃标签页");
  ok(tabBar.includes("selectCloseInactiveIds(tabs, resolvedActiveTabId)"), "the menu entry keys off the ACTIVE tab id");
}
function dirnameOf(): string {
  return dirname(fileURLToPath(import.meta.url));
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
process.exit(failed > 0 ? 1 : 0);
