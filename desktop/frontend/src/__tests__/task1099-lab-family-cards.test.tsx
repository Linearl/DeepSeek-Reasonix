// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1099-lab-family-cards.test.tsx
// 任务 1099 acceptance harness（实验室 pane 子项按功能族聚合成卡片容器，首期
// smartAutomation 试点）：
//  ① 分组呈现：smartAutomation pane 恰好出现 3 个 LabSectionCard（任务书验收
//     划两族「梦境」「决策模式」；卡内实存第三块「后台与续跑」收纳 961 心跳
//     后台化 + 1069 续轮提醒 + 1070 续跑总闸三条，全部子项有明确族归属），
//     标题分别来自三语 locale 键；
//  ② 成员归属逐项断言：每个成员行落在指定族卡体内（不只看卡片数量）；
//  ③ 徽章语义不变：成员徽章（labLabel）仍留在各自成员行（dream/
//     decisionPatterns/heartbeatBackground/stalledIntentNudge/
//     standardContinuation 各恰好一次）、卡头 smartAutomation 徽章恰好一次
//     （621 + 774 非回归）；
//  ④ 键/setter 接线不变：各成员行仍读自己的配置键、写自己的 setter
//     （561 结构性合同的 1099 面复核）；
//  ⑤ 非试点零回归：fullAccess / contextGovernance / tabManagement 三张未改造
//     卡的 pane 分支内不含任何 LabSectionCard；
//  ⑥ 组件/样式合同：LabSectionCard 在 SettingsForm 定义并被 SettingsPanel
//     导入；样式只用 theme token（无裸色值），窄屏媒体查询存在（响应式）；
//  ⑦ 三语 locale：六个新键在 zh/en/zh-TW 均存在。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const form = readFileSync(fileURLToPath(new URL("../components/SettingsForm.tsx", import.meta.url)), "utf8");
const css = readFileSync(fileURLToPath(new URL("../styles.css", import.meta.url)), "utf8");
const zh = readFileSync(fileURLToPath(new URL("../locales/zh.ts", import.meta.url)), "utf8");
const en = readFileSync(fileURLToPath(new URL("../locales/en.ts", import.meta.url)), "utf8");
const zhTW = readFileSync(fileURLToPath(new URL("../locales/zh-TW.ts", import.meta.url)), "utf8");

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\ntask 1099 lab family section cards");

const smartCard = cardSlice('{selected === "smartAutomation" && (', '{selected === "searchHistory" && (');
ok(smartCard.length > 0, "smartAutomation pane branch located");

// ① 三个分组卡 + 标题键各出现一次。
{
  const opens = smartCard.match(/<LabSectionCard\b/g) ?? [];
  const closes = smartCard.match(/<\/LabSectionCard>/g) ?? [];
  ok(opens.length === 3 && closes.length === 3, `smartAutomation pane renders exactly 3 LabSectionCard containers (open ${opens.length}, close ${closes.length})`);
  for (const key of ["settings.lab.family.dream", "settings.lab.family.decisionPatterns", "settings.lab.family.backgroundContinuation"]) {
    ok(smartCard.includes(`title={t("${key}")}`), `family card titled from locale key ${key}`);
  }
}

// ② 成员归属逐项断言：用「族卡开启 → 下一个族卡/分支结尾」切片，断言成员行
//    label 键落在指定族内。
function familySlice(titleKey: string): string {
  const start = smartCard.indexOf(`title={t("${titleKey}")}`);
  if (start < 0) return "";
  const next = smartCard.indexOf("<LabSectionCard", start + 1);
  const end = next > start ? next : smartCard.length;
  return smartCard.slice(start, end);
}
{
  const dream = familySlice("settings.lab.family.dream");
  ok(dream.includes('label={labLabel("dream", t("settings.dream"))}'), "dream family card hosts the 梦境整理 row");
  ok(dream.includes('label={t("settings.dreamCreateTask")}'), "dream family card hosts the 创建自动任务 row");

  const dp = familySlice("settings.lab.family.decisionPatterns");
  ok(dp.includes('label={labLabel("decisionPatterns", t("settings.decisionPatterns"))}'), "decision family card hosts the 决策模式提取 row");
  ok(dp.includes('label={t("settings.decisionPatternsCreateTask")}'), "decision family card hosts the 创建自动任务 row");
  ok(dp.includes('label={t("settings.decisionPatternsThreshold")}'), "decision family card hosts the 聚合阈值 row");
  ok(dp.includes('label={t("settings.decisionPatternsFilePath")}'), "decision family card hosts the 决策模式文件位置 row");
  ok(dp.includes('label={t("settings.decisionPatternsAutoApprove")}'), "decision family card hosts the 跳过人工批准 row");

  const bg = familySlice("settings.lab.family.backgroundContinuation");
  ok(bg.includes('label={labLabel("heartbeatBackground",'), "background family card hosts the 心跳后台化 row");
  ok(bg.includes('label={labLabel("stalledIntentNudge",'), "background family card hosts the 续轮提醒 row");
  ok(bg.includes('label={labLabel("standardContinuation",'), "background family card hosts the 续跑总闸 row");
}

