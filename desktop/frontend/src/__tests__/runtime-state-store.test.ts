import assert from "node:assert/strict";
import { createRuntimeStateStore, selectRuntime, type RuntimeProjection, type RuntimeState } from "../lib/runtimeStateStore";
import { runtimeSyncDiagnostic, startRuntimeStateSync } from "../lib/runtimeStateSync";
import { acceptRuntimeState } from "../lib/runtimeStateReducer";

const state: RuntimeState = { schemaVersion: 1, runtimeEpoch: "controller-a", revision: 1, phase: "executing", running: true,
  turnId: "turn-a", turnStatus: "in_progress", turnEventSeq: 1, pendingPrompt: false, cancelRequested: false, cancellable: true, backgroundJobs: 0, activity: "thinking" };
const projection = (revision: number, changes: Partial<RuntimeState> = {}): RuntimeProjection => ({ epoch: "app-a", revision, topics: [],
  sessions: [{ tabId: "a", scope: "project", workspaceRoot: "/fixture", topicId: "topic", sessionPath: "/fixture/session", sessionGeneration: 1,
    open: true, remote: false, freshness: "synced", state: { ...state, revision, ...changes } }] });
const rawStore = createRuntimeStateStore();
const store = { ...rawStore, accept: (next: RuntimeProjection, authoritative = false) => acceptRuntimeState(rawStore, next, authoritative) };
let updates = 0;
store.subscribe(() => updates++);
assert.equal(store.accept(projection(1)), "accepted");
const first = store.getSnapshot();
assert.equal(store.accept(projection(1)), "duplicate");
assert.equal(store.getSnapshot(), first);
assert.equal(updates, 1);
assert.equal(store.accept(projection(1, { running: false })), "conflict");
assert.equal(store.getSnapshot(), first);
assert.equal(store.accept(projection(0)), "stale");
assert.equal(store.accept(projection(2, { phase: "finishing", activity: "", cancellable: false })), "accepted");
let view = selectRuntime(store.getSnapshot()!.sessions[0]);
assert.equal(view.kind, "finishing"); assert.equal(view.spinning, false); assert.equal(view.cancellable, false); assert.equal(view.running, true);
store.accept(projection(3, { phase: "idle", running: false, activity: "", cancellable: false, backgroundJobs: 2 }));
view = selectRuntime(store.getSnapshot()!.sessions[0]);
assert.equal(view.kind, "background_job"); assert.equal(view.running, false);
store.fail();
// 任务510（b 面隔离）：全局同步失败不再把每个会话一起拖成 unknown——unknown 只由
// 本会话 freshness 决定；全局失败保留在 store.getFailed 供项目树等消费方整体降级。
{
  const isolated = selectRuntime(store.getSnapshot()!.sessions[0]);
  assert.equal(isolated.kind, "background_job");
  assert.equal(isolated.unknown, false);
}
// 任务510（a 保出口）：会话自身 freshness=unknown 仍降级为 unknown（提示 + 防误操作
// 语义保留），但 cancellable 不再被 unknown 强制 false——过期投影锁不死停止出口。
{
  const degraded = selectRuntime({ ...store.getSnapshot()!.sessions[0], freshness: "unknown" as const });
  assert.equal(degraded.kind, "unknown");
  assert.equal(degraded.unknown, true);
  assert.equal(degraded.spinning, false, "unknown 降级态不转圈（提示语义保留）");
  const fresh = store.getSnapshot()!.sessions[0];
  const runningUnknown = selectRuntime({
    ...fresh, freshness: "unknown" as const,
    state: { ...fresh.state, phase: "executing", running: true, cancellable: true, backgroundJobs: 0 },
  });
  assert.equal(runningUnknown.unknown, true);
  assert.equal(runningUnknown.cancellable, true, "unknown 不把可停状态锁成不可取消");
}
assert.equal(store.getSnapshot()!.sessions[0].state.backgroundJobs, 2);
assert.equal(store.accept({ ...projection(1), epoch: "other" }), "conflict");
assert.equal(store.accept({ ...projection(1), epoch: "other" }, true), "accepted");
const mutable = projection(10);
assert.equal(store.accept(mutable, true), "accepted");
mutable.sessions[0].state.running = false;
assert.equal(store.getSnapshot()!.sessions[0].state.running, true, "caller mutation cannot change a committed revision");
const malformed = projection(11);
delete (malformed.sessions[0].state as Partial<RuntimeState>).cancellable;
assert.equal(store.accept(malformed), "conflict", "partial new-schema booleans cannot imply idle or uncancellable");

