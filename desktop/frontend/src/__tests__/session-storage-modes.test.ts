// Run: tsx src/__tests__/session-storage-modes.test.ts
//
// Task 155: the four conversation-store modes are wired end to end. The settings
// control renders its stage/risk copy through dynamic dictionary keys, so neither
// tsc nor the dictionary type can catch a hole - a missing key would ship as a raw
// key name in the panel. This contract keeps the control, the copy and the wire
// fields in step.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const viewTypes = readFileSync(resolve(testDir, "../lib/settingsViewTypes.ts"), "utf8");
const locales: Record<string, string> = {
  en: readFileSync(resolve(testDir, "../locales/en.ts"), "utf8"),
  zh: readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8"),
  "zh-TW": readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8"),
};

const MODES = ["v3_only", "dual_write_read_v3", "dual_write_read_v4", "v4_only"];

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

// 1) The settings control lists every mode and renders its notes.
for (const mode of MODES) {
  ok(panel.includes(`"${mode}"`), `SettingsPanel lists mode ${mode}`);
}
ok(panel.includes("SESSION_STORAGE_MODES"), "control is driven by SESSION_STORAGE_MODES");
ok(panel.includes("settings.sessionStorage.stage."), "stage note is rendered");
ok(panel.includes("settings.sessionStorage.risk."), "risk note is rendered");
ok(panel.includes('t("settings.sessionStorage.restartPending"'), "restart-pending line is rendered");
ok(panel.includes("sessionStorageRestartPending"), "restart-pending flag drives the banner");

// 2) The wire carries both the configured and the boot-effective mode.
ok(viewTypes.includes("sessionStorageEffective"), "view carries the boot-effective mode");
ok(viewTypes.includes("sessionStorageRestartPending"), "view carries the restart-pending flag");

// 3) Every dictionary has all four labels plus stage/risk/restart copy.
for (const [name, dict] of Object.entries(locales)) {
  for (const mode of MODES) {
    ok(dict.includes(`"settings.sessionStorage.mode.${mode}"`), `${name} has the label for ${mode}`);
    ok(dict.includes(`"settings.sessionStorage.stage.${mode}"`), `${name} has the stage note for ${mode}`);
    ok(dict.includes(`"settings.sessionStorage.risk.${mode}"`), `${name} has the risk note for ${mode}`);
  }
  ok(dict.includes('"settings.sessionStorage.restartPending"'), `${name} has the restart-pending line`);
  ok(dict.includes("{{mode}}"), `${name} interpolates the effective mode in the restart line`);
  ok(dict.includes('"settings.sessionStorageHint"'), `${name} keeps the mode hint`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
