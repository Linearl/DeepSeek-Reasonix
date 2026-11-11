// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1543-startup-degrade-ux.test.tsx
//
// tl1543 回归钉（加载面接启动目录扫描降级 UX）：
//   验收① 慢启动显示降级态文案（非裸遮罩/非冻结）——Go budget-out 事件
//     （mode=deferred_to_async_queue）→ store 锁存 → splash/树横幅出用户可读
//     文案 + 重试按钮（组件级 jsdom 渲染 + lib 行为）；
//   验收② 正常启动零变化——负钉：无事件时 splash 的 DOM 形态与引入前逐字节
//     等价（splashSubtitle + 三点动画、无 degraded 标记、无重试按钮），树横幅
//     分支源码钉原样保留；
//   验收②/③ 通道与重试——重试走既有 RequestReconcile 通道（Go
//     RetryStartupCatalogScan → requestProjectTreeCatalogRefresh 同族原语 +
//     侧栏树事件），事件桥复用 emitRuntimeEvent/EventsOn，无新桥生成物；
//   验收④ 三语——zh/zh-TW/en 键集齐备（Record<DictKey,string> 由 tsc 编译期
//     强制，这里再显式断言三文件键在场且非空）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else { failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

const here = dirname(fileURLToPath(import.meta.url));
const srcRoot = resolve(here, "..");
const desktopRoot = resolve(srcRoot, "../..");

// ── A. lib 行为：事件锁存 + 重试走既有通道 ────────────────────────────────────
console.log("\ntl1543 A. degrade event latch and retry channel (lib behaviour)");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
(globalThis as unknown as { Node: unknown }).Node = dom.window.Node;
(globalThis as unknown as { HTMLElement: unknown }).HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

type Subscription = { event: string; callback: (...args: unknown[]) => void };
const subscriptions: Subscription[] = [];
const retryCalls: number[] = [];
(window as unknown as { runtime: unknown }).runtime = {
  EventsOn: (event: string, callback: (...args: unknown[]) => void) => {
    subscriptions.push({ event, callback });
    return () => {};
  },
};
(window as unknown as { go: unknown }).go = {
  main: { App: { RetryStartupCatalogScan: () => { retryCalls.push(1); return Promise.resolve(); } } },
};

const { installStartupDegradeListener, retryStartupCatalogScan, STARTUP_CATALOG_DEGRADED_EVENT } = await import("../lib/startupDegradeBridge");
const { useStartupDegradeStore } = await import("../store/startupDegrade");

// 负钉前置：初始态零降级。
eq(useStartupDegradeStore.getState().degraded, false, "the degrade store starts unlatched");
eq(useStartupDegradeStore.getState().deferredDirs, 0, "the degrade store starts with zero deferred dirs");

// 事件桥：安装即订阅命名事件。
const unsubscribe = installStartupDegradeListener();
ok(subscriptions.some((s) => s.event === STARTUP_CATALOG_DEGRADED_EVENT && s.callback instanceof Function),
  `installStartupDegradeListener subscribes to "${STARTUP_CATALOG_DEGRADED_EVENT}" (existing runtime event bridge)`);

// 验收①正钉：预算打满事件 → 锁存。
const degradeSubscription = subscriptions.find((s) => s.event === STARTUP_CATALOG_DEGRADED_EVENT)!;
degradeSubscription.callback({ deferred: 2, mode: "deferred_to_async_queue" });
eq(useStartupDegradeStore.getState().degraded, true, "a budget-out event latches the degrade store");
eq(useStartupDegradeStore.getState().deferredDirs, 2, "the event's deferred count lands in the store");

// 重试成功 → 既有通道一次调用 + 锁存清除（横幅消失，常规 notice 接管）。
await retryStartupCatalogScan();
eq(retryCalls.length, 1, "retry delegates to the existing RetryStartupCatalogScan binding (RequestReconcile channel family)");
eq(useStartupDegradeStore.getState().degraded, false, "a settled retry clears the latch so regular notices take over");

// 重试失败 → 锁存保留（用户可再试或走既有重载/重建入口），不抛未处理拒绝。
degradeSubscription.callback({ deferred: 3 });
(window as unknown as { go: { main: { App: Record<string, () => Promise<void>> } } }).go.main.App.RetryStartupCatalogScan =
  () => Promise.reject(new Error("channel busy"));
await retryStartupCatalogScan();
eq(useStartupDegradeStore.getState().degraded, true, "a failed retry keeps the latch up (retry again or use the existing doors)");

// 畸形 payload（缺 deferred 数值）仍锁存（降级事实本身不依赖计数）。
useStartupDegradeStore.getState().clearDegraded();
degradeSubscription.callback({});
eq(useStartupDegradeStore.getState().degraded, true, "a malformed payload still latches the degrade fact (deferred count optional)");
useStartupDegradeStore.getState().clearDegraded();
unsubscribe();

