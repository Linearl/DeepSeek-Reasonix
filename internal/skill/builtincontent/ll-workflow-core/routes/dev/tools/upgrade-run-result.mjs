#!/usr/bin/env node
/**
 * upgrade-run-result.mjs — 增强 run_result.json 质量
 *
 * 通过读取每个任务包的 README.md 和 REQ-*.md 提取更高质量的元数据：
 * - 中文标题（从 description / 专题名称 / heading 提取）
 * - 来源层（从前置诊断 / 上游来源 提取）
 * - 备注（从 description / 交付内容 提取）
 * - 创建日期（从最后更新 / 冻结时间 提取）
 * - 子任务统计（从 tasks.md 解析）
 *
 * 用法：
 *   node upgrade-run-result.mjs --base-dir <path> --dry-run
 *   node upgrade-run-result.mjs --base-dir <path> --apply
 *   node upgrade-run-result.mjs --base-dir <path> --apply --dirs v06,v07
 */

import { readFileSync, writeFileSync, existsSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

// ============================================================
// 文件读取
// ============================================================

function readTextSafe(filePath) {
  try { return readFileSync(filePath, "utf-8").replace(/^﻿/, ""); }
  catch { return ""; }
}

function readJsonSafe(filePath) {
  try { return JSON.parse(readTextSafe(filePath)); }
  catch { return null; }
}

// ============================================================
// 元数据提取
// ============================================================

/** 检测是否为有意义的中文标题 */
function isChineseTitle(title) {
  return title && /[一-鿿]/.test(title) && title.length >= 2;
}

/** 从 frontmatter description 提取中文标题 */
function extractTitleFromDesc(content) {
  const m = content.match(/description[：:]\s*"([^"]+)"/);
  if (!m) return null;
  const desc = m[1];
  const bracketMatch = desc.match(/任务包?总线[（(]([^）)]+)[）)]/);
  if (bracketMatch && bracketMatch[1].length >= 3 && /[一-鿿]/.test(bracketMatch[1])) {
    return bracketMatch[1].trim();
  }
  const cleaned = desc
    .replace(/^REQ-\d+\s+/, "")
    .replace(/\s*—\s*原始需求.*$/, "")
    .replace(/\s*（方案.*$/, "")
    .replace(/^dev_\d+\s+(?:需求|待办专题|已完成专题|已归档专题)[：:]?\s*/, "")
    .replace(/^v\d+-r\d+-p\d+[-\w]*\s*/, "")
    .replace(/^任务包?总线[（(]/, "")
    .replace(/^任务总线\s*/, "")
    .trim();
  const result = cleaned
    .replace(/^[-\w]+[（(]/, "")
    .replace(/^[(（]+/, "")
    .replace(/[）)]+$/, "")
    .replace(/\s*[—–-]\s*任务总线\s*$/, "")
    .replace(/\s*任务总线\s*$/, "")
    .trim();
  if (!result || /^v\d+-r\d+/.test(result) || result.length < 3) return null;
  return result || null;
}

/** 从 README.md heading 提取标题 */
function extractTitleFromHeading(content) {
  const m = content.match(/^#\s+(?:v\d+-r\d+-p\d+-\S+\s+)?(.+)$/m);
  if (!m) return null;
  return m[1]
    .replace(/[✅📋🚧⏸️🛑🆕]\s*(?:已完成|待办|进行中|暂停|已终止|待激活)\s*$/, "")
    .replace(/\s*任务总线\s*$/, "")
    .replace(/^REQ-\d+\s+/, "")
    .replace(/-([a-z]+-[a-z]+-[a-z]+-[a-z]+)/g, "")
    .trim() || null;
}

/** 从 README.md 提取专题名称字段 */
function extractTitleFromField(content) {
  const m = content.match(/专题名称[：:]\s*[`"]([^`"]+)[`"]/);
  return m ? m[1].trim() : null;
}

/** 清理标题中的模板后缀/前缀 */
function cleanTitle(title) {
  if (!title) return title;
  let result = title;
  const m = result.match(/任务包?总线[（(]([^）)]+)[）)]?/);
  if (m && m[1].length >= 3) {
    result = result.replace(/任务包?总线[（(][^）)]*[）)]?/g, m[1]);
  } else {
    result = result.replace(/任务包?总线[（(][^）)]*[）)]?\s*/g, "");
  }
  result = result
    .replace(/[—–-]\s*任务包?总线\s*/g, "")
    .replace(/\s*任务包?总线\s*/g, "")
    .replace(/^任务包?总线[：:]\s*/, "")
    .replace(/^任务包?总线\s*/, "")
    .replace(/^[：:]\s*/, "")
    .replace(/^[Bb]atch\s+[A-D]\s+/, "")
    .replace(/^v\d+-r\d+[-\w]*[：:\s]+/, "")
    .replace(/^dev_\d+\s+/, "")
    .replace(/需求定义\s*$/, "")
    .replace(/[（(]\s*$/, "")
    .replace(/^\s*[）)]/, "")
    .trim();
  return result;
}

