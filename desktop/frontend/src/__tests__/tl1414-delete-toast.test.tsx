// tl1414 harness：版本面板副本删除的反馈可见性。
//
// 事故形态（20261105 用户实测）：副本行点删除→确认→静默无反应。根因是活
// tab 自己的转录本以副本行出现，后端 busy 拒绝；前端只把失败写进展开面板
// 里才可见的 outcome 行，无 toast——用户读作「什么都没发生」。本 harness
// 钉六点（反馈对称性，tl1401 同口径）：
//  ① 单删成功 → outcome 行「Deleted」+ toast「Deleted 1 branch(es)」+ 列表重拉；
//  ② 单删失败 → toast 带后端原因原文（不再只留展开式行）+ outcome 行在；
//  ③ 批量删除成功 → toast 报成功计数 + 列表重拉；
//  ④（续跑）整对话删除成功 → toast 报「Deleted N conversation(s)」（对话级
//    词汇，不沿用分支计数）+ 列表重拉——该按钮正是目录拼写根因（Go 端
//    recovery_branch_delete_casing_test.go）修复的受益路径；
//  ⑤（续跑）整对话删除失败 → toast 带原因；
//  ⑥（续跑）删除后 load() 重取失败 → toast + 横幅双落——load() 内部 catch
//    后返回布尔（从不抛错），反馈对称性靠返回值补 toast（旧 try/catch 是
//    永不触发的死代码，本条是它生效的证明）。
//
// tl1415 移植适配：三表合并单会话单表后，副本行的删除按钮与复选框都住在
// `tr.rc-detail__ops` 次行（不再嵌在 `.rc-row--copy` 主行里），选择器按合并
// 表口径取（与 1064 harness 同款）；删除动作本身已由 tl1415 抽成具名回调
// deleteBranchOne，本 harness 断言的可见性行为不变。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
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
  // tl1464：info 档 toast 迁右下统一堆叠（#rt-notice-stack），warn/error 档仍
  // 顶部 .toast-container。删除成功走 info 档（迁右下），失败走 warn/error（留
  // 顶部）——两处同读，断言语义不变（只关心文案出现与否）。
  const top = document.querySelector(".toast-container")?.textContent ?? "";
  const corner = document.querySelector("#rt-notice-stack")?.textContent ?? "";
  return top + corner;
}

const MAIN_PATH = "/sessions/gc.jsonl";
const COPY_PATH = "/sessions/gc-recovery-0123456789abcdef.jsonl";
const COPY_PATH_2 = "/sessions/gc-recovery-fedcba9876543210.jsonl";

const COPY = {
  path: COPY_PATH,
  label: "GC copy 1",
  bytes: 2048,
  modified: "2026-11-05T01:00:00Z",
  messages: 3,
  shared: 1,
  unique: 2,
  scanned: true,
  orphan: false,
};

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC dev session",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-11-05T00:33:00Z",
  copies: [
    COPY,
    {
      path: COPY_PATH_2,
      label: "GC copy 2",
      bytes: 4096,
      modified: "2026-11-05T02:00:00Z",
      messages: 5,
      shared: 2,
      unique: 3,
      scanned: true,
      orphan: false,
    },
  ],
};

function stubBridge(
  deleteImpl: (path: string) => Promise<void>,
  spies: { listCalls: number; deleted: string[] },
  conversationImpl?: (path: string) => Promise<{ mainTrashed: boolean; copiesArchived: number }>,
) {
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: async () => {
          spies.listCalls += 1;
          return [GROUP];
        },
        ListSessionVersionGroups: async () => [],
        ListRecoveryChains: async () => ({
          mainPath: MAIN_PATH,
          mainLabel: "GC dev session",
          chains: [],
          longestPath: "",
          longestHead: "",
        }),
        DeleteRecoveryBranch: deleteImpl,
        DeleteSessionVersionHead: async () => {},
        DeleteConversationWithCopies:
          conversationImpl ?? (async () => ({ mainTrashed: true, copiesArchived: 0 })),
      } as Partial<AppBindings> as AppBindings,
    },
  } as typeof window.go;
}

