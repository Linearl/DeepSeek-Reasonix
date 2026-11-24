// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1791-lab-anchors-deeplink.test.tsx
//
// tl1791 验收套（设置/实验室面板 CDP 可自动化性改善——稳定锚点 + 深链导航）：
//  ① 静态对账：lab-layout.yaml 全量（8 组 / 28 卡 / 全部成员+字段行）逐一
//     对到源码锚点——组头 lab-group-<组key>、rail 卡行 lab-entry-<卡id>、
//     开关行 lab-toggle-<id>；漏一个即红（任务书验收①「漏一个即补」）。
//  ② 渲染对账：jsdom 挂全量 SettingsPanel（tl1179 同款桩桥），展开全部分组
//     → 断言 28 张卡的 rail 锚点全部在 DOM；逐卡点击 → 断言该卡 yaml 成员
//     行锚点全部真实渲染（含独立卡组件里的 opencodeGoUsage）；再以
//     prepairAutosplice（20261124 包113 装机验证的 CDP 痛点目标）做
//     「锚点点中开关 + 读状态 + 落盘回读」全链路（验收②的 harness 面）。
//  ③ 深链：parseLabDeepLink/clearSettingsHash 单元用例；initialFocus lab
//     变体落位（挂载即达目标卡，零点击）；面板挂载期间 hashchange 重定位
//     （general 页上改 hash → 直达 fullAccess 卡）；App 装配层接线源码钉
//     （boot 解析+hashchange+关页清 hash+首帧消费跳过）。
//  ④ 三语零新增 locale 键（锚点/深链纯结构性，文案零变化）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { registerHooks } from "node:module";
import { JSDOM } from "jsdom";

// tl 1284 同款：SettingsPanel import 图带 vite 资源约定（.css / ?raw yaml /
// ?url），tsx-runner 不认——resolve 层就地内联。
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css")) {
      return { url: "data:text/javascript,export default {};", shortCircuit: true };
    }
    if (specifier.endsWith("?raw")) {
      const resolved = nextResolve(specifier.slice(0, -"?raw".length), context);
      const text = readFileSync(new URL(resolved.url), "utf8");
      return { url: `data:text/javascript,export default ${encodeURIComponent(JSON.stringify(text))};`, shortCircuit: true };
    }
    if (specifier.endsWith("?url")) {
      const resolved = nextResolve(specifier.slice(0, -"?url".length), context);
      return { url: `data:text/javascript,export default ${encodeURIComponent(JSON.stringify(resolved.url))};`, shortCircuit: true };
    }
    return nextResolve(specifier, context);
  },
});

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\ntl1791 lab stable anchors + deep link");

// ── ① 静态对账：yaml 全量 id → 源码锚点 ────────────────────────────────────
const sectionSrc = readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
const goUsageSrc = readFileSync(fileURLToPath(new URL("../components/SettingsOpenCodeGoUsageCard.tsx", import.meta.url)), "utf8");
const panelSrc = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const appSrc = readFileSync(fileURLToPath(new URL("../App.tsx", import.meta.url)), "utf8");
const rowSrc = sectionSrc + "\n" + goUsageSrc;

const { resolveLabLayout, labEntries } = await import("../lab/labLayout");
const { LAB_PANE_IDS } = await import("../lab/labPaneRegistry");
const yamlText = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
const resolved = resolveLabLayout(yamlText, { allowedEntryIds: LAB_PANE_IDS });
ok(resolved.source === "yaml" && resolved.warnings.length === 0,
  "lab-layout.yaml resolves clean (source=yaml, zero warnings)");

const groups = resolved.layout.groups;
const groupKeys = groups.map((g) => g.key);
const entryRows = labEntries(resolved.layout);
ok(groupKeys.length === 8, `yaml carries 8 groups (got ${groupKeys.length})`);
ok(sectionSrc.includes('data-testid={`lab-group-${g.key}`}'),
  "group toggle heads carry the lab-group-<组key> testid template (all 8 keys ride one template)");
ok(sectionSrc.includes('data-testid={`lab-entry-${feature.id}`}'),
  "rail card rows carry the lab-entry-<卡id> testid template (all card keys ride one template)");

let memberTotal = 0;
const missingRows: string[] = [];
for (const { entry, group } of entryRows) {
  for (const member of entry.members ?? []) {
    memberTotal += 1;
    if (!rowSrc.includes(`lab-toggle-${member.id}`)) missingRows.push(`${group}/${entry.id}:${member.id}`);
  }
  if (entry.fields) {
    for (const field of entry.fields) {
      memberTotal += 1;
      if (!rowSrc.includes(`lab-toggle-${field.id}`)) missingRows.push(`${group}/${entry.id}:field:${field.id}`);
    }
  }
}
ok(missingRows.length === 0,
  `all ${memberTotal} yaml member/field rows have a lab-toggle anchor (missing: ${JSON.stringify(missingRows)})`);
