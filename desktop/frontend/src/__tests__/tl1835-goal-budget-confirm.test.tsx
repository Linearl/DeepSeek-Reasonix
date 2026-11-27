// tl1835 验收（goalMode 激活确认预算弹窗）：
//  ① 纯函数层：M↔裸 token 换算（显示层换算，键语义向后兼容）、输入校验
//     （上下界沿用 tl1312「预算=无人值守循环的正上界」语义）；
//  ② 弹窗组件：目标名+预设档+自定义输入渲染；默认 2M 选中（验收③：未编辑
//     时默认值不变）；非法输入禁用确认；确认回调携带换算后的裸 token（桥接
//     读数走 AppBindings 源级断言）；取消回调触发；
//  ③ 激活流门控（App.tsx 源级）：开关开 → goal 草稿提交与 /goal 命令两条
//     用户显式激活入口先挂起弹窗；取消路径经 replaceComposerInsert 回填草稿
//     （不激活）；开关关=直通现状（无挂起调用）；
//  ④ 实验室注册：安全/成本控制卡两成员行（yaml+内置默认双侧）+ 灯键谓词 +
//     档位表（可选档）；
//  ⑤ 三语 locale 键平行；
//  ⑥ 桥接面：AppBindings 三方法声明 + mock 桩 + settings readback 字段。
// Go 侧 setter/live 镜像/install 消费的真值测试在
// internal/config/goal_budget_confirm_tl1835_test.go 与
// internal/control/goal_budget_confirm_tl1835_test.go。

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
import assert from "node:assert/strict";
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
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");

const { GoalBudgetConfirmDialog, tokensToGoalBudgetM, goalBudgetMToTokens, normalizeGoalBudgetInputM, GOAL_BUDGET_PRESET_M, GOAL_BUDGET_DEFAULT_TOKENS } = await import("../components/GoalBudgetConfirmDialog");

console.log("\ntl1835 goal budget confirm dialog");

// ── ① 纯函数：M 换算与校验 ──
ok(GOAL_BUDGET_DEFAULT_TOKENS === 2_000_000, "default = 2M tokens (tl1312 unchanged)");
ok(tokensToGoalBudgetM(0) === 2, "unset (0) displays as the tl1312 default 2M");
ok(tokensToGoalBudgetM(5_000_000) === 5, "5M tokens → 5 M");
ok(goalBudgetMToTokens(2) === 2_000_000, "2 M → 2,000,000 raw tokens");
ok(goalBudgetMToTokens(1.5) === 1_500_000, "fractional M (1.5) → 1,500,000 raw tokens (roundtrip-safe)");
ok(normalizeGoalBudgetInputM("2") === 2, "valid custom input accepted");
ok(normalizeGoalBudgetInputM("0.5") === 0.5, "sub-1M custom accepted (>=0.1)");
ok(normalizeGoalBudgetInputM("") === null, "empty input rejected");
ok(normalizeGoalBudgetInputM("abc") === null, "non-numeric input rejected");
ok(normalizeGoalBudgetInputM("-1") === null, "negative rejected (预算=正上界)");
ok(normalizeGoalBudgetInputM("0") === null, "zero rejected");
ok(normalizeGoalBudgetInputM("0.05") === null, "below 0.1M rejected (下界)");
ok(normalizeGoalBudgetInputM("99999") === null, "above 10000M rejected (上界，防裸 token 手滑)");

// ── ② 弹窗组件 ──
const rootEl = document.getElementById("root")!;
const { t: rawT } = await import("../lib/i18n");
let root = createRoot(rootEl);
async function remount(node: React.ReactNode) {
  await act(async () => { root.unmount(); });
  rootEl.innerHTML = "";
  root = createRoot(rootEl);
  await act(async () => { root.render(node); });
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
}

let confirmedTokens: number | null = null;
let cancelled = 0;
async function renderDialog(currentTokens = 0) {
  confirmedTokens = null;
  cancelled = 0;
  await remount(
    <GoalBudgetConfirmDialog
      t={rawT as never}
      goalName="把仓库里的 TODO 全部清掉"
      currentTokens={currentTokens}
      busy={false}
      onConfirm={(tokens) => { confirmedTokens = tokens; }}
      onCancel={() => { cancelled += 1; }}
    />,
  );
}

