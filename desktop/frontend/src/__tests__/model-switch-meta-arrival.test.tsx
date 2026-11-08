// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/model-switch-meta-arrival.test.tsx
//
// tl1389 验收钉（修法本体已在主线 04a488036 合并：后端快路径/重建路径落定推
// tab:meta，前端 onTabMeta 即时 dispatch meta）。本文件补齐任务书要求的两处
// 显示钉，锁住回归面：
//   1. modelLabel 先行更新钉——tab:meta 推送（快路径落定的形状）让
//      state.meta.label 立即更新，全程零新增 ContextUsageForTab /
//      EffortForTab 往返：模型名不再排在 effort 链后面（修前拉取链滞后）。
//   2. effort 显示链独立钉——切换落定的刷新链保持拆链形状：meta 先行
//      dispatch（context/effort 两个往返还在途时 label 已落），effort 档位
//      由自己的往返送达（换 provider 折算后的档位跟上，不被 modelLabel
//      等待、也不阻塞 modelLabel）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";
import { useController } from "../lib/useController";
import { historySliceFromMessages } from "./mockHistorySlice";
import type {
  BalanceInfo,
  ContextInfo,
  EffortInfo,
  Meta,
  TabMeta,
  TabMetaRefreshEvent,
} from "../lib/types";

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

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function flushPromises(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

async function settleUntil(predicate: () => boolean): Promise<boolean> {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return true;
  }
  return false;
}

const tabId = "tab-meta-arrival";
let backendModel = "provider-a/model-a";
let backendEffort: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto", "high"] };
const context: ContextInfo = {
  used: 100,
  window: 1_000,
  sessionTokens: 100,
  cacheHitTokens: 0,
  cacheMissTokens: 100,
};
let contextCalls = 0;
let effortCalls = 0;
let contextLoader: (() => Promise<ContextInfo>) | undefined;
let effortLoader: (() => Promise<EffortInfo>) | undefined;
type ModelSwitchStep = {
  gate: ReturnType<typeof deferred<void>>;
  error?: Error;
};
const modelSwitchSteps: ModelSwitchStep[] = [];
let modelSwitchCalls = 0;

function tabMeta(): TabMeta {
  return {
    id: tabId,
    scope: "project",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    topicId: "topic-meta-arrival",
    topicTitle: "Meta arrival",
    label: backendModel,
    ready: true,
    running: false,
    cancellable: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
  };
}

// 与后端推送形状一致：MetaForTab/notifyTabMetaRefresh 都不带 sessionPath
//（换 session 的推送走 tl1403 的 move 分支，不在本钉范围）。
function metaFor(label: string): Meta {
  return {
    label,
    ready: true,
    eventChannel: "agent:event",
    cwd: "/repo",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    autoApproveTools: false,
    bypass: false,
    collaborationMode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    goal: "",
    goalStatus: "stopped",
  };
}

console.log("\nmodel switch meta arrival");

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

const tabMetaHandlers: Array<(e: TabMetaRefreshEvent) => void> = [];
window.runtime = {
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    if (name === "tab:meta") tabMetaHandlers.push(cb as (e: TabMetaRefreshEvent) => void);
    return () => {};
  },
  BrowserOpenURL: () => {},
};
window.go = {
  main: {
    App: {
      RegisterNavigationIntent: async () => {},
      ListTabs: async () => [tabMeta()],
      MetaForTab: async () => metaFor(backendModel),
      ContextUsageForTab: async () => {
        contextCalls += 1;
        return contextLoader ? contextLoader() : context;
      },
      EffortForTab: async () => {
        effortCalls += 1;
        return effortLoader ? effortLoader() : backendEffort;
      },
      BalanceForTab: async (): Promise<BalanceInfo> => ({ available: false, display: "" }),
      SetModelForTab: async (_tabId: string, name: string) => {
        modelSwitchCalls += 1;
        const step = modelSwitchSteps.shift();
        if (!step) throw new Error("missing model switch step");
        await step.gate.promise;
        if (step.error) throw step.error;
        backendModel = name;
      },
      CloseTab: async () => {
        throw new Error("cannot close tab");
      },
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      HistoryForTab: async () => [],
      HistoryPageForTab: async () => ({ messages: [], startTurn: 0, endTurn: 0, totalTurns: 0, hasOlder: false }),
      HistorySliceForTab: async (tabID: string, req: Parameters<typeof historySliceFromMessages>[2]) =>
        historySliceFromMessages(tabID, [], req),
      HistoryCheckpointTurnsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
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
ok(
  await settleUntil(
    () => controller?.activeTabId === tabId && controller.state.meta?.label === "provider-a/model-a" && controller.state.effort?.current === "auto",
  ),
  "initial surface settles with the outgoing label and effort level",
);

// ── 钉 1：tab:meta 推送让 modelLabel 先行更新（零 Context/Effort 往返）───────
{
  const ctxBefore = contextCalls;
  const effBefore = effortCalls;
  await act(async () => {
    for (const handler of tabMetaHandlers) handler({ tabId, meta: metaFor("provider-b/model-b") });
    await flushPromises();
  });
  eq(controller?.state.meta?.label, "provider-b/model-b", "tab:meta push lands the new model label (composer's modelLabel source)");
  eq(contextCalls, ctxBefore, "the label arrival triggered no ContextUsageForTab round trip");
  eq(effortCalls, effBefore, "the label arrival triggered no EffortForTab round trip (not gated behind the effort chain)");
  eq(controller?.state.effort?.current, "auto", "effort display state is untouched by the label push (independent chains)");
}

// ── 钉 2：切换落定的刷新链拆链形状——meta 先行，effort 独立落定 ──────────────
{
  backendModel = "provider-c/model-c"; // MetaForTab 现在返回切换后的身份
  const effortAfterSwitch: EffortInfo = { supported: true, current: "high", default: "auto", levels: ["auto", "high"] };
  const contextGate = deferred<ContextInfo>();
  const effortGate = deferred<EffortInfo>();
  contextLoader = () => contextGate.promise;
  effortLoader = () => effortGate.promise;
  const effBefore = effortCalls;

  const switchGate = deferred<void>();
  modelSwitchSteps.push({ gate: switchGate });
  let switchPromise: Promise<boolean> | undefined;
  await act(async () => {
    switchPromise = controller?.setModel("provider-c/model-c");
    await flushPromises();
  });
  eq(modelSwitchCalls, 1, "the switch entered the backend once");
  await act(async () => {
    switchGate.resolve();
    await flushPromises();
  });

  ok(
    await settleUntil(() => controller?.state.meta?.label === "provider-c/model-c"),
    "settle chain dispatches the new label while the context/effort round trips are still in flight",
  );
  ok(effortCalls > effBefore, "the settle effort round trip started (folded level is being fetched)");
  eq(controller?.state.effort?.current, "auto", "effort display still shows the outgoing level while its round trip is in flight");

  // effort 往返先就绪、context 后就绪：Promise.all 齐了之后各自 dispatch，
  // effort 档位独立跟上（此前的 label 断言已钉住「不等这条链」）。
  effortGate.resolve(effortAfterSwitch);
  await act(async () => {
    contextGate.resolve(context);
    await flushPromises();
  });
  ok(
    await settleUntil(() => controller?.state.effort?.current === "high"),
    "effort display lands independently once its own round trip resolves (folded level follows the switch)",
  );
  await act(async () => {
    eq(await switchPromise, true, "the switch owns its UI reconciliation");
  });

  contextLoader = undefined;
  effortLoader = undefined;
}

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