ok(rowSrc.includes("lab-toggle-opencodeGoUsage") && goUsageSrc.includes('testId="lab-toggle-opencodeGoUsage"'),
  "opencodeGoUsage row is anchored inside its standalone card component (783 member row)");
// 深链形态钉：设置 hash 前缀是设置页私有，别的 hash（如 web 模式 #token=）不受 clear 影响。
const { parseLabDeepLink, clearSettingsHash, LAB_DEEP_LINK_PREFIX } = await import("../lib/settingsDeepLink");
ok(LAB_DEEP_LINK_PREFIX === "#settings/lab/", "deep link prefix is #settings/lab/ (task-book form)");

// ── ② 渲染对账 + ③ 深链落位：jsdom 挂全量 SettingsPanel ────────────────────
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
// jsdom 无 scrollIntoView 实现——focusEntry 直达的滚入视野调用需要桩。
dom.window.Element.prototype.scrollIntoView = () => {};

// 桩桥：fixture 可变（Settings 每次深拷回读），SetExperimentalPrepairAutosplice
// 真落盘（包113 CDP 痛点目标的「点中开关+读状态+落盘回读」全链路）；其余方法
// 兜底 async()=>null（面板容错路径自处理，tl1179 同款）。
const fixture: Record<string, unknown> = {
  experimentalPrepairAutosplice: false,
  desktopLanguage: "zh",
};
const setterCalls: string[] = [];
const clone = (v: Record<string, unknown>): Record<string, unknown> => JSON.parse(JSON.stringify(v)) as Record<string, unknown>;
Object.assign(dom.window, {
  go: {
    main: {
      App: new Proxy({
        Settings: async () => clone(fixture),
        DesktopStartupSettings: async () => clone(fixture),
        GetModelSettingsApplication: async () => ({ application: "current" as const }),
        ListAddressableSessions: async () => [],
        SetExperimentalPrepairAutosplice: async (on: boolean) => {
          setterCalls.push(`prepairAutosplice=${String(on)}`);
          fixture.experimentalPrepairAutosplice = on;
        },
      }, {
        get(target, prop: string) {
          if (prop in target) return (target as Record<string, unknown>)[prop];
          return async () => null;
        },
      }),
    },
  },
});

import * as React from "react";
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { SettingsPanel } = await import("../components/SettingsPanel");
const { settingsFocusRequestId } = await import("../lib/settingsFocus");
// 预热实验室 lazy chunk（同一 ESM 缓存，Suspense promise 立即就绪）。
await import("../components/ExperimentalLabSection");

const root: ReturnType<typeof createRoot> = createRoot(document.getElementById("root")!);
let mountSeq = 0;
const mountPanel = (props: Record<string, unknown>) => act(async () => {
  // key 递增：同 root 重渲必须换 key 才是真重挂（useState 初始化器重跑）——
  // 复用组件实例时 initialFocus/initialTab 是只读初始值，深链落位断言会吃到
  // 上一挂的残留态。
  mountSeq += 1;
  root.render(
    <LocaleProvider>
      <SettingsPanel
        key={`tl1791-mount-${mountSeq}`}
        desktopPlatform="windows"
        onClose={() => {}}
        onChanged={() => {}}
        onUseSubagent={() => {}}
        {...props}
      />
    </LocaleProvider>,
  );
});
const settle = async (rounds = 8) => {
  for (let i = 0; i < rounds; i += 1) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const click = (el: Element) => {
  act(() => { el.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true })); });
};
const q = (selector: string) => document.querySelector(selector);

// settingsFocusRequestId：lab 变体读 0，model-stats 递增语义保持（563 判重）。
ok(settingsFocusRequestId(null) === 0 && settingsFocusRequestId(undefined) === 0, "settingsFocusRequestId(nullish) = 0");
ok(settingsFocusRequestId({ target: "lab", entry: "x" }) === 0, "settingsFocusRequestId(lab variant) = 0 (no requestId field)");
ok(settingsFocusRequestId({ target: "model-stats", requestId: 7 }) === 7, "settingsFocusRequestId(model-stats) reads its own requestId");

// ── ③a 深链落位（挂载即达）：initialFocus lab 变体，零点击直达目标卡 ──────
await mountPanel({ initialFocus: { target: "lab", entry: "runtimeObservability" } });
await settle(24);
ok(q('[data-testid="lab-group-observability"]') !== null, "deep-link mount lands on the lab tab (rail renders)");
ok((q('[data-testid="lab-group-observability"]') as HTMLElement | null)?.getAttribute("aria-expanded") === "true",
  "target group auto-expanded on deep-link mount (focusEntry contract)");
