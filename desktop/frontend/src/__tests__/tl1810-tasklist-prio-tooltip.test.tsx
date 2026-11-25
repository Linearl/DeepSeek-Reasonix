// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1810-tasklist-prio-tooltip.test.tsx
// tl1810 验收 harness —— 任务面板优先级标签 hover tooltip：
//  ① 行为面（jsdom + 真 TasklistPanel + 注入 bindings）：
//     - P0-P3（含小写 p3，口径同 tl.py _norm_prio 容差）悬停出定义 tooltip；
//     - 延迟钉 ~300ms：hover 后 ~120ms 不出现，~450ms 出现（不遮行操作的
//       前提=先延迟；真实定时器，非 fake timers）；
//     - 未标标准优先级（空 / 遗留符号 🟡）不出 tooltip、原样展示；
//     - mouseleave 收起；
//     - 总览「未完成按优先级」分布行同款（P1 出，未标不出）；
//     - tooltip 由共享 Tooltip portal 到 body，不进表格 DOM（不撑行高）。
//  ② 三语键齐（zh/en/zh-TW 逐键钉值）。
//
// ⚠ harness 约束（tl1746 同款）：jsdom 必须在 import react-dom 之前装好；
// react/react-dom/组件一律动态 import。

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
const { LocaleProvider } = await import("../lib/i18n");
type TasklistQueryResult = import("../lib/bridge").TasklistQueryResult;
type TasklistOverview = import("../lib/bridge").TasklistOverview;

// React 的 onMouseEnter 委托自 mouseover（relatedTarget=null 表示指针自元素
// 外进入）——tooltip-wrap-command / capsule-panel 同一 dispatch 口径。
function hover(el: Element) {
  el.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
}
// onMouseLeave 委托自 mouseout（relatedTarget 指向新落点=已离开触发元素）。
function leave(el: Element) {
  el.dispatchEvent(new dom.window.MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body }));
}

function tooltips() {
  return document.body.querySelectorAll("[role='tooltip']");
}

const FIXTURE_OVERVIEW: TasklistOverview = {
  path: "D:/fixture/tasklist.db",
  total: 5, pending: 5, done: 0, cancelled: 0,
  byStatus: [{ key: "pending", count: 5 }],
  byDomain: [{ key: "UI与交互", count: 5 }],
  byPriority: [{ key: "P2", count: 1 }, { key: "P0", count: 1 }, { key: "p3", count: 1 }, { key: "", count: 1 }, { key: "🟡", count: 1 }],
  openByDomain: [{ key: "UI与交互", count: 5 }],
  // 总览分布：P1 有定义（出 tooltip）；未标不出。
  openByPriority: [{ key: "P1", count: 2 }, { key: "", count: 1 }],
  generatedAt: "2026-11-25T08:00:00+08:00",
  cached: false,
};

const FIXTURE_LIST: TasklistQueryResult = {
  path: "D:/fixture/tasklist.db",
  total: 5,
  limited: false,
  elapsedMs: 1,
  rows: [
    { num: 1810, title: "P2 行", status: "pending", priority: "P2", domain: "UI与交互", createdAt: "2026-11-25", doneAt: "", leftover: "", blockReason: "", groupTag: "" },
    { num: 1811, title: "P0 行", status: "pending", priority: "P0", domain: "UI与交互", createdAt: "2026-11-25", doneAt: "", leftover: "", blockReason: "", groupTag: "" },
    { num: 1812, title: "小写 p3 行（tl.py 容差口径）", status: "pending", priority: "p3", domain: "UI与交互", createdAt: "2026-11-25", doneAt: "", leftover: "", blockReason: "", groupTag: "" },
    { num: 1813, title: "未标优先级行", status: "pending", priority: "", domain: "UI与交互", createdAt: "2026-11-25", doneAt: "", leftover: "", blockReason: "", groupTag: "" },
    { num: 1814, title: "遗留符號行", status: "pending", priority: "🟡", domain: "UI与交互", createdAt: "2026-11-25", doneAt: "", leftover: "", blockReason: "", groupTag: "" },
  ],
};

function fixtureBindings(): TasklistPanelBindings {
  return {
    getOverview: () => Promise.resolve({ ...FIXTURE_OVERVIEW }),
    queryTasks: () => Promise.resolve({ ...FIXTURE_LIST, rows: FIXTURE_LIST.rows.map((r) => ({ ...r })) }),
    settings: () => Promise.resolve({ tasklistDbPath: "" }),
    openBoard: () => Promise.resolve({ path: "D:/fixture/tasklist.html", stale: false, opened: true, error: "" }),
  };
}

console.log("\ntl1810 tasklist prio tooltip: hover definitions + empty-tooltip guard + locales");

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

await act(async () => {
  root.render(
    <LocaleProvider>
      <TasklistPanel open onClose={() => {}} bindings={fixtureBindings()} />
    </LocaleProvider>,
  );
  await flush();
  await flush();
});

