// Run: tsx src/__tests__/session-storage-lock.test.ts
//
// 任务 916: storage version locked to v3. The settings panel keeps exactly one
// selectable stage (v3_only); the v4/v5 stages stay rendered but disabled so a
// user upgraded with v4 data still sees which stage they are on, and the 805
// migration routes below stay reachable — hiding the card would strand them.
// Like its sibling session-storage-modes.test.ts, this is a source contract:
// the panel is a 3k-line component, so assertions read the render source and
// the three dialect dictionaries.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const locales: Record<string, string> = {
  en: readFileSync(resolve(testDir, "../locales/en.ts"), "utf8"),
  zh: readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8"),
  "zh-TW": readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8"),
};

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

// 1) The stage buttons: everything except v3_only is disabled, and v3_only is
//    never disabled by the lock itself (busy/migration still apply to it).
ok(
  panel.includes(`disabled={busy || migrationRunning || mode !== "v3_only"}`),
  "non-v3 stage buttons are disabled by the 916 lock",
);
ok(
  !panel.includes(`disabled={busy || migrationRunning || mode === "v3_only"}`),
  "v3_only itself is not disabled by the lock",
);
// The disabled set is derived from the shared mode list, not a hand-written
// subset — a new stage added to SESSION_STORAGE_MODES locks by default.
ok(panel.includes("SESSION_STORAGE_MODES.map"), "stages render from SESSION_STORAGE_MODES");

// 2) The lock reason is on the card, above the stage note.
ok(panel.includes('t("settings.sessionStorage.locked")'), "the card renders the lock reason line");
const lockedLine = panel.indexOf('t("settings.sessionStorage.locked")');
const stageLine = panel.indexOf("settings.sessionStorage.stage.${storageMode}");
ok(lockedLine >= 0 && stageLine > lockedLine, "the lock reason renders before the stage note");

// 3) The 805 migration section stays in the same card — the path out for a v4
//    data holder must not be hidden with the stage buttons.
ok(panel.includes("SessionMigrationSection"), "the migration section stays hosted in the card");
const migrationRenderLine = panel.split("\n").find((line) => line.includes("<SessionMigrationSection")) ?? "";
ok(
  migrationRenderLine.trim().startsWith("<SessionMigrationSection"),
  "the migration section renders unconditionally (not wrapped in a mode gate)",
);

// 4) Three dialects carry the lock copy and a v3_only stage note that no
//    longer invites the reader into v4 ("Step into v4" / 「想试 v4」).
for (const [name, dict] of Object.entries(locales)) {
  ok(dict.includes('"settings.sessionStorage.locked"'), `${name} has the lock reason key`);
  ok(dict.includes('"settings.sessionStorage.stage.v3_only"'), `${name} keeps the v3 stage note`);
  ok(!dict.includes("想试 v4") && !dict.includes("隨時可進") && !dict.includes("Step into v4"),
    `${name} v3 stage note stops inviting the reader into v4`);
}
ok(
  locales.zh.includes("锁定 v3 保证兼容") && locales["zh-TW"].includes("鎖定 v3 保證相容") && locales.en.includes("locked for compatibility"),
  "all dialects state the lock reason (upstream studio no longer uses v4/v5)",
);

// 5) The stage/risk dictionaries keep all four modes: a user parked on
//    dual_write_read_v4 still gets their own stage + risk copy (the notes are
//    keyed by the configured mode, not by what the buttons allow).
for (const [name, dict] of Object.entries(locales)) {
  for (const mode of ["v3_only", "dual_write_read_v3", "dual_write_read_v4", "v4_only"]) {
    ok(dict.includes(`"settings.sessionStorage.stage.${mode}"`) && dict.includes(`"settings.sessionStorage.risk.${mode}"`),
      `${name} keeps stage/risk copy for ${mode}`);
  }
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
