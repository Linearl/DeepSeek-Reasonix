// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1292-session-storage-subcards.test.ts
//
// tl1292（1287 返修）：会话存储卡子功能拆两组子卡片 + 错别字。源码契约三件：
//   ① 卡内结构——档位段仍是卡头，其下恰好两张 LabSectionCard（1099 既有功能族
//      分组卡，不造新机制）：「从 v4/v5 迁移」收 SessionMigrationSection；
//      「扫描&清理迁移备份」收 DagMigrationSection + DagBackupCleanupSection
//      （迁移在上、清理在下——tl1287⑤ blocked 文案「先在上方完成迁移」依赖
//      这个顺序）。不新增 SettingsField/rail/顶层入口（UI 影响最小口径）。
//   ② 三语键——两组标题键 ×3 方言；v5 三键的「店」（en "store" 直译错字，
//      用户截图读作「庄」）正字为「存储/儲存」，与 settings.sessionStore=
//      会话存储 的既有译法 SSOT 对齐；en 的 "store" 本正确保持不动。
//   ③ 防线——zh/zh-TW 词典全文「店」上限 0（同机制全量扫描结论：改前仅此
//      3 键 ×2 方言用「店」，改后归零），新增即红。
// 排版：子卡②内两步之间的细分隔线走既有 data 钩子选择器，不新增 className
// （class-contract 零新增），规则本身在这里锁住。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const labSection = readFileSync(resolve(testDir, "../components/ExperimentalLabSection.tsx"), "utf8");
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");
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

function count(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1;
}

// ── ① 卡内结构 ────────────────────────────────────────────────────────────
const sessionStoreCard = labSection.slice(
  labSection.indexOf('{selected === "sessionStore" && ('),
  labSection.indexOf('{selected === "viewLayout" && ('),
);
ok(sessionStoreCard.length > 0, "the sessionStore card slice is located");

const migrationCardOpen = '<LabSectionCard title={t("settings.sessionStorage.migrationGroup")}>';
const backupCardOpen = '<LabSectionCard title={t("settings.sessionStorage.backupGroup")}>';
ok(count(sessionStoreCard, "<LabSectionCard") === 2, "exactly two sub-cards live inside the sessionStore card");
ok(count(sessionStoreCard, migrationCardOpen) === 1, "one sub-card is titled with settings.sessionStorage.migrationGroup");
ok(count(sessionStoreCard, backupCardOpen) === 1, "one sub-card is titled with settings.sessionStorage.backupGroup");

const migrationAt = sessionStoreCard.indexOf(migrationCardOpen);
const backupAt = sessionStoreCard.indexOf(backupCardOpen);
ok(migrationAt >= 0 && backupAt > migrationAt, "the migration sub-card comes before the backup sub-card");

// 子卡成员归属：按 open→close 切片，成员必须落在各自卡内。
const closeTag = "</LabSectionCard>";
const migrationCard = sessionStoreCard.slice(migrationAt, sessionStoreCard.indexOf(closeTag, migrationAt) + closeTag.length);
const backupCard = sessionStoreCard.slice(backupAt, sessionStoreCard.indexOf(closeTag, backupAt) + closeTag.length);
ok(migrationCard.includes("<SessionMigrationSection"), "the v4/v5 migration section sits inside the migration sub-card");
ok(!migrationCard.includes("<DagMigrationSection") && !migrationCard.includes("<DagBackupCleanupSection"),
  "the migration sub-card hosts nothing from the DAG family");
ok(backupCard.includes("<DagMigrationSection") && backupCard.includes("<DagBackupCleanupSection"),
  "the backup sub-card hosts both DAG migration and backup cleanup");
ok(backupCard.indexOf("<DagBackupCleanupSection") > backupCard.indexOf("<DagMigrationSection"),
  "inside the backup sub-card the cleanup follows the DAG migration (blocked copy says 先在上方完成迁移)");
ok(!backupCard.includes("<SessionMigrationSection"), "the backup sub-card hosts no v4/v5 migration section");

// 每个动作组件在卡内只挂一次（拆卡不是复制）。
for (const member of ["<SessionMigrationSection", "<DagMigrationSection", "<DagBackupCleanupSection"]) {
  ok(count(sessionStoreCard, member) === 1, `${member.slice(1)} mounts exactly once in the sessionStore card`);
}

// 档位段仍是卡头：档位按钮渲染在第一张子卡之前，不被收进任何子卡。
const modesAt = sessionStoreCard.indexOf("SESSION_STORAGE_SELECTABLE_MODES.map");
ok(modesAt >= 0 && modesAt < migrationAt, "the storage-stage buttons stay above the sub-cards as the card head");
ok(!migrationCard.includes("SESSION_STORAGE_SELECTABLE_MODES") && !backupCard.includes("SESSION_STORAGE_SELECTABLE_MODES"),
  "no sub-card swallows the storage-stage controls");

