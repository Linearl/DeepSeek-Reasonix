#!/usr/bin/env node
/**
 * CAPABILITY_MAP 校验脚本（参考实现）
 *
 * 检查 CAPABILITY_MAP.md 中的文件路径和函数名是否在代码库中存在。
 * 用于定期清理过时条目，保持索引准确性。
 *
 * 适配说明：
 *   1. 修改 CAPABILITY_MAP 指向你的能力索引文件
 *   2. 修改 SEARCH_DIRS 指向你的源码目录
 *
 * 用法：
 *   node validate-capability-map.mjs
 *   node validate-capability-map.mjs --fix  （交互式修复）
 */

import fs from "fs";
import path from "path";

// ─── 路径常量（需适配） ──────────────────────────────────────
const PROJECT_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const CAPABILITY_MAP = process.env.CAPABILITY_MAP_PATH
  || path.join(PROJECT_ROOT, "docs", "2.tech", "CAPABILITY_MAP.md");
const SEARCH_DIRS = [
  process.env.CAPABILITY_SEARCH_DIR || path.join(PROJECT_ROOT, "src"),
];

// ─── 解析 CAPABILITY_MAP ─────────────────────────────────────
function parseCapabilityMap(filePath) {
  if (!fs.existsSync(filePath)) {
    console.error(`CAPABILITY_MAP 不存在: ${filePath}`);
    return [];
  }

  const content = fs.readFileSync(filePath, "utf-8");
  const entries = [];

  for (const line of content.split("\n")) {
    if (!line.includes("|") || line.includes("---")) continue;

    // 提取签名列中的函数名
    const sigMatch = line.match(/`(\w+)\s*\(/);
    if (!sigMatch) continue;
    const funcName = sigMatch[1];

    // 提取文件路径
    const fileMatch = line.match(/\|\s*([^|]+\.(?:ts|js|tsx|jsx))\s*\|/);
    const filePath = fileMatch ? fileMatch[1].trim() : null;

    // 提取模块/说明
    const descMatch = line.match(/\|\s*([^|]+)\s*\|\s*$/);
    const description = descMatch ? descMatch[1].trim() : "";

    entries.push({ funcName, filePath, description, rawLine: line.trim() });
  }

  return entries;
}

// ─── 递归查找文件（跨平台，不依赖 find 命令） ────────────────
function findFilesRecursive(dir, extensions) {
  const results = [];
  try {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      const fullPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (!["node_modules", "dist", ".git", "__tests__"].includes(entry.name)) {
          results.push(...findFilesRecursive(fullPath, extensions));
        }
      } else if (extensions.some((ext) => entry.name.endsWith(ext))) {
        results.push(fullPath);
      }
    }
  } catch { /* skip inaccessible */ }
  return results;
}

// ─── 在代码库中搜索函数名 ────────────────────────────────────
function searchFunctionInCodebase(funcName, searchDirs) {
  const pattern = new RegExp(`(?:export\\s+)?(?:async\\s+)?function\\s+${funcName}\\b|export\\s+(?:const|let|var)\\s+${funcName}\\b`);

  for (const dir of searchDirs) {
    if (!fs.existsSync(dir)) continue;
    const files = findFilesRecursive(dir, [".ts", ".tsx", ".js", ".jsx"]);
    for (const file of files) {
      try {
        const content = fs.readFileSync(file, "utf-8");
        if (pattern.test(content)) {
          return { found: true, file: path.relative(PROJECT_ROOT, file) };
        }
      } catch { /* skip */ }
    }
  }
  return { found: false, file: null };
}

// ─── 主逻辑 ──────────────────────────────────────────────────
function main() {
  const entries = parseCapabilityMap(CAPABILITY_MAP);
  if (entries.length === 0) {
    console.log("未找到有效的 CAPABILITY_MAP 条目");
    process.exit(0);
  }

  console.log(`\nCAPABILITY_MAP 校验报告`);
  console.log(`条目总数: ${entries.length}`);
  console.log(`${"─".repeat(60)}\n`);

  const issues = [];

  for (const entry of entries) {
    // 检查文件路径是否存在
    if (entry.filePath) {
      const fullPath = path.resolve(PROJECT_ROOT, entry.filePath);
      if (!fs.existsSync(fullPath)) {
        issues.push({
          type: "FILE_MISSING",
          severity: "HIGH",
          funcName: entry.funcName,
          detail: `文件不存在: ${entry.filePath}`,
        });
        continue;
      }

      // 检查函数名是否在文件中
      try {
        const content = fs.readFileSync(fullPath, "utf-8");
        const pattern = new RegExp(`(?:export\\s+)?(?:async\\s+)?function\\s+${entry.funcName}\\b|export\\s+(?:const|let|var)\\s+${entry.funcName}\\b`);
        if (!pattern.test(content)) {
          issues.push({
            type: "FUNC_MISSING",
            severity: "MEDIUM",
            funcName: entry.funcName,
            detail: `函数 ${entry.funcName} 不存在于 ${entry.filePath}`,
          });
        }
      } catch { /* skip */ }
    } else {
      // 无文件路径，在整个代码库中搜索
      const result = searchFunctionInCodebase(entry.funcName, SEARCH_DIRS);
      if (!result.found) {
        issues.push({
          type: "FUNC_NOT_FOUND",
          severity: "MEDIUM",
          funcName: entry.funcName,
          detail: `函数 ${entry.funcName} 在代码库中未找到 export 声明`,
        });
      }
    }
  }

  if (issues.length === 0) {
    console.log("✅ 所有条目校验通过\n");
    process.exit(0);
  }

  const high = issues.filter((i) => i.severity === "HIGH");
  const medium = issues.filter((i) => i.severity === "MEDIUM");

  if (high.length > 0) {
    console.log(`🔴 HIGH (${high.length} 项)\n`);
    for (const issue of high) {
      console.log(`  ${issue.funcName}: ${issue.detail}`);
    }
    console.log("");
  }

  if (medium.length > 0) {
    console.log(`🟡 MEDIUM (${medium.length} 项)\n`);
    for (const issue of medium) {
      console.log(`  ${issue.funcName}: ${issue.detail}`);
    }
    console.log("");
  }

  process.exit(high.length > 0 ? 1 : 0);
}

main();