/** Drives the single-copy delete flow: open panel → expand the conversation →
 *  press the copy row's Delete → confirm the danger dialog. */
async function runSingleDeleteFlow(spies: { listCalls: number; deleted: string[] }) {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  // tl1415b-2(点2): 会话行默认折叠——展开钮点开子行(副本行)。基线预存红
  // 顺手清零:旧 .rc-toggle 选择器一轮已不存在,此处当时已崩。
  const toggle = document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle");
  if (!toggle) throw new Error("session expand toggle missing");
  await act(async () => { toggle.click(); });
  await flush();
  await flush();
  const listCallsBefore = spies.listCalls;
  // tl1415 合并表：副本行删除按钮住行内,副本行内第一个 .rc-delete 即
  // 副本 1 的删除（副本行先于候选链行渲染，与 1064 harness 同口径）。
  const deleteBtn = document.querySelector<HTMLButtonElement>(".rc-versions__table .rc-row--copy .rc-delete");
  if (!deleteBtn) throw new Error("copy-row delete button missing");
  await act(async () => { deleteBtn.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  if (!confirmBtn) throw new Error("confirm button missing");
  await act(async () => { confirmBtn.click(); });
  await flush();
  await flush();
  return { root, listCallsBefore };
}

/** Drives the batch delete flow: tick the first copy's checkbox → press
 *  "Delete selected (1)" → confirm. */
async function runBatchDeleteFlow(spies: { listCalls: number; deleted: string[] }) {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  // tl1415b-2(点2): 会话行默认折叠——展开钮点开子行。
  const toggle = document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle");
  if (!toggle) throw new Error("session expand toggle missing");
  await act(async () => { toggle.click(); });
  await flush();
  await flush();
  const listCallsBefore = spies.listCalls;
  // tl1415b-2: 副本复选框按副本行圈定——主线行自本轮起也带复选框(选定 HEAD
  // 语义),泛取表内第一个会误勾主线行。
  const box = document.querySelector<HTMLInputElement>(".rc-versions__table .rc-row--copy input[type=checkbox]");
  if (!box) throw new Error("copy checkbox missing");
  await act(async () => { box.click(); });
  await flush();
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
  return { root, listCallsBefore };
}

console.log("\ntl1414 copy-delete feedback toasts");

// ① 成功案：outcome 行 + toast + 列表重拉。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  stubBridge(async (path) => { spies.deleted.push(path); }, spies);
  const { root, listCallsBefore } = await runSingleDeleteFlow(spies);
  ok(spies.deleted.length === 1 && spies.deleted[0] === COPY_PATH, "① the confirmed delete reaches the backend");
  ok(bodyText().includes("Deleted"), "① the outcome row reports the deletion");
  ok(toastText().includes("Deleted 1 branch(es)"), `① success toast appears (got ${JSON.stringify(toastText())})`);
  ok(spies.listCalls > listCallsBefore, "① the list reloads after the delete");
  await act(async () => { root.unmount(); });
}

// ② 失败案：toast 带后端原因原文（本次事故形态：活 tab 占用被 busy 拒）。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  const busyReason = "this session is in use by another Reasonix window or process — close that window (or quit the other Reasonix instance), then retry";
  stubBridge(async () => { throw new Error(busyReason); }, spies);
  const { root } = await runSingleDeleteFlow(spies);
  ok(spies.deleted.length === 0, "② a refused delete never reaches the archive");
  const text = toastText();
  ok(text.includes("Failed to delete 1 branch(es)"), `② failure toast appears (got ${JSON.stringify(text)})`);
  ok(text.includes("in use by another Reasonix window"), "② the failure toast carries the backend reason");
  ok(bodyText().includes("in use by another Reasonix window"), "② the outcome row keeps the reason too");
  await act(async () => { root.unmount(); });
}

// ③ 批量删除成功案：toast 报成功计数 + 列表重拉。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  stubBridge(async (path) => { spies.deleted.push(path); }, spies);
  const { root, listCallsBefore } = await runBatchDeleteFlow(spies);
  ok(spies.deleted.length === 1 && spies.deleted[0] === COPY_PATH, "③ the confirmed batch delete reaches the backend");
  ok(toastText().includes("Deleted 1 branch(es)"), `③ batch success toast appears (got ${JSON.stringify(toastText())})`);
  ok(spies.listCalls > listCallsBefore, "③ the list reloads after the batch delete");
  await act(async () => { root.unmount(); });
}

