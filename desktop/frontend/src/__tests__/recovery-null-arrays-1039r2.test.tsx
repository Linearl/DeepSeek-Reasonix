// 任务 1039r2 harness：bridge nil 切片（JSON null）进 state 后的 .length 渲染崩。
//
// 1036 装机后用户实测（GC开发专员会话）：点「查看并合并会话副本…」→ 分区
// 横幅「设置加载失败。Cannot read properties of null (reading 'length')」。
// 根因（可证伪）：Go 侧两个面板绑定在特定常态下返回 nil 切片，wails 序列化
// 成 JSON null，前端渲染树对它读 .length 同步抛错，被 SectionBoundary 接成
// 分区失败横幅：
//  ① ListRecoveryChains——全部候选（主转录+副本）的重放/探测都以非限额错误
//     失败时，set.Chains 保持 nil（如 events 日志存在但条目损坏：探测判出
//     DAG、重放报非限额错误，每条候选都被静默跳过）→ RecoveryCopiesSection
//     展开行的 `set.chains.length` 崩；
//  ② ListSessionRepairRecordsFor——修复日志不存在是常态（os.IsNotExist →
//     nil, nil）→ SessionVersionsSection 展开行的 `records.length` 崩。
//
// 本 harness 钉两点（可证伪）：
//  ① chains:null 到达前端 ⇒ 展开不崩、无分区失败横幅，面板存活；
//  ② 修复记录 null 到达前端 ⇒ 展开不崩、无分区失败横幅，展开区照常渲染。

import { JSDOM } from "jsdom";
import { act } from "react";
import type { ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
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

const { StorageSettingsPage } = await import("../components/StorageSettingsPage");
const { LocaleProvider } = await import("../lib/i18n");

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

async function renderPage(node: ReactNode): Promise<{ root: Root; rootEl: HTMLElement }> {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider>{node}</LocaleProvider>);
  });
  await flush();
  return { root, rootEl };
}

async function unmount(root: Root) {
  await act(async () => { root.unmount(); });
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().includes(text));
}

function bodyText(): string {
  return document.body.textContent || "";
}

const GC_PATH = "/sessions/gc.jsonl";
const GC_GROUP = {
  mainPath: GC_PATH,
  mainLabel: "GC开发专员",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 0,
  mainBytes: 49_210_175,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [],
};
const GC_VERSION_GROUP = {
  mainPath: GC_PATH,
  mainLabel: "GC开发专员",
  directory: "/sessions",
  headCount: 2,
  selected: "",
  heads: [],
};

/** stubBridge installs the storage-page face of the bindings; callers override
 *  the per-scenario methods on the returned object. */
function stubBridge(overrides: Partial<AppBindings>) {
  const base = {
    StorageSettings: async () => ({
      defaultWorkspace: "C:/work", statePath: "C:/state", cachePath: "C:/cache", extensionsPath: "C:/plugins",
    }),
    ListRecoveryCopyGroups: async () => [GC_GROUP],
    ListSessionVersionGroups: async () => [GC_VERSION_GROUP],
    ListRecoveryChains: async (mainPath: string) => ({
      mainPath, mainLabel: "", chains: [], longestPath: "", longestHead: "",
    }),
    ListSessionRepairRecordsFor: async () => [],
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

console.log("\nrecovery nil-slice arrays never crash the section render (1039r2)");

// ① ListRecoveryChains 成功返回 chains:null（nil 切片直通）⇒ 副本面板展开
//    该行不得崩、分区失败横幅不得出现。
{
  stubBridge({
    ListRecoveryChains: async (mainPath: string) => ({
      mainPath, mainLabel: "", chains: null, longestPath: "", longestHead: "",
    }) as unknown as Awaited<ReturnType<AppBindings["ListRecoveryChains"]>>,
  });

  const { root } = await renderPage(<StorageSettingsPage />);
  const openBtn = buttonByText("Review and merge");
  ok(Boolean(openBtn), "① the copies panel open button renders");
  await act(async () => { openBtn!.click(); });
  await flush();

  const toggle = document.querySelector<HTMLButtonElement>(".rc-modal .rc-toggle");
  ok(Boolean(toggle), "① the incident conversation row is listed in the modal");
  await act(async () => { toggle!.click(); });
  await flush();

  const text = bodyText();
  ok(!text.includes("Cannot read properties of null"), "① no null-pointer crash text anywhere");
  ok(!document.querySelector("[data-testid='settings-section-failure']"), "① the section boundary did not fire on a chains:null response");
  ok(text.includes("GC开发专员"), "① the conversation row survives the expand");
  await unmount(root);
}

// ② ListSessionRepairRecordsFor 成功返回 null（无修复日志的常态）⇒ 副本面板
//    （任务 1063 合并后 v3 版本块也长在这一行内）展开该行不得崩、分区失败横
//    幅不得出现，展开区照常渲染。
{
  stubBridge({
    ListSessionRepairRecordsFor: async () => null,
  } as Partial<AppBindings>);

  const { root } = await renderPage(<StorageSettingsPage />);
  const openBtn = buttonByText("Review and merge");
  ok(Boolean(openBtn), "② the copies panel open button renders");
  await act(async () => { openBtn!.click(); });
  await flush();
  const expand = document.querySelector<HTMLButtonElement>(".rc-modal .rc-toggle");
  ok(Boolean(expand), "② the versions panel row renders");
  await act(async () => { expand!.click(); });
  await flush();

  const text = bodyText();
  ok(!text.includes("Cannot read properties of null"), "② no null-pointer crash text anywhere");
  ok(!document.querySelector("[data-testid='settings-section-failure']"), "② the section boundary did not fire on a null repair log");
  ok(text.includes("GC开发专员"), "② the conversation row survives the expand");
  ok(Boolean(buttonByText("check health")), "② the expanded area still renders its actions");
  await unmount(root);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
