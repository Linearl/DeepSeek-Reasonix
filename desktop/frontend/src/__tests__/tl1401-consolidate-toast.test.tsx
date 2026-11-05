// tl1401 harness: the versions panel merge now toasts its outcome.
//
// 事故形态（20261105 用户实测）：合成最长链成功落盘，面板里只有展开存储页
// 才看得到的 outcome 行，没有任何弹窗——用户读作「什么都没发生 / 历史丢了」。
// 本 harness 钉两点（反馈对称性）：
//  ① 合并成功 → toast「Merged {count} entries into the main transcript」
//     （count = 合入主线的增量轮数 after-before）；
//  ② 合并失败 → toast「{count} conversation(s) failed to merge」——失败原本
//     同样只有行没有弹窗，与成功静默是同一个问题。

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

function installDom() {
  document.getElementById("root")?.remove();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

async function flush() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

function groupBindings(mergeImpl: () => Promise<unknown>) {
  const copy = {
    path: "/sessions/gc-recovery-0123456789abcdef.jsonl",
    label: "copy 1",
    bytes: 4096,
    modified: "2026-11-05T13:30:00Z",
    messages: 2,
    shared: 1,
    unique: 1,
    scanned: true,
    orphan: false,
  };
  return {
    ListRecoveryCopyGroups: async () => [{
      mainPath: "/sessions/gc.jsonl",
      mainLabel: "GC dev session",
      directory: "/sessions",
      mainExists: true,
      mainMessages: 1,
      mainBytes: 8192,
      mainModified: "2026-11-05T13:00:00Z",
      copies: [copy],
    }],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => ({
      mainPath: "/sessions/gc.jsonl",
      mainLabel: "GC dev session",
      chains: [{
        path: "/sessions/gc.jsonl",
        headId: "",
        label: "GC dev session",
        isMain: true,
        selected: false,
        covered: false,
        messageCount: 1,
        turns: 1,
        lastActivity: "",
        bytes: 8192,
        preview: "",
      }],
      longestPath: copy.path,
      longestHead: "",
    }),
    IsConversationDualOpen: async () => false,
    ConsolidateSessionRecoveryCopies: mergeImpl,
  } as Partial<AppBindings> as AppBindings;
}

async function renderSection() {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><ToastProvider><RecoveryCopiesSection /></ToastProvider></LocaleProvider>);
  });
  await flush();
  return { root };
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().includes(text));
}

function checkboxByLabel(label: string): HTMLInputElement | undefined {
  return Array.from(document.querySelectorAll<HTMLInputElement>("input[type=checkbox]"))
    .find((c) => c.getAttribute("aria-label") === label);
}

/** Drives the full merge flow: open panel → tick the conversation → press
 *  the step-③ merge button → confirm the danger dialog.
 *  tl1415b: 底部③按钮改名「Merge into longest chain (checked)」(demo v5 文案,
 *  hover 带第③步说明);确认框的 confirm 文案键(recoveryCopiesMerge)未动。 */
async function runMergeFlow() {
  const openBtn = buttonByText("Review and merge");
  if (!openBtn) throw new Error("panel open button missing");
  await act(async () => { openBtn.click(); });
  await flush();
  const box = checkboxByLabel("GC dev session");
  if (!box) throw new Error("conversation checkbox missing");
  await act(async () => { box.click(); });
  await flush();
  const mergeBtn = buttonByText("Merge into longest chain");
  if (!mergeBtn) throw new Error("merge button missing");
  await act(async () => { mergeBtn.click(); });
  await flush();
  // The confirm dialog re-uses the merge label; pick the button inside the
  // dialog (rendered last in the body).
  const candidates = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .filter((b) => b.textContent?.trim().includes("Merge selected") && !b.disabled);
  const confirmBtn = candidates[candidates.length - 1];
  if (!confirmBtn) throw new Error("confirm button missing");
  await act(async () => { confirmBtn.click(); });
  await flush();
  await flush();
}

function toastText(): string {
  return document.querySelector(".toast-container")?.textContent ?? "";
}

console.log("\ntl1401 versions-panel merge feedback toasts");

// ① 成功路径：合并成功 → toast 报告合入主线的增量条数。
{
  window.go = { main: { App: groupBindings(async () => ({
    mainPath: "/sessions/gc.jsonl",
    winnerPath: "",
    promoted: false,
    blockedByDivergence: false,
    normalizedMain: false,
    mainMessageCount: 1,
    winnerMessageCount: 2,
    trashed: ["/sessions/gc-recovery-0123456789abcdef.jsonl"],
    skippedNotCovered: [],
    skippedUnloadable: [],
  })) } };
  const { root } = await renderSection();
  await runMergeFlow();
  const text = toastText();
  ok(text.includes("Merged 1 entries into the main transcript"), `① success toast names the grafted count (got ${JSON.stringify(text)})`);
  await act(async () => { root.unmount(); });
}

// ② 失败路径：合成调用 reject → 错误 toast（反馈对称）。
{
  window.go = { main: { App: groupBindings(async () => {
    throw new Error("session is busy in another window");
  }) } };
  const { root } = await renderSection();
  await runMergeFlow();
  const text = toastText();
  ok(text.includes("1 conversation(s) failed to merge"), `② failure toast appears alongside the outcome row (got ${JSON.stringify(text)})`);
  await act(async () => { root.unmount(); });
}

// ③ blocked（双重打开）不触发成功 toast——它是明确的业务原因，有自己的 outcome 行。
{
  window.go = { main: { App: {
    ...groupBindings(async () => ({})),
    IsConversationDualOpen: async () => true,
  } } };
  const { root } = await renderSection();
  await runMergeFlow();
  const text = toastText();
  ok(!text.includes("Merged 1 entries"), "③ a blocked merge does not claim success");
  ok(!text.includes("failed to merge"), "③ a blocked merge is not counted as a failure");
  await act(async () => { root.unmount(); });
}

console.log(`tl1401 consolidate-toast: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
dom.window.close();
