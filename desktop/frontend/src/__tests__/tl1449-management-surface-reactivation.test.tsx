// tl1449：回收站「清空后页面加载失败」——ManagementSurface 看门狗的 settled 锁存在
// 再次进入页面时被重置、却没有人再把它置回（探针只在挂载时报一次），12s 后已渲染
// 的页面被误翻成「设置加载失败。+重试」，整页卸载（含清空确认框，清空因此从未执行）。
// 契约：
// ① 已就绪页面离开再进入（active false→true），看门狗到点不得误杀，页面实例不重挂载；
// ② 失败态重试恢复后再离开/进入，同样不得误杀；
// ③ 误杀以外的真失败（渲染抛错）必须落前端日志（SurfaceBoundary 不再静默吞错）；
// ④ 真挂起（分包从未就绪）照旧升级失败态，且升级本身落日志。
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM('<!doctype html><div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, Event: dom.window.Event, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

// 捕获 setTimeout：看门狗的 12000ms 定时器由测试手动触发（虚拟时间）。
const scheduled: Array<{ ms: number; fn: () => void; cancelled: boolean }> = [];
const realSetTimeout = dom.window.setTimeout.bind(dom.window);
const realClearTimeout = dom.window.clearTimeout.bind(dom.window);
const handles = new Map<number, (typeof scheduled)[number]>();
(dom.window as unknown as { setTimeout: typeof setTimeout }).setTimeout = ((fn: () => void, ms = 0) => {
  const entry = { ms, fn, cancelled: false };
  scheduled.push(entry);
  const handle = realSetTimeout(() => {}, ms) as unknown as number;
  handles.set(handle, entry);
  return handle;
}) as unknown as typeof setTimeout;
(dom.window as unknown as { clearTimeout: typeof clearTimeout }).clearTimeout = ((handle: number) => {
  const entry = handles.get(handle);
  if (entry) entry.cancelled = true;
  realClearTimeout(handle);
}) as unknown as typeof clearTimeout;
// 只触发尚未取消的看门狗定时器——与真实浏览器一致（已清除的定时器不会再跑）。
const fireWatchdog = () => {
  const due = scheduled.filter((entry) => entry.ms >= 12_000 && !entry.cancelled);
  for (const entry of due) { entry.cancelled = true; entry.fn(); }
  return due.length;
};

const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const { ManagementSurface } = await import("../components/ManagementSurface");
const { LocaleProvider } = await import("../lib/i18n");
// 前端日志走既有 reportFrontendLog 的内存环（网关断连也留痕的那一路），测试从环里取证。
const { drainFrontendLogRing } = await import("../lib/frontendLog");
const surfaceLogs = () => drainFrontendLogRing().filter((line) => line.startsWith("[frontend:management-surface]"));

const root = createRoot(document.getElementById("root")!);
let mounts = 0;
function StatefulView() {
  // 挂载计数：页面实例若被看门狗卸载再重建，计数会增加（清空确认框等组件内状态随之丢失）。
  React.useEffect(() => { mounts += 1; }, []);
  return <div id="surface-view">surface ready</div>;
}
const instantLoader = () => Promise.resolve({ default: StatefulView });

async function render(active: boolean, loader: () => Promise<{ default: React.ComponentType }> = instantLoader) {
  await act(async () => {
    root.render(<LocaleProvider><ManagementSurface loader={loader} surfaceProps={{}} active={active} onBack={() => {}} /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); await Promise.resolve(); });
}
const view = () => document.getElementById("surface-view");
const failureAlert = () => document.querySelector('section[aria-label] p[role="alert"]');

// ① 首次进入就绪 → 离开 → 再进入 → 看门狗到点：页面保持、实例不重挂载
await render(true);
assert.ok(view(), "first visit renders the surface");
assert.equal(mounts, 1, "surface mounted once on first visit");
await act(async () => { fireWatchdog(); await Promise.resolve(); });
assert.ok(view(), "settled surface survives the first-visit watchdog");

console.log("CP1 before render(false)", scheduled.length); await render(false);
console.log("CP2 before render(true)", scheduled.length); await render(true);
console.log("CP3 after render(true)", scheduled.length);
assert.ok(view(), "re-entered surface is still rendered");
let fired = 0;
console.log("CP4 before fire");
await act(async () => { fired = fireWatchdog(); await Promise.resolve(); });
console.log("CP5 after fire", fired);
await act(async () => { await Promise.resolve(); });
assert.equal(failureAlert(), null, `re-entered settled surface must not flip to the load-failed fallback (watchdog timers fired: ${fired})`);
assert.ok(view(), "re-entered settled surface keeps rendering after the watchdog deadline");
assert.equal(mounts, 1, "re-entry must not remount the surface instance (in-page state such as an open confirm dialog survives)");

// 再来一轮：多次离开/进入同样不误杀
await render(false);
await render(true);
await act(async () => { fireWatchdog(); await Promise.resolve(); });
assert.equal(failureAlert(), null, "second re-entry stays healthy");
assert.equal(mounts, 1, "second re-entry keeps the same instance");

// ④ 真挂起：分包从未就绪 → 看门狗升级失败态，且升级落日志
await act(async () => { root.unmount(); });
const root2 = createRoot(document.getElementById("root")!);
let gatedCalls = 0;
const gatedLoader = () => {
  gatedCalls += 1;
  if (gatedCalls === 1) return new Promise<{ default: React.ComponentType }>(() => {});
  return Promise.resolve({ default: StatefulView });
};
const render2 = async (active: boolean) => {
  await act(async () => {
    root2.render(<LocaleProvider><ManagementSurface loader={gatedLoader} surfaceProps={{}} active={active} onBack={() => {}} /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); await Promise.resolve(); });
};
drainFrontendLogRing();
await render2(true);
await act(async () => { fireWatchdog(); await Promise.resolve(); });
await act(async () => { await Promise.resolve(); });
assert.ok(failureAlert(), "a genuinely stalled surface still escalates to the failure fallback");
const stallLogs = surfaceLogs();
assert.ok(stallLogs.some((line) => /stall/.test(line)), `stall escalation must be logged (got ${JSON.stringify(stallLogs)})`);

// ② 重试恢复后离开/进入，看门狗不误杀
mounts = 0;
const retry = document.querySelector<HTMLButtonElement>("button.btn--secondary")!;
await act(async () => { retry.click(); });
await act(async () => { await Promise.resolve(); await Promise.resolve(); });
assert.ok(view(), "retry recovers the surface");
await render2(false);
await render2(true);
await act(async () => { fireWatchdog(); await Promise.resolve(); });
assert.equal(failureAlert(), null, "recovered surface survives re-entry + watchdog");
assert.ok(view(), "recovered surface still rendered after re-entry");
await act(async () => { root2.unmount(); });

// ③ 渲染抛错：错误边界展示失败态，并把错误落前端日志（不再静默）
const root3 = createRoot(document.getElementById("root")!);
function Exploding(): React.ReactElement { throw new Error("boom: trash list row"); }
const explodingLoader = () => Promise.resolve({ default: Exploding });
drainFrontendLogRing();
const originalConsoleError = console.error;
console.error = () => {};
try {
  await act(async () => {
    root3.render(<LocaleProvider><ManagementSurface loader={explodingLoader} surfaceProps={{}} active onBack={() => {}} /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); await Promise.resolve(); });
} finally { console.error = originalConsoleError; }
assert.ok(failureAlert(), "render crash shows the failure fallback");
const crashLogs = surfaceLogs();
const crashLog = crashLogs.find((line) => /render/.test(line));
assert.ok(crashLog, `render crash must be logged (got ${JSON.stringify(crashLogs)})`);
assert.match(crashLog!, /boom: trash list row/, "crash log carries the error message");
await act(async () => { root3.unmount(); });

console.log("PASS tl1449 ManagementSurface: re-entry keeps settled latch (no false kill/remount), stall + render crash are logged");
