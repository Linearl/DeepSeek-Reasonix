// tl1734 记忆设置面板两缺陷回归：
//  缺陷B——切到无记忆（后端 Ctrl 未就绪 → available=false）的工作区时，面板不再
//  整页渲染「记忆功能不可用」，而是正常骨架 + 空列表 + 「该项目暂无记忆」引导；
//  真禁用（tab 就绪且 available=false）仍保留兜底文案；加载失败改为可重试错误态。
//  缺陷A——挂载链路消除双全量拉取：等 ListTabs 定出 effectiveTabId 后只发一次
//  MemoryForTab；ListTabs 失败才回退 Memory()；等待期间显示 Loading 占位。
import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemorySettingsPage } from "../components/MemoryPanel";
import type { AppBindings } from "../lib/bridge";
import { LocaleProvider } from "../lib/i18n";
import type { MemoryFact, MemoryView, TabMeta } from "../lib/types";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

function ok(value: unknown, message: string) {
  if (!value) throw new Error(message);
}

function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    await act(async () => {
      await flush();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}: ${(document.body?.textContent || "").slice(0, 400)}`);
}

function installDom() {
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
  globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
  globalThis.HTMLInputElement = dom.window.HTMLInputElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function tabMeta(id: string, overrides: Partial<TabMeta> = {}): TabMeta {
  const workspaceRoot = `/repo/${id}`;
  return {
    id,
    scope: "project",
    workspaceRoot,
    workspaceName: id,
    workspacePath: workspaceRoot,
    topicId: `topic-${id}`,
    topicTitle: id,
    sessionPath: `${workspaceRoot}/sessions/${id}.jsonl`,
    label: `model-${id}`,
    ready: true,
    running: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: false,
    cwd: workspaceRoot,
    ...overrides,
  } as TabMeta;
}

function fact(name: string): MemoryFact {
  return {
    name,
    description: `${name} description`,
    type: "project",
    scope: "project",
    body: `Body of ${name}`,
    freshness: "current",
  } as MemoryFact;
}

function memoryView(overrides: Partial<MemoryView> = {}): MemoryView {
  return {
    docs: [],
    facts: [],
    archives: [],
    scopes: [],
    instructionDiagnostics: [],
    conflicts: [],
    lastRecall: { query: "", hits: [], omitted: 0, charBudget: 0, usedChars: 0 },
    storeDir: "",
    available: true,
    ...overrides,
  };
}

interface MemoryCalls {
  listTabs: number;
  memory: number;
  memoryForTab: string[];
}

interface Harness {
  rootEl: HTMLElement;
  calls: MemoryCalls;
  unmount: () => Promise<void>;
  tabsDeferred: ReturnType<typeof deferred<TabMeta[]>> | null;
}

// Mount MemorySettingsPage with a two-tab backend:
//  - "ready" tab: ready, carries one fact;
//  - "empty" tab: models an unready workspace — backend returns available=false.
// `emptyTabReady` flips the empty tab to ready=true to exercise the genuine
// "memory feature disabled" fallback (B3). `memoryForTabBehavior` lets the
// caller inject failures.
async function mountHarness(opts: {
  emptyTabReady?: boolean;
  memoryForTabBehavior?: (tabId: string) => Promise<MemoryView>;
  memoryBehavior?: () => Promise<MemoryView>;
  listTabsBehavior?: () => Promise<TabMeta[]>;
} = {}): Promise<Harness> {
  const emptyTabReady = opts.emptyTabReady ?? false;
  installDom();
  window.localStorage.setItem("reasonix-lang", "en");
  const calls: MemoryCalls = { listTabs: 0, memory: 0, memoryForTab: [] };
  const tabsDeferred = deferred<TabMeta[]>();
  const readyTab = tabMeta("ready", { active: true, workspaceName: "ReadyProj" });
  const emptyTab = tabMeta("empty", { ready: emptyTabReady, workspaceName: "data_processor" });
  const fullView = memoryView({ facts: [fact("prefers-tabs")], storeDir: "/repo/ready/memory" });
  const unavailableView = memoryView({ available: false });

  window.go = {
    main: {
      App: {
        ListTabs: async () => {
          calls.listTabs += 1;
          if (opts.listTabsBehavior) return opts.listTabsBehavior();
          return tabsDeferred.promise;
        },
        Memory: async () => {
          calls.memory += 1;
          if (opts.memoryBehavior) return opts.memoryBehavior();
          return fullView;
        },
        MemoryForTab: async (tabId: string) => {
          calls.memoryForTab.push(tabId);
          if (opts.memoryForTabBehavior) return opts.memoryForTabBehavior(tabId);
          if (tabId === "ready") return fullView;
          return unavailableView;
        },
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      React.createElement(
        LocaleProvider,
        null,
        React.createElement(MemorySettingsPage),
      ),
    );
    await flush();
  });
  return {
    rootEl,
    calls,
    tabsDeferred,
    unmount: async () => {
      await act(async () => {
        root.unmount();
      });
    },
  };
}

function buttonByText(rootEl: HTMLElement, text: string): HTMLButtonElement | undefined {
  return Array.from(rootEl.querySelectorAll("button")).find((b) => (b.textContent || "").includes(text));
}

function selectOption(_rootEl: HTMLElement, label: string): HTMLButtonElement | undefined {
  // AnchoredPopover portals its menu into document.body, so options live
  // outside the app root.
  return Array.from(document.querySelectorAll('[role="option"]')).find(
    (b) => (b.textContent || "").includes(label),
  ) as HTMLButtonElement | undefined;
}

console.log("\ntl1734 memory settings empty scope");

// B1: 空工作区（tab 未就绪 → available=false）渲染正常骨架 + 空列表引导，
// 不再整页「记忆功能不可用」。
{
  const h = await mountHarness();
  // A2: ListTabs 未返回期间显示 Loading 占位，而非误导性的不可用/空态。
  ok((h.rootEl.textContent || "").includes("Loading…"), "must show loading placeholder before tabs settle");
  await h.tabsDeferred.resolve([tabMeta("ready", { active: true, workspaceName: "ReadyProj" }), tabMeta("empty", { ready: false, workspaceName: "data_processor" })]);
  await waitFor("ready tab facts", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);

  // A1: 挂载链路恰好一次 MemoryForTab（active tab），零次 Memory() 全量兜底。
  ok(h.calls.memoryForTab.length === 1, `expected exactly one MemoryForTab call, got ${h.calls.memoryForTab.length}`);
  ok(h.calls.memory === 0, `expected zero Memory() calls on the happy path, got ${h.calls.memory}`);

  // 切到无记忆工作区（后端 empty view）。
  await act(async () => {
    buttonByText(h.rootEl, "ReadyProj")!.click();
    await flush();
  });
  await act(async () => {
    selectOption(h.rootEl, "data_processor")!.click();
    await flush();
  });
  await waitFor("empty workspace CTA", () => (h.rootEl.textContent || "").includes("No memories for this project yet"));
  ok(!(h.rootEl.textContent || "").includes("Memory unavailable."), "empty workspace must NOT render the unavailable fallback");
  // 骨架仍在：tab 行与工作区选择器都渲染（非整页不可用）。
  ok(h.rootEl.querySelector(".memory-tabs-row"), "tab row must still render for an empty workspace");
  ok(h.rootEl.querySelector(".mem-ws-select"), "workspace selector must still render for an empty workspace");
  await h.unmount();
}

// B2: 空工作区切回正常工作区，数据完整恢复、零回退。
{
  const h = await mountHarness();
  await h.tabsDeferred.resolve([tabMeta("ready", { active: true, workspaceName: "ReadyProj" }), tabMeta("empty", { ready: false, workspaceName: "data_processor" })]);
  await waitFor("ready tab facts", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);
  await act(async () => {
    buttonByText(h.rootEl, "ReadyProj")!.click();
    await flush();
  });
  await act(async () => {
    selectOption(h.rootEl, "data_processor")!.click();
    await flush();
  });
  await waitFor("empty workspace CTA", () => (h.rootEl.textContent || "").includes("No memories for this project yet"));
  await act(async () => {
    buttonByText(h.rootEl, "data_processor")!.click();
    await flush();
  });
  await act(async () => {
    selectOption(h.rootEl, "ReadyProj")!.click();
    await flush();
  });
  await waitFor("facts restored", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);
  ok((h.rootEl.textContent || "").includes("prefers-tabs"), "facts must render after switching back");
  await h.unmount();
}

// B3: 工作区就绪（tab.ready=true）且后端 available=false → 记忆功能真被禁用，
// 保留「Memory unavailable.」兜底。
{
  const h = await mountHarness({ emptyTabReady: true });
  await h.tabsDeferred.resolve([tabMeta("ready", { active: true, workspaceName: "ReadyProj" }), tabMeta("empty", { ready: true, workspaceName: "data_processor" })]);
  await waitFor("ready tab facts", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);
  await act(async () => {
    buttonByText(h.rootEl, "ReadyProj")!.click();
    await flush();
  });
  await act(async () => {
    selectOption(h.rootEl, "data_processor")!.click();
    await flush();
  });
  await waitFor("unavailable fallback", () => (h.rootEl.textContent || "").includes("Memory unavailable."));
  ok(!(h.rootEl.textContent || "").includes("No memories for this project yet"), "disabled memory must not show the empty-workspace CTA");
  await h.unmount();
}

// B4: 加载失败 → 可恢复错误态（空态 + 错误条 + Retry），重试成功即恢复数据。
{
  let attempts = 0;
  const h = await mountHarness({
    memoryForTabBehavior: async (tabId: string) => {
      attempts += 1;
      if (attempts === 1) throw new Error("wails boom");
      if (tabId === "ready") return memoryView({ facts: [fact("prefers-tabs")] });
      return memoryView({ available: false });
    },
  });
  await h.tabsDeferred.resolve([tabMeta("ready", { active: true, workspaceName: "ReadyProj" }), tabMeta("empty", { ready: false, workspaceName: "data_processor" })]);
  await waitFor("error state", () => (h.rootEl.textContent || "").includes("wails boom"));
  ok((h.rootEl.textContent || "").includes("No memories for this project yet"), "error state keeps the empty-workspace CTA");
  const retry = buttonByText(h.rootEl, "Retry");
  ok(retry, "retry button must render on load failure");
  await act(async () => {
    retry!.click();
    await flush();
  });
  await waitFor("facts after retry", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);
  ok(!(h.rootEl.textContent || "").includes("wails boom"), "error must clear after a successful retry");
  await h.unmount();
}

// A2 独立面：ListTabs 永不返回 → 保持 Loading，不发 Memory() 兜底（tabs 仍在途中）；
// tabs 落定为空列表后 → 走 Memory() 兜底渲染（不留永久 Loading 卡死路径）。
{
  const h = await mountHarness();
  await waitFor("loading stays", () => (h.rootEl.textContent || "").includes("Loading…"));
  ok(h.calls.memory === 0, "must not call Memory() while ListTabs is still pending");
  ok(h.calls.memoryForTab.length === 0, "must not call MemoryForTab without an effective tab");
  // Settle the pending ListTabs before unmount: an eternally pending thenable
  // wedges React's act queue and every subsequent block loses its mount
  // effects (verified by the block-order probe). Resolving an empty tab list
  // exercises the "loaded but no workspace" Memory() fallback.
  await h.tabsDeferred.resolve([]);
  await waitFor("empty-tab fallback view", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);
  ok(h.calls.memory === 1, `expected one Memory() fallback after an empty tab list, got ${h.calls.memory}`);
  ok(h.calls.memoryForTab.length === 0, "still no MemoryForTab call without a workspace");
  await h.unmount();
}

// A1 另一半：ListTabs 失败 → 回退 Memory() 全量兜底（面板仍可用）。
{
  const h = await mountHarness({
    listTabsBehavior: async () => {
      throw new Error("list tabs boom");
    },
  });
  await waitFor("memory fallback view", () => h.rootEl.querySelectorAll(".mem-fact").length === 1);
  ok(h.calls.memory === 1, `expected exactly one Memory() fallback call, got ${h.calls.memory}`);
  ok(h.calls.memoryForTab.length === 0, "no MemoryForTab call when ListTabs failed");
  ok(!(h.rootEl.textContent || "").includes("Memory unavailable."), "fallback load must not render unavailable");
  await h.unmount();
}

// L1: 三语键同步（zh/en/zh-TW 均含空工作区引导键；en 为 canonical 字典）。
{
  for (const dict of [en as Record<string, string>, zh as Record<string, string>, zhTW as Record<string, string>]) {
    ok(typeof dict["memory.emptyWorkspaceTitle"] === "string" && dict["memory.emptyWorkspaceTitle"].length > 0, "emptyWorkspaceTitle must exist in every locale");
    ok(typeof dict["memory.emptyWorkspaceBody"] === "string" && dict["memory.emptyWorkspaceBody"].length > 0, "emptyWorkspaceBody must exist in every locale");
    ok(typeof dict["memory.unavailable"] === "string", "unavailable key must exist in every locale");
  }
  ok((en as Record<string, string>)["memory.emptyWorkspaceTitle"] === "No memories for this project yet", "en title pinned for test assertions");
  ok((zh as Record<string, string>)["memory.emptyWorkspaceTitle"] === "该项目暂无记忆", "zh title pinned to the acceptance wording");
}

console.log("tl1734 memory settings empty scope: all assertions passed");