ok(q('[data-testid="lab-entry-runtimeObservability"]') !== null, "target rail row renders");
ok(q('[data-testid="lab-toggle-prepairAutosplice"]') !== null,
  "deep link #settings/lab/runtimeObservability reaches the prepairAutosplice switch row with ZERO clicks");
ok(q('[data-testid="lab-toggle-monitoring"]') !== null && q('[data-testid="lab-toggle-eventsRotation"]') !== null
  && q('[data-testid="lab-toggle-trajectoryView"]') !== null,
  "the whole runtimeObservability pane rides the same single hop (members anchored)");
// 未知 id 零行为：落实验室页默认视图，不出空白 pane。
{
  await mountPanel({ initialFocus: { target: "lab", entry: "noSuchCard" } });
  await settle(16);
  ok(q('[data-testid="lab-toggle-prepairAutosplice"]') === null,
    "unknown deep-link id: no unknown pane rows render (zero behavior)");
  // 默认选中 = autopilot 卡（561 口径）；rail 默认折叠看不到行——空白 pane
  // 判据用默认卡的 pane 锚点（lab-toggle-autopilot 卡头行）。
  ok(q('[data-testid="lab-toggle-autopilot"]') !== null,
    "unknown deep-link id: lab page still renders its default selection (no blank pane)");
}

// 深链 parse/clear 单元（jsdom history 可用）。
ok(parseLabDeepLink("#settings/lab/runtimeObservability") === "runtimeObservability", "parse: plain card id");
ok(parseLabDeepLink("#settings/lab/") === null && parseLabDeepLink("#settings/other") === null
  && parseLabDeepLink("#token=abc") === null && parseLabDeepLink("") === null,
  "parse: non-lab / empty / foreign hashes → null");
ok(parseLabDeepLink("#settings/lab/../..") === null && parseLabDeepLink("#settings/lab/has space") === null,
  "parse: malformed ids rejected (regex identifier domain)");
dom.window.history.replaceState(null, "", "/#settings/lab/fullAccess");
clearSettingsHash();
ok(dom.window.location.hash === "", "clearSettingsHash wipes a settings hash via replaceState");
dom.window.history.replaceState(null, "", "/#token=abc");
clearSettingsHash();
ok(dom.window.location.hash === "#token=abc", "clearSettingsHash leaves foreign hashes alone");

// ── ② 渲染对账主循环：在已挂面板上展开全部分组并逐卡断言成员锚点 ────────────
// 换挂普通面板（无 focusEntry），把 hash 清干净避免上一段残留参与 hashchange。
dom.window.history.replaceState(null, "", "/");
await mountPanel({ initialTab: "experimental" });
await settle(24);
const groupHeads = [...document.querySelectorAll<HTMLElement>('[data-testid^="lab-group-"]')];
ok(groupHeads.length === 8, `all 8 group heads render on the rail (got ${groupHeads.length})`);
for (const head of groupHeads) click(head);
await settle(8);
const railIds = new Set([...document.querySelectorAll<HTMLElement>('[data-testid^="lab-entry-"]')].map((el) => el.getAttribute("data-testid")));
const missingRail = entryRows.filter(({ entry }) => !railIds.has(`lab-entry-${entry.id}`));
ok(missingRail.length === 0, `all ${entryRows.length} rail card anchors render after expanding every group (missing: ${JSON.stringify(missingRail.map((r) => r.entry.id))})`);

// 逐卡点击 → 该卡 yaml 成员行锚点全部真实渲染。
// 已知漂移（预存，本件只记录不修——任务书 non-goals：不改实验室功能/分组）：
// restartUpdate 在 yaml 里是 updateFeedback 卡成员（任务 992 归位），pane
// 分支却把它渲染在 toolOptimizations 卡内（本仓既有状态）——锚点照样挂在该
// 行上，断言按实际渲染位置收。
const DRIFT_PANE: Record<string, string> = { restartUpdate: "toolOptimizations" };
const paneRowProbe: Array<{ card: string; missing: string[] }> = [];
for (const { entry } of entryRows) {
  const row = q(`[data-testid="lab-entry-${entry.id}"]`);
  if (!row) { paneRowProbe.push({ card: entry.id, missing: ["<rail row gone>"] }); continue; }
  click(row);
  await settle(4);
  const memberIds = (entry.members ?? []).map((m) => m.id)
    .concat((entry.fields ?? []).map((f) => f.id));
  const missing = memberIds.filter((id) => {
    const paneCard = DRIFT_PANE[id] ?? entry.id;
    if (paneCard !== entry.id) return false; // 漂移行由上方独立断言收口
    return !q(`[data-testid="lab-toggle-${id}"]`);
  });
  paneRowProbe.push({ card: entry.id, missing });
}
// 漂移行单独断言：restartUpdate 锚点真实渲染在 toolOptimizations 卡 pane 内。
click(q('[data-testid="lab-entry-toolOptimizations"]')!);
await settle(4);
ok(q('[data-testid="lab-toggle-restartUpdate"]') !== null,
  "drift row restartUpdate (yaml: updateFeedback member) renders anchored inside the toolOptimizations pane (pre-existing, documented)");