// UI 影响最小：sessionStore pane 仍只有两张 SettingsField（会话存储 + 事件轮换），
// 没有借拆卡之名加行；LabSectionCard 走既有导入，不另起容器组件。
ok(count(sessionStoreCard, "<SettingsField") === 2, "the sessionStore pane keeps exactly two SettingsField rows (no new row)");
ok(/import \{[^}]*\bLabSectionCard\b[^}]*\} from "\.\/SettingsForm"/.test(labSection),
  "LabSectionCard is the existing SettingsForm export (task 1099), not a new container");

// ── ② 三语键 ──────────────────────────────────────────────────────────────
for (const [name, dict] of Object.entries(locales)) {
  for (const key of ["migrationGroup", "backupGroup"]) {
    ok(dict.includes(`"settings.sessionStorage.${key}"`), `${name} has settings.sessionStorage.${key}`);
  }
  for (const key of ["v5Action", "v5Hint", "v5Missing"]) {
    ok(dict.includes(`"settings.migration.${key}"`), `${name} keeps settings.migration.${key}`);
  }
}
ok(locales.zh.includes('"settings.sessionStorage.migrationGroup": "从 v4/v5 迁移"'), "zh migration group title reads 从 v4/v5 迁移");
ok(locales.zh.includes('"settings.sessionStorage.backupGroup": "扫描&清理迁移备份"'), "zh backup group title reads 扫描&清理迁移备份");
ok(locales["zh-TW"].includes('"settings.sessionStorage.migrationGroup": "從 v4/v5 遷移"'), "zh-TW migration group title reads 從 v4/v5 遷移");
ok(locales["zh-TW"].includes('"settings.sessionStorage.backupGroup": "掃描&清理遷移備份"'), "zh-TW backup group title reads 掃描&清理遷移備份");
ok(locales.en.includes('"settings.sessionStorage.migrationGroup": "Migrate from v4/v5"'), "en migration group title reads Migrate from v4/v5");
ok(locales.en.includes('"settings.sessionStorage.backupGroup": "Scan & clean migration backups"'), "en backup group title reads Scan & clean migration backups");

// 错别字正字：v5 三键与卡名既有译法（会话存储/會話儲存）同源。
ok(locales.zh.includes('"settings.migration.v5Action": "从 v5 存储导入…"'), "zh v5 import button says 从 v5 存储导入");
ok(locales.zh.includes("检测到上游 v5 会话存储") && locales.zh.includes("源存储只读"), "zh v5 hint names 会话存储 / 源存储");
ok(locales.zh.includes("检测到上游 v5 会话存储；仍可点「扫描」重试"), "zh v5 missing copy names 会话存储");
ok(locales["zh-TW"].includes('"settings.migration.v5Action": "從 v5 儲存匯入…"'), "zh-TW v5 import button says 從 v5 儲存匯入");
ok(locales["zh-TW"].includes("偵測到上游 v5 會話儲存") && locales["zh-TW"].includes("源儲存唯讀"), "zh-TW v5 hint names 會話儲存 / 源儲存");
ok(locales.en.includes('"settings.migration.v5Action": "Import from the v5 store…"'), "en v5 import button keeps the correct word store");
ok(locales.zh.includes('"settings.sessionStore": "会话存储"') && locales["zh-TW"].includes('"settings.sessionStore": "會話儲存"'),
  "the card name itself is the SSOT the fix aligns to (会话存储 / 會話儲存)");

// ── ③ 防线：zh/zh-TW 词典全文「店」上限 0，新增即红 ───────────────────────
for (const name of ["zh", "zh-TW"] as const) {
  const hits = locales[name].split("\n").filter((line) => line.includes("店")).length;
  ok(hits === 0, `${name} locale has zero lines containing 店 (store 直译错字 ceiling = 0; found ${hits})`);
}

// ── 排版：子卡②内两步之间的分隔线规则 ───────────────────────────────────
ok(styles.includes(".lab-section-card__body > [data-dag-backup-cleanup]"),
  "styles.css defines the divider for the cleanup block inside a LabSectionCard body");
const dividerRule = styles.slice(styles.indexOf(".lab-section-card__body > [data-dag-backup-cleanup]"));
const dividerBody = dividerRule.slice(0, dividerRule.indexOf("}"));
ok(dividerBody.includes("border-top: 1px solid var(--border-soft)"), "the divider is a 1px --border-soft top rule (theme token, no hardcoded color)");
ok(!/className=["'][^"']*subcard/.test(sessionStoreCard), "the restructure adds no new className (class-contract stays untouched)");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