function deferred<T>() { let resolve!: (value: T) => void; let reject!: (err: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
const synced = createRuntimeStateStore();
let receive!: (snapshot: RuntimeProjection) => void, focus!: () => void;
let pending = deferred<RuntimeProjection>();
let reads = 0, subscribed = false, unsubscribed = false;
const timers = new Map<number, { callback: () => void; delay: number }>();
let timerID = 0;
const stop = startRuntimeStateSync({
  subscribe: callback => { subscribed = true; receive = callback; return () => { unsubscribed = true; }; },
  read: () => { assert.equal(subscribed, true, "subscribe before initial GET"); reads++; return pending.promise; },
  timer: (callback, delay) => { const id = ++timerID; timers.set(id, { callback, delay }); return id; },
  clearTimer: id => { timers.delete(id as number); },
  focus: callback => { focus = callback; return () => {}; },
}, synced);
focus(); focus(); assert.equal(reads, 1, "focus shares in-flight GET");
receive(projection(4));
pending.resolve(projection(2));
await pending.promise; await Promise.resolve();
assert.equal(synced.getSnapshot()!.revision, 4, "old GET cannot overwrite SSE");
assert.equal([...timers.values()][0].delay, 30000);
for (const delay of [5000, 10000, 20000, 30000, 30000]) {
  pending = deferred<RuntimeProjection>();
  [...timers.values()][0].callback();
  pending.reject(new Error("offline"));
  await pending.promise.catch(() => {}); await Promise.resolve();
  assert.equal([...timers.values()][0].delay, delay);
  assert.equal(synced.getSnapshot()!.revision, 4, "failure preserves known state");
}
pending = deferred<RuntimeProjection>(); focus(); pending.resolve(projection(5));
await pending.promise; await Promise.resolve();
assert.equal([...timers.values()][0].delay, 30000);
assert.equal(synced.getFailed(), false);
const final = synced.getSnapshot();
stop(); receive(projection(6)); focus();
assert.equal(synced.getSnapshot(), final); assert.equal(unsubscribed, true); assert.equal(timers.size, 0);

// 任务510（诊断可观测 + 恢复上界）：诊断在转换点触发（应用侧接 ReportFrontendLog
// 落 desktop.log）；降级态重试 5s 起步，恢复不单靠 30s 周期轮询兜底。
{
  const flushMicro = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };
  const diag: Array<{ reason: string; failures: number; stale: number; conflicts: number }> = [];
  const observed = createRuntimeStateStore();
  let flap = deferred<RuntimeProjection>();
  const scheduled: Array<{ callback: () => void; delay: number }> = [];
  const remoteSession = (freshness: "synced" | "unknown", revision: number): RuntimeProjection => ({
    epoch: "app-a", revision, topics: [],
    sessions: [{ tabId: "r", scope: "remote", workspaceRoot: "/r", topicId: "t", sessionPath: "/r/s.jsonl", sessionGeneration: 1,
      open: true, remote: true, freshness, state: { ...state, revision } }],
  });
  const stopFlap = startRuntimeStateSync({
    subscribe: () => () => {},
    read: () => flap.promise,
    timer: (callback, delay) => { scheduled.push({ callback, delay }); return scheduled.length; },
    clearTimer: () => {},
    focus: () => () => {},
    diagnostic: data => diag.push(data),
  }, observed);
  // 首轮 GET：远端会话 freshness=unknown → failures 0→1 → degraded 转换点 + 5s 重试。
  flap.resolve(remoteSession("unknown", 1));
  await flap.promise; await flushMicro();
  assert.equal(diag.map(d => d.reason).join(","), "degraded", "远端失同步在 0→1 转换点记 degraded");
  assert.equal(diag[0].failures, 1);
  assert.equal(scheduled[0].delay, 5000, "降级态首次重试 5s（恢复上界，不单靠 30s 轮询）");
  // 下一轮：恢复 synced → recovered 转换点 + 回到 30s 周期。
  flap = deferred<RuntimeProjection>();
  scheduled[0].callback();
  flap.resolve(remoteSession("synced", 2));
  await flap.promise; await flushMicro();
  assert.equal(diag.map(d => d.reason).join(","), "degraded,recovered", "恢复转换点记 recovered");
  assert.equal(scheduled[1].delay, 30000, "恢复后回到 30s 周期");
  // 读失败 → store.fail → 记原始 reason（failures>0，级别 warn 由应用侧 diagnostic 定）。
  flap = deferred<RuntimeProjection>();
  scheduled[1].callback();
  flap.reject(new Error("offline"));
  await flap.promise.catch(() => {}); await flushMicro();
  assert.equal(diag[diag.length - 1]?.reason, "periodic", "读失败记原始 reason");
  assert.equal(observed.getFailed(), true);
  // 失败后恢复 → recovered。
  flap = deferred<RuntimeProjection>();
  scheduled[2].callback();
  flap.resolve(remoteSession("synced", 3));
  await flap.promise; await flushMicro();
  assert.equal(diag[diag.length - 1]?.reason, "recovered", "失败恢复记 recovered");
  assert.equal(observed.getFailed(), false);
  stopFlap();
}

// runtimeSyncDiagnostic 转发 ReportFrontendLog（desktop.log 落盘通道；任务510）。
{
  const logged: Array<{ feature: string; level: string; message: string; detail: string }> = [];
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: {
    ReportFrontendLog: (feature: string, level: string, message: string, detail: string) => { logged.push({ feature, level, message, detail }); return Promise.resolve(); },
  } } } };
  try {
    runtimeSyncDiagnostic({ reason: "periodic", stale: 0, conflicts: 0, failures: 2 });
    assert.equal(logged.length, 1, "诊断落到 ReportFrontendLog");
    assert.equal(logged[0].feature, "runtime-sync");
    assert.equal(logged[0].level, "warn", "降级态记 warn");
    assert.ok(logged[0].detail.includes("failures=2"), "detail 携带计数现场");
    runtimeSyncDiagnostic({ reason: "recovered", stale: 0, conflicts: 0, failures: 0 });
    assert.equal(logged[1].level, "info", "恢复记 info");
  } finally {
    delete (globalThis as unknown as { window?: unknown }).window;
  }
}
console.log("runtime state: immutable revisions, selectors, GET/SSE ordering, recovery, singleflight and disposal passed");
