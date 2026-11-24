// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1775-tasklist-board-open.test.tsx
// tl1775 验收 harness —— 任务面板「从浏览器打开」按钮：
//  ① 行为面（jsdom + 真 TasklistPanel + 注入 bindings）：工具栏按钮存在且
//     位置在刷新之后；点击把 settings 解析出的 dbPath 传给绑定；stale=true
//     渲染「看板可能过期」提示；error 渲染 Go 侧文案（role=alert）；新鲜
//     成功则无提示（上一次提示被清掉）。
//  ② 接线面（源码断言，tl1746 同层）：TasklistPanel 走 bindings.openBoard
//     注入缝；bridge.ts 契约声明 OpenTasklistBoard + TasklistBoardOpenResult
//     四字段（check-bridge-json-tags 的 TS 半边）；三语键齐。
//
// ⚠ harness 约束（tl1462/tl1746 同款）：jsdom 必须在 import react-dom 之前
// 装好；react/react-dom/组件一律动态 import。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import { en } from "../locales/en";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
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
  globalThis.HTMLInputElement = dom.window.HTMLInputElement;
  globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
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

const dom = installDom();
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { TasklistPanel } = await import("../components/TasklistPanel");
type TasklistPanelBindings = import("../components/TasklistPanel").TasklistPanelBindings;
type TasklistBoardOpenResult = import("../lib/bridge").TasklistBoardOpenResult;
const { LocaleProvider } = await import("../lib/i18n");

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const read = (p: string) => readFileSync(`${frontendRoot}/${p}`, "utf8");

const FIXTURE_OVERVIEW = {
  path: "D:/fixture/tasklist.db",
  total: 1, pending: 1, done: 0, cancelled: 0,
  byStatus: [{ key: "pending", count: 1 }],
  byDomain: [{ key: "UI与交互", count: 1 }],
  byPriority: [{ key: "P1", count: 1 }],
  openByDomain: [{ key: "UI与交互", count: 1 }],
  openByPriority: [{ key: "P1", count: 1 }],
  generatedAt: "2026-11-24T08:00:00+08:00",
  cached: false,
};

function boardBindings(result: TasklistBoardOpenResult, calls: { path: string; n: number }): TasklistPanelBindings {
  return {
    getOverview: () => Promise.resolve({ ...FIXTURE_OVERVIEW }),
    queryTasks: () => Promise.resolve({ path: "D:/fixture/tasklist.db", rows: [], total: 0, limited: false, elapsedMs: 0 }),
    settings: () => Promise.resolve({ tasklistDbPath: "D:/fixture/tasklist.db" }),
    openBoard: (path) => { calls.path = path; calls.n += 1; return Promise.resolve({ ...result }); },
  };
}

async function renderPanel(root: ReturnType<typeof createRoot>, bindings: TasklistPanelBindings) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TasklistPanel open onClose={() => {}} bindings={bindings} />
      </LocaleProvider>,
    );
    await flush();
    await flush();
  });
}

async function clickOpenBoard() {
  const btn = document.querySelector<HTMLButtonElement>("[data-testid='tl-open-board']");
  if (!btn) throw new Error("missing tl-open-board button");
  await act(async () => {
    btn.click();
    await flush();
    await flush();
  });
}

console.log("\ntl1775 board open button: behavior + wiring + trilingual copy");
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