// ── 总览分布行：P1 出定义，未标不出 ─────────────────────────────────
{
  const p1Row = Array.from(document.querySelectorAll(".tlpanel__bar-row"))
    .find((row) => row.querySelector(".tlpanel__bar-label")?.textContent === "P1");
  ok(Boolean(p1Row), "overview renders the P1 distribution row");
  const p1Trigger = p1Row?.querySelector(".tooltip-trigger");
  ok(Boolean(p1Trigger), "P1 bar label is a tooltip trigger");
  await act(async () => {
    if (p1Trigger) hover(p1Trigger);
    await new Promise((resolve) => setTimeout(resolve, 450));
  });
  ok(tooltips().length === 1 && tooltips()[0]?.textContent === en["tasklist.prio.p1"],
    "hovering P1 bar label shows the P1 definition (en fallback in harness)");
  await act(async () => {
    if (p1Trigger) leave(p1Trigger);
    await flush();
  });
  ok(tooltips().length === 0, "mouseleave hides the overview tooltip");

  const unlabeledRow = Array.from(document.querySelectorAll(".tlpanel__bar-row"))
    .find((row) => (row.querySelector(".tlpanel__bar-label")?.textContent ?? "").length > 0
      && !row.querySelector(".tooltip-trigger"));
  ok(Boolean(unlabeledRow), "unlabeled distribution row renders without a tooltip trigger");
}

// ── 列表行 ──────────────────────────────────────────────────────────
await act(async () => {
  (document.querySelector("[data-testid='tl-tab-list']") as HTMLButtonElement)?.click();
  await flush();
  await flush();
});

{
  const triggerIn = (num: number) =>
    document.querySelectorAll(`[data-testid='tl-row-${num}'] .tooltip-trigger`);

  // 空优先级 / 遗留符号：无触发器 = 结构上不可能出空 tooltip（验收③）。
  ok(triggerIn(1813).length === 0, "empty priority row renders '—' with no tooltip trigger");
  ok(document.querySelector("[data-testid='tl-row-1813']")?.textContent?.includes("—") ?? false,
    "empty priority cell keeps the em-dash display");
  ok(triggerIn(1814).length === 0 && (document.querySelector("[data-testid='tl-row-1814']")?.textContent?.includes("🟡") ?? false),
    "legacy symbol row renders raw with no tooltip trigger");

  // 标准优先级：恰好一个触发器（只挂优先级格，不牵连行内其他操作区）。
  for (const num of [1810, 1811, 1812]) {
    ok(triggerIn(num).length === 1, `row ${num} has exactly one tooltip trigger (priority cell only)`);
  }

  // 延迟钉 ~300ms：120ms 未出现 → 450ms 出现（真实定时器）。
  const p2Trigger = triggerIn(1810)[0];
  await act(async () => {
    if (p2Trigger) hover(p2Trigger);
    await new Promise((resolve) => setTimeout(resolve, 120));
  });
  ok(tooltips().length === 0, "~120ms after hover the tooltip has not opened yet (300ms delay honored)");
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 380));
  });
  ok(tooltips().length === 1, "~450ms after hover exactly one tooltip is open");
  ok(tooltips()[0]?.textContent === en["tasklist.prio.p2"], "P2 tooltip carries the P2 definition");
  ok(p2Trigger?.getAttribute("aria-describedby") === tooltips()[0]?.getAttribute("id"),
    "trigger is aria-describedby-linked to the open tooltip");
  ok(!document.querySelector("[data-testid='tl-table'] [role='tooltip']"),
    "tooltip portals to body — not nested inside the table DOM");

  // mouseleave 收起；换行悬停读 P0。
  await act(async () => {
    if (p2Trigger) leave(p2Trigger);
    await flush();
  });
  ok(tooltips().length === 0, "mouseleave hides the row tooltip");

  const p0Trigger = triggerIn(1811)[0];
  await act(async () => {
    if (p0Trigger) hover(p0Trigger);
    await new Promise((resolve) => setTimeout(resolve, 450));
  });
  ok(tooltips().length === 1 && tooltips()[0]?.textContent === en["tasklist.prio.p0"],
    "P0 row tooltip carries the P0 definition");

  // 小写 p3（tl.py trim+upper 容差口径）→ 出 P3 定义。
  await act(async () => {
    if (p0Trigger) leave(p0Trigger);
    await flush();
  });
  const p3Trigger = triggerIn(1812)[0];
  await act(async () => {
    if (p3Trigger) hover(p3Trigger);
    await new Promise((resolve) => setTimeout(resolve, 450));
  });
  ok(tooltips().length === 1 && tooltips()[0]?.textContent === en["tasklist.prio.p3"],
    "lowercase p3 is recognized (tl.py tolerance) and shows the P3 definition");
  await act(async () => {
    if (p3Trigger) leave(p3Trigger);
    await flush();
  });
}

await act(async () => { root.unmount(); });
dom.window.close();

// ── 三语键齐（逐键钉值；漂移即红）───────────────────────────────────
{
  ok(zh["tasklist.prio.p0"] === "阻断级：立即处理", "zh P0 definition");
  ok(zh["tasklist.prio.p1"] === "高优：本批必排", "zh P1 definition");
  ok(zh["tasklist.prio.p2"] === "常规：计划内", "zh P2 definition");
  ok(zh["tasklist.prio.p3"] === "低优：可缓", "zh P3 definition");
  ok(en["tasklist.prio.p0"] === "Blocker: handle immediately", "en P0 definition");
  ok(en["tasklist.prio.p1"] === "High: must land this batch", "en P1 definition");
  ok(en["tasklist.prio.p2"] === "Regular: planned work", "en P2 definition");
  ok(en["tasklist.prio.p3"] === "Low: can wait", "en P3 definition");
  ok(zhTW["tasklist.prio.p0"] === "阻斷級：立即處理", "zh-TW P0 definition");
  ok(zhTW["tasklist.prio.p1"] === "高優先：本批必排", "zh-TW P1 definition");
  ok(zhTW["tasklist.prio.p2"] === "常規：計劃內", "zh-TW P2 definition");
  ok(zhTW["tasklist.prio.p3"] === "低優先：可緩", "zh-TW P3 definition");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
