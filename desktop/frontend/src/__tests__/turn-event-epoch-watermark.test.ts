// 任务645b: 投影器游标清空调用点审计的防护钉。657 给 reducer 装了 seq 水位
// 线，归零点只配了 reset/controller_rebuilt 两个 dispatch 侧动作；审计发现
// 快照路径的 epoch 变更（backend_status 携带新 runtimeEpoch）在投影器侧清游
// 标并从活跃轮起点重放（replayAfter=turnStartSeq-1），reducer 侧却没有配对
// 归零——runtime:rebuilt 推送（sink FIFO）与状态快照（Wails 调用）分属两条
// 通道，快照先到的竞态下旧 epoch 高位水位线把新 epoch 重放整段拦掉，而游标
// 已消费这些 seq：活跃轮 live tail 丢失且无重放重触发。这里把配对钉死：
// 快照采纳新 epoch 的同一动作必须归零水位线（三条采纳路径各一例）；同
// epoch / 首次 sighting 不动水位线；附 LRU 驱逐选择的纯函数钉（645b 另一
// 修复：会被 Task-192 守卫保留的 tab 不再只被删掉 React state）。
// 645b 续审补两条：① predates 三闸（prompt/lifecycle/retry）拒收 epoch 变更
// 快照时归零随行——投影器在同一触发已清游标，拒收只该拒绝快照的时效字段；
// ② closeTab 是终态，force 全量释放（驻留守卫是「切走保留」语义，删了 React
// state 却跳过投影器释放 = 半释放幽灵）。
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { initialState, reducer, selectTabStateLruEvictions } from "../lib/useController";
import type { WireEvent } from "../lib/types";

function equal(actual: unknown, expected: unknown, message: string) {
  if (actual !== expected) throw new Error(`${message}: got ${String(actual)}, want ${String(expected)}`);
}

const ev = (seq: number, event: Omit<WireEvent, "seq">): { type: "event"; e: WireEvent } => ({ type: "event", e: { ...event, seq } });
// snapshotAt 与 reducer 内部 promptEventClock() 同钟（performance.now()）；
// 取测试基线加偏移，保证每个快照都晚于轮内生命周期戳（守卫按 ties=stale）。
const clockBase = typeof performance !== "undefined" ? performance.now() : Date.now();
let clockTick = 0;
const nextSnapshotAt = () => clockBase + 1_000_000 + (clockTick += 1_000);
const status = (fields: {
  running: boolean;
  runtimeEpoch: string;
  turnEventSeq: number;
  snapshotAt?: number;
  turnStartedAt?: number;
}) => ({ type: "backend_status" as const, pendingPrompt: false, ...fields });

// ---- 快照先到序：epoch 变更归零水位线（no-change 捷径采纳路径）----
let s = reducer(initialState, status({ running: false, runtimeEpoch: "epoch-a", turnEventSeq: 40, snapshotAt: nextSnapshotAt() }));
s = reducer(s, ev(40, { kind: "turn_started", turnId: "t1", status: "in_progress" }));
equal(s.appliedEventSeq, 40, "旧 epoch 下水位线推进到 40");
equal(s.runtimeStatusEpoch, "epoch-a", "旧 epoch 已存档");
s = reducer(s, status({ running: false, runtimeEpoch: "epoch-b", turnEventSeq: 1, snapshotAt: nextSnapshotAt() }));
equal(s.runtimeStatusEpoch, "epoch-b", "快照采纳新 epoch（staleness 闸对 epoch 变更放行）");
equal(s.appliedEventSeq, 0, "epoch 变更快照归零水位线——657 配对补全");
const newEpochReplay = reducer(s, ev(1, { kind: "turn_started", turnId: "t2", status: "in_progress" }));
equal(newEpochReplay.appliedEventSeq, 1, "新 epoch 的 seq=1 重放不再被旧高位水位线吞掉");

// ---- 同 epoch 快照不归零 ----
s = reducer(newEpochReplay, status({ running: false, runtimeEpoch: "epoch-b", turnEventSeq: 5, snapshotAt: nextSnapshotAt() }));
equal(s.appliedEventSeq, 1, "同 epoch 快照不归零水位线");

// ---- 首次 sighting（无存储 epoch）不是变更，不动已推进的水位线 ----
let s2 = reducer(initialState, ev(7, { kind: "turn_started", turnId: "t0", status: "in_progress" }));
s2 = reducer(s2, status({ running: false, runtimeEpoch: "epoch-first", turnEventSeq: 7, snapshotAt: nextSnapshotAt() }));
equal(s2.appliedEventSeq, 7, "首次 epoch sighting 不归零（无旧 epoch 可言，重放由新 state 的 0 水位线兜底）");

