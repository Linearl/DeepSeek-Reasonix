// Run: tsx src/__tests__/session-storage-lock.test.ts
//
// 任务 916: storage version locked to v3. 任务 tl1287 (20261031 用户拍板) 深化：
// v4 支持退役后，会话存储卡不再渲染任何 v4 档按钮——分段控件只出 v3 一档；
// 仍停在 v4 档的用户由 stage/risk 行（按已存档位取键）如实看到自己在哪，
// 回去路径是同卡的 805 迁移器（后端对降档永远放行）。
// 与姊妹文件 session-storage-modes.test.ts 同为源码契约测试：M7 卡自任务 561
// 起宿主在 ExperimentalLabSection.tsx（不在 SettingsPanel.tsx），两边都读渲染
// 源 + 三方言词典。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/ExperimentalLabSection.tsx"), "utf8");
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

// 1) tl1287: the segmented control renders from the v3-only selectable list —
//    no v4 stage button exists anywhere in the card.
ok(
  panel.includes("SESSION_STORAGE_SELECTABLE_MODES.map"),
  "the control renders from SESSION_STORAGE_SELECTABLE_MODES (tl1287)",
);
ok(
  panel.includes('const SESSION_STORAGE_SELECTABLE_MODES = ["v3_only"]'),
  "the selectable list is exactly v3_only",
);
ok(
  !panel.includes("SESSION_STORAGE_MODES.map"),
  "no render path walks the full four-mode list (v4 buttons are gone)",
);
// The v3 button is disabled only by busy/migration — never by the mode itself,
// so a user parked on v4 can click straight down to v3 (downgrades always pass
// ValidateSessionStorageTransition).
ok(
  panel.includes("disabled={busy || migrationRunning}") && !panel.includes('mode !== "v3_only"'),
  "v3 stays clickable for v4-parked users (downgrade, not lock)",
);

// 2) The stored-value parser keeps all four modes so stage/risk lines still
//    speak to whoever is parked on a v4 stage (display only — no option).
ok(
  panel.includes('const SESSION_STORAGE_MODES = ["v3_only", "dual_write_read_v3", "dual_write_read_v4", "v4_only"]'),
  "the stored-value parser keeps all four modes",
);
ok(
  panel.includes("settings.sessionStorage.stage.${storageMode}") && panel.includes("settings.sessionStorage.risk.${storageMode}"),
  "stage/risk lines stay keyed by the stored mode",
);

// 3) The lock reason is on the card, above the stage note.
ok(panel.includes('t("settings.sessionStorage.locked")'), "the card renders the lock reason line");
const lockedLine = panel.indexOf('t("settings.sessionStorage.locked")');
const stageLine = panel.indexOf("settings.sessionStorage.stage.${storageMode}");
ok(lockedLine >= 0 && stageLine > lockedLine, "the lock reason renders before the stage note");

// 4) The 805 migration section stays in the same card — the path out for a v4
//    data holder must not be hidden now that the stage buttons are gone.
ok(panel.includes("SessionMigrationSection"), "the migration section stays hosted in the card");
const migrationRenderLine = panel.split("\n").find((line) => line.includes("<SessionMigrationSection")) ?? "";
ok(
  migrationRenderLine.trim().startsWith("<SessionMigrationSection"),
  "the migration section renders unconditionally (not wrapped in a mode gate)",
);

// 5) Three dialects carry the lock copy and a v3_only stage note that no
//    longer invites the reader into v4 ("Step into v4" / 「想试 v4」).
for (const [name, dict] of Object.entries(locales)) {
  ok(dict.includes('"settings.sessionStorage.locked"'), `${name} has the lock reason key`);
  ok(dict.includes('"settings.sessionStorage.stage.v3_only"'), `${name} keeps the v3 stage note`);
  ok(!dict.includes("想试 v4") && !dict.includes("隨時可進") && !dict.includes("Step into v4"),
    `${name} v3 stage note stops inviting the reader into v4`);
  // tl1287: the lock copy says the card no longer OFFERS v4/v5 stages (not
  // just "cannot be selected" — there is nothing to select any more).
  ok(
    (dict.includes("卡内不再提供") || dict.includes("卡內不再提供") || dict.includes("no v4/v5 stage is offered")),
    `${name} lock copy reflects the retired v4 option`,
  );
}
ok(
  locales.zh.includes("锁定 v3 保证兼容") && locales["zh-TW"].includes("鎖定 v3 保證相容") && locales.en.includes("locked for compatibility"),
  "all dialects state the lock reason (upstream studio no longer uses v4/v5)",
);

// 6) The stage/risk dictionaries keep all four modes: a user parked on
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
