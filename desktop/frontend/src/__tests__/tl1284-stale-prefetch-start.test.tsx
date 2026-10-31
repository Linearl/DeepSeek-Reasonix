// tl 1284 验收 harness（设置 lab 页 dataReady 慢 17.5s~23.5s）：
// 复现 desktop.log 20261030 两条 big dataReadyMs 的机制并断言修复后语义。
//  ① 23545ms 伪影（20:01 例）：面板重开、无活预取（TTL 过期现拉）时，起点
//     不得继承上一次会话的预取时间戳——读数必须从本次现拉发起时刻起算。
//  ② 17555ms（22:41 例）起点正确性：消费到的预取读数必须以「该请求自己的
//     发起时刻」为起点（请求本体携带 startedAt），不早不晚。
// 验收断言：重开路径 dataReadyMs < 5000（伪影档读数 ≥ 60000）；消费路径
// 起点与请求发起时刻同源。
//
// Run: node node_modules/.pnpm/tsx@4.23.15/node_modules/tsx/dist/cli.mjs
//   --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1284-stale-prefetch-start.test.tsx

import { registerHooks } from "node:module";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// SettingsPanel 的 import 图里有 vite 约定的资源导入（.css 与 `?raw` 的
// lab-layout.yaml）。tsx-runner 不认这些——在 resolve 层就地内联：.css 给
// 空模块；`?raw` 读出文件内容导出为 default 字符串（tl1179 同款预存红根因，
// 见交付回执）。不放 load 层：tsx 的 load 钩子对不转换的模块回 source:null
// 交还默认 loader，同步 hook 链的外层边界会先于本钩子触发
// ERR_INVALID_RETURN_PROPERTY_VALUE 校验，包不住它的返回值。
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
      // vite ?url：构建期把资源解析为 URL 常量。测试里给文件 URL 原样即可
      // （面板图内不触发 <link> 注入，App.tsx 的注入路径自带 catch）。
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

// ── 可控时钟：模块与面板都动态调 Date.now()，用可变偏移模拟时间流逝 ──
const realNow = Date.now;
let clockOffsetMs = 0;
const now = () => realNow() + clockOffsetMs;
Date.now = now as typeof Date.now;

// ── bridge 打桩：Settings 记录每次调用时刻（用受控时钟），Proxy 兜底其余方法 ──
const fixture = { experimentalToolOptimizations: true, desktopLanguage: "zh" };
const settingsCallAt: number[] = [];
const clone = () => JSON.parse(JSON.stringify(fixture)) as Record<string, unknown>;
Object.assign(dom.window, {
  go: {
    main: {
      App: new Proxy({
        Settings: async () => { settingsCallAt.push(now()); return clone(); },
        GetModelSettingsApplication: async () => ({ application: "current" as const }),
        DesktopStartupSettings: async () => clone(fixture),
      }, {
        get(target, prop: string) {
          if (prop in target) return (target as Record<string, unknown>)[prop];
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
const { prefetchSettingsView, takeSettingsViewPrefetch, SETTINGS_PREFETCH_TTL_MS } = await import("../lib/settingsPrefetch");
const { drainFrontendLogRing } = await import("../lib/frontendLog");

const container = document.getElementById("root")!;
let root: ReturnType<typeof createRoot> | null = null;
const renderPanel = () => act(async () => {
  // 每个会话独立 root：React 19 对已 unmount 的 root 再 render 直接抛错。
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
const dataReadyLines = () =>
  drainFrontendLogRing()
    .filter((line) => line.includes("settings data ready"))
    .map((line) => Number(/dataReadyMs=(\d+)/.exec(line)?.[1] ?? NaN));
const flushQueue = async () => { for (let i = 0; i < 40; i += 1) await Promise.resolve(); };

console.log("\ntl 1284 settings lab dataReady start-clock semantics");

// ── ① 打开会话 A：点击（预取发出）→ 面板挂载消费 → dataReady 从预取发起时刻起算 ──
prefetchSettingsView();
const clickA = now();
await renderPanel();
await settle();
await flushQueue();
const readsA = dataReadyLines();
ok(readsA.length === 1, `session A reports exactly one data-ready line (got ${readsA.length})`);
const readA = readsA[0] ?? NaN;
ok(Number.isFinite(readA) && readA < 5_000, `session A dataReadyMs is request-bound (got ${readA}, click→now=${now() - clickA}ms)`);

// ── ② 重开会话 B：不经过任何新预取（TTL 早已过期），面板重挂载现拉 ──
// 生产对应 20:01 例：22s 后的 reload 没有活预取也没有新预取覆写，旧代码
// peek 到 22s 前的全局时间戳 → dataReadyMs=23545。修复后必须从现拉发起
// 时刻起算（伪影档读数 ≥ 60000ms）。
await unmountPanel();
clockOffsetMs += 60_000; // TTL(10s) 已过，cached 成死快照；时间前进了 60s
const reopenAt = now();
await renderPanel();
await settle();
await flushQueue();
const readsB = dataReadyLines();
ok(readsB.length === 1, `reopen reports exactly one data-ready line (got ${readsB.length})`);
const readB = readsB[0] ?? NaN;
ok(Number.isFinite(readB) && readB < 5_000,
  `reopen WITHOUT a live prefetch measures from its own fetch, not the inherited stale click (got ${readB}ms; artifact scale would be ≥${now() - clickA}ms)`);
ok(settingsCallAt.length >= 2, `reopen issued a fresh backend read (Settings calls=${settingsCallAt.length})`);
const lastCall = settingsCallAt.at(-1) ?? NaN;
ok(Number.isFinite(lastCall) && lastCall >= reopenAt - 1, "fresh read fired at reopen time (TTL-expired snapshot discarded)");

// ── ③ 消费路径起点正确性：新预取在受控时钟下发起，消费读数以它为起点 ──
await unmountPanel();
clockOffsetMs += 30_000;
prefetchSettingsView();
const clickC = now();
clockOffsetMs += 2_000; // 挂载前主线程"卡"2s（生产里面板迟迟挂不上那段）
await renderPanel();
await settle();
await flushQueue();
const readsC = dataReadyLines();
const readC = readsC.at(-1) ?? NaN;
// 起点=预取发起时刻（clickC），不是挂载时刻（clickC+2000）：读数应 ≥2000
// （包含挂载延迟）且 ≪ 继承档（≥90000）。tl 739 的本意保留：点击后主线程
// 被饿的空窗计入读数，但窗口只属于这一次请求。
ok(Number.isFinite(readC) && readC >= 2_000 && readC < 5_000,
  `consumed prefetch counts from ITS OWN fire time (got ${readC}ms; fire→mount=2000ms by construction; inherited scale would be ≥${clockOffsetMs}ms)`);

// take 清场后不再残留可继承状态（防御：任何后续 reload 只能用自己的起点）
const afterTake = takeSettingsViewPrefetch();
ok(afterTake === null, "prefetch cache fully drained after consumption");

await unmountPanel();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
console.log(`PASS tl1284: reopen without live prefetch measures from its own fetch (artifact ${SETTINGS_PREFETCH_TTL_MS}-scale gone); consumed prefetch counts from its own request start`);
