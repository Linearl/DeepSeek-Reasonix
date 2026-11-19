/**
 * req-sync/next.js — 查询可用需求编号
 *
 * 移植自 上游项目 req-sync/next.mjs，适配 ai-novel 目录结构
 */

const {
  CATEGORY_RANGES,
  parseRequirementsMd,
  findRequirementsMd,
  findTaskBase,
} = require("./utils.js");
const { scanTaskPackages } = require("./scan.js");

function cmdNext(args) {
  const category = parseInt(args.category || "0", 10);
  const count = parseInt(args.count || "5", 10);

  if (!args.category) {
    console.error("❌ 必须指定类别：--category <N>（1-9）");
    console.error(
      "   例：node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js next --category 7 --count 5"
    );
    process.exit(1);
  }

  // 找到 requirements.md
  const reqPath = findRequirementsMd(args.docs);
  if (!reqPath) {
    console.error("❌ 未找到 requirements.md");
    process.exit(1);
  }

  const reqMap = parseRequirementsMd(reqPath);
  const usedIds = new Set(reqMap.keys());

  // 同时扫描目录中的编号（有些可能在 requirements.md 之外）
  const baseDir = findTaskBase(args.docs);
  if (baseDir) {
    const pkgs = scanTaskPackages(baseDir, false);
    for (const pkg of pkgs) {
      usedIds.add(pkg.reqId);
    }
  }

  const categories =
    category > 0
      ? [[category, CATEGORY_RANGES[category]]]
      : Object.entries(CATEGORY_RANGES).map(([k, v]) => [parseInt(k), v]);

  console.log("📋 可用需求编号查询\n");

  for (const [catNum, [start, end, name]] of categories) {
    const available = [];
    for (let i = start; i <= end; i++) {
      if (!usedIds.has(i)) {
        available.push(i);
      }
      if (available.length >= count) break;
    }

    if (available.length === 0) {
      console.log(
        `  ${catNum}xxx ${name}：已满（${end - start + 1}/${end - start + 1}）`
      );
    } else {
      const formatted = available
        .map((n) => String(n).padStart(4, "0"))
        .join(", ");
      console.log(
        `  ${catNum}xxx ${name}：${formatted}（共 ${available.length} 个可用）`
      );
    }
  }

  // 统计总览
  const totalUsed = usedIds.size;
  const totalRange = 9000; // 1000-9999
  console.log(
    `\n📊 总计：已用 ${totalUsed} / ${totalRange}（${((totalUsed / totalRange) * 100).toFixed(1)}%）`
  );
}

module.exports = {
  cmdNext,
};
