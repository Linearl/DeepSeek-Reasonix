import assert from "node:assert/strict";
import { sessionCatalogNotice } from "../lib/sessionCatalogPresentation";
import type { SessionCatalogStatus } from "../lib/sessionCatalogTypes";

// Task 550 ①: the repair banner must be driven only by the precise per-state
// counters. The legacy repairPending field counts every unknown-turn session
// whether or not a repair is queued, so when repairActive is missing the
// notice must read as "nothing repairing" — the old fallback manufactured a
// stuck "repairing history, 1 remaining" banner and two renders of the same
// catalog could disagree about it (the flash).

const status = (over: Partial<SessionCatalogStatus>): SessionCatalogStatus => ({
  state: "ready", revision: 1, indexed: 4, total: 4, repairPending: 0, ...over,
});

// 对立输入：repairActive 缺失 + 旧字段 repairPending 残留非 0（三者皆缺失，
// 正是旧回退链的触发形状）→ 不得显示修复横幅。
const staleLegacy: SessionCatalogStatus = {
  state: "ready", revision: 7, indexed: 87, total: 87, repairPending: 1,
} as SessionCatalogStatus;
for (let round = 0; round < 5; round++) {
  assert.equal(sessionCatalogNotice(staleLegacy), null,
    `round ${round}: a missing repairActive must never resurrect the legacy repairPending field into a repair banner`);
}

// mock 桥（只有 repairPending: 0）→ 无横幅。
assert.equal(sessionCatalogNotice(status({ repairPending: 0 } as Partial<SessionCatalogStatus>)), null);

// 正路：repairActive > 0 → 修复横幅；任意重渲染次数下判定一致。
const active = status({ repairPending: 3, repairActive: 1, repairDeferred: 0, repairBlocked: 0 });
for (let round = 0; round < 5; round++) {
  assert.equal(sessionCatalogNotice(active), "repair-active",
    `round ${round}: repairActive > 0 must show the repair-active banner`);
}

// deferred / blocked 各自走自己的文案分支。
assert.equal(sessionCatalogNotice(status({ repairActive: 0, repairDeferred: 2 })), "repair-deferred");
assert.equal(sessionCatalogNotice(status({ repairActive: 0, repairDeferred: 0, repairBlocked: 1 })), "repair-blocked");

// 全零 → 无横幅（库层 0 待修 ⇒ 计数为 0 的对立面校验）。
assert.equal(sessionCatalogNotice(status({ repairActive: 0, repairDeferred: 0, repairBlocked: 0, repairPending: 0 })), null);

// 其余状态分支不受本次改动影响（回归守护）。
assert.equal(sessionCatalogNotice(status({ state: "opening" })), "indexing");
assert.equal(sessionCatalogNotice(status({ state: "rebuilding" })), "indexing");
assert.equal(sessionCatalogNotice(status({ unindexedTargetCount: 2 })), "indexing");
assert.equal(sessionCatalogNotice(status({ state: "degraded", canRebuild: true })), "rebuild");
assert.equal(sessionCatalogNotice(status({ state: "degraded", canRebuild: false })), "failed");
assert.equal(sessionCatalogNotice(status({ lastError: "boom", canRebuild: true })), "rebuild");

console.log("  PASS  session catalog notice (task 550 banner fallback)");
