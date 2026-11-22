// Run: npx tsx src/__tests__/task1124-highrisk-authz-card.test.ts
// tl 1124 acceptance harness → tl1741r2 再换代（用户 20261122 打回裁定「stable
// 清主选项（常开）、子选项保留配置」：autopilot 卡体回归 rail（无主开关行），
// 卡内 .set-highrisk-authorize 聚合卡随宿主 pane 回归——五 checkbox 各写各的
// bridge setter，语义面与 1124 交付时逐字节同口径）：
//  ① 聚合卡在 autopilot 卡 pane 渲染（无主开关卡的子项面）；
//  ② 语义面零改动：bridge 五 setter + view types 五字段 + Go setter 仍在；
//  ③ 三语文案键保留。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 1124 high-risk authorize aggregation card (restored with autopilot card, tl1741r2)");

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const viewTypes = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");
const goEdit = readFileSync(fileURLToPath(new URL("../../../../internal/config/edit.go", import.meta.url)), "utf8");

// ① 聚合卡回归（tl1741r2）：.set-highrisk-authorize 随 autopilot 卡 pane 回归，
//    五行各写各的 setter；宿主卡无主开关（SetDesktopAutopilot 不回归）。
{
  const autopilot = panel.slice(panel.indexOf('{selected === "autopilot" && ('), panel.indexOf('{selected === "modelStrategy" && ('));
  ok(autopilot.includes('className="set-highrisk-authorize"'),
    "the aggregated authorize card renders inside the autopilot card pane (tl1741r2)");
  ok(autopilot.includes('t("settings.highRiskAuthorize")'),
    "the card head renders in the autopilot pane");
  for (const setter of [
    "app.SetAuthorizeOutboundSend(next)",
    "app.SetAuthorizeDataMigrationDelete(next)",
    "app.SetAuthorizeDestructiveChange(next)",
    "app.SetAuthorizeNewTechRoute(next)",
    "app.SetAuthorizePaidQuotaAlloc(next)",
  ]) {
    ok(autopilot.includes(setter), `authorize row wires its own bridge setter: ${setter}`);
  }
  ok(!panel.includes("app.SetDesktopAutopilot("),
    "the autopilot master switch stays retired (card body only, tl1741 removal stands)");
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