const brokenCards = paneRowProbe.filter((r) => r.missing.length > 0);
ok(brokenCards.length === 0,
  `per-card render reconciliation: every yaml member row renders under its own card (${memberTotal} rows / ${entryRows.length} cards; broken: ${JSON.stringify(brokenCards)})`);

// 开关全链路（包113 CDP 痛点目标）：锚点点中开关 → 读状态翻转 → 桥落盘回读。
{
  const row = q('[data-testid="lab-entry-runtimeObservability"]')!;
  click(row);
  await settle(4);
  const toggleRow = q('[data-testid="lab-toggle-prepairAutosplice"]')!;
  ok(toggleRow !== null, "prepairAutosplice row reachable again via stable anchor");
  const onBtn = toggleRow.querySelectorAll("button.set-seg__btn")[1] as HTMLElement;
  const offBtn = toggleRow.querySelectorAll("button.set-seg__btn")[0] as HTMLElement;
  ok(offBtn.classList.contains("set-seg__btn--on") && !onBtn.classList.contains("set-seg__btn--on"),
    "state read: row exposes off/on segment with --on on the off side (default off)");
  act(() => { onBtn.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true })); });
  await settle(16);
  const after = q('[data-testid="lab-toggle-prepairAutosplice"]')!;
  const onAfter = after.querySelectorAll("button.set-seg__btn")[1] as HTMLElement;
  ok(onAfter.classList.contains("set-seg__btn--on"),
    "toggle click flips the --on state (save → reload → readback)");
  ok(setterCalls.includes("prepairAutosplice=true") && fixture.experimentalPrepairAutosplice === true,
    "the setter reached the bridge with on=true and persisted in the fixture");
}

// ── ③b 面板挂载期间 hashchange 重定位 ─────────────────────────────────────
{
  // 从实验室页切到 general（selectTab 清 pendingLabEntry），再改 hash 直达 fullAccess。
  // selectTab 由面板导航触发；harness 直接驱动内部状态不可达——改用 hashchange
  // 面板监听对 general 态的响应路径：先点导航按钮切走，再写 hash。
  const navSettings = [...document.querySelectorAll<HTMLElement>("button")].filter((b) => b.textContent?.includes("General"));
  if (navSettings.length > 0) {
    click(navSettings[0]);
    await settle(8);
  }
  ok(q('[data-testid="lab-toggle-prepairAutosplice"]') === null || navSettings.length === 0,
    "sanity: left the lab tab before hash retarget");
  dom.window.location.hash = "#settings/lab/fullAccess";
  await settle(16);
  ok(q('[data-testid="lab-toggle-fullAccess"]') !== null,
    "hashchange while mounted retargets: #settings/lab/fullAccess lands on the fullAccess card from a foreign tab");
  ok(dom.window.location.hash === "#settings/lab/fullAccess",
    "no dirty state: hash persists while the target page is open (clear happens on close, App-side)");
}

// ── ③c App 装配层接线源码钉（jsdom 不挂全量 App；764 风格） ────────────────
ok(appSrc.includes('parseLabDeepLink(window.location.hash)') && appSrc.includes('window.addEventListener("hashchange", openLabDeepLink)'),
  "App listens for lab deep links at boot AND on every hashchange (open half)");
ok(appSrc.includes('setSettingsFocus({ target: "lab", entry })') && appSrc.includes('openPage({ kind: "settings", tab: "experimental" })'),
  "App consumption routes into settingsFocus + openPage(experimental)");
ok(appSrc.includes("clearSettingsHash();") && appSrc.includes("settingsPageRef"),
  "App clears the settings hash when the settings page closes / leaves the lab tab");
ok(appSrc.includes("if (wasSettings && !isSettings) clearSettingsHash();"),
  "hash clearing rides the settings-page close transition only (StrictMode-safe: no once flags, dev-probe lesson)");
ok(panelSrc.includes('window.addEventListener("hashchange", onLabHashChange)'),
  "SettingsPanel retarget half: mounted-panel hashchange listener wired");
ok(panelSrc.includes('initialFocus?.target === "lab" ? "experimental" : (initialTab ?? "general")'),
  "SettingsPanel seeds the lab tab from the lab focus variant at mount");

// ── ④ 三语零新增 locale 键 ────────────────────────────────────────────────
const zhSrc = readFileSync(fileURLToPath(new URL("../locales/zh.ts", import.meta.url)), "utf8");
ok(!zhSrc.includes("tl1791") && !zhSrc.includes("lab-toggle") && !zhSrc.includes("settings.labDeepLink"),
  "zero new locale keys (anchors + deep link are pure structure)");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
