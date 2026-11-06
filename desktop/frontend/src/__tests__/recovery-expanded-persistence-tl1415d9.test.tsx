// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/recovery-expanded-persistence-tl1415d9.test.tsx
// (vitest runs this file as a false red — the project's component harnesses are
// tsx-runner files; see recovery-panel-headers-tl1415.test.tsx for the pattern.)
//
// tl1415d9 harness: 会话行展开态持久化(用户二轮裁决钉)。
//
// 裁决原文(20261107):折叠语义正解=刷新后不要收起——默认收起只是「开面板时」
// 的行为;用户仍然展开了行,数据刷新/合并生效后应继续保持展开态(展开是用户
// 状态,刷新不重置)。既非撤折叠也非恢复旧平铺。验收追加=展开行→刷新/合并→
// 仍展开。
//
// 真因钉(先复现后修):
//  ② 组件重挂载(设置面板关开/tab 回切/SectionBoundary 重试)把
//     RecoveryCopiesSection.tsx 的 useState(new Set()) 整个重置——面板内
//     load() 不清展开态,但面板层的重挂载链没有保护它(用户实测「刷新后
//     收起了」的真机制)。
//  ③ 展开态按完整 mainPath 记,采纳副本后后端把组 preferred path 换成副本
//     文件(tl1430 语义:文件不搬、主线换文件),旧 path 失配 → 行塌。
//
// 验收映射:
//  ① 展开行→面板内刷新→仍展开(裁决「刷新不重置」的面板内路径)。
//  ② 展开行→组件重挂载→仍展开(重挂载链路)。
//  ③ 展开行→采纳副本(主线 path 变)→重取→仍展开(mainPath 失配场景)。
//  ④ 展开行→合并生效(会话移入已合入组)→组展开时行仍展开(合并场景)。
//  ⑤ 从未展开过的行:重挂载后默认仍折叠(默认收起语义不变)。
//  ⑥ 用户自己收起的行:重挂载后仍收起(只有「用户展开过的」要保持)。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, RecoveryCopyGroupView } from "../lib/bridge";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

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
globalThis.HTMLTableElement = dom.window.HTMLTableElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");

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

/** 展开态记忆是本件新落的持久化——每个场景开头清空,互不串档。 */
function resetExpandedMemory() {
  window.localStorage.clear();
}

const MAIN_PATH = "/sessions/gc.jsonl";
const COPY_PATH = "/sessions/gc-recovery-0123456789abcdef.jsonl";
/** 采纳后的主线 = 原副本文件(tl1430:文件不搬、主线换文件)。 */
const ADOPTED_MAIN_PATH = COPY_PATH;

function groupFixture(mainPath: string, copies: RecoveryCopyGroupView["copies"]): RecoveryCopyGroupView {
  return {
    mainPath,
    mainLabel: "GC",
    directory: "/sessions",
    mainExists: true,
    mainMessages: 12,
    mainTurns: 6,
    mainBytes: 1024,
    mainModified: "2026-10-21T00:33:00Z",
    copies,
  };
}

const COPY_VIEW = {
  path: COPY_PATH,
  label: "GC copy 1",
  bytes: 2048,
  modified: "2026-10-21T01:00:00Z",
  messages: 3,
  turns: 2,
  shared: 1,
  unique: 2,
  scanned: true,
};

