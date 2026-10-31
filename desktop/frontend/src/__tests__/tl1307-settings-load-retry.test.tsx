// tl 1307 定向验收 harness（设置页偶发「设置加载失败」——锁压力窗口前端超时误判）：
// 复现 20261031 20-21 时段机制（单次 settings 读在锁窗口内拒绝）并断言修复后三态：
//  ① 自动恢复：首试拒绝 → 自动退避重试 → 成功——全程无失败横幅，骨架保留，
//     重试落 frontend:lab 打点；
//  ② 骨架保留：重试窗口内 loading 骨架在、失败横幅不在、已加载内容不清（不白屏）；
//  ③ 终态失败：上限内全部失败才落「设置加载失败。」横幅；横幅手动重试重置预算
//     （再给 3 次上限），预算封顶可观测（attempts=3 打点）。
// 附：预取快路（tl 739/1284 语义）不受影响——首试消费预取；预取失败重试现拉，
// 不重放同一失败 promise。
//
// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1307-settings-load-retry.test.tsx

import { registerHooks } from "node:module";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// SettingsPanel 的 import 图里有 vite 约定的资源导入（.css 与 `?raw` 的
// lab-layout.yaml）。tsx-runner 不认这些——在 resolve 层就地内联（tl1284
// harness 同款，见其注释里的预存红根因记录）。
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css")) {
      return { url: "data:text/javascript,export default {};", shortCircuit: true };
    }
    if (specifier.endsWith("?raw")) {
      const resolved = nextResolve(specifier.slice(0, -"?raw".length), context);
      const text = readFileSync(fileURLToPath(resolved.url), "utf8");
      return { url: `data:text/javascript,export default ${encodeURIComponent(JSON.stringify(text))};`, shortCircuit: true };
    }
    if (specifier.endsWith("?url")) {
      const resolved = nextResolve(specifier.slice(0, -"?url".length), context);
      return { url: `data:text/javascript,export default ${encodeURIComponent(JSON.stringify(resolved.url))};`, shortCircuit: true };
    }
    return nextResolve(specifier, context);
  },
});

import * as React from "react"; // tsconfig exclude 了 __tests__，classic JSX 需显式绑定
import { JSDOM } from "jsdom";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  Event: dom.window.Event,
  CustomEvent: dom.window.CustomEvent,
  localStorage: dom.window.localStorage,
  sessionStorage: dom.window.sessionStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
// @ts-expect-error test harness global
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

// ── 退避策略改小：断言的是「有上限、有退避、有恢复」，不是真实等待时长 ──
const { settingsLoadRetry } = await import("../lib/settingsLoadRetry");
settingsLoadRetry.delaysMs = [20, 30];

// ── bridge 打桩：Settings 按程序队列逐次应答（默认拒绝），Proxy 兜底其余方法 ──
const fixture = { experimentalToolOptimizations: true, desktopLanguage: "zh" };
const clone = () => JSON.parse(JSON.stringify(fixture)) as Record<string, unknown>;
type Program = () => Promise<Record<string, unknown>>;
const settingsPrograms: Program[] = [];
const settingsCalls: number[] = [];
let nowMs = 0;
Object.assign(dom.window, {
  go: {
    main: {
      App: new Proxy({
        Settings: async () => {
          settingsCalls.push(nowMs);
          nowMs += 1;
          const program = settingsPrograms.shift();
          if (!program) throw new Error("settings read rejected (lock-window spike)");
          return program();
        },
        GetModelSettingsApplication: async () => ({ application: "current" as const }),
      }, {
        get(target: Record<string, unknown>, prop: string) {
          if (prop in target) return target[prop];
          return async () => null;
        },
      }),
    },
  },
});

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { SettingsPanel } = await import("../components/SettingsPanel");
// 预热 SettingsPanel 内部的 lazy chunk（同一 ESM 缓存，promise 立即就绪）。
await import("../components/ExperimentalLabSection");
const { prefetchSettingsView, takeSettingsViewPrefetch } = await import("../lib/settingsPrefetch");
const { drainFrontendLogRing } = await import("../lib/frontendLog");

