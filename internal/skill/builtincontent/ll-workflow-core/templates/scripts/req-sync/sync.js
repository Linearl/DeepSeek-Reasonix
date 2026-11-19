/**
 * req-sync/sync.js — requirements.md 同步命令
 *
 * 移植自 上游项目 req-sync/sync.mjs，适配 ai-novel：
 *   - 10 列全量清单表格（编号/任务标题/优先级/当前状态/来源层/目标版本/开始日期/完成日期/详情文档/备注）
 *   - §4 按状态看板
 *   - 智能数据合并（scan + 原文件手工数据 + 备份文件）
 *   - 标题去重、中文排版规范化
 */

const { readdirSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");
const {
  WARNING_NOTICE,
  parseRequirementsMd,
  findRequirementsMd,
  findTaskBase,
  readTextSafe,
  normalizeChineseSpacing,
} = require("./utils.js");
const { scanTaskPackages } = require("./scan.js");

// ============================================================
// 功能 4: sync — 更新 requirements.md
// ============================================================

function cmdSync(args) {
  const dryRun = !args.apply;

  const baseDir = findTaskBase(args.docs);
  if (!baseDir) {
    console.error("❌ 未找到任务包目录（docs_dev/1.task）");
    process.exit(1);
  }

  const reqPath = findRequirementsMd(args.docs);
  if (!reqPath) {
    console.error("❌ 未找到 requirements.md");
    process.exit(1);
  }

  console.error(`📂 扫描任务包...`);
  const pkgs = scanTaskPackages(baseDir, args.git);
  console.error(`📦 发现 ${pkgs.length} 个任务包`);

  // 解析原 requirements.md 用于合并已有手工数据
  const existingReqMap = parseRequirementsMd(reqPath);
  console.error(`📝 原文件已有 ${existingReqMap.size} 条记录`);

  // 合并：以 scan 数据为基础，用原文件数据补充缺失字段
  for (const pkg of pkgs) {
    const existing = existingReqMap.get(pkg.reqId);
    if (!existing) continue;

    // 标题：如果 scan 结果是英文 slug 推断的，优先用原文件的中文标题
    if (
      pkg.title &&
      /^[A-Z]/.test(pkg.title) &&
      existing.title &&
      /[一-龥]/.test(existing.title)
    ) {
      pkg.title = existing.title;
    }
    // 来源层：原文件有则保留
    if (!pkg.source && existing.source && existing.source !== "-") {
      pkg.source = existing.source;
    }
    // 备注：原文件有则保留
    if (!pkg.note && existing.note && existing.note !== "-") {
      pkg.note = existing.note;
    }
    // 日期：原文件有则保留（原文件可能是手工维护的精确日期）
    if (
      (pkg.startDate === "-" || !pkg.startDate) &&
      existing.startDate &&
      existing.startDate !== "-"
    ) {
      pkg.startDate = existing.startDate;
    }
    if (
      (pkg.endDate === "-" || !pkg.endDate) &&
      existing.endDate &&
      existing.endDate !== "-"
    ) {
      pkg.endDate = existing.endDate;
    }
    // 详情文档路径：优先保留原文件路径，但如果扫描到更高优先级层（B.2.done > B.todo），则更新
    if (existing.docPath && existing.docPath !== "-") {
      const layerPriority = {
        "B.2.done": 1,
        "B.todo": 2,
        "B.1.paused": 3,
        "B.3.cancelled": 4,
        "A.inactive": 5,
      };
      const scannedLayer = (pkg.relPath || "").split("/")[0];
      const existingLayer = (existing.docPath || "")
        .replace(/^`|`$/g, "")
        .split("/")[0];
      const scannedPrio = layerPriority[scannedLayer] ?? 99;
      const existingPrio = layerPriority[existingLayer] ?? 99;
      // 仅当扫描层优先级更高时覆盖路径
      if (scannedPrio < existingPrio) {
        const bare = existing.docPath.replace(/^`|`$/g, "");
        const fileName = bare.split("/").pop() || "README.md";
        pkg.docPath = `\`${pkg.relPath}/${fileName}\``;
      } else {
        pkg.docPath = existing.docPath;
      }
    }
  }

  // 回填：原文件中有但 scan 未覆盖的条目（如历史别名）
  const scannedIds = new Set(pkgs.map((p) => p.reqId));
  const orphanEntries = [];

  for (const [id, existing] of existingReqMap) {
    if (!scannedIds.has(id)) {
      orphanEntries.push({
        id,
        reqId: id,
        title: existing.title,
        priority: existing.priority,
        status: existing.status,
        source: existing.source,
        targetVersion: existing.targetVersion,
        startDate: existing.startDate,
        endDate: existing.endDate,
        docPath: existing.docPath,
        note: existing.note,
        hasRunResult: false,
        relPath: "",
        dirName: "",
        _orphan: true,
      });
    }
  }

  // 再从备份文件补充（避免历史别名因当前文件已丢失而遗漏）
  try {
    const bakFiles = readdirSync(baseDir)
      .filter((f) => f.startsWith("requirements.md.bak."))
      .sort()
      .reverse();
    if (bakFiles.length > 0) {
      const bakPath = join(baseDir, bakFiles[0]);
      const bakMap = parseRequirementsMd(bakPath);
      for (const [id, bakEntry] of bakMap) {
        if (!scannedIds.has(id) && !orphanEntries.some((e) => e.reqId === id)) {
          orphanEntries.push({
            id,
            reqId: id,
            title: bakEntry.title,
            priority: bakEntry.priority,
            status: bakEntry.status,
            source: bakEntry.source,
            targetVersion: bakEntry.targetVersion,
            startDate: bakEntry.startDate,
            endDate: bakEntry.endDate,
            docPath: bakEntry.docPath,
            note: bakEntry.note,
            hasRunResult: false,
            relPath: "",
            dirName: "",
            _orphan: true,
          });
        }
      }
    }
  } catch {
    /* 备份文件不可用时静默跳过 */
  }

  if (orphanEntries.length > 0) {
    console.error(
      `📎 保留原文件独有条目 ${orphanEntries.length} 条：${orphanEntries.map((e) => e.reqId).join(", ")}`
    );
    pkgs.push(...orphanEntries);
  }

  // 读取原文件
  const original = readTextSafe(reqPath);
  const lines = original.split("\n");

  // 定位 §3 和 §4 的边界（旧格式可能没有这两个章节）
  let section3Start = lines.findIndex((l) => l.trim().startsWith("## 3."));
  let section4Start = lines.findIndex((l) => l.trim().startsWith("## 4."));
  const section5Start = lines.findIndex((l) => l.trim().startsWith("## 5."));

  let before = [];
  let after = [];

  if (section3Start !== -1 && section4Start !== -1) {
    // 旧格式已有 §3/§4 → 替换这两节
    before = lines.slice(0, section3Start);
    after = section5Start !== -1 ? lines.slice(section5Start) : [];
  } else {
    // 旧格式（如统计/2xxx 表格）→ 保留前 14 行（YAML front matter + 标题 + WARNING）作为 before
    // 找到第一个 `---` 后的 WARNING 行，保留到那里
    const warningIdx = lines.findIndex((l) =>
      l.includes("DO NOT EDIT THIS FILE MANUALLY")
    );
    if (warningIdx !== -1) {
      // 保留到 WARNING 注释块之后（含尾部空行）
      let insertEnd = warningIdx + 1;
      // 跳过 WARNING 行（通常是 5 行连续的 >）
      while (
        insertEnd < lines.length &&
        lines[insertEnd].trim().startsWith(">")
      ) {
        insertEnd++;
      }
      // 确保 before 末尾有空行分隔
      let sliceEnd = insertEnd;
      while (sliceEnd < lines.length && lines[sliceEnd].trim() === "") {
        sliceEnd++;
      }
      before = lines.slice(0, sliceEnd);
    } else {
      // 没有 WARNING，保留 YAML front matter + 标题
      before = lines.slice(0, Math.min(lines.length, 5));
    }
    // after 为空，旧格式之后的内容全部丢弃
  }

  // 按类别分组生成 §3 表格
  const grouped = {};
  for (const pkg of pkgs) {
    const catNum = Math.floor(pkg.reqId / 1000);
    if (!grouped[catNum]) grouped[catNum] = [];
    grouped[catNum].push(pkg);
  }

  // §3 表格头（10 列：编号/任务标题/优先级/当前状态/来源层/目标版本/开始日期/完成日期/详情文档/备注）
  const tableHeader =
    "| 编号 | 任务标题 | 优先级 | 当前状态 | 来源层 | 目标版本 | 开始日期 | 完成日期 | 详情文档 | 备注 |";
  const tableSep = "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |";

  function formatRow(pkg) {
    const title = normalizeChineseSpacing(pkg.title);
    const note = normalizeChineseSpacing(pkg.note);
    return `| ${pkg.id || pkg.reqId} | ${title} | ${pkg.priority} | ${pkg.status} | ${pkg.source || "-"} | ${pkg.targetVersion} | ${pkg.startDate} | ${pkg.endDate} | ${pkg.docPath} | ${note} |`;
  }

  // 生成 §3 内容
  const section3Lines = [];
  section3Lines.push("");
  section3Lines.push(`> 说明：本章是唯一全量清单，不保留"旧编号"列。`);
  section3Lines.push("");

  const catNames = {
    1: "架构和基础设施任务（1xxx）",
    2: "核心功能开发任务（2xxx）",
    3: "用户界面和体验任务（3xxx）",
    4: "测试和质量保证任务（4xxx）",
    5: "文档和规范任务（5xxx）",
    6: "运维和部署任务（6xxx）",
    7: "技术债务和重构任务（7xxx）",
    8: "研究和调研任务（8xxx）",
    9: "未分类和临时任务（9xxx）",
  };

  for (let cat = 1; cat <= 9; cat++) {
    const items = grouped[cat] || [];
    if (items.length === 0) continue;

    section3Lines.push(`### 3.${cat - 1} ${catNames[cat]}`);
    section3Lines.push("");
    section3Lines.push(tableHeader);
    section3Lines.push(tableSep);
    for (const item of items) {
      section3Lines.push(formatRow(item));
    }
    section3Lines.push("");
  }

  // 按状态分组生成 §4
  const byStatus = {
    "🚧 进行中": [],
    "📋 待办": [],
    "🆕 待激活": [],
    "✅ 已完成": [],
    "🛑 已终止": [],
  };

  for (const pkg of pkgs) {
    const s = pkg.status;
    if (byStatus[s]) {
      byStatus[s].push(pkg);
    } else {
      byStatus["📋 待办"].push(pkg); // fallback
    }
  }

  const section4Lines = [];
  section4Lines.push("");
  section4Lines.push("> 说明：用于快速看板，不替代第 3 章全量清单。");
  section4Lines.push("");

  const statusSections = [
    ["🚧 进行中", "4.1"],
    ["📋 待办", "4.2"],
    ["🆕 待激活", "4.3"],
    ["✅ 已完成", "4.4"],
    ["🛑 已终止", "4.5"],
  ];

  for (const [statusLabel, secNum] of statusSections) {
    const items = byStatus[statusLabel] || [];
    if (items.length === 0) continue;

    section4Lines.push(`### ${secNum} ${statusLabel}`);
    section4Lines.push("");
    section4Lines.push("| 编号 | 任务标题 | 文档 |");
    section4Lines.push("| --- | --- | --- |");
    for (const item of items) {
      section4Lines.push(
        `| ${item.reqId} | ${normalizeChineseSpacing(item.title)} | ${item.docPath} |`
      );
    }
    section4Lines.push("");
  }

  // 组装新内容
  // 注意：before 和 after 已在上方根据 §3/§4 是否存在而设定

  // 确保 before 中包含 WARNING_NOTICE
  const beforeText = before.join("\n");
  if (!beforeText.includes("DO NOT EDIT THIS FILE MANUALLY")) {
    let insertIdx = 0;
    if (before[0] && before[0].trim() === "---") {
      let endFM = before.findIndex((l, i) => i > 0 && l.trim() === "---");
      if (endFM === -1) endFM = 0;
      insertIdx = endFM + 1;
      while (
        insertIdx < before.length &&
        (!before[insertIdx].startsWith("# ") ||
          before[insertIdx].startsWith("## "))
      ) {
        insertIdx++;
      }
      if (
        insertIdx < before.length &&
        before[insertIdx].startsWith("# ")
      ) {
        insertIdx++;
      }
    }
    before = [
      ...before.slice(0, insertIdx),
      "",
      ...WARNING_NOTICE,
      "",
      ...before.slice(insertIdx),
    ];
  }

  const newContent = [
    ...before,
    "## 3. 📚 全量任务清单（按类别）",
    ...section3Lines,
    "---",
    "",
    "## 4. 📍 任务清单视图（按状态）",
    ...section4Lines,
    "---",
    "",
    ...after,
  ].join("\n");

  // 对比差异
  const originalLines = original.split("\n").length;
  const newLines = newContent.split("\n").length;

  console.log("\n📊 sync 预览：");
  console.log(`  任务包总数：${pkgs.length}`);
  console.log(`  有 run_result.json：${pkgs.filter((p) => p.hasRunResult).length}`);
  console.log(`  原文件行数：${originalLines}`);
  console.log(`  新文件行数：${newLines}`);
  console.log(
    `  差异行数：${newLines - originalLines > 0 ? "+" : ""}${newLines - originalLines}`
  );

  // 统计各类别
  console.log("\n  各类别任务数：");
  for (let cat = 1; cat <= 9; cat++) {
    const count = (grouped[cat] || []).length;
    if (count > 0) {
      console.log(`    ${cat}xxx：${count} 条`);
    }
  }

  const contentChanged = original !== newContent;

  if (dryRun) {
    if (!contentChanged) {
      console.log(`\n✅ Dry-run：requirements.md 已是最新，无需更新`);
      return;
    }
    const outPath = reqPath.replace(".md", ".new.md");
    writeFileSync(outPath, newContent, "utf-8");
    console.log(`\n✅ Dry-run 完成，新内容已写入：${outPath}`);
    console.log(
      "   确认无误后执行：node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js sync --apply"
    );
  } else if (contentChanged) {
    if (args.backup) {
      const backupPath = reqPath.replace(".md", `.bak.${Date.now()}.md`);
      writeFileSync(backupPath, original, "utf-8");
      console.log(`\n📋 原文件已备份：${backupPath}`);
    }
    writeFileSync(reqPath, newContent, "utf-8");
    console.log(
      `✅ requirements.md 已更新（${originalLines}→${newLines} 行）`
    );
  } else {
    console.log(`\n✅ requirements.md 已是最新，无需更新`);
  }
}

module.exports = {
  cmdSync,
};
