// tl1402 harness: the versions panel's synthesis feedback + the consolidated
// reload's controller half.
//
// 用户实测（20261105）：点「合成最长链」后没有任何弹窗反馈（数据其实已落
// 盘），打开着的对话也停在打开时的旧快照——读作「丢了历史」。两条缺陷面：
//  ① 成功/失败没有 toast（反馈不对称：outcome 行只有展开面板才可见）；
//  ② 已打开该会话的 tab 不重载转录投影（未订阅合成完成事件）。
//
// 本 harness 钉两半：
//  Part A（toast 对称，面板面）：合成成功弹「已合成 N 条入主线」info toast；
//    no-op / skipped / 后端报错三种失败路径对称弹 error toast——反馈不再
//    随面板关闭而消失。订阅半（session:consolidated 事件→callback）由
//    tl1401-consolidated-reload.test.tsx 钉；Go 端 emit 由 desktop Go 测试钉。
//  Part B（reload 的 controller 半）：reloadTranscriptsForConsolidatedSession
//    对 sessionPath 匹配的已打开 tab 重新拉取历史（reset 语义），不匹配的
//    tab 不动——这是「打开中的 tab 即时看到合成后内容」的执行端。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, HeadSynthesisReport } from "../lib/bridge";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.HTMLInputElement = dom.window.HTMLInputElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

// ─────────────────────────────────────────────────────────────────────────────
// Part A: synthesis toasts (panel surface).

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");

const MAIN_PATH = "/sessions/gc.jsonl";

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [],
};

const CHAINS = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  chains: [
    {
      path: MAIN_PATH,
      headId: "",
      label: "GC",
      isMain: true,
      selected: true,
      covered: false,
      messageCount: 12,
      turns: 6,
      lastActivity: "2026-10-21T00:33:00Z",
      bytes: 1024,
      preview: "hello",
    },
    {
      path: MAIN_PATH,
      headId: "deadbeefdeadbeef",
      label: "GC fork",
      isMain: true,
      selected: false,
      covered: false,
      messageCount: 8,
      turns: 4,
      lastActivity: "2026-10-20T00:00:00Z",
      bytes: 512,
      preview: "forked tail",
    },
  ],
  longestPath: MAIN_PATH,
  longestHead: "deadbeefdeadbeef",
  newestPath: MAIN_PATH,
  newestHead: "deadbeefdeadbeef",
};

function installDom(): HTMLElement {
  document.getElementById("root")?.remove();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes(text),
  );
}

function toastTexts(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".toast")).map((t) => t.textContent || "");
}

