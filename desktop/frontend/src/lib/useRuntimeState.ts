import { useEffect, useSyncExternalStore } from "react";
import { app } from "./bridge";
import { runtimeStateStore, selectRuntime } from "./runtimeStateStore";

export function useRuntimeSession(tabId?: string, sessionPath?: string) {
  const snapshot = useSyncExternalStore(runtimeStateStore.subscribe, runtimeStateStore.getSnapshot);
  // 任务510：不再订阅全局 failed——unknown 只由本会话 freshness 决定（b 收敛故障面）。
  const session = snapshot?.sessions.find(session => session.open && session.tabId === tabId && (!sessionPath || session.sessionPath === sessionPath));
  return selectRuntime(session);
}

export function useRuntimeStateSync() {
  useEffect(() => {
    if (!app.GetRuntimeStateSnapshot) return;
    let disposed = false;
    let stop: (() => void) | undefined;
    void import("./runtimeStateSync").then(({ startAppRuntimeStateSync }) => {
      if (!disposed) stop = startAppRuntimeStateSync();
    });
    return () => { disposed = true; stop?.(); };
  }, []);
}
