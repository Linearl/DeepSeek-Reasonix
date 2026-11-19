/**
 * req-sync/scan.js — 任务包扫描与 backfill 命令
 *
 * 移植自 上游项目 req-sync/scan.mjs，适配 ai-novel 目录结构：
 *   - ai-novel: B.todo/v0.1/{taskDir}/（B.todo 有版本目录，B.2.done 等直接扁平）
 *   - taskDir 格式: v{VER}-r{REQID}-{complexity}-{SLUG}
 *   - run_result.json 中 reqId 为纯数字字符串如 "2001"，兼容 "REQ-2001"
 */

const {
  existsSync,
  readdirSync,
  statSync,
  writeFileSync,
} = require("node:fs");
const { join, resolve } = require("node:path");
const {
  CATEGORY_RANGES,
  DIR_STATUS_MAP,
  STATUS_MAP,
  parseDirName,
  inferStatusLayer,
  readJsonSafe,
  readTextSafe,
  parseReadme,
  inferTitle,
  gitLogDates,
  findTaskBase,
} = require("./utils.js");

// ============================================================
// 任务包扫描
// ============================================================

/**
 * 递归扫描任务包目录，收集所有 vXX-rXXXX 目录
 *
 * ai-novel 结构：
 *   B.todo/v0.1/v0.1-r2001-.../   （B.todo 有版本目录 v0.1）
 *   B.2.done/v0.1-r2001-.../      （其他层直接扁平，无版本中间层）
 *   B.1.paused/、B.3.cancelled/、A.inactive/ 一般为空
 *
 * 扫描策略：
 *   对于每个 statusLayer，检查内部子目录：
 *   1. 如果子目录直接是任务包（含 run_result.json 或符合 parseDirName），扁平处理
 *   2. 否则视为版本目录，递归一层扫描
 */
function scanTaskPackages(baseDir, useGit = false) {
  const packages = [];
  const statusLayers = [
    "B.2.done",
    "B.todo",
    "B.1.paused",
    "B.3.cancelled",
    "A.inactive",
  ];

  for (const layer of statusLayers) {
    const layerPath = join(baseDir, layer);
    if (!existsSync(layerPath)) continue;

    const entries = readdirSync(layerPath).filter((d) => {
      try {
        return statSync(join(layerPath, d)).isDirectory();
      } catch {
        return false;
      }
    });

    for (const entry of entries) {
      const entryPath = join(layerPath, entry);

      // 判断：自身是任务包（含 run_result.json 或 parseDirName 匹配且内含 README）
      const selfRunResult = join(entryPath, "run_result.json");
      const isTaskDir =
        existsSync(selfRunResult) ||
        (parseDirName(entry) && existsSync(join(entryPath, "README.md")));

      if (isTaskDir) {
        // 扁平模式：entry 本身就是任务目录
        const pkg = _readTaskPackage(entryPath, entry, layer, baseDir, useGit);
        if (pkg) packages.push(pkg);
        continue;
      }

      // 否则当作版本目录（如 B.todo/v0.1/），扫描内部子目录
      const subEntries = readdirSync(entryPath).filter((d) => {
        try {
          return statSync(join(entryPath, d)).isDirectory();
        } catch {
          return false;
        }
      });

      for (const subEntry of subEntries) {
        const taskPath = join(entryPath, subEntry);
        const pkg = _readTaskPackage(taskPath, subEntry, layer, baseDir, useGit);
        if (pkg) packages.push(pkg);
      }
    }
  }

  // 去重：同一 reqId 出现多次时，保留最优记录并警告
  const layerPriority = {
    "B.2.done": 1,
    "B.todo": 2,
    "B.1.paused": 3,
    "B.3.cancelled": 4,
    "A.inactive": 5,
  };
  const seen = new Map();
  const deduped = [];
  const conflicts = [];

  for (const pkg of packages) {
    const existing = seen.get(pkg.reqId);
    if (!existing) {
      seen.set(pkg.reqId, pkg);
      deduped.push(pkg);
      continue;
    }

    // 冲突：记录并选择最优
    const existLayer = existing.relPath.split("/")[0];
    const pkgLayer = pkg.relPath.split("/")[0];
    const existPrio = layerPriority[existLayer] ?? 99;
    const pkgPrio = layerPriority[pkgLayer] ?? 99;

    let keep = existing;
    let drop = pkg;
    if (pkgPrio < existPrio) {
      keep = pkg;
      drop = existing;
      seen.set(pkg.reqId, pkg);
      deduped[deduped.indexOf(existing)] = pkg;
    } else if (
      pkgPrio === existPrio &&
      pkg.targetVersion > existing.targetVersion
    ) {
      keep = pkg;
      drop = existing;
      seen.set(pkg.reqId, pkg);
      deduped[deduped.indexOf(existing)] = pkg;
    }

    conflicts.push({ keep, drop });
  }

  // 输出冲突警告
  for (const { keep, drop } of conflicts) {
    console.error(`⚠️ 重复编号 REQ-${keep.reqId}：`);
    console.error(`   保留：${keep.relPath}（"${keep.title}"）`);
    console.error(`   丢弃：${drop.relPath}（"${drop.title}"）`);
  }

  // 按编号排序
  deduped.sort((a, b) => a.reqId - b.reqId);
  return deduped;
}

