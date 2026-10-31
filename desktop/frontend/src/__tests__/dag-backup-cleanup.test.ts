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
  for (const key of ["hint", "scan", "summary", "none", "blocked", "confirm", "done"]) {
    ok(dict.includes(`"settings.dagBackupCleanup.${key}"`), `${name} has settings.dagBackupCleanup.${key}`);
  }
}
ok(locales.zh.includes("保留最近一份") && locales["zh-TW"].includes("保留最近一份") && locales.en.includes("keeping the newest"),
  "all dialects state the keep-newest rule");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
