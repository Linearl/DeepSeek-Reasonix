import assert from "node:assert/strict";
import { createRepairNoticeGate, REPAIR_NOTICE_HOLD_MS, sessionCatalogNotice } from "../lib/sessionCatalogPresentation";
import type { SessionCatalogStatus } from "../lib/sessionCatalogTypes";

// Task 703: the sidebar status row used to flash "正在修复历史记录" / "历史记录稍后重试"
// for a blink. Every indexing wave claims its rows for a few hundred
// milliseconds, and a status poll landing inside a wave raised repairActive
// above zero just long enough to paint the banner. The hold gate requires a
// repair condition to persist before the banner appears; a return to "nothing
// repairing" is always immediate.

// 时钟注入：固定 t0，手动推进。
let now = 1_000_000;
const tick = (ms: number) => { now += ms; };
const gate = () => createRepairNoticeGate(() => now);

// 1) repair-active 首见 → 不显示（闪现被吞）。
let g = gate();
assert.equal(g("repair-active"), null, "a first-seen repair notice must not paint immediately");

// 2) 未满驻留期（含临界的 -1ms）→ 仍不显示。
tick(REPAIR_NOTICE_HOLD_MS - 1);
assert.equal(g("repair-active"), null, "a repair notice below the hold threshold must stay hidden");

// 3) 满驻留期 → 显示。
tick(1);
assert.equal(g("repair-active"), "repair-active", "a repair notice that held for the full window must show");

// 4) 消失 → 立即隐藏；再出现重新计时（波次间归零不得残留横幅）。
tick(500);
assert.equal(g(null), null);
tick(100);
assert.equal(g("repair-active"), null, "a re-raised repair notice must re-arm the hold window");
tick(REPAIR_NOTICE_HOLD_MS);
assert.equal(g("repair-active"), "repair-active", "the re-armed notice shows after holding again");

// 5) 修复类别互切（active→deferred）按新条件重新计时，不得借上一类的驻留直接显示。
g = gate();
assert.equal(g("repair-active"), null);
tick(REPAIR_NOTICE_HOLD_MS);
assert.equal(g("repair-active"), "repair-active");
assert.equal(g("repair-deferred"), null, "switching repair category restarts the hold");
tick(REPAIR_NOTICE_HOLD_MS - 1);
assert.equal(g("repair-deferred"), null);
tick(1);
assert.equal(g("repair-deferred"), "repair-deferred");

// 6) 启动/降级类即时不抑制：indexing（打开中/重建中）、failed、rebuild 直接显示，
//    否则真实故障会被延迟藏成盲区。
g = gate();
assert.equal(g("indexing"), "indexing");
assert.equal(g("failed"), "failed");
assert.equal(g("rebuild"), "rebuild");
assert.equal(g(null), null);

// 7) repair 条件短暂为真后归零（典型波次 <2s）→ 全程不显示。
g = gate();
assert.equal(g("repair-active"), null);
tick(300);
assert.equal(g(null), null, "wave ended");
tick(100);
assert.equal(g("repair-active"), null);
tick(300);
assert.equal(g(null), null);
assert.ok(300 + 100 + 300 < REPAIR_NOTICE_HOLD_MS);

// 8) 上游判定器回归锚（task 550 语义不被本次改动触碰）：legacy repairPending
//    残留不得制造 repair 横幅。
const status = (over: Partial<SessionCatalogStatus>): SessionCatalogStatus => ({
  state: "ready", revision: 1, indexed: 4, total: 4, repairPending: 0, ...over,
});
const staleLegacy = {
  state: "ready", revision: 7, indexed: 87, total: 87, repairPending: 1,
} as SessionCatalogStatus;
assert.equal(sessionCatalogNotice(staleLegacy), null);
assert.equal(sessionCatalogNotice(status({ repairActive: 1 })), "repair-active");
assert.equal(sessionCatalogNotice(status({ repairDeferred: 2 })), "repair-deferred");
assert.equal(sessionCatalogNotice(status({ repairBlocked: 1 })), "repair-blocked");
assert.equal(sessionCatalogNotice(status({})), null);

console.log("  PASS  repair notice hold gate (task 703 flash suppression)");
