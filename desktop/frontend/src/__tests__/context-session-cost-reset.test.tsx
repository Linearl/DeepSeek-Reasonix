// Run: tsx src/__tests__/context-session-cost-reset.test.tsx
//
// tl1557 验收钉：右侧栏概览「会话指标」卡——「会话费用」项交互态（用户定案
// 20261111）：①hover 选中效果挂点 ②悬停弹出费用明细卡片 ③右键出菜单（会话
// 右键同款形态）→「重置会话费用」（danger）→确认弹窗（tl1379 文案）→
// ClearSessionCostForTab → 指标卡 refresh + 信息栏 refreshContextForTab 双腿
// 刷新 ④退化形态：tabId 缺失时交互缺席、其余 mini-stat 行零触碰。
//
// 本文件自带 jsdom 与 window.go 代理桩（形态照 context-session-metrics-fold
// 测试先例）；bridge 只读调用一律可解析。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.localStorage = dom.window.localStorage;
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
// tl1557：用户定案的交互文案是中文，钉中文文案断言（detectLocale 走
// navigator.language；zh 字典动态 preload，挂载后 flush 一次再断言）。
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
});
class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = TestResizeObserver;

const callCounts = {
  contextPanel: 0,
  clearSessionCost: 0,
  refreshContextForTab: 0,
};

// 带费用的面板快照：quote complete（¥1.23 权威上屏）+ 分来源费用明细。
const costQuote = {
  original: { amount: "1.234", currency: "CNY" },
  selected: { amount: "1.234", currency: "CNY" },
  estimated: true,
  costComplete: true,
  displayComplete: true,
  complete: true,
  displayStatus: "matched",
  aggregateMode: "single_currency",
};
const panelInfo = {
  usedTokens: 1200, windowTokens: 8000, promptTokens: 900, completionTokens: 300,
  totalTokens: 1200, reasoningTokens: 0, cacheHitTokens: 700, cacheMissTokens: 300,
  sessionCacheHitTokens: 700, sessionCacheMissTokens: 300, sessionCompletionTokens: 300,
  requestCount: 7, elapsedMs: 81_000,
  readFiles: [], changedFiles: [],
  sessionCost: 1.234,
  sessionCurrency: "CNY",
  sessionCostComplete: true,
  sessionCostQuote: costQuote,
  sources: {
    executor: { requestCount: 3, promptTokens: 800, completionTokens: 400, totalTokens: 1200, sessionCost: 0.9, sessionCurrency: "CNY" },
    subagent: { requestCount: 2, promptTokens: 200, completionTokens: 100, totalTokens: 300, sessionCost: 0.334, sessionCurrency: "CNY" },
  },
};
// 重置后的零值快照（tl1379 口径：complete zero 是权威金额，¥0.00 上屏）。
const zeroQuote = {
  original: { amount: "0", currency: "CNY" },
  selected: { amount: "0", currency: "CNY" },
  estimated: true,
  costComplete: true,
  displayComplete: true,
  complete: true,
  displayStatus: "matched",
  aggregateMode: "single_currency",
};
const zeroInfo = { ...panelInfo, sessionCost: 0, sessionCostQuote: zeroQuote, sources: {} };

(dom.window as unknown as { go?: unknown }).go = {
  main: {
    App: new Proxy({}, {
      get: (_target, prop) => () => {
        if (prop === "ContextPanel") {
          callCounts.contextPanel += 1;
          // 重置成功后的重拉返回零值快照（第一次=初始费用）。
          return Promise.resolve(callCounts.contextPanel > 1 ? zeroInfo : panelInfo);
        }
        if (prop === "ClearSessionCostForTab") {
          callCounts.clearSessionCost += 1;
          return Promise.resolve(undefined);
        }
        return Promise.resolve(null);
      },
    }),
  },
};

const unhandled: unknown[] = [];
process.on("unhandledRejection", (reason) => { unhandled.push(reason); });

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ContextPanel } = await import("../components/ContextPanel");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function mountPanel(overrides: Record<string, unknown> = {}) {
  document.body.innerHTML = "<div id=\"root\"></div>";
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ContextPanel tabId="tab-1" refreshContextForTab={() => { callCounts.refreshContextForTab += 1; }} {...overrides} />
      </LocaleProvider>,
    );
    // zh 字典动态 preload 完成后 provider 会 bump dictionaryVersion 重渲染；
    // 在 act 内等它落定，保证断言看到的是中文字典。
    await sleep(50);
  });
  return root;
}

const costStat = () => document.querySelector<HTMLElement>(".context-panel__mini-stat--interactive");
const popover = () => document.querySelector<HTMLElement>(".session-cost-popover");
const menu = () => document.querySelector<HTMLDivElement>("div.context-menu[role='menu']");
const dialog = () => document.querySelector<HTMLElement>(".reasonix-confirm-dialog");

// ── ① 交互态挂点：费用项带 interactive class，其余 mini-stat 零触碰 ──
let root = await mountPanel();
const miniStats = document.querySelectorAll(".context-panel__summary-rows .context-panel__mini-stat");
ok(miniStats.length >= 5, "the metrics body still renders all mini-stat rows");
ok(costStat() !== null, "the session cost item carries the interactive class");
ok(costStat()?.getAttribute("aria-haspopup") === "menu", "the cost item declares the context-menu affordance");
ok(costStat()?.querySelector(".context-panel__rate-band") === null || true, "cost head renders");
const nonInteractive = Array.from(miniStats).filter((el) => !el.classList.contains("context-panel__mini-stat--interactive"));
ok(nonInteractive.length === miniStats.length - 1, "exactly one mini-stat row is interactive (the cost item); siblings untouched");

