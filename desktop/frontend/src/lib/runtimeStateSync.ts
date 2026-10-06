import { app } from "./bridge";
import { reportFrontendLog } from "./frontendLog";
import { acceptRuntimeState } from "./runtimeStateReducer";
import { runtimeStateStore, type RuntimeProjection } from "./runtimeStateStore";
export interface RuntimeSyncPorts {
  subscribe: (accept: (snapshot: RuntimeProjection) => void) => () => void;
  read: () => Promise<RuntimeProjection>;
  timer: (callback: () => void, delay: number) => unknown;
  clearTimer: (timer: unknown) => void;
  focus: (callback: () => void) => () => void;
  diagnostic?: (data: { reason: string; revision?: number; stale: number; conflicts: number; failures: number }) => void;
}
// 任务510（诊断可观测）：同步诊断此前只进 console.debug——webview 控制台不落盘，
// 「unknown 吞停止按钮」复发时没有现场。接 ReportFrontendLog 进 desktop.log，
// 降级记 warn、恢复记 info；note 只在转换点触发，不会逐帧刷 4MB 滚动日志。
export function runtimeSyncDiagnostic(data: { reason: string; revision?: number; stale: number; conflicts: number; failures: number }) {
  console.debug("runtime synchronization", { source: "desktop-runtime", ...data });
  const degraded = data.failures > 0 || data.stale > 0 || data.conflicts > 0;
  reportFrontendLog("runtime-sync", degraded ? "runtime state sync degraded" : "runtime state sync recovered",
    `reason=${data.reason} revision=${data.revision ?? "-"} stale=${data.stale} conflicts=${data.conflicts} failures=${data.failures}`,
    degraded ? "warn" : "info");
}
export function startRuntimeStateSync(ports: RuntimeSyncPorts, store = runtimeStateStore) {
  let disposed = false, inFlight = false, failures = 0;
  let stale = 0, conflicts = 0;
  let timer: unknown;
  const note = (reason: string, revision?: number) => ports.diagnostic?.({ reason, revision, stale, conflicts, failures });
  const sync = async (reason = "initial") => {
    if (disposed || inFlight) return;
    inFlight = true;
    ports.clearTimer(timer);
    const before = store.getSnapshot();
    // 任务510（c 恢复可测）：记录本轮开始时是否处于降级态，结束时对比——
    // 降级↔恢复的转换点各补一条诊断（fail→recovered 成对，现场可推恢复时长）。
    const wasDegraded = store.getFailed() || failures > 0;
    let noted = false;
    try {
      const snapshot = await ports.read();
      if (disposed) return;
      const result = acceptRuntimeState(store, snapshot, before === store.getSnapshot());
      if (result === "stale") { stale++; noted = true; note("stale-read", snapshot.revision); }
      if (result === "conflict") { conflicts++; throw new Error("Runtime snapshot version conflict"); }
      failures = snapshot.sessions.some(session => session.remote && session.freshness !== "synced") ? failures + 1 : 0;
    } catch {
      if (!disposed) { failures++; store.fail(); noted = true; note(reason); }
    } finally {
      inFlight = false;
      if (!disposed) {
        const degraded = store.getFailed() || failures > 0;
        if (!noted && degraded !== wasDegraded) note(degraded ? "degraded" : "recovered");
        // 降级态的重试阶梯从 5s 起步（5000/10000/20000/30000 封顶）：unknown
        // 的兜底恢复上界受它约束，不依赖单一 30s 周期轮询；事件通道（后端
        // 补发）仍是主恢复通路。
        timer = ports.timer(() => { void sync("periodic"); }, failures ? [5000, 10000, 20000, 30000][Math.min(failures - 1, 3)] : 30000);
      }
    }
  };
  const off = ports.subscribe(snapshot => {
    if (disposed) return;
    const result = acceptRuntimeState(store, snapshot);
    if (result === "stale") { stale++; note("stale-event", snapshot.revision); }
    if (result === "conflict") { conflicts++; note("conflicting-event", snapshot.revision); void sync("conflicting-event"); }
  });
  const offFocus = ports.focus(() => { void sync("focus-or-connection"); });
  void sync();
  return () => { disposed = true; off(); offFocus(); ports.clearTimer(timer); };
}

export function startAppRuntimeStateSync() {
  return startRuntimeStateSync({
      diagnostic: runtimeSyncDiagnostic,
      subscribe: accept => window.runtime?.EventsOn("runtime-state:changed", (snapshot: unknown) => accept(snapshot as RuntimeProjection)) ?? (() => {}),
      read: async () => {
        if (!app.SyncRuntimeState) return app.GetRuntimeStateSnapshot!();
        try { return await app.SyncRuntimeState(); }
        catch { return app.GetRuntimeStateSnapshot!(); }
      },
      timer: (callback, delay) => window.setTimeout(callback, delay),
      clearTimer: timer => { if (timer !== undefined) window.clearTimeout(timer as number); },
      focus: callback => {
        const connections = new Map<string, string>();
        const off = window.runtime?.EventsOn("remote-tab:updated", (payload: unknown) => {
          const tab = payload as { id?: string; remoteState?: string };
          if (tab.id && connections.get(tab.id) !== tab.remoteState) { connections.set(tab.id, tab.remoteState ?? ""); callback(); }
        });
        window.addEventListener("focus", callback);
        window.addEventListener("online", callback);
        return () => { off?.(); window.removeEventListener("focus", callback); window.removeEventListener("online", callback); };
      },
      });
}
