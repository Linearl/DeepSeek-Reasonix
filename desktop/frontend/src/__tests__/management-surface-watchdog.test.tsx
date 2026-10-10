// 任务 739：ManagementSurface 的 Suspense 挂起看门狗（12s 静默升级为可操作失败态）。
// 契约：
// ① 分包按时就绪 → 正常渲染，看门狗到点不得把已就绪页面翻成失败态（settled 锁存）；
// ② 分包一直挂起 → 看门狗到点出现失败提示 + 重试按钮；
// ③ 重试后分包就绪 → 立即恢复渲染。
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM('<!doctype html><div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, Event: dom.window.Event, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

// 捕获 setTimeout：看门狗的 12000ms 定时器由测试手动触发（虚拟时间）。
const scheduled: Array<{ ms: number; fn: () => void }> = [];
const realSetTimeout = dom.window.setTimeout.bind(dom.window);
(dom.window as unknown as { setTimeout: typeof setTimeout }).setTimeout = ((fn: () => void, ms = 0) => {
  const entry = { ms, fn };
  scheduled.push(entry);
  return realSetTimeout(() => {}, ms) as unknown as number;
}) as unknown as typeof setTimeout;
const fireTimers = (ms: number) => {
  const due = scheduled.filter((entry) => entry.ms >= ms);
  for (const entry of due) entry.fn();
  return due.length;
};

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ManagementSurface } = await import("../components/ManagementSurface");
const { LocaleProvider } = await import("../lib/i18n");

const root = createRoot(document.getElementById("root")!);
// 首次调用永挂起（模拟主线程饿死下分包解不了），重试时的再次调用立即就绪
// ——组件在重试时会重新执行 loader 造新 promise，这是被测契约的一部分。
let loaderCalls = 0;
const gatedLoader = () => {
  loaderCalls += 1;
  if (loaderCalls === 1) return new Promise<{ default: React.ComponentType }>(() => {});
  return Promise.resolve({ default: () => <div id="surface-view">surface ready</div> });
};
const instantLoader = () => Promise.resolve({ default: () => <div id="surface-view">surface ready</div> });

async function renderSurface(loader: () => Promise<{ default: React.ComponentType }>) {
  await act(async () => {
    root.render(<LocaleProvider><ManagementSurface loader={loader} surfaceProps={{}} active onBack={() => {}} /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); });
}
const view = () => document.getElementById("surface-view");
const stallFallback = () => document.querySelector('section[aria-label] p[role="alert"]');
const loadingStatus = () => document.querySelector('p[role="status"]');

// ① 分包一直挂起：先停在加载态，看门狗到点升级为失败态（含重试按钮）
await renderSurface(gatedLoader);
assert.ok(loadingStatus(), "suspended surface shows loading status");
assert.equal(stallFallback(), null, "no failure alert before the watchdog fires");
let fired = 0;
await act(async () => { fired = fireTimers(12_000); });
assert.equal(fired, 1, "watchdog schedules exactly one stall timer");
await act(async () => { await Promise.resolve(); });
assert.ok(stallFallback(), "watchdog escalates a stalled surface to the failure fallback");
assert.ok(document.querySelector("button.btn--secondary"), "failure fallback offers retry");

// ③ 重试：loader 再次调用即就绪 → 立即恢复渲染
const retry = document.querySelector<HTMLButtonElement>("button.btn--secondary")!;
await act(async () => { retry.click(); });
await act(async () => { await Promise.resolve(); await Promise.resolve(); });
assert.equal(loaderCalls, 2, "retry re-invokes the loader");
assert.ok(view(), "retry after the chunk resolved renders the surface immediately");
assert.equal(stallFallback(), null, "recovered surface shows no failure alert");

// ① 已就绪页面上看门狗到点不得误杀（settled 锁存）
await act(async () => { fireTimers(12_000); await Promise.resolve(); });
assert.ok(view(), "watchdog firing after the surface settled must not tear down the rendered view");
assert.equal(stallFallback(), null, "settled surface never shows the watchdog failure fallback");

// ② 换一个立刻就绪的 loader：无挂起、无看门狗介入
await renderSurface(instantLoader);
assert.ok(view(), "instant loader renders without suspension");
fireTimers(12_000);
await act(async () => { await Promise.resolve(); });
assert.ok(view(), "watchdog is harmless for an already-ready surface");

console.log("PASS ManagementSurface watchdog: stall escalation, retry recovery, settled latch, no false kill");