// ---- running 分支采纳路径同样归零 ----
let s3 = reducer(initialState, status({ running: false, runtimeEpoch: "ea", turnEventSeq: 30, snapshotAt: nextSnapshotAt() }));
s3 = reducer(s3, ev(30, { kind: "turn_started", turnId: "t9", status: "in_progress" }));
s3 = reducer(s3, status({ running: true, runtimeEpoch: "eb", turnEventSeq: 1, snapshotAt: nextSnapshotAt(), turnStartedAt: clockBase + 500 }));
equal(s3.appliedEventSeq, 0, "running 分支采纳 runtimeStatus，epoch 变更同样归零");
equal(s3.runtimeStatusEpoch, "eb", "running 分支存档新 epoch");

// ---- idle 分支（结算收尾路径）同样归零 ----
let s4 = reducer(initialState, status({ running: true, runtimeEpoch: "ec", turnEventSeq: 50, snapshotAt: nextSnapshotAt() }));
s4 = reducer(s4, ev(50, { kind: "turn_started", turnId: "t10", status: "in_progress" }));
s4 = reducer(s4, status({ running: false, runtimeEpoch: "ed", turnEventSeq: 1, snapshotAt: nextSnapshotAt() }));
equal(s4.appliedEventSeq, 0, "idle 分支采纳 runtimeStatus，epoch 变更同样归零");
equal(s4.running, false, "idle 分支语义不回退（running 收敛为 false）");

// ---- epoch 翻回（A→B→A）同样归零：每次重建 seq 从 1 重启 ----
let s5 = reducer(initialState, status({ running: false, runtimeEpoch: "e1", turnEventSeq: 20, snapshotAt: nextSnapshotAt() }));
s5 = reducer(s5, ev(20, { kind: "turn_started", turnId: "t11", status: "in_progress" }));
s5 = reducer(s5, status({ running: false, runtimeEpoch: "e2", turnEventSeq: 1, snapshotAt: nextSnapshotAt() }));
s5 = reducer(s5, ev(1, { kind: "turn_started", turnId: "t12", status: "in_progress" }));
equal(s5.appliedEventSeq, 1, "e2 下水位线推进到 1");
s5 = reducer(s5, status({ running: false, runtimeEpoch: "e1", turnEventSeq: 1, snapshotAt: nextSnapshotAt() }));
equal(s5.appliedEventSeq, 0, "epoch 翻回 e1 同样归零（不假设 epoch 单调）");

// ---- 645b LRU 驱逐选择：会被 retain 守卫保留的 tab 不进驱逐名单 ----
const ids = ["a", "b", "c", "d", "e"];
const lastActive: Record<string, number> = { a: 1, b: 5, c: 3, d: 9, e: 2 };
const noPin = () => false;

equal(
  JSON.stringify(selectTabStateLruEvictions(ids, (id) => lastActive[id], 3, noPin).map((e) => e.id)),
  JSON.stringify(["c", "e", "a"]),
  "窗口外按最久未活跃排序：保留最新 2 个非活跃（d,b）+ 活跃 tab，驱逐 c,e,a",
);
equal(
  JSON.stringify(selectTabStateLruEvictions(ids, (id) => lastActive[id], 0, noPin).map((e) => e.id)),
  "[]",
  "limit 0 = 不限额，驱逐名单为空",
);
equal(
  JSON.stringify(selectTabStateLruEvictions(ids, (id) => lastActive[id], 3, (id) => id === "e").map((e) => e.id)),
  JSON.stringify(["c", "a"]),
  "窗口外的 pin tab（正在跑轮）被跳过，不删其 React state",
);
equal(
  JSON.stringify(selectTabStateLruEvictions(ids, (id) => lastActive[id], 99, noPin).map((e) => e.id)),
  "[]",
  "窗口覆盖全部 tab 时不驱逐",
);
equal(
  selectTabStateLruEvictions(["x"], () => undefined, 1, noPin)[0]?.lastActive,
  0,
  "lastActive 缺失按 0（最旧）参与排序",
);