// ── B. 组件级：splash 三态（jsdom 真渲染） ───────────────────────────────────
console.log("\ntl1543 B. StartupSplash three-state degrade UX (component)");

import { LocaleProvider } from "../lib/i18n";
import { StartupSplash } from "../components/StartupSplash";
const { en } = await import("../locales/en");

const TIMING = { minVisibleMs: 10, fadeOutMs: 20, maxHoldMs: 5000 };
let root: Root | null = null;
const container = document.getElementById("root") as HTMLElement | null;

async function renderSplash(onDone: () => void) {
  await act(async () => {
    root = createRoot(container!);
    root.render(<LocaleProvider><StartupSplash hold={true} onDone={onDone} timing={TIMING} /></LocaleProvider>);
  });
}
async function unmountSplash() {
  if (!root) return;
  await act(async () => { root.unmount(); });
  root = null;
}

const splashSubText = () => document.querySelector(".startup-splash__sub")?.textContent ?? null;

{
  // 验收②负钉：无事件 → 与引入前逐字节等价的形态（splashSubtitle + 三点 + 无降级标记）。
  let done = 0;
  await renderSplash(() => { done += 1; });
  const el = document.querySelector(".startup-splash");
  ok(Boolean(el), "the splash renders on mount");
  eq(splashSubText(), en["app.splashSubtitle"], "state ① normal loading keeps the exact splashSubtitle copy");
  ok(document.querySelectorAll(".startup-splash__dots span").length === 3, "state ① keeps the three pulse dots");
  ok(document.querySelector(".startup-splash__sub[data-degraded]") === null, "state ① paints no degraded marker on the subtitle");
  ok(document.querySelector(".startup-splash__retry") === null, "state ① paints no retry affordance");
  await unmountSplash();
  ok(done === 0, "baseline splash did not dismiss by itself within the hold");

  // 验收①正钉：事件抵达（splash 挂载期间）→ 状态②③：降级文案 + 重试按钮，点动画让位。
  const splashSubs = subscriptions.filter((s) => s.event === STARTUP_CATALOG_DEGRADED_EVENT);
  ok(splashSubs.length >= 1, "the mounted splash also subscribed to the degrade event");
  await renderSplash(() => { done += 1; });
  const latestSub = subscriptions[subscriptions.length - 1]!;
  latestSub.callback({ deferred: 1 });
  await act(async () => { await sleep(0); });
  eq(splashSubText(), en["projectTree.scanDegraded"], "state ② swaps the subtitle to the exact degrade copy (background scan, UI usable)");
  ok(document.querySelector(".startup-splash__sub[data-degraded='true']") !== null, "state ② marks the degraded subtitle");
  ok(document.querySelectorAll(".startup-splash__dots span").length === 0, "state ② retires the pulse dots (not a bare spinner anymore)");
  const retryButton = document.querySelector(".startup-splash__retry") as HTMLButtonElement | null;
  ok(retryButton !== null, "state ③ paints the retry affordance");
  eq(retryButton?.textContent, en["projectTree.scanDegradedRetry"], "state ③ retry button carries the exact retry copy");

  // 重试点击：走既有通道 + 不触发遮罩点击跳过（stopPropagation）+ 锁存清除回到状态①。
  // （先还原 A 段故意换上的失败版桩——重试成功语义。）
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.RetryStartupCatalogScan =
    () => { retryCalls.push(1); return Promise.resolve(); };
  await act(async () => { retryButton!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); await sleep(0); });
  ok(done === 0, "the retry click does not double as the overlay dismiss (stopPropagation)");
  eq(useStartupDegradeStore.getState().degraded, false, "a settled retry from the splash clears the latch");
  await act(async () => { await sleep(0); });
  ok(document.querySelectorAll(".startup-splash__dots span").length === 3, "after a settled retry the splash returns to state ① markup");
  await unmountSplash();
}

// ── C. 源码契约钉 ────────────────────────────────────────────────────────────
console.log("\ntl1543 C. source contracts (Go event source, tree banner slot, bridge declarations, three locales)");

