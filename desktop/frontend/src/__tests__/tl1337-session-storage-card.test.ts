// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1337-session-storage-card.test.ts
//
// tl1337（返修，任务书 20261101 21:2x 存储域实例）：会话存储卡卡内改「左文右
// 操作」子项行——用户原话「存储实际相当于有很多子项，需要 1.做一个大卡片
// 2.卡片内部有多个子项，子项标题和说明性文字放左边」。取代 tl1292 的
// LabSectionCard 子卡口径（同卡上代防线，随本件退役；其仍有效的三语键与
// 「店」错字防线在本文件继承）。源码契约四件：
//   ① 三子项行——恰三张 settings-field 行（复用既有网格，零新布局机制）：
//     ①存储版本（左=标题+cardDesc 简述；右=v3 按钮+推荐徽章+状态行）
//     ②从 v4/v5 迁移（左=标题+migration.hint；右=805 两按钮+扫描态）
//     ③扫描&清理迁移备份（左=标题+backupGroupDesc 含 dagbak；右=DAG 迁移
//       与备份清理，迁移在上清理在下——tl1287⑤ blocked 文案依赖此顺序）。
//     原 stacked 大段（sessionStorageHint/settings-field--wrapped）不再渲染。
//   ② 状态行收编——锁定/阶段/风险三行仅非 v3 档（仍停 v4 用户）按已存档位
//      渲染（正向条件写法，避开 session-storage-lock 的 v3 可点禁令子串）；
//      restartPending 照旧条件渲染；锁定先于阶段（同上契约）。
//   ③ 三语键——cardDesc/backupGroupDesc 新键 ×3 方言；tl1292 组标题键与 v5
//      三键继续在册；sessionStorageHint 键保留在册（键位合同不拆，渲染已退役）。
//   ④ 防线——zh/zh-TW 词典全文「店」上限 0（tl1292 既有防线，新增即红）；
//      右列容器 .lab-subitem__ops 走 flex 纵排（styles.css 在册）。
// 纯聚合展示：配置键/setter/徽章零变化（档位面由 tl1338/1005/621 各自防线钉）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const labSection = readFileSync(resolve(testDir, "../components/ExperimentalLabSection.tsx"), "utf8");
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");
const migrationSection = readFileSync(resolve(testDir, "../components/SessionMigrationSection.tsx"), "utf8");
const dagMigrationSection = readFileSync(resolve(testDir, "../components/DagMigrationSection.tsx"), "utf8");
const cleanupSection = readFileSync(resolve(testDir, "../components/DagBackupCleanupSection.tsx"), "utf8");
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

// ── ① 三子项行（左文右操作）──────────────────────────────────────────────
const sessionStoreCard = labSection.slice(
  labSection.indexOf('{selected === "sessionStore" && ('),
  labSection.indexOf('{selected === "viewLayout" && ('),
);
ok(sessionStoreCard.length > 0, "the sessionStore card slice is located");

// 恰三张 settings-field 子项行（①存储版本 ②迁移 ③备份清理），行即族边界，
// tl1292 的 LabSectionCard 视觉容器不再嵌两层。
ok(count(sessionStoreCard, "<SettingsField") === 3, "exactly three subitem rows (settings-field) live in the sessionStore card");
ok(!sessionStoreCard.includes("<LabSectionCard"), "no LabSectionCard sub-card remains (subitem rows replace the tl1292 wrappers)");

// 旧 stacked 大段口径退役：--wrapped 网格与 sessionStorageHint 渲染点不再存在
// （键保留在词典，见 ③；此处钉的是渲染位消失）。
ok(!sessionStoreCard.includes("settings-field--wrapped"), "the stacked --wrapped mega-field is gone");
ok(!sessionStoreCard.includes('t("settings.sessionStorageHint")'), "the long sessionStorageHint paragraph no longer renders in the card");

// 子项① 存储版本：左=标题（labLabel 行级徽章）+cardDesc；右=v3 档按钮+推荐徽章。
const row1At = sessionStoreCard.indexOf('label={labLabel("sessionStorage"');
ok(row1At >= 0, "subitem ① keeps the 存储版本 title with its row badge");
ok(sessionStoreCard.slice(row1At, row1At + 400).includes('hint={t("settings.sessionStorage.cardDesc")}'),
  "subitem ① hint reads the short cardDesc summary");
const modesAt = sessionStoreCard.indexOf("SESSION_STORAGE_SELECTABLE_MODES.map");
ok(modesAt > row1At, "the v3 stage buttons stay inside subitem ① (card head row)");
ok(sessionStoreCard.includes('<TierBadge tier="recommended" translator={t} />'), "the v3 button keeps its recommended pill");

// 子项② 从 v4/v5 迁移：左=标题+migration.hint；右=805 两路由按钮+扫描态。
const row2At = sessionStoreCard.indexOf('label={t("settings.sessionStorage.migrationGroup")}');
ok(row2At > row1At, "subitem ② 从 v4/v5 迁移 follows subitem ①");
ok(sessionStoreCard.slice(row2At, row2At + 400).includes('hint={t("settings.migration.hint")}'),
  "subitem ② hint reuses settings.migration.hint (no new key)");
const migrationAt = sessionStoreCard.indexOf("<SessionMigrationSection");
ok(migrationAt > row2At, "the 805 migration section sits inside subitem ②");
ok(count(sessionStoreCard, "<SessionMigrationSection") === 1, "the migration section mounts exactly once");

