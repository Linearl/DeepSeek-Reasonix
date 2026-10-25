// Run: npx tsx src/__tests__/task1124-highrisk-authz-card.test.ts
// tl 1124 acceptance harness (高风险授权五开关聚合卡):
//  ① 聚合结构：五开关在 SettingsPanel 的 autopilot pane 中聚合为**一张**
//     .set-highrisk-authorize 清单卡（卡头 + 5 条清单行），不再是五个独立
//     的 SettingsField —— 聚合前的五个 SettingsField 形态必须消失；
//  ② 语义零变化：五项各读各的授权键（s.authorize*）、各走各的桥接 setter
//     （app.SetAuthorize*，一次点击只调用一项，无批量合并写）、默认值语义
//     （默认全不勾 = Boolean(s.*) 为假）逐项在卡内可见；
//  ③ 说明文案保留：五项各自的 Hint 键（settings.authorize*Hint）仍在卡内
//     渲染；卡头补充标题 + 总说明两把新钥（三语均有）；
//  ④ 五开关的语义面（Go setter + config 键 + bridge 声明）零改动 —— 本测试
//     核验 bridge.ts 五个 SetAuthorize* 方法仍在、settingsViewTypes 五个
//     字段仍声明。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 1124 high-risk authorize aggregation card");

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const viewTypes = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");

// ① 聚合结构.
{
  ok(panel.includes('className="set-highrisk-authorize"'),
    "the autopilot pane renders ONE .set-highrisk-authorize card");
  ok(panel.includes('className="set-highrisk-authorize__head"') &&
     panel.includes('set-highrisk-authorize__title') &&
     panel.includes('set-highrisk-authorize__hint'),
    "the card carries a head block (title + overall hint)");
  // 聚合前的五个独立 SettingsField 形态必须消失。
  const oldStandaloneCount = (panel.match(/<SettingsField label=\{t\("settings\.authorize/g) ?? []).length;
  ok(oldStandaloneCount === 0,
    `the five standalone SettingsField rows are gone (found ${oldStandaloneCount})`);
  // 五行清单行都在卡内（按 key 断言）。
  const rowKeys = [
    "authorizeOutboundSend",
    "authorizeDataMigrationDelete",
    "authorizeDestructiveChange",
    "authorizeNewTechRoute",
    "authorizePaidQuotaAlloc",
  ];
  const missingRows = rowKeys.filter((k) => !panel.includes(`{ key: "${k}"`));
  ok(missingRows.length === 0,
    `the card lists all five rows (missing: ${JSON.stringify(missingRows)})`);
  ok(panel.includes('className="set-highrisk-authorize__row"'),
    "rows render with the aggregated-row class (one map template covering all five)");
}

// ② 语义零变化：读写键 + setter + 默认值语义.
{
  const setters = [
    "SetAuthorizeOutboundSend",
    "SetAuthorizeDataMigrationDelete",
    "SetAuthorizeDestructiveChange",
    "SetAuthorizeNewTechRoute",
    "SetAuthorizePaidQuotaAlloc",
  ];
  const missingSetters = setters.filter((s) => !panel.includes(`app.${s}(`));
  ok(missingSetters.length === 0,
    `every row wires its own bridge setter (missing: ${JSON.stringify(missingSetters)})`);
  const stateKeys = [
    "s.authorizeOutboundSend",
    "s.authorizeDataMigrationDelete",
    "s.authorizeDestructiveChange",
    "s.authorizeNewTechRoute",
    "s.authorizePaidQuotaAlloc",
  ];
  const missingState = stateKeys.filter((s) => !panel.includes(s));
  ok(missingState.length === 0,
    `every row reads its own settings-view key (missing: ${JSON.stringify(missingState)})`);
  // 默认值语义：全不勾（checked 由 Boolean(s.*) 派生，出厂 false）。
  ok((panel.match(/checked: Boolean\(s\.authorize/g) ?? []).length >= 5,
    "every checkbox defaults from its boolean state (unchecked by default)");
  // bridge 五个方法仍在（语义面零改动）。
  const missingBridge = setters.filter((s) => !bridge.includes(`${s}(enabled: boolean)`));
  ok(missingBridge.length === 0,
    `bridge.ts keeps all five setters declared (missing: ${JSON.stringify(missingBridge)})`);
  // settingsViewTypes 五个字段仍在。
  const missingTypes = [
    "authorizeOutboundSend",
    "authorizeDataMigrationDelete",
    "authorizeDestructiveChange",
    "authorizeNewTechRoute",
    "authorizePaidQuotaAlloc",
  ].filter((k) => !viewTypes.includes(k));
  ok(missingTypes.length === 0,
    `settingsViewTypes keeps all five fields (missing: ${JSON.stringify(missingTypes)})`);
}

// ③ 说明文案保留（五 Hint 键 + 卡头两把新钥三语）.
{
  const hintKeys = [
    "settings.authorizeOutboundSendHint",
    "settings.authorizeDataMigrationDeleteHint",
    "settings.authorizeDestructiveChangeHint",
    "settings.authorizeNewTechRouteHint",
    "settings.authorizePaidQuotaAllocHint",
  ];
  const missingHints = hintKeys.filter((k) => !panel.includes(`t("${k}")`));
  ok(missingHints.length === 0,
    `every row renders its retained hint (missing: ${JSON.stringify(missingHints)})`);
  ok(panel.includes('t("settings.highRiskAuthorize")') && panel.includes('t("settings.highRiskAuthorizeHint")'),
    "the card head renders the title + overall-hint keys");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = [
      "settings.highRiskAuthorize",
      "settings.highRiskAuthorizeHint",
      ...hintKeys,
    ].filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0,
      `${locale} carries the card head keys + all five row hints (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
