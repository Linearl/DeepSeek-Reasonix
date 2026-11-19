/**
 * req-sync/archive.js — 任务包自动归档命令
 *
 * 移植自 上游项目 req-sync/archive.mjs，适配 ai-novel 目录结构：
 *   - ai-novel: B.todo/v0.1/{taskDir}/ → B.2.done/v0.1/{taskDir}/
 *   - 扁平目标：B.2.done/v0.1-r2001-.../（如果 B.2.done 无版本层）
 *
 * 功能：
 *   1. archive: 将 B.todo 中 status=done（或 completed，旧版兼容）的任务包迁移至 B.2.done
 *   2. cleanup-stale: 清理 B.todo 中与 B.2.done 同名的残留副本
 *
 * 校验规则：
 *   - 必需文件：README.md, REQ-*.md, REQ-*-original.md, tasks.md, run_result.json
 *   - 可选文件：design.md, decision_log.md
 *   - 冲突检测：按文件夹名对比 B.2.done 是否已存在
 */

const {
  existsSync,
  readdirSync,
  statSync,
  mkdirSync,
  rmSync,
  copyFileSync,
} = require("node:fs");
const { join, relative } = require("node:path");
const { execSync } = require("node:child_process");
const { findTaskBase, parseDirName, readJsonSafe } = require("./utils.js");

// ============================================================
// Git 辅助
// ============================================================

let _repoRoot = null;
function getRepoRoot() {
  if (_repoRoot) return _repoRoot;
  try {
    _repoRoot = execSync("git rev-parse --show-toplevel", {
      encoding: "utf-8",
      timeout: 5000,
    }).trim();
  } catch {
    _repoRoot = null;
  }
  return _repoRoot;
}

function toGitRel(absPath) {
  const repoRoot = getRepoRoot();
  if (!repoRoot) return null;
  return relative(repoRoot, absPath).replace(/\\/g, "/");
}

function gitRm(absPath) {
  const repoRoot = getRepoRoot();
  const rel = toGitRel(absPath);
  if (!rel || !repoRoot) return false;
  try {
    execSync(`git rm -rf -- "${rel}"`, {
      encoding: "utf-8",
      timeout: 10000,
      cwd: repoRoot,
    });
    return true;
  } catch {
    return false;
  }
}

function gitAdd(absPath) {
  const repoRoot = getRepoRoot();
  const rel = toGitRel(absPath);
  if (!rel || !repoRoot) return false;
  try {
    execSync(`git add -- "${rel}"`, {
      encoding: "utf-8",
      timeout: 10000,
      cwd: repoRoot,
    });
    return true;
  } catch {
    return false;
  }
}

/**
 * 物理迁移（git mv）：跨目录搬移整个包目录
 */
function gitMvDir(srcPkgPath, destPkgPath) {
  const repoRoot = getRepoRoot();
  if (!repoRoot) return false;
  if (!existsSync(srcPkgPath)) return false;
  if (existsSync(destPkgPath)) {
    console.error(`   ⚠️ 目标已存在，跳过物理迁移：${destPkgPath}`);
    return false;
  }

  const srcRel = toGitRel(srcPkgPath);
  const destRel = toGitRel(destPkgPath);
  if (!srcRel || !destRel) return false;

  // 1. 优先 git mv
  try {
    execSync(`git mv "${srcRel}" "${destRel}"`, {
      encoding: "utf-8",
      timeout: 30000,
      cwd: repoRoot,
    });
    return true;
  } catch {
    // 2. 跨设备/部分失败时回退
    try {
      copyDirRecursive(srcPkgPath, destPkgPath);
      gitAdd(destPkgPath);
      gitRm(srcPkgPath);
      return true;
    } catch (fallbackErr) {
      console.error(`   ❌ git mv 与 copy 回退均失败：${fallbackErr.message}`);
      return false;
    }
  }
}

/**
 * 安全删除目录
 */
function safeRemoveDir(absPath) {
  if (!existsSync(absPath)) return true;

  try {
    rmSync(absPath, { recursive: true, force: true });
  } catch {
    /* ignore */
  }
  if (!existsSync(absPath)) return true;

  gitRm(absPath);

  if (existsSync(absPath)) {
    try {
      rmSync(absPath, { recursive: true, force: true });
    } catch {
      /* ignore */
    }
  }

  return !existsSync(absPath);
}