// 子项③ 扫描&清理迁移备份：左=标题+backupGroupDesc（dagbak 说明在简述内）；
// 右=DAG 迁移在上、备份清理在下（tl1287⑤ blocked 文案「先在上方完成迁移」）。
const row3At = sessionStoreCard.indexOf('label={t("settings.sessionStorage.backupGroup")}');
ok(row3At > row2At, "subitem ③ 扫描&清理迁移备份 follows subitem ②");
ok(sessionStoreCard.slice(row3At, row3At + 400).includes('hint={t("settings.sessionStorage.backupGroupDesc")}'),
  "subitem ③ hint reads backupGroupDesc (dagbak note condensed inside)");
const dagAt = sessionStoreCard.indexOf("<DagMigrationSection");
const cleanupAt = sessionStoreCard.indexOf("<DagBackupCleanupSection");
ok(dagAt > row3At && cleanupAt > dagAt, "DAG migration comes before backup cleanup inside subitem ③");
ok(count(sessionStoreCard, "<DagMigrationSection") === 1 && count(sessionStoreCard, "<DagBackupCleanupSection") === 1,
  "both DAG-family sections mount exactly once");

// showHint=false：说明上左列后，三节各自的说明行不再在右列重复渲染。
ok(sessionStoreCard.includes('showHint={false}'), "the subitem rows pass showHint={false} to the op sections");
ok(migrationSection.includes("showHint = true") && migrationSection.includes("{showHint &&"),
  "SessionMigrationSection renders its hint line only when showHint");
ok(dagMigrationSection.includes("showHint = true") && dagMigrationSection.includes("{showHint &&"),
  "DagMigrationSection renders its hint line only when showHint");
ok(cleanupSection.includes("showHint = true") && cleanupSection.includes("{showHint &&"),
  "DagBackupCleanupSection renders its hint line only when showHint");

// ── ② 状态行收编（信息不丢）─────────────────────────────────────────────
// restartPending 照旧条件渲染；锁定/阶段/风险仅非 v3 档渲染，且锁定先于阶段
// （session-storage-lock 契约）；v3 档可点性禁令子串（mode !== "v3_only"）不出现。
const pendingAt = sessionStoreCard.indexOf('t("settings.sessionStorage.restartPending"');
const lockedAt = sessionStoreCard.indexOf('t("settings.sessionStorage.locked")');
const stageAt = sessionStoreCard.indexOf("settings.sessionStorage.stage.${storageMode}");
const riskAt = sessionStoreCard.indexOf("settings.sessionStorage.risk.${storageMode}");
ok(pendingAt > modesAt && pendingAt < row2At, "the restart-pending status line stays inside subitem ① right column");
ok(lockedAt > modesAt && stageAt > lockedAt && riskAt > stageAt, "locked → stage → risk render in that order (lock contract)");
const condAt = sessionStoreCard.indexOf('storageMode === "v3_only" ? null : (');
ok(condAt > modesAt && condAt < row2At, "locked/stage/risk are gated behind the not-v3 branch (legacy parked users keep full status)");
ok(!labSection.includes('mode !== "v3_only"'), "the forbidden v3-lock substring stays absent (v3 remains clickable)");

// ── ③ 三语键 ─────────────────────────────────────────────────────────────
for (const [name, dict] of Object.entries(locales)) {
  for (const key of ["cardDesc", "backupGroupDesc", "migrationGroup", "backupGroup"]) {
    ok(dict.includes(`"settings.sessionStorage.${key}"`), `${name} has settings.sessionStorage.${key}`);
  }
  // tl1292 继承：v5 三键在册；sessionStorageHint 键保留（渲染退役、键位合同不拆）。
  for (const key of ["v5Action", "v5Hint", "v5Missing"]) {
    ok(dict.includes(`"settings.migration.${key}"`), `${name} keeps settings.migration.${key}`);
  }
  ok(dict.includes('"settings.sessionStorageHint"'), `${name} keeps the retired sessionStorageHint key (locale contract)`);
}
ok(locales.zh.includes("保留最近一份") && locales["zh-TW"].includes("保留最近一份") && locales.en.includes("the newest one is always kept"),
  "backupGroupDesc states the keep-newest rule in all dialects");
ok(locales.zh.includes("dagbak-tl1227") && locales["zh-TW"].includes("dagbak-tl1227") && locales.en.includes("dagbak-tl1227"),
  "backupGroupDesc names the dagbak backup file (用户点名的 dagbak 说明)");

// ── ④ 防线 ───────────────────────────────────────────────────────────────
// tl1292 继承：zh/zh-TW 词典全文「店」上限 0（store 直译错字，新增即红）。
for (const name of ["zh", "zh-TW"] as const) {
  const hits = locales[name].split("\n").filter((line) => line.includes("店")).length;
  ok(hits === 0, `${name} locale has zero lines containing 店 (store 直译错字 ceiling = 0; found ${hits})`);
}
// 右列容器在册：flex 纵排（按钮在上、状态行在下左对齐），换行放行不再截断。
ok(styles.includes(".lab-subitem__ops"), "styles.css registers the right-column ops container");
const opsRule = styles.slice(styles.indexOf(".lab-subitem__ops"));
const opsBody = opsRule.slice(0, opsRule.indexOf("}"));
ok(opsBody.includes("flex-direction: column"), "the ops container stacks controls above status lines");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