/** 综合提取最佳中文标题 */
function extractBestTitle(readmeContent, reqContent, currentTitle) {
  const cleanedCurrent = cleanTitle(currentTitle);
  if (cleanedCurrent && cleanedCurrent !== currentTitle && isChineseTitle(cleanedCurrent) && cleanedCurrent.length >= 4) {
    return cleanedCurrent;
  }
  if (isChineseTitle(currentTitle) && currentTitle.length >= 4
      && !/任务包?总线/.test(currentTitle) && !/任务总线/.test(currentTitle)) {
    return currentTitle;
  }
  for (const content of [readmeContent, reqContent]) {
    if (!content) continue;
    const fromDesc = extractTitleFromDesc(content);
    if (fromDesc && isChineseTitle(fromDesc)) return fromDesc;
    const fromField = extractTitleFromField(content);
    if (fromField && isChineseTitle(fromField)) return fromField;
    const fromHeading = extractTitleFromHeading(content);
    if (fromHeading && isChineseTitle(fromHeading)) return fromHeading;
  }
  return currentTitle;
}

/** 从 README.md 提取来源层信息 */
function extractSource(readmeContent) {
  if (!readmeContent) return "";
  const diagMatch = readmeContent.match(/前置诊断[：:]\s*[`"]?([^`\n"]+)/);
  if (diagMatch) return diagMatch[1].trim().replace(/[`"]/g, "");
  const upstreamMatch = readmeContent.match(/上游来源[：:]\s*[`"]?([^`\n"]+)/);
  if (upstreamMatch) return upstreamMatch[1].trim().replace(/[`"]/g, "");
  const sourceMatch = readmeContent.match(/来源层[：:]\s*[`"]?([^`\n"]+)/);
  if (sourceMatch) return sourceMatch[1].trim().replace(/[`"]/g, "");
  return "";
}

/** 从 README.md / REQ-*.md 提取备注 */
function extractNote(readmeContent, reqContent, currentTitle) {
  for (const content of [readmeContent, reqContent]) {
    if (!content) continue;
    const descMatch = content.match(/description[：:]\s*"([^"]+)"/);
    if (descMatch) {
      const desc = descMatch[1]
        .replace(/^REQ-\d+\s*/, "")
        .replace(/^dev_\d+\s*(?:需求|待办专题|已完成专题|已归档专题)[：:]?\s*/, "")
        .replace(/^v\d+-r\d+-p\d+[-\w]*\s*/, "")
        .replace(/^任务包总线[（(]/, "")
        .replace(/^任务总线\s*/, "")
        .replace(/^[-\w]+[（(]/, "")
        .replace(/^[(（]+/, "")
        .replace(/[）)]+$/, "")
        .replace(/\s*[—–-]\s*任务总线\s*$/, "")
        .replace(/\s*任务总线\s*$/, "")
        .trim();
      if (desc.length > 5 && desc.length < 100 && desc !== currentTitle) return desc;
    }
  }
  return "";
}

/** 从 README.md / REQ-*.md 提取日期 */
function extractDates(readmeContent, reqContent) {
  let created = null, completed = null;
  for (const content of [readmeContent, reqContent]) {
    if (!content) continue;
    if (!created) {
      const dateMatch = content.match(/(?:最后更新|冻结时间|创建时间|开始时间)[：:]\s*[`"]?(\d{4}-\d{2}-\d{2})/);
      if (dateMatch) created = dateMatch[1];
    }
    if (!completed) {
      const compMatch = content.match(/(?:完成时间|归档时间|结束时间)[：:]\s*[`"]?(\d{4}-\d{2}-\d{2})/);
      if (compMatch) completed = compMatch[1];
    }
  }
  return { created, completed };
}

/** 从 README.md 提取当前状态 */
function extractStatus(readmeContent) {
  if (!readmeContent) return null;
  const statusMatch = readmeContent.match(/当前(?:状态|阶段)[：:]\s*[`"]([^`"]+)[`"]/);
  if (!statusMatch) return null;
  const s = statusMatch[1].toLowerCase();
  if (s.includes("已完成") || s.includes("已归档") || s.includes("done")) return "completed";
  if (s.includes("进行中") || s.includes("in_progress")) return "in_progress";
  if (s.includes("待办") || s.includes("pending")) return "pending";
  if (s.includes("暂停") || s.includes("paused")) return "paused";
  if (s.includes("终止") || s.includes("cancelled")) return "cancelled";
  return null;
}

// ============================================================
// 主逻辑
// ============================================================

function processPackage(pkgPath, currentRR) {
  const readmePath = join(pkgPath, "README.md");
  const readmeContent = readTextSafe(readmePath);

  let reqContent = "";
  try {
    const files = readdirSync(pkgPath).filter((f) => f.startsWith("REQ-") && f.endsWith(".md") && !f.includes("-original"));
    if (files.length > 0) reqContent = readTextSafe(join(pkgPath, files[0]));
  } catch { /* 目录不可读时跳过 */ }

  const title = extractBestTitle(readmeContent, reqContent, currentRR.title);
  const source = extractSource(readmeContent) || currentRR.source || "";
  const note = extractNote(readmeContent, reqContent, title) || currentRR.note || "";
  const dates = extractDates(readmeContent, reqContent);
  const status = extractStatus(readmeContent) || currentRR.status;

  const updated = { ...currentRR };
  if (title !== currentRR.title) updated.title = title;
  if (!currentRR.source && source) updated.source = source;
  if (!currentRR.note && note) updated.note = note;
  if (!currentRR.createdAt && dates.created) updated.createdAt = dates.created;
  if (!currentRR.completedAt && dates.completed) updated.completedAt = dates.completed;
  if (status && status !== currentRR.status) updated.status = status;

  // 从 tasks.md 补充子任务统计
  if (!updated.tasks) {
    const tasksPath = join(pkgPath, "tasks.md");
    if (existsSync(tasksPath)) {
      const tasksContent = readTextSafe(tasksPath);
      const completedMatches = tasksContent.match(/✅|已完成|\[x\]/g);
      const totalMatches = tasksContent.match(/^##\s+T-/gm);
      if (totalMatches) {
        updated.tasks = {
          total: totalMatches.length,
          completed: completedMatches ? completedMatches.length : 0,
          pending: totalMatches.length - (completedMatches ? completedMatches.length : 0),
        };
      }
    }
  }

  return updated;
}

function main() {
  const args = process.argv.slice(2);
  const dryRun = !args.includes("--apply");
  const dirsArg = args.find((a) => a.startsWith("--dirs="));
  const filterDirs = dirsArg ? dirsArg.split("=")[1].split(",") : null;
  const baseDirArg = args.find((a) => a.startsWith("--base-dir="));
  let baseDir = baseDirArg ? baseDirArg.split("=")[1] : null;

  // 自动检测 baseDir（若未通过 --base-dir 指定）
  if (!baseDir) {
    // 也支持位置参数
    const posArgs = args.filter((a) => !a.startsWith("-"));
    if (posArgs.length > 0 && existsSync(posArgs[0])) {
      baseDir = posArgs[0];
    } else {
      // 自动从 CWD 向上查找
      let dir = process.cwd();
      for (let i = 0; i < 10; i++) {
        const candidate = join(dir, "docs", "1.task");
        if (existsSync(candidate)) { baseDir = candidate; break; }
        const parent = join(dir, "..");
        if (parent === dir) break;
        dir = parent;
      }
    }
  }

  if (!baseDir || !existsSync(baseDir)) {
    console.error("❌ 未找到任务包目录。请用 --base-dir <path> 指定 docs/1.task 目录路径。");
    console.error("用法：node upgrade-run-result.mjs --base-dir <path> [--dry-run|--apply] [--dirs v06,v07]");
    process.exit(1);
  }

  const statusLayers = ["B.2.done", "B.todo", "B.1.paused", "B.3.cancelled"];
  let processed = 0, improved = 0, skipped = 0, errors = 0;

  for (const layer of statusLayers) {
    const layerPath = join(baseDir, layer);
    if (!existsSync(layerPath)) continue;

    let versions;
    try { versions = readdirSync(layerPath); } catch { continue; }

    for (const ver of versions) {
      if (!ver.startsWith("v")) continue;
      if (filterDirs && !filterDirs.includes(ver)) continue;

      const verPath = join(layerPath, ver);
      let dirs;
      try { dirs = readdirSync(verPath); } catch { continue; }

      for (const dirName of dirs) {
        if (!dirName.match(/^v\d+-r\d+/)) continue;

        const pkgPath = join(verPath, dirName);
        const rrPath = join(pkgPath, "run_result.json");
        const currentRR = readJsonSafe(rrPath);
        if (!currentRR) { skipped++; continue; }

        try {
          const updated = processPackage(pkgPath, currentRR);
          processed++;
          const changed = JSON.stringify(updated) !== JSON.stringify(currentRR);
          if (changed) {
            improved++;
            if (dryRun) {
              console.log(`[improve] ${layer}/${ver}/${dirName}`);
              if (updated.title !== currentRR.title) console.log(`  title: "${currentRR.title}" → "${updated.title}"`);
              if (!currentRR.source && updated.source) console.log(`  source: "${updated.source}"`);
              if (!currentRR.note && updated.note) console.log(`  note: "${updated.note}"`);
              if (!currentRR.createdAt && updated.createdAt) console.log(`  createdAt: "${updated.createdAt}"`);
            } else {
              writeFileSync(rrPath, JSON.stringify(updated, null, 2) + "\n", "utf-8");
            }
          }
        } catch (err) {
          errors++;
          console.error(`❌ ${dirName}: ${err.message}`);
        }
      }
    }
  }

  console.log(`\n📊 upgrade 结果：`);
  console.log(`  处理：${processed}`);
  console.log(`  改进：${improved}`);
  console.log(`  跳过：${skipped}`);
  console.log(`  错误：${errors}`);
  if (dryRun) console.log(`\n   确认后执行：node upgrade-run-result.mjs --base-dir <path> --apply`);
}

main();
