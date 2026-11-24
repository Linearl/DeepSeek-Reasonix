// 任务 1368 线2 harness：版本副本面板的整对话级清除（tl1787 件1 起文案
// 明示为「归档到回收站（含全部副本）」，入口自「更多▾」弹层提到底部栏——
// 勾选会话即出现的固定位；能力本体与后端面不变，后端由
// desktop/recovery_conversation_delete_test.go 钉住）。
//
// 验收：勾选行点「归档到回收站（含全部副本）」→ 主文件+全部副本+侧车入回
// 收站，侧栏与面板均消失，回收站可还原。本 harness 从前端面钉四点：
//  ① 未勾选时底部无该入口（选中即现，不占空位）；勾选后入口带计数可用；
//  ② 点击先弹确认，未确认不得调用后端；
//  ③ 确认后以勾选的 mainPath 调 DeleteConversationWithCopies；回执区报
//     「主线入回收站 + 副本 N 条归档」；列表刷新后该行从面板消失；
//  ④ 后端拒绝时回执区出现失败行（错误原文直出）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

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

function bodyText(): string {
  return document.body.textContent || "";
}

const MAIN_PATH = "/sessions/tl1368.jsonl";
const COPY_PATH = "/sessions/tl1368-recovery-0123456789abcdef.jsonl";

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "tl1368 conversation",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 9,
  mainBytes: 512,
  mainModified: "2026-11-03T00:00:00Z",
  copies: [
    {
      path: COPY_PATH,
      label: "tl1368 copy 1",
      bytes: 256,
      modified: "2026-11-03T01:00:00Z",
      messages: 3,
      shared: 1,
      unique: 2,
      scanned: true,
    },
  ],
};

const CHAIN_SET = {
  mainPath: MAIN_PATH,
  mainLabel: "tl1368 conversation",
  chains: [],
  longestPath: "",
  longestHead: "",
};

type Spies = {
  listCalls: number;
  deletedConversations: string[];
  /** Successful deletes only — the mock's list view reflects the world after
   *  a deletion actually happened, not after a refused one. */
  deletedOk: string[];
  report: { mainTrashed: boolean; copiesArchived: number } | null;
};

function stubBridge(overrides: Partial<AppBindings>, spies: Spies, groupsAfterDelete: ReturnType<typeof Array.prototype.slice> = []) {
  const base = {
    ListRecoveryCopyGroups: async () => {
      spies.listCalls += 1;
      // After a confirmed delete the panel reloads against the post-delete
      // world: the conversation row (and everything it listed) is gone.
      return spies.deletedOk.length > 0 ? groupsAfterDelete : [GROUP];
    },
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => CHAIN_SET,
    ListSessionRepairRecordsFor: async () => [],
    DeleteRecoveryBranch: async () => {},
    DeleteSessionVersionHead: async () => {},
    DeleteConversationWithCopies: async (mainPath: string) => {
      spies.deletedConversations.push(mainPath);
      spies.deletedOk.push(mainPath);
      return spies.report ?? { mainTrashed: true, copiesArchived: 0 };
    },
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openPanelWithSelection(spies: Spies, overrides: Partial<AppBindings> = {}, groupsAfterDelete: never[] = []) {
  stubBridge(overrides, spies, groupsAfterDelete);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();
  const row = document.querySelector<HTMLInputElement>(".rc-row--group input[type=checkbox]");
  ok(Boolean(row), "the conversation row renders its selection checkbox");
  await act(async () => { row?.click(); });
  await flush();
  return root;
}

console.log("\nrecovery conversation delete with copies (1368 线2)");

// ① tl1787 件1：未勾选时底部无归档入口（选中即现的固定位，不占空位）。
{
  const spies: Spies = { listCalls: 0, deletedConversations: [], deletedOk: [], report: null };
  stubBridge({}, spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Manage session copies")?.click(); });
  await flush();

  const refresh = buttonByText("Refresh");
  ok(Boolean(refresh), "① the Refresh button is present");
  const del = buttonByText("Archive to Recycle Bin (with every copy)");
  ok(!del, "① the archive entry is absent with nothing selected (tl1787: 选中即现)");

  await act(async () => root.unmount());
}

// ② 勾选后入口带计数可用；点击先弹确认，未确认不得调用后端。
{
  const spies: Spies = { listCalls: 0, deletedConversations: [], deletedOk: [], report: null };
  const root = await openPanelWithSelection(spies);

  const del = buttonByText("Archive to Recycle Bin (with every copy)");
  ok(Boolean(del) && !del?.disabled, "② the archive entry is enabled with a checked conversation");
  ok((del?.textContent || "").includes("(1)"), "② the entry counts the checked conversations");

  await act(async () => { del?.click(); });
  await flush();
  ok(Boolean(document.querySelector(".reasonix-confirm-dialog")), "② the archive asks for confirmation first");
  ok(spies.deletedConversations.length === 0, "② nothing reaches the backend before confirmation");

  await act(async () => root.unmount());
}

// ③ 确认后调用后端（mainPath 正确）；回执区报读数；刷新后该行消失。
{
  const spies: Spies = { listCalls: 0, deletedConversations: [], deletedOk: [], report: { mainTrashed: true, copiesArchived: 1 } };
  const root = await openPanelWithSelection(spies, {}, []);

  const del = buttonByText("Archive to Recycle Bin (with every copy)");
  await act(async () => { del?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Archive to Recycle Bin (with every copy)");
  ok(Boolean(confirmBtn), "③ the confirmation dialog offers the archive action");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.deletedConversations.length === 1 && spies.deletedConversations[0] === MAIN_PATH,
    "③ the confirmed delete reaches the backend with the conversation's main path");
  ok(bodyText().includes("main transcript trashed, 1 copies archived"),
    "③ the outcome line reports the main-transcript trash and the archived copy count");
  ok(!document.body.textContent?.includes("tl1368 conversation") || spies.listCalls >= 2,
    "③ the list reloads after the delete");
  const toggles = document.querySelectorAll<HTMLButtonElement>(".rc-modal .rc-toggle");
  ok(toggles.length === 0, "③ the deleted conversation disappears from the panel");

  await act(async () => root.unmount());
}

// ④ 后端拒绝 → 回执区失败行（错误原文直出）。
{
  const spies: Spies = { listCalls: 0, deletedConversations: [], deletedOk: [], report: null };
  const root = await openPanelWithSelection(spies, {
    DeleteConversationWithCopies: async (mainPath: string) => {
      spies.deletedConversations.push(mainPath);
      throw new Error("this session is in use by another Reasonix window");
    },
  });

  const del = buttonByText("Archive to Recycle Bin (with every copy)");
  await act(async () => { del?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Archive to Recycle Bin (with every copy)");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.deletedConversations.length === 1, "④ the backend was called");
  ok(bodyText().includes("this session is in use by another Reasonix window"),
    "④ the refusal lands in the outcome list verbatim");
  const row = document.querySelector<HTMLInputElement>(".rc-row--group input[type=checkbox]");
  ok(Boolean(row), "④ a refused conversation stays in the panel");

  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