// ④（续跑）整对话删除成功案：会话行复选框 → 「Delete selected conversations (1)」
//    → 确认 → toast 报对话级成功计数 + 列表重拉。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  const conversations: string[] = [];
  stubBridge(
    async () => {},
    spies,
    async (path) => {
      conversations.push(path);
      return { mainTrashed: true, copiesArchived: 2 };
    },
  );
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  // tl1415b-2: 整对话删除走会话行勾选(会话行恒可见,无需展开子行);旧
  // .rc-toggle 展开步随选择器退役。
  const listCallsBefore = spies.listCalls;
  const groupBox = document.querySelector<HTMLInputElement>(".rc-row--group input[type=checkbox]");
  if (!groupBox) throw new Error("group checkbox missing");
  await act(async () => { groupBox.click(); });
  await flush();
  const convBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().startsWith("Delete selected conversations"));
  if (!convBtn) throw new Error("conversation delete button missing");
  await act(async () => { convBtn.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete selected conversations");
  if (!confirmBtn) throw new Error("conversation confirm button missing");
  await act(async () => { confirmBtn.click(); });
  await flush();
  await flush();
  ok(conversations.length === 1 && conversations[0] === MAIN_PATH, "④ the confirmed conversation delete reaches the backend");
  ok(toastText().includes("Deleted 1 conversation(s)"), `④ conversation success toast appears (got ${JSON.stringify(toastText())})`);
  ok(spies.listCalls > listCallsBefore, "④ the list reloads after the conversation delete");
  await act(async () => { root.unmount(); });
}

// ⑤（续跑）整对话删除失败案：toast 带后端原因原文。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  const convReason = "recovery branch must be a direct child of its session directory";
  stubBridge(
    async () => {},
    spies,
    async () => { throw new Error(convReason); },
  );
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  // tl1415b-2: 同④——会话行勾选无需展开子行。
  const groupBox = document.querySelector<HTMLInputElement>(".rc-row--group input[type=checkbox]");
  await act(async () => { groupBox?.click(); });
  await flush();
  const convBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().startsWith("Delete selected conversations"));
  await act(async () => { convBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete selected conversations");
  await act(async () => { confirmBtn?.click(); });
  await flush();
  await flush();
  const text = toastText();
  ok(text.includes("Failed to delete 1 conversation(s)"), `⑤ conversation failure toast appears (got ${JSON.stringify(text)})`);
  ok(text.includes("direct child of its session directory"), "⑤ the failure toast carries the backend reason");
  await act(async () => { root.unmount(); });
}

// ⑥（续跑）删除后 load() 重取失败：toast + 横幅双落——load() 从不抛错，
//    反馈对称性靠返回值（旧 try/catch 是永不触发的死代码，本条是它生效的证明）。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: async () => {
          spies.listCalls += 1;
          if (spies.listCalls > 1) throw new Error("bridge unavailable after delete");
          return [GROUP];
        },
        ListSessionVersionGroups: async () => [],
        ListRecoveryChains: async () => ({
          mainPath: MAIN_PATH,
          mainLabel: "GC dev session",
          chains: [],
          longestPath: "",
          longestHead: "",
        }),
        DeleteRecoveryBranch: async (path: string) => { spies.deleted.push(path); },
        DeleteSessionVersionHead: async () => {},
      } as Partial<AppBindings> as AppBindings,
    },
  } as typeof window.go;
  const { root } = await runSingleDeleteFlow(spies);
  ok(spies.listCalls >= 2, "⑥ the post-delete reload was attempted");
  ok(toastText().includes("Settings could not be loaded"), `⑥ the failed reload toasts (got ${JSON.stringify(toastText())})`);
  ok(Boolean(document.querySelector(".banner--error")), "⑥ the failed reload still raises the in-panel banner");
  await act(async () => { root.unmount(); });
}

console.log(`tl1414 delete-toast: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
dom.window.close();