// ============================================================
// 归档必需文件
// ============================================================

function validatePackage(pkgPath) {
  const entries = readdirSync(pkgPath);
  const names = new Set(entries);
  const missing = [];

  if (!names.has("README.md")) missing.push("README.md");
  if (!names.has("run_result.json")) missing.push("run_result.json");
  if (!names.has("tasks.md")) missing.push("tasks.md");

  const hasReq = entries.some(
    (e) => /^REQ-\d+.*\.md$/.test(e) && !e.includes("-original")
  );
  if (!hasReq) missing.push("REQ-XXXX.md");

  const hasOriginal = entries.some((e) => e.includes("-original"));
  if (!hasOriginal) missing.push("REQ-XXXX-original.md");

  return { valid: missing.length === 0, missing };
}

// ============================================================
// 目录操作
// ============================================================

function copyDirRecursive(src, dest) {
  mkdirSync(dest, { recursive: true });
  const entries = readdirSync(src, { withFileTypes: true });
  for (const entry of entries) {
    const srcPath = join(src, entry.name);
    const destPath = join(dest, entry.name);
    if (entry.isDirectory()) {
      copyDirRecursive(srcPath, destPath);
    } else {
      copyFileSync(srcPath, destPath);
    }
  }
}

function collectFiles(dirPath) {
  const files = new Map();
  function walk(current, prefix) {
    const entries = readdirSync(current, { withFileTypes: true });
    for (const entry of entries) {
      const full = join(current, entry.name);
      const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (entry.isDirectory()) {
        walk(full, rel);
      } else {
        const st = statSync(full);
        files.set(rel, { size: st.size, mtimeMs: st.mtimeMs });
      }
    }
  }
  walk(dirPath, "");
  return files;
}

function compareDirectories(todoPath, donePath) {
  const todoFiles = collectFiles(todoPath);
  const doneFiles = collectFiles(donePath);

  const missingInDone = [];
  const newerInTodo = [];
  let identical = true;

  for (const [rel, todoInfo] of todoFiles) {
    const doneInfo = doneFiles.get(rel);
    if (!doneInfo) {
      missingInDone.push(rel);
      identical = false;
    } else if (
      todoInfo.mtimeMs > doneInfo.mtimeMs ||
      todoInfo.size !== doneInfo.size
    ) {
      newerInTodo.push(rel);
      identical = false;
    }
  }

  return { identical, missingInDone, newerInTodo };
}

// ============================================================
// 主逻辑
// ============================================================

function cmdArchive(args) {
  const baseDir = findTaskBase(args.docs);
  if (!baseDir) {
    console.error("❌ 未找到任务包目录（docs_dev/1.task）");
    process.exit(1);
  }

  const dryRun = !args.apply;
  const cleanupStale = !!args.cleanupStale;
  const todoPath = join(baseDir, "B.todo");
  const donePath = join(baseDir, "B.2.done");
  const cancelledPath = join(baseDir, "B.3.cancelled");

  if (!existsSync(todoPath)) {
    console.log("📭 B.todo 目录不存在，无需归档");
    return;
  }

  const toArchive = [];
  const stale = [];
  const skipped = [];
  const invalid = [];

  // 扫描 B.todo（扁平 + 版本目录混合）
  const todoEntries = readdirSync(todoPath).filter((d) => {
    try {
      return statSync(join(todoPath, d)).isDirectory();
    } catch {
      return false;
    }
  });

  for (const entry of todoEntries) {
    const entryPath = join(todoPath, entry);
    const isTaskDir =
      existsSync(join(entryPath, "run_result.json")) ||
      (parseDirName(entry) && existsSync(join(entryPath, "README.md")));

    if (isTaskDir) {
      // 扁平模式：entry 本身就是任务目录
      _collectArchiveItem(
        entryPath,
        entry,
        null,
        baseDir,
        todoPath,
        donePath,
        cancelledPath,
        toArchive,
        stale,
        skipped,
        invalid,
        cleanupStale
      );
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
      _collectArchiveItem(
        taskPath,
        subEntry,
        entry,
        baseDir,
        todoPath,
        donePath,
        cancelledPath,
        toArchive,
        stale,
        skipped,
        invalid,
        cleanupStale
      );
    }
  }

  // 输出
  _printArchiveSummary(toArchive, stale, skipped, invalid, cleanupStale);

  if (dryRun) {
    if (toArchive.length > 0 || stale.length > 0) {
      console.log(
        `\n💡 dry-run 模式。执行：node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js archive --apply`
      );
      if (stale.length > 0)
        console.log(
          `💡 含残留清理：node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js archive --apply --cleanup-stale`
        );
    }
    return;
  }

  if (toArchive.length === 0 && stale.length === 0) return;

  _executeArchive(toArchive, stale, donePath, cancelledPath, baseDir);
}