await renderDialog(0);
const dialogEl = () => document.querySelector(".lab-pick-dialog");
ok(dialogEl() !== null, "dialog renders via lab-pick-dialog shell (portal family, zero new CSS)");
ok(document.querySelector(".modal-backdrop") !== null, "modal-backdrop present");
ok(document.body.textContent!.includes("把仓库里的 TODO 全部清掉"), "goal name displayed");
ok(document.querySelectorAll(".lab-bulk-dialog__restart-actions .btn").length === GOAL_BUDGET_PRESET_M.length,
  "preset tier buttons rendered (1/2/5/10 M)");
// 验收③：未编辑时默认 2M 选中（aria-pressed 主档）。
const tierButtons = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".lab-bulk-dialog__restart-actions .btn"));
const pressedIdx = tierButtons().findIndex((b) => b.getAttribute("aria-pressed") === "true");
ok(GOAL_BUDGET_PRESET_M[pressedIdx] === 2, "2M preset pressed by default (unset readback)");

// 验收①：不改预算直接确认 → 回调携带当前默认（App 层与 readback 相同 ⇒ 零写桥）。
const confirmBtns = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".lab-bulk-dialog__actions .btn"));
await act(async () => { confirmBtns()[0].click(); });
ok(confirmedTokens === 2_000_000, "confirm without editing carries 2,000,000 (default unchanged)");

// 改预算：点 5M 档 → 确认回调携带 5,000,000。
await renderDialog(2_000_000);
await act(async () => { tierButtons()[2].click(); });
await act(async () => { confirmBtns()[0].click(); });
ok(confirmedTokens === 5_000_000, "preset 5M → confirm carries 5,000,000 raw tokens");

