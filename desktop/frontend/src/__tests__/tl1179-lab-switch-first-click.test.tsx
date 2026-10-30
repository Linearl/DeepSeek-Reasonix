// tl 1179 验收 harness（实验室开关需点击两次才切换）：
//  ① 基线：首次点击即生效——写入一次、保存后 reload、按钮 UI 翻转；
//  ② 复现断言（本工单根因）：设置面板内切 tab 会经 openPage 钩子填一份
//     写前配置快照进 settingsPrefetch 缓存（无人消费、TTL 10s 内留存），
//     首次点击开关后的保存读回不得吃到这份旧快照——吃到即 UI/行为停在
//     旧态，「第二次点击才生效」。断言：预取留存下首点仍翻转；
//  ③ 二次点击不回归。
//
// Run: node node_modules/.pnpm/tsx@4.23.15/node_modules/tsx/dist/cli.mjs
//   --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1179-lab-switch-first-click.test.tsx

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
import { registerHooks } from "node:module";

// tl 1284 补：SettingsPanel import 图带 vite 资源约定（.css / ?raw yaml /
// ?url 样式常量），tsx-runner 不认——resolve 层就地内联，解开本 harness 的
// 预存红（ERR_UNKNOWN_FILE_EXTENSION，主仓同 commit 同样红）。只能在 resolve
// 层做：tsx 的 load 钩子对不转换模块回 source:null，同步外层 load 钩子会在
// 自己的边界先吃 ERR_INVALID_RETURN_PROPERTY_VALUE。
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

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  // Event 必须与 JSDOM 的 window 配套：runApply 成功路径会
  // window.dispatchEvent(new Event("reasonix:model-catalog-changed"))，
  // 若 Event 是 Node 原生构造器，dispatchEvent 抛 TypeError → 每次保存
  // 额外走 catch 的二次 reload（现拉），恰好掩盖预取污染（tl 1179 根因面）。
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