// ---- 645b 续审①：predates 三闸拒收 epoch 变更快照，归零必须随行 ----
// 投影器在 dispatchRuntimeStatusForTab 里先于 dispatch 调 observeRuntime：
// epoch 不同即清游标 + 从 replayAfter 重播种 + requestReplay。快照随后被
// 时效闸拒收只是拒绝采纳其运行时字段，水位线若不归零，异步到达的重放事件
// 会被旧 epoch 高位 seq 整段吞掉——游标已消费，无重触发，live tail 永久丢。
{
  // 闸1 predatesPrompt：approval 已到，快照 snapshotAt 不晚于 promptArrivedAt。
  let s1 = reducer(initialState, status({ running: false, runtimeEpoch: "epoch-a", turnEventSeq: 40, snapshotAt: nextSnapshotAt() }));
  s1 = reducer(s1, ev(41, { kind: "approval_request", approval: { id: "ap-1", tool: "exit_plan_mode", subject: "Approve" } }));
  equal(s1.appliedEventSeq, 41, "approval 事件推进水位线到 41");
  const promptAt = s1.promptArrivedAt;
  equal(typeof promptAt, "number", "approval 记录 promptArrivedAt");
  const promptRejected = reducer(s1, status({ running: false, runtimeEpoch: "epoch-b", turnEventSeq: 1, snapshotAt: promptAt as number }));
  equal(promptRejected.appliedEventSeq, 0, "predatesPrompt 拒收 epoch-b 快照仍归零水位线（投影器已清游标）");
  equal(promptRejected.runtimeStatusEpoch, "epoch-a", "拒收不采纳新 epoch（快照时效字段整体拒绝）");
  equal(promptRejected.approval?.id, "ap-1", "拒收保留 approval（时效闸的本意不变）");
  // 同 epoch 对照：拒收路径不归零、引用相等。
  const sameEpochReject = reducer(s1, status({ running: false, runtimeEpoch: "epoch-a", turnEventSeq: 41, snapshotAt: promptAt as number }));
  equal(sameEpochReject, s1, "同 epoch 的 predatesPrompt 拒收返回原引用（水位线不动）");

  // 闸2 predatesTurnLifecycle：turn_started 观测后，snapshotAt 早于观测戳。
  let s2 = reducer(initialState, status({ running: false, runtimeEpoch: "epoch-a", turnEventSeq: 40, snapshotAt: nextSnapshotAt() }));
  s2 = reducer(s2, ev(40, { kind: "turn_started", turnId: "t-lc", status: "in_progress" }));
  equal(s2.appliedEventSeq, 40, "turn_started 推进水位线到 40");
  equal(typeof s2.turnLifecycleObservedAt, "number", "turn_started 记录生命周期观测戳");
  const lifecycleRejected = reducer(s2, status({ running: true, runtimeEpoch: "epoch-b", turnEventSeq: 1, snapshotAt: clockBase }));
  equal(lifecycleRejected.appliedEventSeq, 0, "predatesTurnLifecycle 拒收 epoch-b 快照仍归零水位线");
  equal(lifecycleRejected.runtimeStatusEpoch, "epoch-a", "lifecycle 拒收不采纳新 epoch");
  equal(lifecycleRejected.turnLifecycleObservedAt, s2.turnLifecycleObservedAt, "拒收不推进生命周期观测戳");

  // 闸3 predatesRetry：retrying 观测后，idle 快照 snapshotAt 早于观测戳。
  let s3 = reducer(initialState, status({ running: false, runtimeEpoch: "epoch-a", turnEventSeq: 50, snapshotAt: nextSnapshotAt() }));
  s3 = reducer(s3, ev(50, { kind: "retrying", retryAttempt: 1, retryMax: 3 }));
  equal(s3.appliedEventSeq, 50, "retrying 推进水位线到 50");
  equal(typeof s3.retry?.observedAt, "number", "retrying 记录观测戳");
  const retryRejected = reducer(s3, status({ running: false, runtimeEpoch: "epoch-b", turnEventSeq: 1, snapshotAt: clockBase }));
  equal(retryRejected.appliedEventSeq, 0, "predatesRetry 拒收 epoch-b 快照仍归零水位线");
  equal(retryRejected.runtimeStatusEpoch, "epoch-a", "retry 拒收不采纳新 epoch");
  equal(typeof retryRejected.retry?.observedAt, "number", "拒收保留 retry 指示（不被 idle 快照清掉）");
}

// ---- 645b 续审②：closeTab 终态 force 全量释放（源守卫） ----
{
  const thisDir = dirname(fileURLToPath(import.meta.url));
  const controller = readFileSync(join(thisDir, "../lib/useController.ts"), "utf8").replace(/\n\s*/g, " ");
  equal(/releaseTranscriptState\(tabId, \{ force: true \}\)/.test(controller), true, "closeTab 以 force 调用 releaseTranscriptState（关闭是终态，不驻留）");
  equal(/opts\?: \{ force\?: boolean \}/.test(controller), true, "releaseTranscriptState 接受 force 选项");
  equal(/!opts\?\.force && getTranscriptStore\(\)\.shouldRetainOnSwitch/.test(controller), true, "驻留守卫仅在非 force 时生效");
}

console.log("turn event epoch watermark tests passed");
