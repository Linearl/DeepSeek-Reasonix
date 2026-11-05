// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tab-runtime-reconcile.test.ts
//
// tl1410 会话运行状态双源失步——顶栏 tabMetas 对账钉。
//
// 现场（用户 20261105 报告）：会话工作结束后顶栏 TabBar 仍「Running」、侧栏
// ProjectTree 已不在工作，数秒后自行恢复。根因：Go 侧同源（ListTabs 与
// GetRuntimeStateSnapshot 都实时派生 controller RuntimeStatus），前端两条链
// 独立取数（顶栏=5 种 Wire 事件触发 ListTabs+15s 兜底；侧栏=runtime-state
// 推送+30s 轮询），CPU 风暴事件泵阻塞时 turn_done 丢失/延迟 → tabMetas 残留
// running=true，侧栏先恢复。
//
// 验收（任务书逐条）：
// ① 失步检测双向：store 已翻转而 tab.running 残留（用户现场）与反向都触发对账；
// ② 语义同口径：claimsWork = running||pendingPrompt||backgroundJobs>0（Go
//    tabs.go:2490 同口径），后台任务/待确认不算失步；
// ③ 不可信投影不当真相：legacy schema、510 unknown（freshness!=synced）跳过；
// ④ 找不到的 tabId（file tab/已关）不误报；remote/非 open 会话不纳入；
// ⑤ 事件丢失 ≤5s 收敛：只更新 store 不发任何事件（模拟 turn_done 丢失），
//    tick 到点触发 refresh（拉取通道收敛）；
// ⑥ 冷却：refresh 后 COOLDOWN 内不重复触发（防 tl1266 断开投影类误报死循环），
//    COOLDOWN 过后恢复；
// ⑦ 一致零 IPC；store 无快照不触发；
// ⑧ 订阅回检：store commit（事件通路正常）立即回检，不等 tick。

import assert from "node:assert/strict";
import {
  TAB_RUNTIME_RECONCILE_COOLDOWN_MS,
  TAB_RUNTIME_RECONCILE_TICK_MS,
  createTabRuntimeReconciler,
  findTabRuntimeMismatch,
  reconcileClaimsWork,
  type ReconcileTab,
} from "../lib/tabRuntimeReconcile";
import type { RuntimeProjection, RuntimeState } from "../lib/runtimeStateStore";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = JSON.stringify(actual) === JSON.stringify(expected);
  if (same) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function state(over: Partial<RuntimeState> = {}): RuntimeState {
  return {
    schemaVersion: 1, runtimeEpoch: "e1", revision: 1, phase: "idle", running: false,
    turnId: "", turnStatus: "", turnEventSeq: 0, pendingPrompt: false, cancelRequested: false,
    cancellable: false, backgroundJobs: 0, activity: "", ...over,
  };
}
function projection(sessions: Array<{ tabId: string; running?: boolean; pendingPrompt?: boolean; backgroundJobs?: number; remote?: boolean; open?: boolean; freshness?: "synced" | "unknown"; schemaVersion?: number }>): RuntimeProjection {
  return {
    epoch: "epoch-1", revision: 1, topics: [],
    sessions: sessions.map(session => ({
      tabId: session.tabId, scope: "project", workspaceRoot: "/w", topicId: `t-${session.tabId}`,
      sessionPath: `/w/${session.tabId}.jsonl`, sessionGeneration: 1,
      open: session.open ?? true, remote: session.remote ?? false,
      freshness: session.freshness ?? "synced",
      state: state({
        running: session.running ?? false,
        pendingPrompt: session.pendingPrompt ?? false,
        backgroundJobs: session.backgroundJobs ?? 0,
        schemaVersion: session.schemaVersion ?? 1,
      }),
    })),
  };
}
const tab = (id: string, running: boolean): ReconcileTab => ({ id, running });

// ---- ② 语义合成（claimsWork 与 Go TabMeta.Running 同口径） ----
eq(reconcileClaimsWork(state({})), false, "全零态 → 不在干活");
eq(reconcileClaimsWork(state({ running: true })), true, "running → 在干活");
eq(reconcileClaimsWork(state({ pendingPrompt: true })), true, "待确认 → 在干活（Go claimsWork 含 PendingPrompt）");
eq(reconcileClaimsWork(state({ backgroundJobs: 2 })), true, "后台任务 → 在干活（Go claimsWork 含 BackgroundJobs）");
eq(reconcileClaimsWork(undefined), undefined, "无 state → 不可信");
eq(reconcileClaimsWork(state({ schemaVersion: 0 })), undefined, "legacy schema → 不可信（不当真相）");

// ---- ③④ 失步检测过滤 ----
eq(findTabRuntimeMismatch(undefined, [tab("t1", true)]), null, "store 无快照 → 不触发");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false }]), [tab("t1", false)]), null, "两侧一致 → 不触发");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false }]), [tab("t1", true)]), "t1", "用户现场：store 已结束、tab.running 残留 true → 失步");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: true }]), [tab("t1", false)]), "t1", "反向：store 在跑、tab.running 残留 false → 失步");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", pendingPrompt: true }]), [tab("t1", true)]), null, "待确认场景口径一致（tab.running=true）→ 不触发");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", backgroundJobs: 1 }]), [tab("t1", true)]), null, "后台任务场景口径一致 → 不触发");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false, freshness: "unknown" }]), [tab("t1", true)]), null, "510 unknown 投影不当真相 → 不触发");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false, schemaVersion: 0 }]), [tab("t1", true)]), null, "legacy 投影不当真相 → 不触发");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false, open: false }]), []), null, "非 open 会话 → 跳过");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false, remote: true }]), [tab("t1", true)]), null, "remote 会话不纳入本对账 → 跳过");
eq(findTabRuntimeMismatch(projection([{ tabId: "t1", running: false }]), []), null, "tabMetas 找不到 tabId（file tab/已关）→ 不误报");
eq(findTabRuntimeMismatch(projection([{ tabId: "t2", running: false }]), [tab("t1", true)]), null, "不同 tabId 不比对 → 不误报");

