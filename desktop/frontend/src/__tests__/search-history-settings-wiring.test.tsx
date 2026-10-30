// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/search-history-settings-wiring.test.tsx
//
// tl1282 验收（设置面板搜索框挂检索历史，JSDOM 真渲染）:
//   ④ 挂现有检索框不新增入口——下拉渲染在既有 settings-center__search label 内，
//     默认（未聚焦）零渲染；聚焦且无查询词才出现，blur/Escape/选中后收起；
//   ① 选中历史词回填 query 并置顶去重；Enter 提交当前查询词入史；
//   ② 单条删除/一键清空即时生效且写穿存储；
//   ③ 持久化——交互后 localStorage（webview profile 跨重启载体）留有最新列表，
//     重挂载（重启路径）读回。
//
// 环境注意（与 channel-chat-panel.test 同款）：react-dom 必须 await import 在
// JSDOM 全局就位之后——静态 import 会让 react-dom 在无 window 环境求值，
// isInputEventSupported 永久 false，受控输入的 onChange 全部失效（2594 排查实录）。

import * as React from "react"; // tsx 对 __tests__ 走 classic JSX——必须显式绑定 React
import { JSDOM } from "jsdom";

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
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
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.ResizeObserver = TestResizeObserver;

// 全局就位后再加载 react/react-dom 与被测组件。
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { SettingsNavigation, SETTINGS_NAV_TABS } = await import("../components/SettingsNavigation");
const { SEARCH_HISTORY_STORAGE_KEY } = await import("../lib/searchHistory");
type SettingsNavigationItem = Awaited<typeof import("../components/SettingsNavigation")>["SettingsNavigationItem"];

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}
function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function setNativeValue(element: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
  if (!setter) throw new Error("native value setter unavailable");
  setter.call(element, value);
  element.dispatchEvent(new window.Event("input", { bubbles: true }));
}
function typeQuery(input: HTMLInputElement, value: string) {
  setNativeValue(input, value);
}