const CHAIN_SET = (mainPath: string) => ({
  mainPath,
  mainLabel: "GC",
  chains: [
    {
      path: mainPath,
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
  ],
  longestPath: mainPath,
  longestHead: "",
});

/** currentList 供「刷新/合并后重取」改写——stub 读变量而非闭包快照。 */
let currentList: RecoveryCopyGroupView[] = [];
let currentVersionGroups: { mainPath: string; mainLabel: string; directory: string; headCount: number; selected: string; heads: unknown[] }[] = [];

function stubBridge() {
  const base = {
    ListRecoveryCopyGroups: async () => currentList,
    ListSessionVersionGroups: async () => currentVersionGroups,
    ListRecoveryChains: async (path: string) => CHAIN_SET(path),
    ListSessionRepairRecordsFor: async () => [],
    DeleteRecoveryBranch: async () => {},
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
    InspectSessionHealthFor: async () => ({ healthy: true, issues: [] }),
    RepairSessionFor: async () => ({ path: MAIN_PATH, repaired: false, actions: [], healthy: true }),
  };
  window.go = {
    main: {
      App: base as unknown as AppBindings,
    },
  } as typeof window.go;
}

/** openPanel 挂一个全新组件实例并打开面板(tl1415b-2:会话行默认折叠)。 */
async function openPanel(): Promise<{ unmount: () => Promise<void> }> {
  stubBridge();
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  return {
    unmount: async () => {
      await act(async () => root.unmount());
    },
  };
}

async function expandSession(index = 0): Promise<void> {
  const toggle = document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle")[index];
  await act(async () => { toggle?.click(); });
  await flush();
  await flush();
}

async function collapseSession(index = 0): Promise<void> {
  const toggle = document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle")[index];
  await act(async () => { toggle?.click(); });
  await flush();
}

async function clickRefresh(): Promise<void> {
  await act(async () => { buttonByText("Refresh")?.click(); });
  await flush();
}

function sessionToggleState(index = 0): string | undefined {
  return document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle")[index]?.getAttribute("aria-expanded");
}

console.log("\nrecovery session-row expanded persistence (tl1415d9)");

// ① 面板内刷新(列表重取)不重置展开态——裁决「刷新不重置」的面板内路径。
{
  resetExpandedMemory();
  currentList = [groupFixture(MAIN_PATH, [COPY_VIEW])];
  currentVersionGroups = [];
  const panel = await openPanel();
  ok(sessionToggleState() === "false", "① a fresh panel starts with the session row collapsed");
  await expandSession();
  ok(sessionToggleState() === "true", "① expanding the session row opens its children");
  await clickRefresh();
  ok(sessionToggleState() === "true", "① the row stays expanded after an in-panel refresh");
  await panel.unmount();
}

// ② 组件重挂载(设置面板关开/tab 回切/边界重试)后仍展开——真因②钉。
{
  resetExpandedMemory();
  currentList = [groupFixture(MAIN_PATH, [COPY_VIEW])];
  currentVersionGroups = [];
  const panel = await openPanel();
  await expandSession();
  ok(sessionToggleState() === "true", "② the row is expanded before the remount");
  await panel.unmount();
  const reopened = await openPanel();
  ok(sessionToggleState() === "true", "② the row the user expanded stays expanded after a remount (用户裁决:重挂载不重置)");
  await reopened.unmount();
}

// ③ 采纳副本→主线 path 变→重取后仍展开——真因③钉(mainPath 失配)。
{
  resetExpandedMemory();
  currentList = [groupFixture(MAIN_PATH, [COPY_VIEW])];
  currentVersionGroups = [];
  const panel = await openPanel();
  await expandSession();
  ok(sessionToggleState() === "true", "③ the row is expanded before the adoption");
  // 采纳生效后的列表:主线换成原副本文件,旧主线文件落回副本清单。
  currentList = [groupFixture(ADOPTED_MAIN_PATH, [{ ...COPY_VIEW, path: MAIN_PATH, label: "GC previous main" }])];
  await clickRefresh();
  ok(sessionToggleState() === "true", "③ the row stays expanded when the mainline path changed (采纳后仍展开)");
  await panel.unmount();
}

// ④ 合并生效→会话移入已合入组→组展开时会话行仍展开——合并场景钉。
{
  resetExpandedMemory();
  currentList = [groupFixture(MAIN_PATH, [COPY_VIEW])];
  currentVersionGroups = [];
  const panel = await openPanel();
  await expandSession();
  // 合并生效后的列表:副本清零、版本读数仍 >1(headCount=2)——phase 翻到
  // 已合入,但行还有子行可看(纯行无展开钮,不合本钉)。
  currentList = [groupFixture(MAIN_PATH, [])];
  currentVersionGroups = [{ mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 2, selected: MAIN_PATH, heads: [] }];
  await clickRefresh();
  // 行已移入已合入组(默认折叠)——先展开组段,再验会话行展开态。
  const groupRows = document.querySelectorAll<HTMLTableRowElement>("tr.rc-grouprow");
  await act(async () => { groupRows[1]?.click(); });
  await flush();
  await flush();
  ok(sessionToggleState() === "true", "④ after a merge moved the row into the merged group, it renders expanded (合并后仍展开)");
  await panel.unmount();
}

// ⑤ 从未展开过的行:重挂载后默认仍折叠——默认收起语义不变。
{
  resetExpandedMemory();
  currentList = [groupFixture(MAIN_PATH, [COPY_VIEW])];
  currentVersionGroups = [];
  const panel = await openPanel();
  await panel.unmount();
  const reopened = await openPanel();
  ok(sessionToggleState() === "false", "⑤ a row the user never expanded stays collapsed after a remount");
  await reopened.unmount();
}

// ⑥ 用户自己收起的行:重挂载后仍收起——只有「用户展开过的」要保持。
{
  resetExpandedMemory();
  currentList = [groupFixture(MAIN_PATH, [COPY_VIEW])];
  currentVersionGroups = [];
  const panel = await openPanel();
  await expandSession();
  await collapseSession();
  ok(sessionToggleState() === "false", "⑥ the row is collapsed after the user collapses it");
  await panel.unmount();
  const reopened = await openPanel();
  ok(sessionToggleState() === "false", "⑥ a row the user collapsed stays collapsed after a remount");
  await reopened.unmount();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