// ③ 徽章语义不变（源码计数，621 口径：成员徽章各恰一次；卡头恰一次）。
{
  for (const id of ["dream", "decisionPatterns", "heartbeatBackground", "stalledIntentNudge", "standardContinuation", "smartAutomation"]) {
    const n = (smartCard.match(new RegExp(`labLabel\\("${id}"`, "g")) ?? []).length;
    ok(n === 1, `member badge stays exactly once on its own row (${id} × ${n})`);
  }
}

// ④ 键/setter 接线不变（561 合同的 1099 面复核——聚合容器不得改写读写路径）。
{
  const wirings: Array<[string, string]> = [
    ["dream", "app.SetExperimentalDream(on)"],
    ["decisionPatterns", "app.SetExperimentalDecisionPatterns(on)"],
    ["heartbeatBackground", "app.SetExperimentalHeartbeatBackground(on)"],
    ["stalledIntentNudge", "app.SetStalledIntentNudge(on)"],
    ["standardContinuation", "app.SetStandardContinuation(on)"],
  ];
  for (const [id, setter] of wirings) {
    ok(smartCard.includes(setter), `member keeps its own setter (${setter})`);
  }
  ok(smartCard.includes("s.decisionPatternThreshold") && smartCard.includes("s.decisionPatternFilePath") && smartCard.includes("s.decisionPatternAutoApprove"),
    "decision dials keep reading their own config keys");
}

// ⑤ 非试点卡零回归：未改造卡的 pane 分支不含 LabSectionCard。
{
  // 用各卡在源码中的后继分支做切片终点（marker 不存在会让 cardSlice 返回空、
  // 测试假红/假绿——561 同款纪律）。
  const fullAccessSlice = cardSlice('{selected === "fullAccess" && (', '{selected === "updateFeedback" && (');
  ok(fullAccessSlice.length > 0 && !fullAccessSlice.includes("<LabSectionCard"), "fullAccess pane has no family card (zero regression)");
  const ctxSlice = cardSlice('{selected === "contextGovernance" && (', '{selected === "devDebug" && (');
  ok(ctxSlice.length > 0 && !ctxSlice.includes("<LabSectionCard"), "contextGovernance pane has no family card (zero regression)");
  const tabSlice = cardSlice('{selected === "tabManagement" && (', '{selected === "smartAutomation" && (');
  ok(tabSlice.length > 0 && !tabSlice.includes("<LabSectionCard"), "tabManagement pane has no family card (zero regression)");
}

// ⑥ 组件/样式合同。
{
  ok(form.includes("export function LabSectionCard"), "LabSectionCard is defined in SettingsForm");
  ok(panel.includes('import { LabSectionCard, SettingsField, SettingsSection } from "./SettingsForm"'), "SettingsPanel imports LabSectionCard");
  const block = css.slice(css.indexOf(".lab-section-card {"), css.indexOf("@media (max-width: 800px) {\n  /* 窄屏下 settings-field 栈化"));
  const bareColors = block.match(/#[0-9a-fA-F]{3,8}\b|rgba?\(/g) ?? [];
  ok(bareColors.length === 0, `family-card CSS carries no token-bypassing colors (found ${bareColors.length})`);
  ok(css.includes("var(--border-soft)") && css.includes("var(--radius-md)") && css.includes("var(--bg-soft)"), "family card styles go through theme tokens");
  ok(css.includes("@media (max-width: 800px)") && block !== "", "narrow-screen media query keeps the card responsive");
  ok(css.includes(".lab-section-card__body > .settings-field:first-child"), "in-card first row restores the borderless top (divider = in-card only)");
}

// ⑦ 三语 locale。
{
  const keys = ["settings.lab.family.dream", "settings.lab.family.dreamHint", "settings.lab.family.decisionPatterns", "settings.lab.family.decisionPatternsHint", "settings.lab.family.backgroundContinuation"];
  for (const [dialect, text] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
    const missing = keys.filter((k) => !text.includes(`"${k}"`));
    ok(missing.length === 0, `${dialect} carries all family-card keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
