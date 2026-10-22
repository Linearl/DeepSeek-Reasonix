// 任务 1064 harness：版本管理面板手动删除分支（单删 + 批量）。
//
// 验收（tl 1064 正文）：
//  ① 单删/批量删均可用；当前版本不可选不可删；
//  ② 删除有确认+回收语义；
//  ③ 删除后其余版本完整性零破坏；
//  ④ tsc/harness 绿无预存红。
//
// 本 harness 从合并后的单一分区（任务 1063）出发，钉四点：
//  ① 副本行渲染删除入口；勾选复选框后底部出现「删除所选」；
//  ② 点单删/批量删都会先弹确认（未确认不得调用 DeleteRecoveryBranch）；
//  ③ 当前版本（selected=true 的 v3 head）行不渲染删除入口；
//  ④ 删除成功后列表即时刷新（LoadRecoveryCopyGroups 被重新调用），结果区
//     出现「已删除」。

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

const MAIN_PATH = "/sessions/gc.jsonl";
const COPY_PATH = "/sessions/gc-recovery-0123456789abcdef.jsonl";
const COPY_PATH_2 = "/sessions/gc-recovery-fedcba9876543210.jsonl";

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [
    {
      path: COPY_PATH,
      label: "GC copy 1",
      bytes: 2048,
      modified: "2026-10-21T01:00:00Z",
      messages: 3,
      shared: 1,
      unique: 2,
      scanned: true,
    },
    {
      path: COPY_PATH_2,
      label: "GC copy 2",
      bytes: 4096,
      modified: "2026-10-21T02:00:00Z",
      messages: 5,
      shared: 2,
      unique: 3,
      scanned: true,
    },
  ],
};

const CHAIN_SET = {
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
      messageCount: 3,
      turns: 2,
      lastActivity: "2026-10-20T00:00:00Z",
      bytes: 512,
      preview: "forked",
    },
  ],
  longestPath: MAIN_PATH,
  longestHead: "",
};

function stubBridge(overrides: Partial<AppBindings>, spies: { listCalls: number; deleted: string[] }) {
  const base = {
    ListRecoveryCopyGroups: async () => {
      spies.listCalls += 1;
      return [GROUP];
    },
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => CHAIN_SET,
    ListSessionRepairRecordsFor: async () => [],
    DeleteRecoveryBranch: async (path: string) => {
      spies.deleted.push(path);
    },
    DeleteSessionVersionHead: async () => {},
    PreviewRecoveryChain: async () => ({
      path: MAIN_PATH,
      isMain: true,
      messageCount: 12,
      turns: 6,
      firstUserText: "hello",
      tailLines: [],
      sharedWithMain: 12,
      uniqueToChain: 0,
      lastActivity: "2026-10-21T00:33:00Z",
      degraded: false,
    }),
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

console.log("\nrecovery branch delete single + batch (1064)");

// ① 副本行渲染删除入口 + 复选框；勾选后底部出现「删除所选」。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  stubBridge({}, spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  const open = buttonByText("Review and merge");
  await act(async () => { open?.click(); });
  await flush();

  const toggles = document.querySelectorAll<HTMLButtonElement>(".rc-modal .rc-toggle");
  ok(toggles.length === 1, "① one conversation row is listed");
  await act(async () => { (toggles[0] as HTMLButtonElement).click(); });
  await flush();

  const deleteButtons = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-delete"));
  ok(deleteButtons.length === 3, "① both copy rows and the non-current version row render a delete entry");
  const checkboxes = Array.from(document.querySelectorAll<HTMLInputElement>(".rc-row--copy input[type=checkbox]"));
  ok(checkboxes.length === 2, "① both copy rows render a selection checkbox");
  ok(!buttonByText("Delete selected"), "① no batch button before anything is checked");

  await act(async () => { checkboxes[0].click(); });
  await flush();
  const batch = buttonByText("Delete selected");
  ok(Boolean(batch), "① the batch button appears once a branch is checked");
  ok((batch?.textContent || "").includes("(1)"), "① the batch button counts the checked branches");

  await act(async () => root.unmount());
}

// ② 点单删先弹确认——未确认不得调用 DeleteRecoveryBranch（回收语义的安全门）。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  stubBridge({}, spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();

  const deleteButtons = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-delete"));
  await act(async () => { deleteButtons[0].click(); });
  await flush();
  ok(Boolean(document.querySelector(".reasonix-confirm-dialog")), "② single delete asks for confirmation first");
  ok(spies.deleted.length === 0, "② no deletion happens before the user confirms");

  await act(async () => root.unmount());
}

// ③ 当前版本（selected=true）不渲染删除入口——1064 安全约束①的前端面。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  stubBridge({}, spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();

  const headRows = Array.from(document.querySelectorAll<HTMLTableRowElement>(".rc-versions__table tbody tr"));
  ok(headRows.length === 2, "③ the v3 block lists both heads");
  const selectedRow = headRows.find((r) => r.textContent?.includes("selected"));
  const unselectedRow = headRows.find((r) => !r.textContent?.includes("selected"));
  ok(Boolean(selectedRow), "③ the selected head row is identifiable");
  ok(Boolean(unselectedRow), "③ an unselected head row exists");
  ok(!selectedRow?.querySelector(".rc-delete"), "③ the current version row has no delete entry");
  ok(Boolean(unselectedRow?.querySelector(".rc-delete")), "③ a non-current version row has a delete entry");

  await act(async () => root.unmount());
}

// ④ 删除成功后列表即时刷新 + 结果区出现「已删除」。
{
  const spies = { listCalls: 0, deleted: [] as string[] };
  stubBridge({}, spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();

  const listCallsBefore = spies.listCalls;
  const deleteButtons = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-delete"));
  await act(async () => { deleteButtons[0].click(); });
  await flush();
  // Confirm dialog's confirm button (the danger-toned primary in the dialog).
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  ok(Boolean(confirmBtn), "④ the confirmation dialog offers a confirm action");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.deleted.length === 1 && spies.deleted[0] === COPY_PATH, "④ the confirmed delete reaches the backend with the branch path");
  ok(spies.listCalls > listCallsBefore, "④ the list reloads immediately after the delete");
  ok(bodyText().includes("Deleted"), "④ the outcome list reports the deletion");

  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