// ---- ⑤⑥⑦⑧ reconciler 行为（手动时钟 + 手动驱动 tick/订阅） ----
function makeHarness(snapshot: RuntimeProjection | undefined) {
  const listeners = new Set<() => void>();
  let current = snapshot;
  let clock = 0;
  const refreshes: number[] = [];
  let tickCb: (() => void) | undefined;
  const harness = {
    refreshes,
    commit(next: RuntimeProjection, options?: { silent?: boolean }) {
      current = next;
      if (!options?.silent) listeners.forEach(listener => listener());
    },
    setTabs: (tabs: ReconcileTab[]) => { reconciler?.setTabMetas(tabs); },
    advanceTick: () => { clock += TAB_RUNTIME_RECONCILE_TICK_MS; tickCb?.(); },
    emit: () => listeners.forEach(listener => listener()),
    time: () => clock,
    stop() { reconciler?.dispose(); },
  };
  const reconciler = createTabRuntimeReconciler({
    subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    getSnapshot: () => current,
    setInterval_: (callback) => { tickCb = callback; return 1; },
    clearInterval_: () => { tickCb = undefined; },
    refresh: () => { refreshes.push(clock); },
    now: () => clock,
  });
  (harness as { reconciler?: typeof reconciler }).reconciler = reconciler;
  return harness;
}

// ⑤ 事件丢失（silent commit，模拟 turn_done 与 runtime-state:changed 都丢失、
// 30s 轮询拉到新投影）→ tick 兜底 ≤5s 触发拉取。
{
  const h = makeHarness(projection([{ tabId: "t1", running: false }]));
  h.setTabs([tab("t1", true)]);
  h.advanceTick();
  eq(h.refreshes.length, 1, "事件丢失：tick ≤5s 检出失步并触发 ListTabs 拉取");
  eq(h.refreshes[0], TAB_RUNTIME_RECONCILE_TICK_MS, "首次触发发生在第一个 tick（≤5s 收敛验收）");
  h.stop();
}

// ⑤b 拉取后 tabMetas 被拉齐（setTabs 回填最新列表）→ 后续 tick 不再触发。
{
  const h = makeHarness(projection([{ tabId: "t1", running: false }]));
  h.setTabs([tab("t1", true)]);
  h.advanceTick();
  h.setTabs([tab("t1", false)]); // refreshTabMetas 完成后的新 tabMetas
  const before = h.refreshes.length;
  h.advanceTick();
  eq(h.refreshes.length, before, "拉齐后继续 tick 不再触发（一致零 IPC）");
  h.stop();
}

// ⑥ 冷却：refresh 后 COOLDOWN 内仍失步不再触发；COOLDOWN 过后恢复。
{
  const h = makeHarness(projection([{ tabId: "t1", running: false }]));
  h.setTabs([tab("t1", true)]);
  h.advanceTick(); // 第 1 次触发，lastRefresh=5s
  h.advanceTick(); // 10s < 5+15s 冷却内
  eq(h.refreshes.length, 1, "冷却内（拉取未生效仍失步）不重复触发");
  for (let i = 0; i < 4; i += 1) h.advanceTick(); // 推进到 25s ≥ 5+15s
  eq(h.refreshes.length, 2, "冷却过后恢复触发");
  ok(TAB_RUNTIME_RECONCILE_COOLDOWN_MS >= TAB_RUNTIME_RECONCILE_TICK_MS * 2,
    "冷却覆盖至少两个 tick（误报 IPC 上界受控）");
  h.stop();
}

// ⑦ 一致零 IPC。
{
  const h = makeHarness(projection([{ tabId: "t1", running: true }]));
  h.setTabs([tab("t1", true)]);
  h.advanceTick();
  h.advanceTick();
  eq(h.refreshes.length, 0, "两侧一致：tick 空转零 IPC");
  h.stop();
}

// ⑧ 订阅回检：store commit（事件通路正常）立即触发，不等 tick。
{
  const h = makeHarness(projection([{ tabId: "t1", running: true }]));
  h.setTabs([tab("t1", true)]); // 初始一致，无任何 tick 触发
  h.commit(projection([{ tabId: "t1", running: false }])); // turn 结束推送到达，tabMetas 未跟上
  eq(h.refreshes.length, 1, "事件通路正常：store commit 即回检触发（毫秒级收敛）");
  h.stop();
}

// ⑧b 订阅回检同样受冷却约束。
{
  const h = makeHarness(projection([{ tabId: "t1", running: true }]));
  h.setTabs([tab("t1", true)]);
  h.commit(projection([{ tabId: "t1", running: false }])); // 触发第 1 次
  h.commit(projection([{ tabId: "t1", running: true }])); // 失步再现（反向），冷却内
  eq(h.refreshes.length, 1, "冷却同样约束订阅回检（事件风暴不放大 IPC）");
  h.stop();
}

// ⑨ dispose 后不再触发。
{
  const h = makeHarness(projection([{ tabId: "t1", running: false }]));
  h.setTabs([tab("t1", true)]);
  h.stop();
  h.advanceTick();
  h.emit();
  eq(h.refreshes.length, 0, "dispose 后 tick/订阅都不再触发");
}

process.stdout.write(`\ntl1410 tab-runtime-reconcile: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