const ITEMS: SettingsNavigationItem[] = SETTINGS_NAV_TABS.map((id) => ({ id, label: id, meta: "" }));
async function renderNav(): Promise<{ root: ReturnType<typeof createRoot> }> {
  const handle = { root: createRoot(document.getElementById("root")!) };
  await act(async () => {
    handle.root.render(
      <LocaleProvider>
        <SettingsNavigation items={ITEMS} activeTab="general" onSelect={() => {}} />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  return handle;
}
async function unmount(handle: { root: ReturnType<typeof createRoot> }) {
  await act(async () => { handle.root.unmount(); await flushTimers(); });
}
function searchInput(): HTMLInputElement {
  const input = document.querySelector<HTMLInputElement>(".settings-center__search input");
  if (!input) throw new Error("settings search input not mounted");
  return input;
}
function dropdown(): Element | null {
  return document.querySelector(".settings-center__search .search-history");
}
function historyRows(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".search-history__term-text")).map((el) => el.textContent ?? "");
}
async function focusInput() {
  await act(async () => { searchInput().focus(); await flushTimers(); });
}
function stored(): unknown {
  return JSON.parse(dom.window.localStorage.getItem(SEARCH_HISTORY_STORAGE_KEY) ?? "null");
}

// ── 场景一：默认零改动 → 聚焦出历史 ─────────────────────────────────────────
console.log("\ntl1282 settings ① default view untouched, focus reveals history");

dom.window.localStorage.clear();
dom.window.localStorage.setItem(SEARCH_HISTORY_STORAGE_KEY, JSON.stringify(["memory", "mcp"]));

{
  const handle = await renderNav();
  eq(dropdown(), null, "unfocused by default → no history dropdown (default view zero change)");

  await focusInput();
  ok(dropdown() !== null, "focusing the existing search box shows the history dropdown");
  eq(historyRows(), ["memory", "mcp"], "stored entries render newest-first");

  // 输入即隐藏（历史让位给实时过滤结果）。
  await act(async () => { typeQuery(searchInput(), "m"); await flushTimers(); });
  eq(dropdown(), null, "typing hides the dropdown (live filter takes over)");

  await act(async () => { typeQuery(searchInput(), ""); await flushTimers(); });
  ok(dropdown() !== null, "clearing the query brings the dropdown back while focused");

  await act(async () => { searchInput().blur(); await flushTimers(); });
  eq(dropdown(), null, "blur closes the dropdown");

  // Escape 收起（blur 关过一次，重新聚焦再发 Escape）。
  await focusInput();
  ok(dropdown() !== null, "refocus reopens the dropdown");
  await act(async () => {
    searchInput().dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
  eq(dropdown(), null, "Escape closes the dropdown while focus stays in the input");

  await unmount(handle);
}

// ── 场景二：Enter 提交 + 选历史词回填置顶 + 写穿存储 ────────────────────────
console.log("\ntl1282 settings ② commit / select-backfill / storage write-through");

dom.window.localStorage.clear();
{
  const handle = await renderNav();

  // Enter 提交当前查询词。
  await act(async () => { typeQuery(searchInput(), "sandbox"); await flushTimers(); });
  await act(async () => {
    searchInput().dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await flushTimers();
  });
  eq(stored(), ["sandbox"], "Enter records the committed term to storage");

  // 预置侧栏写入的共用历史后重挂载（重启路径读回）。
  await unmount(handle);
  dom.window.localStorage.setItem(SEARCH_HISTORY_STORAGE_KEY, JSON.stringify(["memory", "sandbox", "mcp"]));
  const handle2 = await renderNav();
  await focusInput();
  eq(historyRows(), ["memory", "sandbox", "mcp"], "remount reads back the shared store (restart path)");

  const rows = document.querySelectorAll<HTMLButtonElement>(".search-history__term");
  ok(rows.length === 3, "three history rows render");
  await act(async () => { rows[rows.length - 1].click(); await flushTimers(); }); // 点最旧的 "mcp"
  eq(searchInput().value, "mcp", "clicking a history term backfills the query");
  eq(dropdown(), null, "selection closes the dropdown");
  eq(stored(), ["mcp", "memory", "sandbox"], "selected term is promoted to front in storage (dedupe-to-top)");

  // 输入查询词后点导航项 = 有效检索提交（复用任务 764 的 onSelect(query) 时机）。
  await act(async () => { typeQuery(searchInput(), "providers"); await flushTimers(); });
  const navItem = document.querySelector<HTMLButtonElement>(".settings-center__navitem");
  ok(navItem !== null, "nav items still render (search contract intact)");
  if (navItem) {
    await act(async () => { navItem.click(); await flushTimers(); });
    eq(stored(), ["providers", "mcp", "memory", "sandbox"], "clicking a nav result with an active query records it");
  }

  // 单条删除（选中历史词时已显式收起下拉，走一次真实 blur→focus 重开）。
  await act(async () => { searchInput().blur(); await flushTimers(); });
  await focusInput();
  await act(async () => { typeQuery(searchInput(), ""); await flushTimers(); });
  ok(historyRows().length === 4, "four history rows visible before removal");
  const removeBtn = document.querySelectorAll<HTMLButtonElement>(".search-history__remove")[1];
  await act(async () => { removeBtn.click(); await flushTimers(); });
  eq(historyRows(), ["providers", "memory", "sandbox"], "single-row delete removes exactly that term");
  eq(stored(), ["providers", "memory", "sandbox"], "single delete persists");

  // 一键清空。
  const clearBtn = document.querySelector<HTMLButtonElement>(".search-history__clear");
  ok(clearBtn !== null, "clear-all button renders in the dropdown head");
  if (clearBtn) {
    await act(async () => { clearBtn.click(); await flushTimers(); });
    eq(dropdown(), null, "clear-all closes the (now empty) dropdown");
    eq(stored(), [], "clear-all persists empty storage");
    await focusInput();
    eq(dropdown(), null, "refocus with empty history shows no dropdown");
  }

  await unmount(handle2);
}

process.stdout.write(`\ntl1282 settings wiring: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
