// Run: tsx src/__tests__/context-session-metrics-fold.test.tsx
//
// tl1500 验收钉（子②）：右侧栏概览「会话指标」卡折叠——
//   ① 纯偏好层：默认展开；置折叠 → localStorage 单键 "collapsed"；
//      置展开 → "expanded"；损坏值回退默认（lib/sessionMetricsFold.ts，
//      形态照 toolGroupingPreference 先例）。
//   ② 渲染层：chevron 折叠头 aria-expanded 翻转、卡片本体随折叠显隐、
//      重挂载从 localStorage 恢复（持久化恢复钉）。
//
// 本文件自带 jsdom 与 window.go 代理桩（所有 App 方法回安全默认值），
// bridge/面板内部的只读调用一律可解析；ContextPanel 仍走 bridge mock 世界
// 时同样无害。非 bridge 噪声拒绝会被记录并在尾部汇报。

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
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
});
class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = TestResizeObserver;

// 上下文面板读数桩：给会话指标卡一份真实感数据（展开态可顺带钉数值行）。
const panelInfo = {
  usedTokens: 1200, windowTokens: 8000, promptTokens: 900, completionTokens: 300,
  totalTokens: 1200, reasoningTokens: 0, cacheHitTokens: 700, cacheMissTokens: 300,
  sessionCacheHitTokens: 700, sessionCacheMissTokens: 300, sessionCompletionTokens: 300,
  requestCount: 7, elapsedMs: 81_000,
  readFiles: [], changedFiles: [],
};
(dom.window as unknown as { go?: unknown }).go = {
  main: {
    App: new Proxy({}, {
      get: (_target, prop) => () => {
        if (prop === "ContextPanel") return Promise.resolve(panelInfo);
        return Promise.resolve(null);
      },
    }),
  },
};

const unhandled: unknown[] = [];
process.on("unhandledRejection", (reason) => { unhandled.push(reason); });

// 全局就位后再拉组件（bridge 在调用期读 window.go；即便走 mock 世界也安全）。
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ContextPanel } = await import("../components/ContextPanel");
const {
  getSessionMetricsCollapsed,
  setSessionMetricsCollapsed,
  resetSessionMetricsFoldForTests,
} = await import("../lib/sessionMetricsFold");

const STORAGE_KEY = "reasonix-session-metrics-fold";

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

// ── ① 纯偏好层 ──
resetSessionMetricsFoldForTests();
ok(getSessionMetricsCollapsed() === false, "missing storage defaults to expanded (default experience unchanged)");
ok(localStorage.getItem(STORAGE_KEY) === null, "the default state writes nothing to storage");

setSessionMetricsCollapsed(true);
ok(getSessionMetricsCollapsed() === true, "collapsing flips the in-memory preference");
ok(localStorage.getItem(STORAGE_KEY) === "collapsed", "collapsing persists the single collapsed key");

setSessionMetricsCollapsed(false);
ok(localStorage.getItem(STORAGE_KEY) === "expanded", "re-expanding persists the expanded value");
ok(getSessionMetricsCollapsed() === false, "re-expanding flips the in-memory preference back");

resetSessionMetricsFoldForTests();
localStorage.setItem(STORAGE_KEY, "collapsed");
ok(getSessionMetricsCollapsed() === true, "a stored collapsed value hydrates on first read");

resetSessionMetricsFoldForTests();
localStorage.setItem(STORAGE_KEY, "garbage");
ok(getSessionMetricsCollapsed() === false, "a corrupted storage value falls back to expanded");

resetSessionMetricsFoldForTests();
localStorage.removeItem(STORAGE_KEY);

// ── ② 渲染层 ──
// React 19：root.unmount() 后同一 root 实例不可再 render，同容器重建 root
// 也会告警——每次挂载前重建容器节点 + 新建 root，精确模拟「关面板再开面板」。
let root: ReturnType<typeof createRoot> | null = null;
async function mountPanel() {
  document.body.innerHTML = "<div id=\"root\"></div>";
  root = createRoot(document.getElementById("root")!);
  await act(async () => root!.render(<LocaleProvider><ContextPanel tabId="tab-1" /></LocaleProvider>));
}
const section = () => document.querySelector<HTMLElement>(".context-panel__session-section");
const foldButton = () => section()?.querySelector<HTMLButtonElement>("button.context-panel__fold");
const metricsBody = () => section()?.querySelector<HTMLElement>(".context-panel__session-metrics");

await mountPanel();
ok(section() !== null, "the session metrics card renders on the overview page");
ok(foldButton()?.getAttribute("aria-expanded") === "true", "the fold head ships expanded (aria-expanded=true)");
ok(metricsBody() !== null, "the expanded card shows the metrics body");
ok(metricsBody()?.textContent?.includes("1m 21s") === true, "the expanded card renders the elapsed readout from the panel fixture");

// 折叠交互：本体消失、aria 翻转、单键落盘。
await act(async () => { foldButton()?.click(); });
ok(foldButton()?.getAttribute("aria-expanded") === "false", "clicking the chevron flips aria-expanded to false");
ok(metricsBody() === null, "the collapsed card removes its metrics body");
ok(localStorage.getItem(STORAGE_KEY) === "collapsed", "the interaction persists the collapsed state");

// 持久化恢复：卸载重挂，折叠态从 localStorage 恢复。
await act(async () => root.unmount());
await mountPanel();
ok(foldButton()?.getAttribute("aria-expanded") === "false", "a remount restores the collapsed state from storage");
ok(metricsBody() === null, "the restored-collapsed card still hides its metrics body");

// 再展开回默认值形态。
await act(async () => { foldButton()?.click(); });
ok(foldButton()?.getAttribute("aria-expanded") === "true", "expanding again flips aria-expanded back to true");
ok(metricsBody() !== null, "expanding again shows the metrics body");
ok(localStorage.getItem(STORAGE_KEY) === "expanded", "the expanded state is persisted for the next mount");

await act(async () => root.unmount());
dom.window.close();

const noise = unhandled.filter((reason) => !(reason instanceof Error && /ContextPanel|PlanUsage|OpenCodeGo|bridge/i.test(String((reason as Error).message))));
console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total${noise.length ? `, ${noise.length} unhandled bridge rejections` : ""}`);
if (failed > 0) process.exit(1);
if (noise.length > 0) {
  for (const reason of noise) console.error("  unhandled rejection:", reason);
  process.exit(1);
}
