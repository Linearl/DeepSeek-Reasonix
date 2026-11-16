// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/recovery-delete-feedback-tl1647.test.tsx
//
// tl1647: 版本面板删除反馈状态混乱修复（2026-11-16 用户实测三重矛盾：①同一
// 路径先删成功后报「not a recovery copy」——根因=幽灵行（ListRecoveryChains
// 对已删主线产「文件消失」降级行，Go 侧已根除，钉在 recovery_chains_test.go
// TestListRecoveryChainsDropsVanishedMainRow）；②底部状态区成功/失败/生效提
// 示并存；③错误原因 toast 与面板行重复渲染两份）。本 harness 钉 FE 面：
//  ① 单一权威反馈：批量删除部分成功时只弹**一条**部分成功 toast（成功+失败
//     两条并存退役），失败原因全文只在面板 outcome 明细行一处；
//  ② 本轮清场：上一轮删除的 outcome 行不与本轮并存（旧形态「上轮已删除行+
//     本轮失败行+删除已生效提示」同屏即用户现场）；
//  ③ 生效提示判定：「删除已生效」只在真的有删除成功时渲染；全失败轮不再配
//     成功文案；文案不再让用户「重新打开/刷新标签页」（面板删除后已自动重
//     取列表，该下一步过时）；
//  ④ 入口同源（渲染层防线）：主线文件自身的文件形行/降级占位行不再渲染删
//     除钮——点了才被 refuseMainDelete 弹错的坑在入口层消失（与批量/单删的
//     函数级闸同一判定式）；采纳后降级的旧主线行（后端 demoted 通道认的形
//     态，tl1415h）删除入口保持可用，零回退。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, RecoveryChainSet } from "../lib/bridge";

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

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else failed += 1;
}

function installDom(): HTMLElement {
  document.getElementById("root")?.remove();
  window.localStorage.clear();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

async function flush() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().includes(text));
}

function bodyText(): string {
  return document.body.textContent || "";
}

function toastText(): string {
  const top = document.querySelector(".toast-container")?.textContent ?? "";
  const corner = document.querySelector("#rt-notice-stack")?.textContent ?? "";
  return top + corner;
}

const MAIN_PATH = "/sessions/fb.jsonl";
const COPY_PATH = "/sessions/fb-recovery-0123456789abcdef.jsonl";
const COPY_PATH_2 = "/sessions/fb-recovery-fedcba9876543210.jsonl";

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "FB session",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-11-16T00:33:00Z",
  copies: [
    {
      path: COPY_PATH,
      label: "FB copy 1",
      bytes: 2048,
      modified: "2026-11-16T01:00:00Z",
      messages: 3,
      shared: 1,
      unique: 2,
      scanned: true,
      orphan: false,
    },
    {
      path: COPY_PATH_2,
      label: "FB copy 2",
      bytes: 4096,
      modified: "2026-11-16T02:00:00Z",
      messages: 5,
      shared: 2,
      unique: 3,
      scanned: true,
      orphan: false,
    },
  ],
};

const EMPTY_CHAINS: RecoveryChainSet = {
  mainPath: MAIN_PATH,
  mainLabel: "FB session",
  chains: [],
  longestPath: "",
  longestHead: "",
  newestPath: "",
  newestHead: "",
};

type Bridge = {
  branches: (path: string) => Promise<void>;
  listGroups?: () => Promise<unknown[]>;
};

function stubBridge(bridge: Bridge) {
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: bridge.listGroups ?? (async () => [GROUP]),
        ListSessionVersionGroups: async () => [],
        ListRecoveryChains: async () => EMPTY_CHAINS,
        DeleteRecoveryBranch: bridge.branches,
        DeleteSessionVersionHead: async () => {},
      } as Partial<AppBindings> as AppBindings,
    },
  } as typeof window.go;
}

