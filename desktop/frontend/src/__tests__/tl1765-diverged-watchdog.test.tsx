// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1765-diverged-watchdog.test.tsx
//
// tl1765（20261122 GC专员截图现场）：turn_done 断链后本地 reducer 卡 running，
// 而 runtime 投影（runtimeStateSync 通道，引擎态）已经翻到 idle——三态分叉。
// 旧守卫 `tabId: runtimeState.known ? undefined : activeTabId` 在投影 known
// 时整个禁用 stale-turn watchdog，但投影通道只更新 runtimeStateStore、从不
// dispatch 各 tab reducer 的 backend_status，「接管」不覆盖本地 running 翻面，
// 分叉永不收敛（用户切回 tab 看到「正在工作」假象 + 停止按钮被投影否决锁死，
// 修一已解锁出口；本判例钉收敛通路）。契约：
// ① 投影 known+idle、本地 running（分叉）→ watchdog 保持武装并收敛（missed
//    turn-done reset 生效，running 翻 false）；
// ② 投影 known+running（一致忙）→ watchdog 维持旧禁用（无双通道重复对账）；
// ③ 投影 known+idle、本地 idle（一致闲）→ watchdog 维持旧禁用（无空转）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";
import type { HistoryMessage, TabMeta, WireEvent } from "../lib/types";
import { runtimeStateStore, type RuntimeProjection, type RuntimeState } from "../lib/runtimeStateStore";
import { useController } from "../lib/useController";
import { historySliceFromMessages } from "./mockHistorySlice";

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

function flushPromises(): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(async () => { await flushPromises(); });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

const originalNow = Date.now;
let fakeNow = 1_000_000;

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Date.now = () => fakeNow;
  return dom;
}

// The watchdog arms through window.setTimeout with the 30s quiet delay; trap
// those so each tick can be fired deterministically (same shape as the
// stale-turn-watchdog precedent).
function installTimers(dom: JSDOM) {
  let nextTimerID = 1;
  const watchdogTimers = new Map<number, () => void>();
  window.setTimeout = ((handler: TimerHandler, delay?: number) => {
    const id = nextTimerID++;
    if ((delay ?? 0) >= 29_000 && typeof handler === "function") watchdogTimers.set(id, handler);
    else globalThis.setTimeout(handler, delay);
    return id;
  }) as typeof window.setTimeout;
  window.clearTimeout = ((id?: number) => {
    if (id !== undefined) watchdogTimers.delete(id);
  }) as typeof window.clearTimeout;
  return { watchdogTimers };
}

function projectionFor(tabId: string, state: Partial<RuntimeState>): RuntimeProjection {
  const runtime: RuntimeState = {
    schemaVersion: 1, runtimeEpoch: "epoch-tl1765", revision: 1, phase: "idle", running: false,
    turnId: "", turnStatus: "", turnEventSeq: 0, pendingPrompt: false,
    cancelRequested: false, cancellable: false, backgroundJobs: 0, activity: "", ...state,
  };
  return {
    epoch: "app-tl1765", revision: 3, topics: [],
    sessions: [{ tabId, scope: "project", workspaceRoot: "/repo", topicId: "topic-tl1765",
      sessionPath: "/repo/sessions/diverged.jsonl", sessionGeneration: 1, open: true,
      remote: false, freshness: "synced", state: runtime }],
  };
}

