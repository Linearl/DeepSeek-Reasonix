// Run: npx tsx src/__tests__/task1124-highrisk-authz-card.test.ts
// tl 1124 acceptance harness → tl1741 换代（用户 20261121 三拍：autopilot 稳定
// 卡随「稳定档不再提供开关」移除——卡内 .set-highrisk-authorize 聚合卡随宿主
// pane 退役；authorize_* 五键保留为活键，config 手写可调，Go setter / bridge
// 声明 / settingsViewTypes 字段 / 三语 Hint 键全部保留——语义面零改动）：
//  ① 聚合卡不在实验室面板渲染（随 autopilot pane 移除）；
//  ② 语义面零改动：bridge 五 setter + view types 五字段 + Go setter 仍在；
//  ③ 三语文案键保留（退役不删——图墙/未来重挂位可复用）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 1124 high-risk authorize aggregation card (retired with autopilot pane, tl1741)");

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const viewTypes = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");
const goEdit = readFileSync(fileURLToPath(new URL("../../../../internal/config/edit.go", import.meta.url)), "utf8");

// ① 聚合卡退役（tl1741）：.set-highrisk-authorize 随 autopilot pane 移除。
{
  ok(!panel.includes('className="set-highrisk-authorize"'),
    "the aggregated authorize card left the lab panel with the autopilot pane (tl1741)");
  ok(!panel.includes('t("settings.highRiskAuthorize")'),
    "the card head renders nowhere (card retired)");
}

// ② 语义面零改动：bridge 五 setter + view types 五字段 + Go setter 仍在。
{
  const setters = [
    "SetAuthorizeOutboundSend",
    "SetAuthorizeDataMigrationDelete",
    "SetAuthorizeDestructiveChange",
    "SetAuthorizeNewTechRoute",
    "SetAuthorizePaidQuotaAlloc",
  ];
  const missingBridge = setters.filter((s) => !bridge.includes(`${s}(enabled: boolean)`));
  ok(missingBridge.length === 0,
    `bridge.ts keeps all five setters declared (missing: ${JSON.stringify(missingBridge)})`);
  const missingTypes = [
    "authorizeOutboundSend",
    "authorizeDataMigrationDelete",
    "authorizeDestructiveChange",
    "authorizeNewTechRoute",
    "authorizePaidQuotaAlloc",
  ].filter((k) => !viewTypes.includes(k));
  ok(missingTypes.length === 0,
    `settingsViewTypes keeps all five fields (missing: ${JSON.stringify(missingTypes)})`);
  const missingGo = setters.filter((s) => !goEdit.includes(`func (c *Config) ${s}`));
  ok(missingGo.length === 0,
    `internal/config keeps all five Go setters (missing: ${JSON.stringify(missingGo)})`);
}

// ③ 三语文案键保留（退役不删）。
{
  const hintKeys = [
    "settings.authorizeOutboundSendHint",
    "settings.authorizeDataMigrationDeleteHint",
    "settings.authorizeDestructiveChangeHint",
    "settings.authorizeNewTechRouteHint",
    "settings.authorizePaidQuotaAllocHint",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = [
      "settings.highRiskAuthorize",
      "settings.highRiskAuthorizeHint",
      ...hintKeys,
    ].filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0,
      `${locale} keeps the card head keys + all five row hints (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