async function openPanel(): Promise<ReturnType<typeof createRoot>> {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  const toggle = document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle");
  if (!toggle) throw new Error("session expand toggle missing");
  await act(async () => { toggle.click(); });
  await flush();
  await flush();
  return root;
}

async function clickCopyDelete(index: number) {
  const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-versions__table .rc-row--copy .rc-delete"));
  const btn = buttons[index];
  if (!btn) throw new Error(`copy delete button ${index} missing`);
  await act(async () => { btn.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  if (!confirmBtn) throw new Error("confirm button missing");
  await act(async () => { confirmBtn.click(); });
  await flush();
  await flush();
}

async function runBatchDelete(checkIndexes: number[], branchImpl: (path: string) => Promise<void>) {
  const boxes = Array.from(document.querySelectorAll<HTMLInputElement>(".rc-versions__table .rc-row--copy input[type=checkbox]"));
  for (const i of checkIndexes) {
    const box = boxes[i];
    if (!box) throw new Error(`copy checkbox ${i} missing`);
    await act(async () => { box.click(); });
    await flush();
  }
  const batchBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().startsWith("Delete selected ("));
  if (!batchBtn) throw new Error("batch delete button missing");
  await act(async () => { batchBtn.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  if (!confirmBtn) throw new Error("batch confirm button missing");
  await act(async () => { confirmBtn.click(); });
  await flush();
  await flush();
}

console.log("\ntl1647 delete feedback (single authoritative receipt)");

// ① 部分成功聚合：批量删 2 条（1 成 1 败）→ 单条部分成功 toast（不再两条并
//    存）；失败原因全文只在面板 outcome 行一处。
{
  const failReason = "this session is in use by another Reasonix window or process — close that window (or quit the other Reasonix instance), then retry";
  stubBridge({
    branches: async (path) => {
      if (path === COPY_PATH_2) throw new Error(failReason);
    },
  });
  const root = await openPanel();
  await runBatchDelete([0, 1], async () => {});
  const text = toastText();
  ok(text.includes("Deleted 1 branch(es); failed to delete 1"), `① the partial-success toast is the single receipt (got ${JSON.stringify(text)})`);
  ok(!text.includes("Deleted 1 branch(es)."), "① no standalone success toast rides along");
  ok(!text.includes("Failed to delete 1 branch(es)"), "① no standalone failure toast rides along");
  ok(!text.includes("in use by another Reasonix window"), "① the toast does not repeat the backend reason (错误单处)");
  ok(bodyText().includes("in use by another Reasonix window"), "① the outcome row keeps the reason text (明细在面板)");
  ok(bodyText().includes("Deleted"), "① the deleted outcome line renders");
  await act(async () => { root.unmount(); });
}

// ② 本轮清场：先删失败（面板留失败明细行）→ 再删成功 → 上一轮失败行不再与
//    本轮并存。
{
  const staleReason = "stale failure from the previous round";
  let calls = 0;
  stubBridge({
    branches: async () => {
      calls += 1;
      if (calls === 1) throw new Error(staleReason);
    },
  });
  const root = await openPanel();
  await clickCopyDelete(0);
  ok(bodyText().includes(staleReason), "② the first (failed) round renders its reason");
  await clickCopyDelete(1);
  ok(!bodyText().includes(staleReason), "② the previous round's failure row is cleared before this round renders (本轮清场)");
  ok(bodyText().includes("Deleted"), "② this round's deleted line renders");
  await act(async () => { root.unmount(); });
}

// ③ 生效提示判定：失败轮不渲染「删除已生效」；成功轮渲染新文案（不含过时
//    的「重新打开会话/刷新标签页」下一步），并清掉上一轮的失败行。
{
  let calls = 0;
  stubBridge({
    branches: async () => {
      calls += 1;
      if (calls === 1) throw new Error("round one refused");
    },
  });
  const root = await openPanel();
  await clickCopyDelete(0);
  ok(!bodyText().includes("deletion has taken effect"), "③ a failed round renders no success hint");
  await clickCopyDelete(1);
  ok(bodyText().includes("the list has refreshed"), "③ a successful round renders the refreshed-list hint");
  ok(!bodyText().includes("Reopen the conversation"), "③ the stale 'reopen the conversation' next step is gone (下一步已由自动刷新取代)");
  ok(!bodyText().includes("round one refused"), "③ the failed first round's rows are cleared by the second round");
  await act(async () => { root.unmount(); });
}

// ④ 入口同源（渲染层防线）：链清单缺失/降级占位形态下，主线文件自身的行不
//    渲染删除钮。构造=authority 落在副本（Selected 副本链）+主线文件行无
//    selected——采纳后降级的旧主线形态，tl1415h 后端放行它的删除；而主线
//    指针命中的文件（currentMainChain.path）永远不该有删除钮。
{
  stubBridge({
    branches: async () => {},
    listGroups: async () => [GROUP],
  });
  // 直接以 chains 返回主线行（selected=false）+副本行（selected=true）构造
  // 「副本被采纳为主线」形态：主线行落版本区（fileShaped），其 path==扫描
  // 起点==preferredMainPathOf 回落值仅当 currentMainChain 缺席——这里
  // currentMainChain=副本行，主线行 path≠currentMainChain.path，闸放行
  // （tl1415f 零回退：demoted 行保留删除入口）。
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: async () => [GROUP],
        ListSessionVersionGroups: async () => [],
        ListRecoveryChains: async (): Promise<RecoveryChainSet> => ({
          mainPath: MAIN_PATH,
          mainLabel: "FB session",
          chains: [
            {
              path: MAIN_PATH, headId: "", label: "FB main", isMain: true, selected: false,
              covered: false, messageCount: 12, turns: 3, lastActivity: "2026-11-16T00:33:00Z",
              bytes: 1024, preview: "tail", degraded: false,
            },
            {
              path: COPY_PATH, headId: "", label: "FB copy 1", isMain: false, selected: true,
              covered: false, messageCount: 30, turns: 5, lastActivity: "2026-11-16T01:00:00Z",
              bytes: 2048, preview: "tail", degraded: false,
            },
          ],
          longestPath: COPY_PATH, longestHead: "", newestPath: COPY_PATH, newestHead: "",
        }),
        DeleteRecoveryBranch: async () => {},
        DeleteSessionVersionHead: async () => {},
      } as Partial<AppBindings> as AppBindings,
    },
  } as typeof window.go;
  const root = await openPanel();
  // 副本 1（Selected=当前主线文件）的行被主线行升格收走（同 path 去重）；
  // 版本区文件形行=主线文件行（demoted）。删除入口必须在（tl1415f 零回退）。
  const demotedDelete = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-chain .rc-delete"));
  ok(demotedDelete.length === 1, `④ the demoted mainline row keeps exactly one delete entry (tl1415f 零回退, got ${demotedDelete.length})`);
  await act(async () => { root.unmount(); });
}

// ④b 占位形态：链枚举整体被拒（synthetic 占位行 path=扫描起点）时，占位行
//     不渲染删除钮（点了才弹 refuseMainDelete 的坑在入口层消失）。
{
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: async () => [GROUP],
        ListSessionVersionGroups: async () => [],
        ListRecoveryChains: async (): Promise<RecoveryChainSet> => {
          throw new Error("enumeration refused");
        },
        DeleteRecoveryBranch: async () => {},
        DeleteSessionVersionHead: async () => {},
      } as Partial<AppBindings> as AppBindings,
    },
  } as typeof window.go;
  const root = await openPanel();
  const placeholderDelete = document.querySelector<HTMLButtonElement>("tr.rc-chain .rc-delete");
  ok(!placeholderDelete, "④b the synthetic placeholder row renders no delete button (入口同源)");
  await act(async () => { root.unmount(); });
}

console.log(`tl1647 delete-feedback: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
dom.window.close();
