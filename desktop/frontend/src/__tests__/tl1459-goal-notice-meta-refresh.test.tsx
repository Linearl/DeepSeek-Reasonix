// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1459-goal-notice-meta-refresh.test.tsx
//
// tl1459: the 2026-11-06 early-stop desync. The backend stopped the Goal after
// the turn's turn_done (a durability failure cancelled the turn), the frontend
// had already refreshed its meta on turn_done, and nothing told it to look
// again — the Goal control kept showing "on" for a stopped FSM. A goal_*
// notice now makes the controller re-read MetaForTab, so the composer profile
// drops the Goal in the same breath as the notice lands. Non-goal notices stay
// free of the extra fetch.

import { JSDOM } from "jsdom";
// jsdom globals must exist before react-dom is imported.
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
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { useController } = await import("../lib/useController");
const { composerProfileFromMeta } = await import("../lib/composerProfile");
import type { AppBindings } from "../lib/bridge";
import type { ContextInfo, EffortInfo, GoalStatus, HistorySliceRequest, Meta, TabMeta, WireEvent } from "../lib/types";
const { historySliceFromMessages } = await import("./mockHistorySlice");

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
  return new Promise((resolvePromise) => setTimeout(resolvePromise, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

const GOAL = "finish the constitution";
let backendGoalStatus: GoalStatus = "running";
let metaCalls = 0;

function tabMeta(): TabMeta {
  return {
    id: "tab-a",
    scope: "project",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    topicId: "topic-a",
    topicTitle: "General",
    sessionPath: "/repo/sessions/tab-a.jsonl",
    label: "model",
    ready: true,
    running: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
    goal: GOAL,
    goalStatus: backendGoalStatus,
    collaborationMode: "goal",
  };
}

function metaForTab(): Meta {
  return {
    label: "model",
    ready: true,
    eventChannel: "agent:event",
    cwd: "/repo",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    autoApproveTools: false,
    bypass: false,
    collaborationMode: "goal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    goal: GOAL,
    goalStatus: backendGoalStatus,
  };
}

const context: ContextInfo = { used: 0, window: 100, sessionTokens: 0 };
const effortInfo: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };
const eventHandlers: Array<(e: WireEvent) => void> = [];

window.runtime = {
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    if (name === "agent:event") eventHandlers.push(cb as (e: WireEvent) => void);
    return () => {};
  },
  BrowserOpenURL: () => {},
};
window.go = {
  main: {
    App: {
      RegisterNavigationIntent: async () => {},
      ListTabs: async () => [tabMeta()],
      MetaForTab: async () => {
        metaCalls += 1;
        return metaForTab();
      },
      ContextUsageForTab: async () => context,
      EffortForTab: async () => effortInfo,
      BalanceForTab: async () => ({ available: false, display: "" }),
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      HistoryForTab: async () => [],
      HistoryPageForTab: async () => ({ messages: [], startTurn: 0, endTurn: 0, totalTurns: 0, hasOlder: false }),
      HistorySliceForTab: async (tabId: string, req: HistorySliceRequest) => historySliceFromMessages(tabId, [], req),
      HistoryCheckpointTurnsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
      SetActiveTab: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;
function Probe() {
  controller = useController();
  return null;
}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);
await act(async () => {
  root.render(<Probe />);
  await flushPromises();
});
await waitFor("active tab", () => controller?.activeTabId === "tab-a");
await act(async () => {
  for (const handler of eventHandlers) handler({ kind: "turn_done", tabId: "tab-a" } as WireEvent);
  await flushPromises();
});
await waitFor("running goal in meta", () => controller?.state.meta?.goalStatus === "running");
ok(composerProfileFromMeta(controller?.state.meta).goal === GOAL, "before the stop the Goal control shows the Goal as on");

// The backend stops the Goal after turn_done; only the goal_* notice follows.
backendGoalStatus = "stopped";
const callsBeforeNotice = metaCalls;
await act(async () => {
  for (const handler of eventHandlers) {
    handler({
      kind: "notice",
      tabId: "tab-a",
      level: "warn",
      code: "goal_stopped",
      text: "goal stopped: the turn was cancelled because a durable save failed: checkpoint conflict — turns 4",
      detail: "cause=durability_failure status=stopped reason=checkpoint conflict",
    } as WireEvent);
  }
  await flushPromises();
});
await waitFor("meta refresh after goal_stopped", () => controller?.state.meta?.goalStatus === "stopped");
ok(metaCalls > callsBeforeNotice, `goal_stopped re-read MetaForTab (${callsBeforeNotice} → ${metaCalls})`);
ok(composerProfileFromMeta(controller?.state.meta).goal === "", "after the notice the Goal control no longer shows the Goal as on");
const notice = controller?.state.items.find((item) => item.kind === "notice" && item.code === "goal_stopped");
ok(notice?.kind === "notice" && notice.text.startsWith("Goal stopped: saving the conversation failed"), "the notice renders the localized human half");
ok(notice?.kind === "notice" && notice.detail === "cause=durability_failure status=stopped reason=checkpoint conflict", "the notice keeps the technical detail");

// A non-goal notice does not cost a MetaForTab fetch.
const callsBeforePlain = metaCalls;
await act(async () => {
  for (const handler of eventHandlers) {
    handler({ kind: "notice", tabId: "tab-a", level: "warn", code: "session_recovery_adopted", text: "session changed on disk; adopted the newer transcript" } as WireEvent);
  }
  await flushPromises();
  await flushPromises();
});
ok(metaCalls === callsBeforePlain, "a non-goal notice does not re-read MetaForTab");

await act(async () => {
  root.unmount();
});
dom.window.close();

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