// 自定义输入：合法值生效；非法值禁用确认。（React 受控输入在 jsdom 里必须
// 走原型 value setter 再派发 input 事件，直接赋值不触发 onChange。）
const nativeInputValueSetter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!;
function typeCustomInput(value: string) {
  const input = document.querySelector<HTMLInputElement>('input[type="number"]')!;
  nativeInputValueSetter.call(input, value);
  input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
}
await renderDialog(2_000_000);
await act(async () => { typeCustomInput("3.5"); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
await act(async () => { confirmBtns()[0].click(); });
ok(confirmedTokens === 3_500_000, "custom 3.5 M → confirm carries 3,500,000 raw tokens");

await renderDialog(2_000_000);
await act(async () => { typeCustomInput("0.01"); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
ok(document.querySelector(".lab-bulk-dialog__warn") !== null, "invalid custom input renders the warning banner");
ok(confirmBtns()[0].disabled === true, "invalid custom input disables confirm");

// 取消路径（验收②在组件层的投影）：取消回调触发、不触发确认。
await renderDialog(2_000_000);
await act(async () => { confirmBtns()[1].click(); });
ok(cancelled === 1 && confirmedTokens === null, "cancel fires onCancel only (不激活)");

// ── ③ 激活流门控（App.tsx 源级）──
const appSrc = readFileSync(fileURLToPath(new URL("../App.tsx", import.meta.url)), "utf8");
ok(appSrc.includes("if (goalBudgetConfirmEnabled) {"), "handleSend carries the confirm gate (switch on ⇒ dialog)");
ok(appSrc.includes('stashPendingGoalBudget({ kind: "draft"'), "goal draft submit path stashes the pending activation");
ok(appSrc.includes('stashPendingGoalBudget({ kind: "slash"'), "/goal command path stashes the pending activation (两条用户显式入口)");
ok(appSrc.includes("SetGoalTokenBudgetForTab(pending.tabId, tokens)"), "confirm persists via the ForTab bridge (持久化+本 tab 镜像 live)");
ok(appSrc.includes("tokens !== goalTokenBudgetTokens"), "confirm skips the bridge write when the value is unchanged (未编辑零写桥)");
ok(appSrc.includes("replaceComposerInsert(pending.tabId"), "cancel restores the draft via the insert channel (不激活且不丢稿)");
ok(appSrc.includes("<GoalBudgetConfirmDialog"), "App mounts GoalBudgetConfirmDialog");

// ── ④ 实验室注册（安全/成本控制卡两成员行）──
const yamlSrc = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
const labDefaultSrc = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
for (const src of [[yamlSrc, "lab-layout.yaml"], [labDefaultSrc, "labLayoutDefault.ts"]] as const) {
  const cardStart = src[0].indexOf('id: "safetyCostControl"') >= 0
    ? src[0].indexOf('id: "safetyCostControl"')
    : src[0].indexOf("id: safetyCostControl");
  const cardBody = src[0].slice(cardStart, src[0].indexOf("modelStrategy", cardStart));
  ok(cardBody.includes("goalBudgetConfirm"), `${src[1]}: goalBudgetConfirm member under safetyCostControl`);
  ok(cardBody.includes("goalTokenBudgetM"), `${src[1]}: goalTokenBudgetM member under safetyCostControl`);
}
const lightKeysSrc = readFileSync(fileURLToPath(new URL("../lab/labLightKeys.ts", import.meta.url)), "utf8");
ok(lightKeysSrc.includes("goalBudgetConfirm:"), "light key predicate registered for goalBudgetConfirm");
ok(lightKeysSrc.includes("goalTokenBudgetM:"), "light key predicate registered for goalTokenBudgetM (≠2M 即亮)");
const tiersSrc = readFileSync(fileURLToPath(new URL("../lib/experimentTiers.ts", import.meta.url)), "utf8");
ok(tiersSrc.includes('goalBudgetConfirm: "optional"'), "tier table: goalBudgetConfirm = optional");
ok(tiersSrc.includes('goalTokenBudgetM: "optional"'), "tier table: goalTokenBudgetM = optional");
const labSectionSrc = readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
ok(labSectionSrc.includes('testId="lab-toggle-goalBudgetConfirm"'), "safety-cost pane renders the confirm switch row");
ok(labSectionSrc.includes('testId="lab-toggle-goalTokenBudgetM"'), "safety-cost pane renders the budget number row");
ok(labSectionSrc.includes("app.SetGoalBudgetConfirm(on)"), "switch row wires SetGoalBudgetConfirm");
ok(labSectionSrc.includes("app.SetGoalTokenBudget("), "budget row wires SetGoalTokenBudget (M×1e6 换算在调用点)");

// ── ⑤ 三语 locale 平行 ──
const zh = (await import("../locales/zh")).zh;
const en = (await import("../locales/en")).en;
const zhTW = (await import("../locales/zh-TW")).zhTW;
const goalKeys = Object.keys(en).filter((k) => k.startsWith("composer.goalBudget.") || k.startsWith("settings.goalBudgetConfirm") || k.startsWith("settings.goalTokenBudgetM"));
ok(goalKeys.length >= 11, `locale tables carry the tl1835 key family (${goalKeys.length} keys)`);
ok(goalKeys.every((k) => k in zh && k in zhTW), "zh / zh-TW carry every tl1835 key (三语平行)");

// ── ⑥ 桥接面 ──
const bridgeSrc = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
ok(bridgeSrc.includes("SetGoalBudgetConfirm(enabled: boolean): Promise<void>;"), "AppBindings declares SetGoalBudgetConfirm");
ok(bridgeSrc.includes("SetGoalTokenBudget(tokens: number): Promise<void>;"), "AppBindings declares SetGoalTokenBudget");
ok(bridgeSrc.includes("SetGoalTokenBudgetForTab(tabId: string, tokens: number): Promise<void>;"), "AppBindings declares SetGoalTokenBudgetForTab");
ok(bridgeSrc.includes("async SetGoalBudgetConfirm() {}") && bridgeSrc.includes("async SetGoalTokenBudgetForTab() {}"), "mock stubs present");
const viewTypesSrc = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");
ok(viewTypesSrc.includes("goalBudgetConfirm?: boolean;") && viewTypesSrc.includes("goalTokenBudget?: number;"), "SettingsView readback fields declared");

await act(async () => { root.unmount(); });
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
