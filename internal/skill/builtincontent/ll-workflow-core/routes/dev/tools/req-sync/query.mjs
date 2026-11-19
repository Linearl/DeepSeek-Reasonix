/**
 * req-sync/query.mjs — 查询编号详情
 */

import { join } from "node:path";
import {
  parseRequirementsMd,
  findRequirementsMd,
  resolvePaths,
  readJsonSafe,
} from "./utils.mjs";
import { scanTaskPackages } from "./scan.mjs";

export function cmdQuery(args) {
  const idStr = args._[0];
  if (!idStr) {
    console.error("❌ 用法：node scripts/req-sync.mjs query <编号>");
    process.exit(1);
  }

  const idNum = parseInt(idStr, 10);
  if (isNaN(idNum)) {
    console.error(`❌ 无效编号：${idStr}`);
    process.exit(1);
  }

  // 从 requirements.md 查询
  const reqPath = findRequirementsMd();
  const reqMap = reqPath ? parseRequirementsMd(reqPath) : new Map();
  const req = reqMap.get(idNum);

  // 从任务包目录查询
  const ctx = resolvePaths({
    workspaceRoot: args.workspaceRoot,
    profileOverride: args.profileOverride,
  });
  const baseDir = ctx.baseDir;
  let pkgInfo = null;
  if (baseDir) {
    const pkgs = scanTaskPackages(baseDir);
    pkgInfo = pkgs.find((p) => p.reqId === idNum);
  }

  console.log(`🔍 查询 REQ-${idNum}\n`);

  if (req) {
    console.log("📝 requirements.md 记录：");
    console.log(`  标题：${req.title}`);
    console.log(`  优先级：${req.priority}`);
    console.log(`  状态：${req.status}`);
    console.log(`  来源层：${req.source}`);
    console.log(`  目标版本：${req.targetVersion}`);
    console.log(`  开始日期：${req.startDate}`);
    console.log(`  完成日期：${req.endDate}`);
    console.log(`  详情文档：${req.docPath}`);
    console.log(`  备注：${req.note}`);
  } else {
    console.log("  ⚠️ 在 requirements.md 中未找到");
  }

  if (pkgInfo) {
    console.log("\n📁 任务包信息：");
    console.log(`  路径：${pkgInfo.relPath}`);
    console.log(`  目录名：${pkgInfo.dirName}`);
    console.log(`  有 run_result.json：${pkgInfo.hasRunResult ? "✅" : "❌"}`);

    if (pkgInfo.hasRunResult) {
      const rrPath = join(baseDir, pkgInfo.relPath, "run_result.json");
      const rr = readJsonSafe(rrPath);
      if (rr) {
        console.log("  run_result.json 状态：");
        console.log(`    status: ${rr.status}`);
        if (rr.tasks) {
          console.log(`    tasks: ${rr.tasks.completed}/${rr.tasks.total} 完成`);
        }
      }
    }
  } else {
    console.log("\n📁 任务包：未找到匹配目录");
  }

  // 一致性检查
  if (req && pkgInfo) {
    const issues = [];
    if (pkgInfo.status !== req.status) {
      issues.push(`状态不一致：requirements.md="${req.status}" vs 目录推断="${pkgInfo.status}"`);
    }
    if (pkgInfo.targetVersion !== req.targetVersion) {
      issues.push(`版本不一致：requirements.md="${req.targetVersion}" vs 目录="${pkgInfo.targetVersion}"`);
    }
    if (issues.length > 0) {
      console.log("\n⚠️ 一致性问题：");
      for (const issue of issues) {
        console.log(`  - ${issue}`);
      }
    }
  }

  if (!req && !pkgInfo) {
    console.log("\n❌ 该编号在 requirements.md 和任务包目录中均未找到");
  }
}