function _collectArchiveItem(
  pkgPath,
  dirName,
  versionDir,
  baseDir,
  todoPath,
  donePath,
  cancelledPath,
  toArchive,
  stale,
  skipped,
  invalid,
  cleanupStale
) {
  const parsed = parseDirName(dirName);
  if (!parsed) return;

  const rr = readJsonSafe(join(pkgPath, "run_result.json"));
  const relativePkgPath = relative(baseDir, pkgPath);

  // 判断目标目录位置（B.todo 有版本层 → B.2.done 也可能有版本层）
  const doneBase = donePath;
  const cancelledBase = cancelledPath;

  const isCancelled = rr && rr.status === "cancelled";
  const destBase = isCancelled ? cancelledBase : doneBase;
  const destPkgPath = versionDir
    ? join(destBase, versionDir, dirName)
    : join(destBase, dirName);

  // 残留清理：目标目录已有同名目录
  if (existsSync(destPkgPath)) {
    const archiveStatuses = isCancelled ? ["cancelled"] : ["done", "completed"];
    if (cleanupStale && rr && archiveStatuses.includes(rr.status)) {
      stale.push({
        parsed,
        dir: dirName,
        pkgPath,
        destPkgPath,
        relativePkgPath,
      });
    } else {
      const destLabel = isCancelled ? "B.3.cancelled" : "B.2.done";
      skipped.push({
        pkg: relativePkgPath,
        reason:
          rr && archiveStatuses.includes(rr.status)
            ? `已归档（同名目录已存在于 ${destLabel}，加 --cleanup-stale 可清理残留）`
            : `已归档（同名目录已存在于 ${destLabel}）`,
      });
    }
    return;
  }

  // 正常归档
  if (!rr || !["done", "completed", "cancelled"].includes(rr.status)) return;

  const { valid, missing } = validatePackage(pkgPath);
  if (!valid) {
    invalid.push({
      pkg: relativePkgPath,
      reason: `缺少必需文件：${missing.join(", ")}`,
    });
    return;
  }

  toArchive.push({
    parsed,
    dir: dirName,
    versionDir,
    pkgPath,
    destPkgPath,
    relativePkgPath,
  });
}

function _printArchiveSummary(toArchive, stale, skipped, invalid, cleanupStale) {
  if (toArchive.length > 0) {
    console.log(`\n📦 待归档任务包：${toArchive.length} 个`);
    for (const item of toArchive) {
      console.log(`   REQ-${item.parsed.reqId}  ${item.relativePkgPath}`);
    }
  }

  if (stale.length > 0) {
    console.log(`\n🧹 待清理残留副本：${stale.length} 个`);
    for (const item of stale) {
      const cmp = compareDirectories(item.pkgPath, item.destPkgPath);
      if (cmp.identical) {
        console.log(
          `   REQ-${item.parsed.reqId}  ${item.relativePkgPath}  → 完全一致，删除 B.todo 副本`
        );
      } else {
        if (cmp.missingInDone.length > 0) {
          console.log(
            `   REQ-${item.parsed.reqId}  ${item.relativePkgPath}  → B.2.done 缺少 ${cmp.missingInDone.length} 个文件：${cmp.missingInDone.slice(0, 5).join(", ")}${cmp.missingInDone.length > 5 ? "..." : ""}`
          );
        }
        if (cmp.newerInTodo.length > 0) {
          console.log(
            `   REQ-${item.parsed.reqId}  ${item.relativePkgPath}  → B.todo 比 B.2.done 更新 ${cmp.newerInTodo.length} 个文件：${cmp.newerInTodo.slice(0, 5).join(", ")}${cmp.newerInTodo.length > 5 ? "..." : ""}`
          );
        }
      }
    }
  }

  if (skipped.length > 0 && !cleanupStale) {
    console.log(`\n⏭️  跳过 ${skipped.length} 个（已归档）：`);
    for (const s of skipped) console.log(`   ${s.pkg}`);
  }

  if (invalid.length > 0) {
    console.log(`\n⚠️  跳过 ${invalid.length} 个（校验未通过）：`);
    for (const inv of invalid)
      console.log(`   ${inv.pkg} — ${inv.reason}`);
  }

  if (
    toArchive.length === 0 &&
    stale.length === 0 &&
    invalid.length === 0 &&
    skipped.length === 0
  ) {
    console.log("📭 无待处理任务包");
  }
}

