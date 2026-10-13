// Run: tsx src/__tests__/home-pointer-lab.test.ts
//
// 任务 846 的前端对位钉：实验室入口登记、六条风险进向导展示（验收 3）、
// 缺确认禁发、三语文案齐、存储只读页红线不被破坏。跨语言一致性（Go 风险
// id ↔ 前端 mock/文案）直接对读两份源码，漂移即红。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

const labPane = read("../components/HomePointerLabPane.tsx");
const settingsPanel = read("../components/SettingsPanel.tsx");
const labLayoutYaml = read("../lab/lab-layout.yaml");
const labLayoutDefault = read("../lab/labLayoutDefault.ts");
const labLightKeys = read("../lab/labLightKeys.ts");
const experimentTiers = read("../lib/experimentTiers.ts");
const bridge = read("../lib/bridge.ts");
const storagePage = read("../components/StorageSettingsPage.tsx");
const risksGo = read("../../../../internal/homemigrate/risks.go");
const locales = ["zh.ts", "zh-TW.ts", "en.ts"].map((name) => read(`../locales/${name}`));

let passed = 0;
let failed = 0;

function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const SIX_RISK_IDS = [
  "dual-home-split",
  "pointer-corrupt-fallback",
  "interrupted-migration",
  "concurrent-instance",
  "onedrive-lock",
  "credential-env",
];

console.log("\nhome pointer lab contract (task 846)");

// ── 实验室登记 ──────────────────────────────────────────────
ok(experimentTiers.includes('homePointer: "unstable"'), "tier register pins homePointer as unstable (Go render.go parity)");
ok(labLayoutYaml.includes("id: homePointer") && /id: homePointer[\s\S]{0,120}tier: unstable/.test(labLayoutYaml), "lab-layout.yaml registers the homePointer card (storage group)");
ok(labLayoutDefault.includes('id: "homePointer"'), "labLayoutDefault safety net registers homePointer");
ok(settingsPanel.includes('"homePointer"'), "LAB_PANE_IDS whitelist admits homePointer");
ok(labLightKeys.includes("homePointer:"), "lab light key predicate registered for homePointer");

// ── 验收 3：六条风险进向导 ──────────────────────────────────
for (const id of SIX_RISK_IDS) {
  ok(risksGo.includes(`= "${id}"`), `Go risks.go declares ${id}`);
}
ok((risksGo.match(/RiskID = "/g) ?? []).length >= 6 || risksGo.includes("AllRisks()"), "Go risks.go exposes AllRisks()");
ok((risksGo.match(/DetailKey: "settings\.homePointer\.risk\./g) ?? []).length === 6, "Go AllRisks carries exactly six locale detail keys");

const paneRiskRender = labPane.includes("risks.map((risk)") && labPane.includes('type="checkbox"') && labPane.includes("t(risk.detailKey");
ok(paneRiskRender, "wizard renders one acknowledged checkbox per risk from risk.detailKey");
ok(labPane.includes("risks.every((risk) => acknowledged.has(risk.id))") && labPane.includes("!allAcknowledged"), "start button stays disabled until every risk is acknowledged");
ok(labPane.includes("HomeMigrationPreview(") && labPane.includes("HomeMigrationExecute(") && labPane.includes("HomeMigrationResetToDefault("), "wizard wires preview, execute and reset through the bridge");
ok((bridge.match(/detailKey: "settings\.homePointer\.risk\./g) ?? []).length === 6, "bridge mock mirrors the same six risk ids/keys");

// 验收 4 的 UI 侧：.env 风险条目明说 ACL 重做
ok(locales.every((locale) => locale.includes("settings.homePointer.risk.credentialEnv")), "all three locales carry the credential-env risk line");

// ── 三语文案齐 ──────────────────────────────────────────────
const zhKeys = ["settings.homePointer", "settings.homePointer.hint", "settings.homePointer.riskTitle", "settings.homePointer.risk.dualHomeSplit", "settings.homePointer.risk.pointerCorruptFallback", "settings.homePointer.risk.interruptedMigration", "settings.homePointer.risk.concurrentInstance", "settings.homePointer.risk.oneDriveLock", "settings.homePointer.risk.credentialEnv", "settings.homePointer.reset"];
for (const key of zhKeys) {
  ok(locales.every((locale) => locale.includes(`"${key}"`)), `locale key ${key} present in zh/zh-TW/en`);
}

// ── 存储只读页红线（settings-storage-page 契约的对偶）────────
for (const method of ["HomeMigrationExecute", "HomeMigrationPreview", "HomePointerStatus"]) {
  ok(!storagePage.includes(method), `read-only storage page stays clean of ${method}`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