/**
 * 读取单个任务包信息
 */
function _readTaskPackage(pkgPath, dirName, layerName, baseDir, useGit) {
  const parsed = parseDirName(dirName);
  // 非标准命名的目录跳过（除非有 run_result.json）
  const rrPath = join(pkgPath, "run_result.json");
  const rr = readJsonSafe(rrPath);
  if (!parsed && !rr) return null;

  // 读取 README.md
  const readmePath = join(pkgPath, "README.md");
  const readmeInfo = parseReadme(readmePath);

  // 构建相对路径（从 baseDir 算起）
  const relPath = `${layerName}/${dirName}`;

  // 推断状态
  const { status: inferStatus } = inferStatusLayer(relPath);

  // 合并数据（多源融合，优先级：run_result > README > 目录推断）
  const reqId = rr?.reqId
    ? parseInt(String(rr.reqId).replace("REQ-", ""), 10)
    : rr?.requirementId
      ? parseInt(String(rr.requirementId).replace("REQ-", ""), 10)
      : rr?.taskId
        ? parseInt(String(rr.taskId).replace("REQ-", ""), 10)
        : parsed?.reqId || null;

  if (reqId === null) return null;

  const title =
    rr?.title || readmeInfo.title || inferTitle(pkgPath, dirName, readmeInfo);

  const priority = rr?.priority
    ? rr.priority.toLowerCase()
    : readmeInfo.priority || (parsed ? `p${Math.ceil(reqId / 1000)}` : "p1");

  let status = inferStatus;
  if (rr?.status) {
    status = STATUS_MAP[rr.status] || rr.status;
  }

  // 日期提取
  let startDate = rr?.createdAt || rr?.date || rr?.started_at || null;
  let endDate = rr?.completedAt || rr?.ended_at || rr?.updatedAt || null;

  // 如果 run_result 日期不够且启用了 --git，尝试 git log（仅对 done 层）
  if (useGit && layerName === "B.2.done" && (!startDate || !endDate)) {
    const repoRoot = resolve(baseDir, "../..");
    if (existsSync(repoRoot)) {
      const gitDates = gitLogDates(pkgPath, repoRoot);
      if (!startDate) startDate = gitDates.start;
      if (!endDate) endDate = gitDates.end;
    }
  }

  const targetVersion = parsed?.version || "unknown";

  return {
    reqId,
    title,
    priority,
    status,
    source: rr?.source || "",
    targetVersion,
    startDate: startDate || "-",
    endDate: endDate || "-",
    docPath: `\`${relPath}/README.md\``,
    note: rr?.note || "",
    hasRunResult: !!rr,
    relPath,
    dirName,
  };
}

// ============================================================
// 功能 3: scan — 扫描所有任务包
// ============================================================

function cmdScan(args) {
  const baseDir = findTaskBase(args.docs);
  if (!baseDir) {
    console.error("❌ 未找到任务包目录（docs_dev/1.task）");
    process.exit(1);
  }

  console.error(`📂 扫描目录：${baseDir}`);
  const pkgs = scanTaskPackages(baseDir, args.git);
  console.error(`📦 发现 ${pkgs.length} 个任务包\n`);

  // 输出 JSON 到 stdout
  console.log(JSON.stringify(pkgs, null, 2));
}

