// Run: tsx src/__tests__/session-storage-modes.test.ts
//
// Task 155: the four conversation-store modes are wired end to end. The settings
// control renders its stage/risk copy through dynamic dictionary keys, so neither
// tsc nor the dictionary type can catch a hole - a missing key would ship as a raw
// key name in the panel. This contract keeps the control, the copy and the wire
// fields in step.
// tl1287 (20261031): v4 support retired — the card OFFERS only v3; the four-mode
// list survives as the stored-value parser and as stage/risk copy for whoever is
// still parked on a v4 stage. M7 card host is ExperimentalLabSection.tsx since
// task 561 (not SettingsPanel.tsx).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/ExperimentalLabSection.tsx"), "utf8");
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

// 1) The card keeps every mode name somewhere (parser + copy keys) but OFFERS
//    only v3: the selectable list is exactly ["v3_only"] (tl1287).
for (const mode of MODES) {
  ok(panel.includes(`"${mode}"`), `card parser keeps mode ${mode}`);
}
ok(panel.includes("SESSION_STORAGE_MODES"), "stored-value parser is driven by SESSION_STORAGE_MODES");
ok(panel.includes('const SESSION_STORAGE_SELECTABLE_MODES = ["v3_only"]'), "the control offers exactly v3_only (tl1287)");
ok(!panel.includes("SESSION_STORAGE_MODES.map"), "no render path turns the four-mode list into buttons");
ok(panel.includes("settings.sessionStorage.stage."), "stage note is rendered");
ok(panel.includes("settings.sessionStorage.risk."), "risk note is rendered");
ok(panel.includes('t("settings.sessionStorage.restartPending"'), "restart-pending line is rendered");
ok(panel.includes("sessionStorageRestartPending"), "restart-pending flag drives the banner");

// 1b) 任务 805: the migration routes hang off the same card, two-step confirm,
// with the stage buttons locked while a run is in flight.
const section = readFileSync(resolve(testDir, "../components/SessionMigrationSection.tsx"), "utf8");
ok(panel.includes("SessionMigrationSection"), "the storage card hosts the migration section");
ok(panel.includes("migrationRunning"), "the panel locks stage buttons with the migration state");
ok(section.includes("app.PlanSessionV4Backfill"), "v4 backfill plans first (dry-run default)");
ok(section.includes("app.RunSessionV4Backfill"), "v4 backfill runs on the second click");
// tl1287④: the v5 entry is UNCONDITIONAL now — a missing store shows a hint
// naming the probed path instead of hiding the button.
ok(section.includes("app.SessionV5StoreAvailable") && section.includes("app.SessionV5StoreLocation"), "the v5 hint names the probed store path");
ok(!section.includes("v5Available &&"), "the v5 button is not gated on v5Available (tl1287④)");
ok(section.includes("app.PlanSessionV5Import") && section.includes("app.RunSessionV5Import"), "the v5 import rides the same confirm flow");
ok(section.includes("settings.migration.registryBroken") && section.includes("settings.migration.registryMissing"), "the confirm step carries the registry pre-check warning (tl1287④)");
ok(section.includes("MIGRATION_PROGRESS_EVENT") || section.includes("sessionStorageMigrationProgress"), "progress lines arrive via runtime events");

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
  for (const key of [
    "settings.migration.action",
    "settings.migration.planSummary",
    "settings.migration.mode.incremental",
    "settings.migration.mode.full",
    "settings.migration.backupDone",
    "settings.migration.restoreNote",
  ]) {
    ok(dict.includes(`"${key}"`), `${name} has the migration key ${key}`);
  }
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
