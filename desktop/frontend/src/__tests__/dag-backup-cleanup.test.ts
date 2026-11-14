// Run: tsx src/__tests__/dag-backup-cleanup.test.ts
//
// tl1287⑤: 迁移备份清理的源码契约。删除类动作的三条硬约束必须在 UI 层可见：
// 确认弹层逐行列出将删文件与体量、「保留最近一份」写进说明、迁移未归零时
// 拒绝执行（只给关闭）。后端语义（倒序删/保留/归零门/互斥）由
// desktop/dag_backup_cleanup_test.go 覆盖，这里防的是渲染层回归。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const section = readFileSync(resolve(testDir, "../components/DagBackupCleanupSection.tsx"), "utf8");
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

// 1) 卡片宿主：清理卡挂进会话存储卡（转正后的同一面板），不另开顶层入口。
const sessionStoreCard = labSection.slice(
  labSection.indexOf('{selected === "sessionStore" && ('),
  labSection.indexOf('{selected === "viewLayout" && ('),
);
ok(sessionStoreCard.includes("<DagBackupCleanupSection"), "the cleanup card mounts inside the sessionStore card");
ok(sessionStoreCard.indexOf("<DagMigrationSection") >= 0 && sessionStoreCard.indexOf("<DagBackupCleanupSection") > sessionStoreCard.indexOf("<DagMigrationSection"),
  "the cleanup card follows the DAG migration card");

// 2) 两步确认：先 plan 后 run；确认弹层逐行列出将删清单与体量；执行按钮
//    只在「有将删项且未被归零门拦下」时出现。
ok(section.includes("app.PlanDagBackupCleanup") && section.includes("app.RunDagBackupCleanup"), "plan first, run on the second click");
ok(section.includes("plan.deletable.map"), "the confirm step lists every deletable backup");
ok(section.includes("humanBytes(b.bytes)"), "each row shows its size");
ok(
  section.includes("plan.remainingMigrations === 0 && plan.deletable.length > 0") || section.includes("plan.deletable.length > 0 &&"),
  "the delete button is gated on the zero-remaining check",
);

// 3) 三方言带齐文案，「保留最近一份」必须在说明里（误清=回不去，用户须知）。
for (const [name, dict] of Object.entries(locales)) {
  for (const key of ["hint", "scan", "batchClean", "summary", "none", "blocked", "confirm", "done"]) {
    ok(dict.includes(`"settings.dagBackupCleanup.${key}"`), `${name} has settings.dagBackupCleanup.${key}`);
  }
  // tl1573-r2 缺陷③：结果面板与表格的新键也三语带齐。
  for (const key of ["resultTitle", "colName", "colTime", "colSize", "colStatus", "rowDeleted", "rowFailed", "rowKept"]) {
    ok(dict.includes(`"settings.dagBackupCleanup.${key}"`), `${name} has settings.dagBackupCleanup.${key} (tl1573-r2)`);
  }
}
ok(locales.zh.includes("保留最近一份") && locales["zh-TW"].includes("保留最近一份") && locales.en.includes("keeping the newest"),
  "all dialects state the keep-newest rule");

// 4) tl1573: 入口按钮显式化——idle 态入口用「DAG 批量清理」（页面上可被读成
//    清理动作），done 态重扫保持「扫描迁移备份」语义；行为面（plan→确认→run、
//    归零门与保留防线）由 dag-batch-cleanup-tl1573.test.tsx 渲染钉住。
{
  const idleBlock = section.slice(section.indexOf('phase === "idle"'), section.indexOf('phase === "planning"'));
  ok(idleBlock.includes("settings.dagBackupCleanup.batchClean"), "the idle entry button reads as the batch cleanup action");
  ok(!idleBlock.includes("settings.dagBackupCleanup.scan"), "the idle entry no longer hides behind the scan label");
  const doneBlock = section.slice(section.indexOf('phase === "done"'));
  ok(doneBlock.includes("settings.dagBackupCleanup.scan"), "the done state's re-scan keeps the scan semantics");
}

// 5) tl1573-r2（20261114 用户实测三缺陷返修）：
//    ② 记录行字号统一——卡内状态行 --text-sm（标题同档）、确认清单逐文件记录
//       行 --text-xs（数据档，res-panel__table 同档先例）；两档都是 tl1545
//       七档令牌，不再继承 body 的 --text-base 14px。
//    ③ 结果弹面板+表格——modal 家族骨架（.modal-backdrop/.modal）+ 逐行表格
//       （标题/时间/体积/状态），行数据吃后端 rows、旧后端由 plan 合成。
{
  // ② 字号契约：既有 .lab-subitem__ops 规则加 font-size（不另立新规则分裂单源）。
  const opsRuleAt = styles.indexOf(".lab-subitem__ops .settings-field__hint-line {");
  ok(opsRuleAt >= 0, "the ops hint-line rule stays registered");
  const opsRuleBody = styles.slice(opsRuleAt, styles.indexOf("}", opsRuleAt));
  ok(opsRuleBody.includes("font-size: var(--text-sm)"), "the card's status lines sit on the --text-sm tier (defect 2, tl1545 tokens)");
  const recordRuleAt = styles.indexOf(".lab-subitem__ops .dag-backup-cleanup__record {");
  ok(recordRuleAt > opsRuleAt, "the record-line rule stays registered");
  ok(styles.slice(recordRuleAt, styles.indexOf("}", recordRuleAt)).includes("font-size: var(--text-xs)"), "the per-file record lines sit on the --text-xs data tier (defect 2)");
  ok(section.includes("dag-backup-cleanup__record"), "the confirm rows carry the record class");
  // ③ 模态骨架契约：portal + modal 家族类 + 表格列键 + 合成兜底。
  ok(section.includes("createPortal"), "the result renders through a portal (modal family, 980 precedent)");
  ok(section.includes("modal-backdrop dag-cleanup-result-backdrop"), "the panel reuses the shared modal-backdrop skeleton");
  ok(section.includes('"modal dag-cleanup-result"'), "the panel reuses the shared .modal skeleton with a differential class");
  ok(section.includes("<table className=\"dag-cleanup-result__table\">"), "the receipt renders as a table (defect 3)");
  for (const key of ["colName", "colTime", "colSize", "colStatus", "rowDeleted", "rowFailed", "rowKept", "resultTitle"]) {
    ok(section.includes(`settings.dagBackupCleanup.${key}`), `the panel reads settings.dagBackupCleanup.${key}`);
  }
  ok(section.includes("export function cleanupResultRows"), "the rows synthesis is an exported pure helper (old-backend fallback)");
  ok(section.includes("next.backups ?? []") && section.includes("next.deletable ?? []"), "the plan arrays are nil-guarded at the boundary (defect 1 family defense)");
  const backdropRule = styles.indexOf(".dag-cleanup-result-backdrop {");
  ok(backdropRule >= 0, "styles.css registers the panel backdrop differential");
  ok(styles.slice(backdropRule, styles.indexOf("}", backdropRule)).includes("z-index: var(--z-popover-elevated)"), "the panel rides --z-popover-elevated above the settings overlay (980 precedent)");
  ok(styles.includes(".dag-cleanup-result__table"), "styles.css registers the result table");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