// ============================================================
// 功能 3.5: backfill — 为缺少 run_result.json 的任务包批量生成
// ============================================================

function cmdBackfill(args) {
  const baseDir = findTaskBase(args.docs);
  if (!baseDir) {
    console.error("❌ 未找到任务包目录（docs_dev/1.task）");
    process.exit(1);
  }

  const dryRun = !args.apply;
  const statusLayers = [
    "B.2.done",
    "B.todo",
    "B.1.paused",
    "B.3.cancelled",
    "A.inactive",
  ];
  let created = 0;
  let skipped = 0;
  let errors = 0;

  for (const layer of statusLayers) {
    const layerPath = join(baseDir, layer);
    if (!existsSync(layerPath)) continue;

    const entries = readdirSync(layerPath).filter((d) => {
      try {
        return statSync(join(layerPath, d)).isDirectory();
      } catch {
        return false;
      }
    });

    for (const entry of entries) {
      const entryPath = join(layerPath, entry);
      const isTaskDir =
        existsSync(join(entryPath, "run_result.json")) ||
        (parseDirName(entry) && existsSync(join(entryPath, "README.md")));

      if (isTaskDir) {
        // 扁平模式
        _backfillSingle(entryPath, entry, layer, baseDir, dryRun, {
          created,
          skipped,
          errors,
        });
        continue;
      }

      // 版本目录模式
      const subEntries = readdirSync(entryPath).filter((d) => {
        try {
          return statSync(join(entryPath, d)).isDirectory();
        } catch {
          return false;
        }
      });

      for (const subEntry of subEntries) {
        const taskPath = join(entryPath, subEntry);
        const result = _backfillSingle(taskPath, subEntry, layer, baseDir, dryRun);
        created += result.created;
        skipped += result.skipped;
        errors += result.errors;
      }
    }
  }

  console.log(`\n📊 backfill 结果：`);
  console.log(`  跳过（已有）：${skipped}`);
  if (!dryRun) {
    console.log(`  新创建：${created}`);
    console.log(`  失败：${errors}`);
  } else {
    console.log(`  将创建：${skipped > 0 ? "（见上方 dry-run 列表）" : "无"}`);
    console.log(
      `\n   确认后执行：node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js backfill --apply`
    );
  }
}

function _backfillSingle(pkgPath, dirName, layerName, baseDir, dryRun) {
  const result = { created: 0, skipped: 0, errors: 0 };
  const parsed = parseDirName(dirName);
  if (!parsed) return result;

  const rrPath = join(pkgPath, "run_result.json");
  if (existsSync(rrPath)) {
    result.skipped++;
    return result;
  }

  // 从 README.md 提取信息
  const readmePath = join(pkgPath, "README.md");
  const readmeInfo = parseReadme(readmePath);

  const reqId = readmeInfo.reqId || `${parsed.reqId}`;
  const title =
    readmeInfo.title ||
    parsed.slug
      .split("-")
      .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
      .join(" ");
  const priority = readmeInfo.priority || "p1";
  const { status: inferStatus } = inferStatusLayer(`${layerName}/${dirName}`);

  // 尝试从 README.md 的内容提取 description 作为备注
  const readmeContent = readTextSafe(readmePath);
  const descMatch = readmeContent.match(/description[：:]\s*"([^"]+)"/);
  const note = descMatch ? descMatch[1] : "";

  const rr = {
    reqId,
    title,
    priority,
    version: parsed.version,
    status:
      Object.entries(STATUS_MAP).find(([, v]) => v === inferStatus)?.[0] ||
      "pending",
    createdAt: null,
    completedAt: null,
    source: "",
    note,
  };

  if (dryRun) {
    console.log(`[dry-run] 将创建：${rrPath.replace(baseDir + "/", "")}`);
    console.log(`  ${JSON.stringify(rr)}`);
    result.skipped++;
  } else {
    try {
      writeFileSync(rrPath, JSON.stringify(rr, null, 2) + "\n", "utf-8");
      result.created++;
    } catch (err) {
      console.error(`❌ 写入失败：${rrPath} — ${err.message}`);
      result.errors++;
    }
  }

  return result;
}

// ============================================================
// 导出
// ============================================================

module.exports = {
  scanTaskPackages,
  cmdScan,
  cmdBackfill,
};