async function runScenario(label: string, projectionState: Partial<RuntimeState>, sendFirst: boolean) {
  console.log(`\n${label}`);
  const dom = installDom();
  const { watchdogTimers } = installTimers(dom);

  const tabID = "tab-tl1765";
  const sessionPath = "/repo/sessions/diverged.jsonl";
  let backendRunning = false;
  let revision = 1;
  let history: HistoryMessage[] = [];
  const agentEventHandlers: Array<(event: WireEvent) => void> = [];

  function tabMeta(): TabMeta {
    return {
      id: tabID, scope: "project", workspaceRoot: "/repo", workspaceName: "repo",
      workspacePath: "/repo", topicId: "topic-tl1765", topicTitle: "GC专员",
      sessionPath, sessionRevision: revision, sessionDigest: `digest-${revision}`,
      label: "model", ready: true, running: backendRunning, cancellable: backendRunning,
      mode: "normal", toolApprovalMode: "ask", tokenMode: "full", active: true, cwd: "/repo",
    };
  }

  window.runtime = {
    EventsOn: (name, handler) => {
      if (name === "agent:event") agentEventHandlers.push(handler as (event: WireEvent) => void);
      return () => {};
    },
    BrowserOpenURL: () => {},
  };
  window.go = {
    main: {
      App: {
        ListTabs: async () => [tabMeta()],
        MetaForTab: async () => ({
          label: "model", ready: true, eventChannel: "agent:event", sessionPath,
          sessionRevision: revision, sessionDigest: `digest-${revision}`, cwd: "/repo",
          workspaceRoot: "/repo", workspaceName: "repo", workspacePath: "/repo",
          autoApproveTools: false, bypass: false, collaborationMode: "normal",
          toolApprovalMode: "ask", tokenMode: "full", goal: "", goalStatus: "stopped",
        }),
        ContextUsageForTab: async () => ({ used: 0, window: 1000, sessionTokens: 0 }),
        EffortForTab: async () => ({ supported: true, current: "auto", default: "auto", levels: ["auto"] }),
        BalanceForTab: async () => ({ available: false, display: "" }),
        JobsForTab: async () => [],
        CheckpointsForTab: async () => [],
        HistorySliceForTab: async (_id: string, request: Parameters<typeof historySliceFromMessages>[3]) =>
          historySliceFromMessages(tabID, history, request, { revision, digest: `digest-${revision}` }),
        HistoryCheckpointTurnsForTab: async () => [],
        ReplayPendingPrompts: async () => {},
        SubmitToTabWithID: async () => { backendRunning = true; },
      } as Partial<AppBindings> as AppBindings,
    },
  };

  type Controller = ReturnType<typeof useController>;
  let controller: Controller | undefined;
  function Probe() {
    controller = useController();
    return null;
  }
  const rootElement = document.getElementById("root");
  if (!rootElement) throw new Error("missing root");
  const root = createRoot(rootElement);
  await act(async () => {
    root.render(<Probe />);
    await flushPromises();
  });
  await waitFor("initial session", () => controller?.activeTabId === tabID);

  // Seed the projection channel BEFORE the divergence so the hook's first
  // render already sees runtimeState.known=true (the legacy disable shape).
  runtimeStateStore.commit(projectionFor(tabID, projectionState));
  runtimeStateStore.flush();

  if (sendFirst) {
    // Optimistic submit: local reducer enters running with zero agent events —
    // the canonical lost-turn stream shape (20261122 field: turn_done lost, so
    // no live evidence ever arrives after the last turn activity).
    fakeNow += 60_000;
    await act(async () => {
      await controller?.send("hello");
      await flushPromises();
    });
    ok(controller?.state.running ?? false, "local reducer is running after the optimistic submit");
    ok(watchdogTimers.size >= 1, `watchdog stays armed while diverged (projection ${projectionState.running ? "running" : "idle"}, local running)`);
    // Fire the 30s quiet probe: reconcileTabRuntime runs with hydrateSessionData
    // on, the engine-side idle snapshot flows through backend_status, and the
    // task-874 missed-turn-done reset settles the local face. The engine went
    // idle behind the scenes (the lost-turn_done field shape) — flip the
    // backend mock before the probe, same as the stale-turn-watchdog precedent.
    backendRunning = false;
    const timer = watchdogTimers.entries().next().value as [number, () => void] | undefined;
    if (timer) {
      watchdogTimers.delete(timer[0]);
      fakeNow += 30_000;
      await act(async () => {
        timer[1]();
        await flushPromises();
      });
    }
    await waitFor("convergence", () => !(controller?.state.running ?? true));
    ok(controller?.state.running === false, "diverged local running converges to idle after one watchdog probe");
  } else {
    await act(async () => { await flushPromises(); });
    ok(watchdogTimers.size === 0, `watchdog stays disabled when channels agree (projection ${projectionState.running ? "running" : "idle"})`);
  }

  await act(async () => { root.unmount(); });
  Date.now = originalNow;
  dom.window.close();
}

// ① 分叉（投影 idle + 本地 running）：watchdog 保持武装并收敛。
await runScenario("diverged: projection idle while local reducer runs", { running: false, phase: "idle" }, true);

// ② 一致忙（投影 running + 本地 running）：旧禁用维持（无双通道重复对账）。
await runScenario("aligned busy: projection running matches local running", { running: true, phase: "executing", cancellable: true }, false);

// ③ 一致闲（投影 idle + 本地 idle）：旧禁用维持（无空转）。
await runScenario("aligned idle: projection idle matches local idle", { running: false, phase: "idle" }, false);

console.log(`\ntl1765 diverged watchdog: ${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