function _executeArchive(toArchive, stale, donePath, cancelledPath, baseDir) {
  let migrated = 0;
  let cleaned = 0;
  let failed = 0;

  for (const item of toArchive) {
    try {
      // 确保目标父目录存在
      const destParent = item.versionDir
        ? join(item.versionDir === "B.2.done" ? donePath : cancelledPath, item.versionDir)
        : (item.destPkgPath.includes("B.3.cancelled") ? cancelledPath : donePath);
      mkdirSync(destParent, { recursive: true });

      const destLabel = item.destPkgPath.includes("B.3.cancelled")
        ? "B.3.cancelled"
        : "B.2.done";
      const relDest = relative(baseDir, item.destPkgPath).replace(/\\/g, "/");
      console.log(`📋 迁移：${item.relativePkgPath} → ${relDest}`);

      const moved = gitMvDir(item.pkgPath, item.destPkgPath);
      if (!moved) {
        throw new Error("git mv 失败且 copy 回退也失败");
      }

      if (existsSync(item.pkgPath)) {
        if (!safeRemoveDir(item.pkgPath)) {
          throw new Error(`原路径未完全删除：${item.relativePkgPath}`);
        }
      }

      const destEntries = readdirSync(item.destPkgPath);
      if (destEntries.length === 0) {
        throw new Error("迁移后目标目录为空");
      }

      console.log(`   ✓ 校验通过（${destEntries.length} 个文件）`);
      migrated++;
    } catch (err) {
      console.error(`   ❌ 失败：${err.message}`);
      failed++;
    }
  }

  // 残留清理
  for (const item of stale) {
    try {
      const cmp = compareDirectories(item.pkgPath, item.destPkgPath);

      if (!cmp.identical) {
        console.log(
          `🧹 清理残留：${item.relativePkgPath}（需同步 ${cmp.missingInDone.length + cmp.newerInTodo.length} 个文件）`
        );

        for (const rel of cmp.missingInDone) {
          const srcFile = join(item.pkgPath, rel);
          const destFile = join(item.destPkgPath, rel);
          mkdirSync(join(destFile, ".."), { recursive: true });
          copyFileSync(srcFile, destFile);
        }

        for (const rel of cmp.newerInTodo) {
          copyFileSync(
            join(item.pkgPath, rel),
            join(item.destPkgPath, rel)
          );
        }

        if (cmp.missingInDone.length + cmp.newerInTodo.length > 0) {
          gitAdd(item.destPkgPath);
        }

        const recheck = compareDirectories(item.pkgPath, item.destPkgPath);
        if (!recheck.identical) {
          console.log(
            `   ⚠️ 同步后仍有 ${recheck.missingInDone.length + recheck.newerInTodo.length} 处差异，跳过清理`
          );
          continue;
        }
      }

      console.log(
        `🧹 清理残留：${item.relativePkgPath}（${cmp.identical ? "完全一致" : "已同步"}）`
      );
      if (!gitRm(item.pkgPath)) {
        if (!safeRemoveDir(item.pkgPath)) {
          console.log(`   ⚠️ 未完全删除：${item.relativePkgPath}`);
          continue;
        }
      }
      console.log(`   ✓ 已清理`);
      cleaned++;
    } catch (err) {
      console.error(`   ❌ 清理失败：${err.message}`);
      failed++;
    }
  }

  // 汇总
  const parts = [];
  if (migrated > 0) parts.push(`迁移 ${migrated} 个`);
  if (cleaned > 0) parts.push(`清理残留 ${cleaned} 个`);
  if (failed > 0) parts.push(`失败 ${failed} 个`);
  if (parts.length > 0) {
    console.log(`\n📊 归档完成：${parts.join("，")}`);
  }
}

module.exports = {
  cmdArchive,
};