async function openPanelAndSynthesize(report: HeadSynthesisReport | Error): Promise<ReturnType<typeof createRoot> | null> {
  const stub = {
    ListRecoveryCopyGroups: async () => [GROUP],
    // tl1415b: CHAINS 描述了双 head 会话,版本清单照实报 headCount=2——否则
    // 该会话按「×0 且单版本」渲染成纯行,heads 子行(与合成链读数)不可达。
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 2, selected: "", heads: [] },
    ],
    ListRecoveryChains: async () => CHAINS,
    ListSessionRepairRecordsFor: async () => [],
    ScanRecoveryCopyGroup: async () => GROUP,
    DeleteRecoveryBranch: async () => {},
    SynthesizeSessionHeads: async (): Promise<HeadSynthesisReport> => {
      if (report instanceof Error) throw report;
      return report;
    },
  };
  window.go = {
    main: {
      App: stub as unknown as AppBindings,
    },
  } as typeof window.go;

  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  // tl1415b: v3-only 会话在已合入组,默认折叠——点组段行展开。
  await act(async () => { (document.querySelectorAll("tr.rc-grouprow")[1] as HTMLElement)?.click(); });
  await flush();
  // tl1415b-2(点2/点3): 会话行默认折叠+合成唯一入口在底部③——展开会话行,
  // 勾选后点底部「以选定 HEAD 合成最长链」,无副本+多 heads 按形态路由到头
  // 合成(确认链不变,仍是「Start synthesis」)。
  const toggles = Array.from(document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle"));
  for (const tgl of toggles) {
    await act(async () => { tgl.click(); });
  }
  await flush();
  await flush();
  const checkbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  await act(async () => { checkbox?.click(); });
  await flush();
  const mergeBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Merge into longest chain"),
  );
  await act(async () => { mergeBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();
  return root;
}

console.log("\nsynthesis feedback toasts (tl1402)");

// A① 成功：合成成功必须有 toast——「已合成 N 条入主线」，N=参与分支并入的
//    独有事件数（用户实测 380→438 的增量正是这个读数）。
{
  const report: HeadSynthesisReport = {
    mainPath: MAIN_PATH,
    promoted: true,
    prefixed: 2,
    trashed: [`${MAIN_PATH}#deadbeef`],
    items: [{ path: MAIN_PATH, unique: 4, shared: 8, uniqueToHead: 0, archived: true }],
    afterMessages: 16,
    afterTurns: 8,
    skipped: [],
  };
  const root = await openPanelAndSynthesize(report);
  const toasts = toastTexts();
  ok(toasts.some((t) => t.includes("Merged 4 entries into the main transcript")), "A① a successful synthesis toasts '已合成 N 条入主线' with the folded count");
  ok((document.body.textContent || "").includes("Synthesized"), "A① the outcome banner still carries the synthesis reading");
  await act(async () => root?.unmount());
}

// A② no-op 失败对称：items 为空（引擎无事可做）按失败处理，error toast 与
//    outcome 行同文案。
{
  const report: HeadSynthesisReport = {
    mainPath: MAIN_PATH,
    promoted: false,
    trashed: [],
    items: [],
    afterMessages: 12,
    afterTurns: 6,
    skipped: [],
  };
  const root = await openPanelAndSynthesize(report);
  ok(toastTexts().some((t) => t.includes("only one chain; there is nothing to synthesize")), "A② the no-op refusal toasts the same localized reason as its outcome row");
  await act(async () => root?.unmount());
}

// A③ skipped 失败对称：有副本参与但全部被跳过——error toast 透传 skipped
//    明细（1039 先例：后端原文直出供诊断）。
{
  const report: HeadSynthesisReport = {
    mainPath: MAIN_PATH,
    promoted: false,
    trashed: [],
    items: [{ path: "/sessions/gc-recovery-copy.jsonl", unique: 4, shared: 8, uniqueToHead: 0, archived: false }],
    afterMessages: 12,
    afterTurns: 6,
    skipped: ["/sessions/gc-recovery-copy.jsonl"],
  };
  const root = await openPanelAndSynthesize(report);
  ok(toastTexts().some((t) => t.includes("/sessions/gc-recovery-copy.jsonl")), "A③ the stalled run toasts the skipped detail verbatim");
  await act(async () => root?.unmount());
}

// A④ 后端报错对称：throw 的错误也弹 error toast（已知错误命中三语文案）。
{
  const root = await openPanelAndSynthesize(new Error("session lease held elsewhere"));
  ok(toastTexts().some((t) => t.includes("session lease held elsewhere")), "A④ a backend error toasts its message (failure is never silent)");
  await act(async () => root?.unmount());
}

// ─────────────────────────────────────────────────────────────────────────────
// Part B: the reload's controller half — matched tabs refetch history.

const { useController } = await import("../lib/useController");
const { historySliceFromMessages } = await import("./mockHistorySlice");
import type { HistorySliceRequest, TabMeta } from "../lib/types";

const TAB_A = "tab-consolidated-a";
const TAB_B = "tab-consolidated-b";
const SESSION_A = "/sessions/consolidated-a.jsonl";
const SESSION_B = "/sessions/consolidated-b.jsonl";

function tabMeta(id: string, sessionPath: string): TabMeta {
  return {
    id,
    scope: "global",
    workspaceRoot: "",
    workspaceName: id,
    workspacePath: "",
    gitBranch: "",
    topicId: `topic-${id}`,
    topicTitle: id,
    sessionPath,
    label: `model-${id}`,
    ready: true,
    running: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: false,
    cwd: "",
  };
}

const sliceCalls: string[] = [];
const tabsById = new Map([
  [TAB_A, tabMeta(TAB_A, SESSION_A)],
  [TAB_B, tabMeta(TAB_B, SESSION_B)],
]);

window.runtime = {
  EventsOn: () => () => {},
  BrowserOpenURL: () => {},
} as typeof window.runtime;
window.go = {
  main: {
    App: {
      RegisterNavigationIntent: async () => ({}),
      ListTabs: async () => Array.from(tabsById.values()).map((tab) => ({ ...tab, active: tab.id === TAB_A })),
      MetaForTab: async (tabID: string) => {
        const tab = tabsById.get(tabID)!;
        return {
          label: tab.label,
          ready: tab.ready,
          eventChannel: "agent:event",
          cwd: "",
          workspaceRoot: "",
          workspaceName: tab.id,
          workspacePath: "",
          sessionPath: tab.sessionPath,
          gitBranch: "",
          autoApproveTools: false,
          bypass: false,
          collaborationMode: "normal",
          toolApprovalMode: "ask",
          tokenMode: "full",
          goal: "",
          goalStatus: "stopped",
        };
      },
      ContextUsageForTab: async () => ({ used: 1, window: 100, sessionTokens: 1 }),
      EffortForTab: async () => ({ supported: true, current: "auto", default: "auto", levels: ["auto"] }),
      BalanceForTab: async () => ({ available: false, display: "" }),
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
      HistorySliceForTab: async (tabID: string, req: HistorySliceRequest) => {
        sliceCalls.push(tabID);
        return historySliceFromMessages(tabID, [{ role: "user", content: `history ${tabID}` }], req);
      },
    } as Partial<AppBindings> as AppBindings,
  },
} as typeof window.go;

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;

function Probe() {
  controller = useController();
  return null;
}

const partBRoot = createRoot(installDom());
await act(async () => {
  partBRoot.render(<Probe />);
  await new Promise((r) => setTimeout(r, 0));
});
for (let attempt = 0; attempt < 30; attempt += 1) {
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  if (controller?.activeTabId === TAB_A) break;
}

const sliceCallsBefore = sliceCalls.length;
await act(async () => {
  await controller?.reloadTranscriptsForConsolidatedSession(SESSION_A);
});
const callsAfterReload = sliceCalls.slice(sliceCallsBefore);
ok(callsAfterReload.includes(TAB_A), "B① the matched tab's history is refetched after the consolidated event");
ok(!callsAfterReload.includes(TAB_B), "B② an open tab on another session is left untouched");

// The reload is a full reset: a second consolidated event reloads again (no
// revision-based veto swallowing the refresh).
await act(async () => {
  await controller?.reloadTranscriptsForConsolidatedSession(SESSION_A);
});
const secondReload = sliceCalls.slice(sliceCallsBefore + callsAfterReload.length);
ok(secondReload.includes(TAB_A), "B③ a second consolidated event reloads again (reset semantics, no cache veto)");

await act(async () => { partBRoot.unmount(); });

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
