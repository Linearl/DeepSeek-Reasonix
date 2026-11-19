/**
 * req-sync/scan.mjs — 任务包扫描与 backfill 命令
 */

import { existsSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import {
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
  resolvePaths,
} from "./utils.mjs";

// ============================================================
// 任务包扫描
// ============================================================

/**
 * 递归扫描任务包目录，收集所有 vXX-rXXXX 目录
 */
export function scanTaskPackages(baseDir, useGit = false) {
  const packages = [];
  const statusLayers = [
    { layer: "B.2.done", path: ctx.donePath },
    { layer: "B.todo", path: ctx.todoPath },
    { layer: "B.1.paused", path: ctx.pausedPath },
    { layer: "B.3.cancelled", path: ctx.cancelledPath },
    { layer: "A.inactive", path: ctx.inactivePath },
  ];
  for (const { layer, path: layerPath } of statusLayers) {
    if (!existsSync(layerPath)) continue;

    // 遍历版本目录 v06/v07/v08/v09
    const versions = readdirSync(layerPath).filter((d) => {
      try {
        return statSync(join(layerPath, d)).isDirectory() && d.startsWith("v");
      } catch {
        return false;
      }
    });

    for (const ver of versions) {
      const verPath = join(layerPath, ver);
      const dirs = readdirSync(verPath).filter((d) => {
        try {
          return statSync(join(verPath, d)).isDirectory();
        } catch {
          return false;
        }
      });

      for (const dir of dirs) {
        const parsed = parseDirName(dir);
        if (!parsed) continue; // 跳过非标准命名的目录

        const pkgPath = join(verPath, dir);
        const relPath = `${layer}/${ver}/${dir}`;
        const { status: inferStatus } = inferStatusLayer(relPath);

        // 读取 run_result.json
        const rrPath = join(pkgPath, "run_result.json");
        const rr = readJsonSafe(rrPath);

        // 读取 README.md
        const readmePath = join(pkgPath, "README.md");
        const readmeInfo = parseReadme(readmePath);

        // 合并数据（多源融合，优先级：run_result > README > 目录推断）
        const reqId = rr?.reqId
          ? parseInt(String(rr.reqId).replace("REQ-", ""), 10)
          : rr?.requirementId
            ? parseInt(String(rr.requirementId).replace("REQ-", ""), 10)
            : rr?.taskId
              ? parseInt(String(rr.taskId).replace("REQ-", ""), 10)
              : parsed.reqId;

        const title =
          rr?.title || readmeInfo.title || inferTitle(pkgPath, dir, readmeInfo);

        const priority = rr?.priority
          ? rr.priority.toLowerCase()
          : readmeInfo.priority || parsed.priority;

        let status = inferStatus;
        if (rr?.status) {
          status = STATUS_MAP[rr.status] || rr.status;
        }

        // 日期提取
        let startDate = rr?.createdAt || rr?.date || rr?.started_at || null;
        let endDate =
          rr?.completedAt || rr?.ended_at || rr?.updatedAt || null;

        // 如果 run_result 日期不够且启用了 --git，尝试 git log（仅对 done 层）
        if (useGit && layer === "B.2.done" && (!startDate || !endDate)) {
          const gitDates = gitLogDates(pkgPath, resolve(baseDir, "../.."));
          if (!startDate) startDate = gitDates.start;
          if (!endDate) endDate = gitDates.end;
        }

        packages.push({
          reqId,
          title,
          priority,
          status,
          source: rr?.source || "",
          targetVersion: parsed.version,
          startDate: startDate || "-",
          endDate: endDate || "-",
          docPath: `\`${relPath}/README.md\``,
          note: rr?.note || "",
          hasRunResult: !!rr,
          relPath,
          dirName: dir,
        });
      }
    }
  }

  // 扫描 B.3.cancelled 和 A.inactive 中的 standalone 文件（无标准目录结构）
  for (const { layer, path: layerPath } of [
    { layer: "B.3.cancelled", path: ctx.cancelledPath },
    { layer: "A.inactive", path: ctx.inactivePath },
  ]) {
    if (!existsSync(layerPath)) continue;

    const versions = readdirSync(layerPath).filter((d) => {
      try { return statSync(join(layerPath, d)).isDirectory() && d.startsWith("v"); }
      catch { return false; }
    });

    for (const ver of versions) {
      const verPath = join(layerPath, ver);
      // 查找 standalone REQ-*.md 文件（非目录结构内的）
      const files = readdirSync(verPath).filter((f) => {
        const match = f.match(/^REQ-(\d+)-.+\.md$/);
        return match && !f.includes("-original") && statSync(join(verPath, f)).isFile();
      });

      for (const file of files) {
        const idMatch = file.match(/^REQ-(\d+)-(.+)\.md$/);
        if (!idMatch) continue;
        const reqId = parseInt(idMatch[1], 10);
        // 检查是否已被标准目录扫描覆盖
        if (packages.some((p) => p.reqId === reqId)) continue;

        const filePath = join(verPath, file);
        const content = readTextSafe(filePath);
        const { status } = inferStatusLayer(`${layer}/${ver}/${file}`);
        const catNum = Math.floor(reqId / 1000);
        const catName = CATEGORY_RANGES[catNum]?.[2] || "未知";

        // 从文件内容提取标题
        let title = "";
        const h1Match = content.match(/^#\s+(.+)$/m);
        if (h1Match) title = h1Match[1]
          .replace(/^REQ-\d+\s+/g, "")
          .replace(/[✅📋🚧⏸️🛑🆕]\s*/g, "")
          .trim();
        if (!title) title = idMatch[2].replace(/-/g, " ");
        // 提取优先级
        let priority = "p1";
        const verMatch = content.match(/-p(\d)[-\s]/);
        if (verMatch) priority = `p${verMatch[1]}`;

        const relPath = `${layer}/${ver}/${file}`;
        packages.push({
          reqId,
          title,
          priority,
          status,
          source: "",
          targetVersion: `dev_${ver.replace("v", "")}`,
          startDate: "-",
          endDate: "-",
          docPath: `\`${relPath}\``,
          note: layer === "A.inactive" ? "待激活需求" : "已终止/取消",
          hasRunResult: false,
          relPath,
          dirName: file,
          standalone: true,
        });
      }
    }
  }

  // 去重：同一 reqId 出现多次时，保留最优记录并警告
  const layerPriority = { "B.2.done": 1, "B.todo": 2, "B.1.paused": 3, "B.3.cancelled": 4, "A.inactive": 5 };
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
    } else if (pkgPrio === existPrio && pkg.targetVersion > existing.targetVersion) {
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

// ============================================================
// 功能 3: scan — 扫描所有任务包
// ============================================================

export function cmdScan(args) {
  const ctx = resolvePaths({
    workspaceRoot: args.workspaceRoot,
    profileOverride: args.profileOverride,
  });
  const baseDir = ctx.baseDir;
  if (!baseDir || !existsSync(baseDir)) {
    console.error(`❌ 未找到任务包目录（1.task）source=${ctx.source} baseDir=${baseDir}`);
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

export function cmdBackfill(args) {
  const ctx = resolvePaths({
    workspaceRoot: args.workspaceRoot,
    profileOverride: args.profileOverride,
  });
  const baseDir = ctx.baseDir;
  if (!baseDir || !existsSync(baseDir)) {
    console.error(`❌ 未找到任务包目录（1.task）source=${ctx.source} baseDir=${baseDir}`);
    process.exit(1);
  }

  const dryRun = !args.apply;
  const statusLayers = [
    { layer: "B.2.done", path: ctx.donePath },
    { layer: "B.todo", path: ctx.todoPath },
    { layer: "B.1.paused", path: ctx.pausedPath },
    { layer: "B.3.cancelled", path: ctx.cancelledPath },
    { layer: "A.inactive", path: ctx.inactivePath },
  ];
  let created = 0;
  let skipped = 0;
  let errors = 0;

  for (const { layer, path: layerPath } of statusLayers) {
    if (!existsSync(layerPath)) continue;

    const versions = readdirSync(layerPath).filter((d) => {
      try { return statSync(join(layerPath, d)).isDirectory() && d.startsWith("v"); }
      catch { return false; }
    });

    for (const ver of versions) {
      const verPath = join(layerPath, ver);
      const dirs = readdirSync(verPath).filter((d) => {
        try { return statSync(join(verPath, d)).isDirectory(); }
        catch { return false; }
      });

      for (const dir of dirs) {
        const parsed = parseDirName(dir);
        if (!parsed) continue;

        const pkgPath = join(verPath, dir);

        // 跳过已有 run_result.json 的
        const rrPath = join(pkgPath, "run_result.json");
        if (existsSync(rrPath)) { skipped++; continue; }

        // 从 README.md 提取信息
        const readmePath = join(pkgPath, "README.md");
        const readmeInfo = parseReadme(readmePath);

        const reqId = readmeInfo.reqId || `REQ-${parsed.reqId}`;
        const title = readmeInfo.title || parsed.slug.split("-")
          .map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(" ");
        const priority = (readmeInfo.priority || parsed.priority).toUpperCase();
        const { status: inferStatus } = inferStatusLayer(`${layer}/${ver}/${dir}`);

        // 尝试从 README.md 的 frontmatter 提取 description 作为备注
        const readmeContent = readTextSafe(readmePath);
        const descMatch = readmeContent.match(/description[：:]\s*"([^"]+)"/);
        const note = descMatch ? descMatch[1] : "";

        const rr = {
          reqId,
          title,
          priority,
          version: parsed.version,
          status: Object.entries(STATUS_MAP).find(([, v]) => v === inferStatus)?.[0] || "pending",
          createdAt: null,
          completedAt: layer === "B.2.done" ? null : null,
          source: "",
          note,
        };

        if (dryRun) {
          console.log(`[dry-run] 将创建：${rrPath.replace(baseDir + "/", "")}`);
          console.log(`  ${JSON.stringify(rr)}`);
        } else {
          try {
            writeFileSync(rrPath, JSON.stringify(rr, null, 2) + "\n", "utf-8");
            created++;
          } catch (err) {
            console.error(`❌ 写入失败：${rrPath} — ${err.message}`);
            errors++;
          }
        }
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
    console.log(`\n   确认后执行：node scripts/req-sync.mjs backfill --apply`);
  }
}