// ── ① 行为面 ────────────────────────────────────────────────────────
{
  const root = createRoot(rootEl);
  const calls = { path: "", n: 0 };
  await renderPanel(root, boardBindings({ path: "D:/fixture/tasklist.html", stale: false, opened: true, error: "" }, calls));

  // 按钮存在，且位置在刷新按钮之后、关闭按钮之前（头部「刷新/关闭旁」；
  // 关闭按钮无 data-testid → tl-open-board 是工具栏最后一个带 testid 的元素）。
  const toolbar = document.querySelector(".wf-panel__toolbar");
  const ids = Array.from(toolbar?.querySelectorAll("[data-testid]") ?? []).map((el) => el.getAttribute("data-testid"));
  const atRefresh = ids.indexOf("tl-refresh");
  const atOpen = ids.indexOf("tl-open-board");
  ok(atOpen > 0, "toolbar renders the tl-open-board button");
  ok(atOpen > atRefresh && atOpen === ids.length - 1, "button sits after refresh, before the untestid close button");

  // 点击 → 桩收到 settings 解析出的 dbPath；新鲜成功 → 无提示。
  await clickOpenBoard();
  ok(calls.n === 1, "click hands off to bindings.openBoard exactly once");
  ok(calls.path === "D:/fixture/tasklist.db", "openBoard receives the settings-resolved dbPath");
  ok(!document.querySelector("[data-testid='tl-board-stale']") && !document.querySelector("[data-testid='tl-board-error']"),
    "fresh successful open renders no hint");

  // stale → 「看板可能过期」提示（三语键渲染，非 alert 语气）。
  await act(async () => { root.unmount(); });
  await flush();

  const root2 = createRoot(rootEl);
  const calls2 = { path: "", n: 0 };
  await renderPanel(root2, boardBindings({ path: "D:/fixture/tasklist.html", stale: true, opened: true, error: "" }, calls2));
  await clickOpenBoard();
  const stale = document.querySelector("[data-testid='tl-board-stale']");
  ok(Boolean(stale), "stale result renders the refresh hint");
  ok((stale?.textContent ?? "").length > 0 && stale?.textContent === en["tasklist.board.stale"],
    "stale hint carries the locale copy");
  ok(stale?.getAttribute("role") !== "alert", "stale hint is informational (no alert role)");

  await act(async () => { root2.unmount(); });
  await flush();

  // error → Go 侧文案原样展示（role=alert，同列表错误语气）。
  const root3 = createRoot(rootEl);
  const calls3 = { path: "", n: 0 };
  await renderPanel(root3, boardBindings({ path: "", stale: false, opened: false, error: "看板尚未导出：D:/fixture/tasklist.html" }, calls3));
  await clickOpenBoard();
  const errLine = document.querySelector("[data-testid='tl-board-error']");
  ok(Boolean(errLine) && (errLine?.textContent ?? "").includes("看板尚未导出"),
    "error result surfaces the Go-side reason verbatim");
  ok(errLine?.getAttribute("role") === "alert", "error hint is announced (role=alert)");

  await act(async () => { root3.unmount(); });
  await flush();
  dom.window.close();
}

// ── ② 接线面（源码断言）─────────────────────────────────────────────
{
  const panelTsx = read("src/components/TasklistPanel.tsx");
  ok(panelTsx.includes('data-testid="tl-open-board"'), "panel carries the tl-open-board testid");
  ok(panelTsx.includes("openBoard: (path) => app.OpenTasklistBoard(path)"), "REAL_BINDINGS routes openBoard to the bridge");
  ok(panelTsx.includes("openBoard(path: string): Promise<TasklistBoardOpenResult>;"), "bindings seam declares openBoard for harness injection");

  const bridgeTs = read("src/lib/bridge.ts");
  ok(bridgeTs.includes("OpenTasklistBoard(path: string): Promise<TasklistBoardOpenResult>;"), "bridge contract declares OpenTasklistBoard");
  ok(bridgeTs.includes("export interface TasklistBoardOpenResult {"), "bridge contract declares TasklistBoardOpenResult");
  const iface = bridgeTs.slice(bridgeTs.indexOf("export interface TasklistBoardOpenResult {"));
  const ifaceBody = iface.slice(0, iface.indexOf("}"));
  ok(["path", "stale", "opened", "error"].every((f) => ifaceBody.includes(f)),
    "contract carries path/stale/opened/error (check-bridge-json-tags TS half)");

  ok(zh["tasklist.board.open"] === "浏览器打开" && zhTW["tasklist.board.open"] === "瀏覽器開啟" && en["tasklist.board.open"] === "Open in browser",
    "trilingual button copy lands in all three locales");
  ok(Boolean(zh["tasklist.board.stale"]) && Boolean(zhTW["tasklist.board.stale"]) && Boolean(en["tasklist.board.stale"]),
    "trilingual stale hint exists in all three locales");
}

process.stdout.write(`\ntl1775: ${passed} passed, ${failed} failed\n`);
process.exit(failed === 0 ? 0 : 1);