// ── bridge 打桩：真替身挂 window.go.main.App（bridge 的 app Proxy 调用点解析）。
// fixture 是可变配置——Settings() 每次返回深拷贝，SetExperimental* 改 fixture，
// 与真后端「写盘后 Settings 读盘」同构。─────────────────────────────────────
type Fixture = Record<string, unknown>;
const fixture: Fixture = {
  // normalizeSettingsView 对缺省字段全部兜底；这里只放本链路断言面。
  experimentalSmoothScrollFollow: false,
  experimentalSessionWall: false,
  desktopLanguage: "zh",
};
const calls = {
  settings: 0,
  smoothScrollSet: [] as boolean[],
};
const clone = (v: Fixture): Fixture => JSON.parse(JSON.stringify(v)) as Fixture;
Object.assign(dom.window, {
  go: {
    main: {
      App: new Proxy({
        Settings: async () => {
          calls.settings += 1;
          const snap = clone(fixture);
          // 快照版本号：区分「写前快照（旧）」与「写后快照（新）」——保存读回
          // 若吃到写前快照，onChanged 与 setS 拿到的就是旧态。
          (snap as { _harnessSnapSeq?: number })._harnessSnapSeq = calls.settings;
          return snap;
        },
        SetExperimentalSmoothScrollFollow: async (on: boolean) => { calls.smoothScrollSet.push(on); fixture.experimentalSmoothScrollFollow = on; },
        GetModelSettingsApplication: async () => ({ application: "current" as const }),
        DesktopStartupSettings: async () => clone(fixture),
        ListAddressableSessions: async () => [],
      }, {
        get(target, prop: string) {
          if (prop in target) return (target as Record<string, unknown>)[prop];
          // 未显式打桩的方法兜底：返回 null 的异步（面板容错路径自会处理）。
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
const { en } = await import("../locales/en");
const { prefetchSettingsView } = await import("../lib/settingsPrefetch");
const LABEL_ON = String(en["settings.smoothScrollFollow.on"]);
const LABEL_OFF = String(en["settings.smoothScrollFollow.off"]);

const root = createRoot(document.getElementById("root")!);
// onChanged 读数记录：保存后 runApply 把 reload 的读回交给 onChanged——
// 版本号暴露它拿到的是写前还是写后快照。
const onChangedReads: Array<{ snapSeq: number | undefined; smooth: unknown }> = [];
const renderPanel = () => act(async () => {
  root.render(
    <LocaleProvider>
      <SettingsPanel
        initialTab="experimental"
        desktopPlatform="windows"
        onClose={() => {}}
        onChanged={(next) => {
          onChangedReads.push({ snapSeq: (next as { _harnessSnapSeq?: number } | null | undefined)?._harnessSnapSeq, smooth: (next as { experimentalSmoothScrollFollow?: unknown } | null | undefined)?.experimentalSmoothScrollFollow });
        }}
        onUseSubagent={() => {}}
      />
    </LocaleProvider>,
  );
});
// lazy chunk（ExperimentalSection void import）解析需要多轮宏任务（tsx loader
// 链式解析几十个模块依赖）。
const settle = async (rounds = 30) => {
  for (let i = 0; i < rounds; i += 1) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const flushQueue = async () => { for (let i = 0; i < 40; i += 1) await Promise.resolve(); };

await renderPanel();
await settle();

console.log("\ntl 1179 lab switch first-click");

// ── 前置：实验页已挂载，viewLayout 组展开 + 条目选中 ──
const groupToggles = Array.from(document.querySelectorAll<HTMLButtonElement>("button.experimental-lab__group-toggle"));
ok(groupToggles.length > 0, `lab rail mounted with group headers (got ${groupToggles.length})`);
for (const btn of groupToggles) await act(async () => { btn.click(); });
const entryBtn = document.getElementById("lab-entry-viewLayout") as HTMLButtonElement | null;
ok(entryBtn != null, "viewLayout entry exists in rail");
if (entryBtn) await act(async () => { entryBtn.click(); });
await settle(4);

const onBtn = () =>
  Array.from(document.querySelectorAll<HTMLButtonElement>("button.set-seg__btn"))
    .find((b) => b.textContent === LABEL_ON);
const offBtn = () =>
  Array.from(document.querySelectorAll<HTMLButtonElement>("button.set-seg__btn"))
    .find((b) => b.textContent === LABEL_OFF);

ok(onBtn() != null && offBtn() != null, "smoothScrollFollow segmented control rendered (on/off labels)");
ok(!(onBtn()?.className.includes("set-seg__btn--on") ?? true), "before click: switch NOT highlighted (off state)");

// ── ① 基线：无预取干扰下首次点击即生效 ──
const settingsBefore = calls.settings;
const btn1 = onBtn()!;
await act(async () => { btn1.click(); });
await flushQueue();
await settle(4);

ok(calls.smoothScrollSet.length === 1 && calls.smoothScrollSet[0] === true,
  `first click issued exactly ONE write with on=true (got ${JSON.stringify(calls.smoothScrollSet)})`);
ok(calls.settings > settingsBefore, `save triggered a settings reload (Settings reads ${settingsBefore} → ${calls.settings})`);
ok(onBtn()?.className.includes("set-seg__btn--on") ?? false,
  "FIRST CLICK flips the UI: on-label highlighted right after the save reload");
ok(fixture.experimentalSmoothScrollFollow === true, "fixture (config-on-disk analogue) now true");

// ── ② 复现断言：切 tab 留下的预取快照不得污染保存读回 ──
// 真实路径：设置内切 tab → onNavigate → setSettingsTarget → openPage({settings})
// → prefetchSettingsView() 以「写前配置」填缓存并留存。此处直接调同一入口
// 填充等价快照（此刻 fixture=false，与「切到 Lab 后立刻点开关」同构）。
const prefetchSnapSeq = calls.settings;
await act(async () => { prefetchSettingsView(); });
const btn2 = offBtn()!;
await act(async () => { btn2.click(); });
await flushQueue();
await settle(4);

ok(calls.smoothScrollSet.length === 2 && calls.smoothScrollSet[1] === false,
  `click with a stale prefetch present still writes on=false (got ${JSON.stringify(calls.smoothScrollSet)})`);
ok((onChangedReads.at(-1)?.snapSeq ?? 0) > prefetchSnapSeq,
  `save readback fetched a FRESH post-write snapshot (onChanged snapSeq=${onChangedReads.at(-1)?.snapSeq} > prefetch snapSeq=${prefetchSnapSeq}), not the stale pre-write prefetch`);
ok(offBtn()?.className.includes("set-seg__btn--on") ?? false,
  "save readback ignores the stale pre-write prefetch: UI flips on the FIRST click");
ok(fixture.experimentalSmoothScrollFollow === false, "fixture back to false");

// ── ③ 再点一次（写前预取已消费）不回归 ──
const settingsMid = calls.settings;
const btn3 = onBtn()!;
await act(async () => { btn3.click(); });
await flushQueue();
await settle(4);

ok(calls.smoothScrollSet.length === 3 && calls.smoothScrollSet[2] === true,
  `third click writes on=true (got ${JSON.stringify(calls.smoothScrollSet)})`);
ok(calls.settings > settingsMid, `later saves still reload (Settings reads ${settingsMid} → ${calls.settings})`);
ok(onBtn()?.className.includes("set-seg__btn--on") ?? false,
  "subsequent clicks keep flipping the UI (no regression)");
ok(fixture.experimentalSmoothScrollFollow === true, "fixture on again");

await act(async () => { root.unmount(); });

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