{
  const goLifecycle = readFileSync(resolve(desktopRoot, "session_catalog_lifecycle.go"), "utf8");
  ok(goLifecycle.includes("deferredDirs := runStartupReconcileWave("), "Go: the wave's deferred count is captured (was discarded with `_ =`)");
  ok(goLifecycle.includes("a.emitStartupCatalogDegraded(deferredDirs)"), "Go: budget-out feeds the frontend degrade event");
  ok(goLifecycle.includes('"deferred_to_async_queue"'), "Go: the event payload reuses the WARN's mode tag vocabulary");
  ok(goLifecycle.includes("func (a *App) RetryStartupCatalogScan()"), "Go: the retry binding exists");
  ok(/RetryStartupCatalogScan\(\) \{\n\ta\.emitProjectTreeChanged\(\)/.test(goLifecycle),
    "Go: the retry rides the existing refresh primitive (requestProjectTreeCatalogRefresh + tree event), no new reconcile mechanism");

  const splashSrc = readFileSync(resolve(srcRoot, "components/StartupSplash.tsx"), "utf8");
  ok(splashSrc.includes('t("app.splashSubtitle")') && splashSrc.includes("startup-splash__dots"),
    "splash: the state ① markup (subtitle + dots) is still the non-degraded branch");
  ok(splashSrc.includes('data-degraded="true"') && splashSrc.includes('t("projectTree.scanDegraded")'),
    "splash: states ②③ swap the subtitle for the degrade copy and the retry button");

  const treeSrc = readFileSync(resolve(srcRoot, "components/ProjectTree.tsx"), "utf8");
  ok(treeSrc.includes("startupScanDegraded ?"), "tree: the degrade latch gates the catalog-progress slot");
  ok(treeSrc.includes('data-degraded="true"') && treeSrc.includes("project-tree__catalog-progress") && treeSrc.includes("project-tree__catalog-rebuild"),
    "tree: the degrade banner reuses the existing banner slot and button classes (no new surface)");
  ok(treeSrc.includes("installStartupDegradeListener()"), "tree: the degrade listener is installed while mounted");
  ok(treeSrc.includes("retryStartupCatalogScan()"), "tree: the banner retry delegates to the shared retry");

  const bridgeSrc = readFileSync(resolve(srcRoot, "lib/startupDegradeBridge.ts"), "utf8");
  ok(bridgeSrc.includes("window.runtime?.EventsOn"), "bridge: perception rides the existing EventsOn channel (no wails generate needed)");
  ok(bridgeSrc.includes("app.RetryStartupCatalogScan?.()"), "bridge: retry delegates through the hand-written typed bridge");

  const typesSrc = readFileSync(resolve(srcRoot, "lib/sessionCatalogTypes.ts"), "utf8");
  ok(typesSrc.includes("RetryStartupCatalogScan?(): Promise<void>;"), "bindings: the optional method is declared on SessionCatalogBindings");
  const mockSrc = readFileSync(resolve(srcRoot, "lib/sessionCatalogBridge.ts"), "utf8");
  ok(mockSrc.includes("async RetryStartupCatalogScan()"), "bindings: the browser-dev mock answers with the same shape");

  const storeSrc = readFileSync(resolve(srcRoot, "store/startupDegrade.ts"), "utf8");
  ok(storeSrc.includes("create<StartupDegradeState>"), "store: the latch uses the same zustand mechanism as overlays");

  // 验收④ 三语：键在场且非空（tsc 的 Record<DictKey,string> 已编译期强制键集一致）。
  const zhSrc = readFileSync(resolve(srcRoot, "locales/zh.ts"), "utf8");
  const twSrc = readFileSync(resolve(srcRoot, "locales/zh-TW.ts"), "utf8");
  const enSrc = readFileSync(resolve(srcRoot, "locales/en.ts"), "utf8");
  const keyRe = /"projectTree\.scanDegraded": "([^"]+)"/;
  const retryKeyRe = /"projectTree\.scanDegradedRetry": "([^"]+)"/;
  for (const [label, source] of [["zh", zhSrc], ["zh-TW", twSrc], ["en", enSrc]] as const) {
    const copy = keyRe.exec(source)?.[1];
    const retryCopy = retryKeyRe.exec(source)?.[1];
    ok(Boolean(copy), `locale ${label}: the degrade copy key is present`);
    ok(Boolean(retryCopy), `locale ${label}: the retry copy key is present`);
  }
  // splash 与树横幅同源复用同两键（无重复文案键）。
  ok(splashSrc.includes('t("projectTree.scanDegraded")') && treeSrc.includes('t("projectTree.scanDegraded")'),
    "splash and tree banner read the same shared keys (no duplicate copy keys)");
  // 文案语义抽查：中文面必须让用户读出「后台继续 + 界面可用」；英文面同义。
  ok(zhSrc.includes("目录扫描仍在后台继续，界面已可正常使用"), "locale zh: the copy states background-continues + UI-usable");
  ok(twSrc.includes("目錄掃描仍在背景繼續，介面已可正常使用"), "locale zh-TW: the copy states background-continues + UI-usable");
  ok(enSrc.includes("Folder scanning continues in the background — the app is ready to use"), "locale en: the copy states background-continues + UI-usable");
}

console.log(`\ntl1543: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