const container = document.getElementById("root")!;
let root: ReturnType<typeof createRoot> | null = null;
const renderPanel = () => act(async () => {
  root = createRoot(container);
  root.render(
    <LocaleProvider>
      <SettingsPanel
        initialTab="experimental"
        desktopPlatform="windows"
        onClose={() => {}}
        onChanged={() => {}}
        onUseSubagent={() => {}}
      />
    </LocaleProvider>,
  );
});
const unmountPanel = () => act(async () => { root?.unmount(); root = null; });
const settle = async (rounds = 30) => {
  for (let i = 0; i < rounds; i += 1) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const waitFor = async (label: string, predicate: () => boolean) => {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
};
const resetScenario = () => {
  settingsPrograms.length = 0;
  settingsCalls.length = 0;
  takeSettingsViewPrefetch(); // 清掉跨场景残留的预取快照
  drainFrontendLogRing();
};
const logLines = () => drainFrontendLogRing();
const banner = () => document.querySelector(".settings-load-error");
const skeleton = () => document.querySelector(".settings-page-content .empty");

console.log("\ntl 1307 settings load auto-retry (failure != terminal)");

// ── ①+② 首试拒绝 → 退避重试期间骨架保留、无横幅 → 重试成功自动恢复 ──
{
  let release!: (view: Record<string, unknown>) => void;
  const hungSecondRead = new Promise<Record<string, unknown>>((resolve) => { release = resolve; });
  settingsPrograms.push(
    async () => { throw new Error("lock-window spike"); }, // 首试：锁窗口内拒绝
    () => hungSecondRead,                                   // 重试 1：挂起（观察骨架窗口）
  );
  await renderPanel();
  await waitFor("second attempt in flight", () => settingsCalls.length === 2);
  // 重试窗口（退避后第二次读挂起中）：骨架在、失败横幅不在、终态失败打点不在。
  ok(banner() === null, "no failure banner while retry in flight");
  ok(skeleton() !== null, "loading skeleton retained during retry (no white screen)");
  const midLines = logLines();
  ok(midLines.some((l) => l.includes("settings load retry") && l.includes("attempt=2")),
    `retry attempt logged (got ${midLines.filter((l) => l.includes("settings load retry")).length} retry lines)`);
  ok(!midLines.some((l) => l.includes("settings load failed")), "no terminal-failure line while retry in flight");
  // 释放重试成功：内容到达、无横幅、无白屏。
  await act(async () => { release(clone()); });
  await settle();
  ok(banner() === null, "no failure banner after retry succeeds");
  ok(skeleton() === null, "skeleton cleared after retry succeeds");
  ok(settingsCalls.length === 2, `exactly 2 backend reads for fail-then-recover (got ${settingsCalls.length})`);
  const finalLines = logLines();
  ok(finalLines.some((l) => l.includes("settings data ready")), "data-ready line reported after auto-recovery");
  ok(!finalLines.some((l) => l.includes("settings load failed")), "no terminal-failure line after auto-recovery");
  await unmountPanel();
}

// ── ③ 终态失败：上限内（3 次）全部失败才落横幅；预算封顶可观测 ──
{
  resetScenario();
  await renderPanel();
  await waitFor("terminal failure banner", () => banner() !== null);
  ok(settingsCalls.length === 3, `exactly 3 attempts (1+2 retries) before terminal failure (got ${settingsCalls.length})`);
  ok(banner() !== null && banner().getAttribute("role") === "alert", "terminal failure banner rendered with role=alert");
  ok(skeleton() === null, "skeleton cleared at terminal failure");
  const lines = logLines();
  const retryLines = lines.filter((l) => l.includes("settings load retry"));
  ok(retryLines.length === 2 && retryLines.every((l) => l.includes("attempt=2") || l.includes("attempt=3")),
    `exactly 2 retry lines (attempt=2, attempt=3) at cap (got ${retryLines.length})`);
  const failedLines = lines.filter((l) => l.includes("settings load failed"));
  ok(failedLines.length === 1 && failedLines[0].includes("attempts=3"),
    `single terminal-failure line with attempts=3 (got ${failedLines.length}: ${failedLines[0] ?? ""})`);
}

// ── ③附 横幅手动重试重置自动重试预算：再给满 3 次上限，不是只剩 0 次 ──
{
  const retryButton = banner()?.querySelector("button");
  ok(retryButton != null, "manual retry button present on failure banner");
  await act(async () => { retryButton?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  await waitFor("second terminal failure", () => settingsCalls.length === 6);
  await settle();
  ok(banner() !== null, "banner persists after manual retry also exhausts its budget");
  ok(settingsCalls.length === 6, `manual retry granted a fresh 3-attempt budget (got ${settingsCalls.length})`);
  const failedAgain = logLines().filter((l) => l.includes("settings load failed"));
  ok(failedAgain.length === 1 && failedAgain[0].includes("attempts=3"),
    `second terminal line also attempts=3 (got ${failedAgain.length}: ${failedAgain[0] ?? ""})`);
  await unmountPanel();
}

// ── 附 预取快路不受影响：首试消费预取成功 → 零重试 ──
{
  resetScenario();
  settingsPrograms.push(async () => clone());
  prefetchSettingsView(); // 入口点击：预取发出（吃掉队列里的成功程序）
  await renderPanel();
  await settle();
  ok(banner() === null && skeleton() === null, "prefetch fast path: content rendered, no banner");
  ok(settingsCalls.length === 1, `prefetch consumed at first attempt, no extra reads (got ${settingsCalls.length})`);
  const lines = logLines();
  ok(!lines.some((l) => l.includes("settings load retry")), "no retry lines on prefetch fast path");
  ok(lines.some((l) => l.includes("settings data ready") && l.includes("prefetchStart=true")),
    "tl1284 data-ready semantics intact (prefetchStart=true)");
  await unmountPanel();
}

// ── 附 预取失败不重放同一失败：重试现拉新请求并恢复 ──
{
  resetScenario();
  settingsPrograms.push(
    async () => { throw new Error("prefetch rejected"); }, // 预取本体失败
    async () => clone(),                                    // 首试消费即拒 → 重试 1 现拉成功
  );
  prefetchSettingsView();
  await renderPanel();
  await waitFor("retry after failed prefetch succeeds", () => skeleton() === null && banner() === null);
  ok(settingsCalls.length === 2, `failed prefetch retried with a FRESH read (got ${settingsCalls.length})`);
  const lines = logLines();
  ok(lines.some((l) => l.includes("settings load retry") && l.includes("attempt=2")),
    "retry fired after prefetch failure");
  ok(!lines.some((l) => l.includes("settings load failed")), "no terminal failure when retry recovers");
  await unmountPanel();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
console.log("PASS tl1307: settings read failure auto-retries with backoff (skeleton kept, no banner mid-retry), terminal only after capped attempts; prefetch fast path unchanged");