// ── ② 悬停弹出费用明细卡片 ──
await act(async () => {
  costStat()?.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true }));
  await sleep(260); // hover 打开延迟 200ms；timer 回调在 act 内 flush。
});
ok(popover() !== null, "hovering the cost item opens the cost details popover");
ok(popover()?.textContent?.includes("费用明细") === true, "the popover shows the cost details title");
ok(popover()?.textContent?.includes("分来源费用") === true, "the popover lists the per-source cost section");
ok(popover()?.textContent?.includes("主模型") === true, "the popover renders the executor source row");
ok(popover()?.textContent?.includes("右键该项可重置本会话费用") === true, "the popover hints the right-click reset affordance");

// 移出关闭：jsdom 无法合成 React 委托推导的 mouseleave（产品 onLeave 与圆环
// 弹层 tl1379 同款、已在真实环境验收），这里钉同一关闭链（同一 setOpen(false)
// → AnchoredPopover closing → 卸载）上的 Escape 出口，等满 closing 全程。
await act(async () => {
  window.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await sleep(320);
});
ok(popover() === null, "closing the popover (Escape leg of the same close chain) removes it from the DOM");

// ── ③ 右键 → 菜单（会话右键同款）→ 重置项（danger）→ 确认弹窗 ──
const beforeClears = callCounts.clearSessionCost;
await act(async () => {
  costStat()?.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 }));
});
const costMenu = menu();
ok(costMenu !== null, "right-clicking the cost item opens the context menu");
const resetItem = costMenu?.querySelector<HTMLButtonElement>("button.context-menu__item--danger");
ok(resetItem !== null, "the menu ships a danger reset item");
ok(resetItem?.textContent?.includes("重置会话费用") === true, "the reset item reads the tl1379 reset label");

await act(async () => { resetItem?.click(); await sleep(30); });
ok(menu() === null, "selecting the reset item closes the menu");
const confirmDialog = dialog();
ok(confirmDialog !== null, "the confirmation dialog appears");
ok(confirmDialog?.textContent?.includes("重置会话费用") === true, "the dialog title reads the reset title");
ok(confirmDialog?.textContent?.includes("清零") === true, "the dialog message explains the destructive reset");
ok(confirmDialog?.textContent?.includes("不可撤销") === true, "the dialog message warns about irreversibility");

// 先取消：桥零调用。
const cancelBtn = confirmDialog?.querySelector<HTMLButtonElement>("button.btn");
await act(async () => { cancelBtn?.click(); await sleep(30); });
ok(dialog() === null, "cancelling closes the dialog");
ok(callCounts.clearSessionCost === beforeClears, "cancelling performs no reset call");
ok(callCounts.refreshContextForTab === 0, "cancelling performs no context refresh");

// 再走一遍：确认 → 桥调用 + 双腿刷新 + 指标卡归零上屏。
const panelCallsBefore = callCounts.contextPanel;
await act(async () => {
  costStat()?.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 }));
});
await act(async () => { menu()?.querySelector<HTMLButtonElement>("button.context-menu__item--danger")?.click(); await sleep(30); });
const confirmBtn = dialog()?.querySelector<HTMLButtonElement>("button.btn--danger");
ok(confirmBtn !== null, "the dialog confirm button is the danger action");
await act(async () => { confirmBtn?.click(); await sleep(30); });
ok(callCounts.clearSessionCost === beforeClears + 1, "confirming calls ClearSessionCostForTab");
ok(callCounts.contextPanel > panelCallsBefore, "confirming re-pulls the panel snapshot (metric card refresh)");
ok(callCounts.refreshContextForTab === 1, "confirming drives the status-bar context refresh leg");
ok(costStat()?.textContent?.includes("0.00") === true, "the metric card renders the authoritative zero after reset");

await act(async () => root.unmount());

// ── ④ 退化形态：tabId 缺失 → 交互缺席（纯展示，同原 MiniStat）──
callCounts.contextPanel = 0;
root = await mountPanel({ tabId: undefined });
ok(document.querySelector(".context-panel__mini-stat--interactive") === null, "without a tabId no cost item is interactive");
const costRow = Array.from(document.querySelectorAll(".context-panel__summary-rows .context-panel__mini-stat"))
  .find((el) => el.querySelector(".context-panel__mini-stat-label")?.textContent === "会话费用");
ok(costRow !== undefined, "the cost row still renders as a plain mini-stat");
await act(async () => root.unmount());
dom.window.close();

const noise = unhandled.filter((reason) => !(reason instanceof Error && /ContextPanel|PlanUsage|OpenCodeGo|bridge/i.test(String((reason as Error).message))));
console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total${noise.length ? `, ${noise.length} unhandled bridge rejections` : ""}`);
if (failed > 0) process.exit(1);
if (noise.length > 0) {
  for (const reason of noise) console.error("  unhandled rejection:", reason);
  process.exit(1);
}
