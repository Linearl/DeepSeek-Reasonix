#!/usr/bin/env node
/**
 * 重复定义扫描器
 *
 * 扫描指定目录下的 TypeScript/JavaScript 文件，查找同名但不同定义的函数/类/常量。
 * 用于发现潜在的重复实现或命名冲突。
 *
 * 用法：
 *   node scripts/diagnostics/scan-duplicate-definitions.mjs <目录>
 *   node scripts/diagnostics/scan-duplicate-definitions.mjs backend/src --min-occurrences 2
 *   node scripts/diagnostics/scan-duplicate-definitions.mjs webapp/src --min-occurrences 3
 */

import fs from "fs";
import path from "path";

const DEFAULT_MIN_OCCURRENCES = 2;
const SCAN_EXTS = new Set([".ts", ".tsx", ".js", ".jsx"]);
const SKIP_DIRS = new Set(["node_modules", "dist", ".git", "__tests__", "__mocks__", "test-results"]);

// ─── 文件扫描 ──────────────────────────────────────────────

function scanDirectory(dir, files = []) {
  try {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      if (SKIP_DIRS.has(entry.name)) continue;
      const fullPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        scanDirectory(fullPath, files);
      } else if (entry.isFile() && SCAN_EXTS.has(path.extname(entry.name).toLowerCase())) {
        files.push(fullPath);
      }
    }
  } catch {
    // skip inaccessible dirs
  }
  return files;
}

// ─── 声明提取 ──────────────────────────────────────────────

function extractDeclarations(content, filePath) {
  const declarations = [];
  const lines = content.split("\n");

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineNum = i + 1;

    // export function / function
    const funcMatch = line.match(
      /^(?:export\s+)?(?:(?:async|generator|static)\s+)*function\s+(\w+)\s*(?:<[^>]+>)?\s*\(([^)]*)\)/
    );
    if (funcMatch) {
      declarations.push({
        name: funcMatch[1],
        type: "function",
        params: funcMatch[2].trim(),
        file: filePath,
        line: lineNum,
        signature: `function ${funcMatch[1]}(${funcMatch[2].trim()})`,
      });
      continue;
    }

    // export class / class
    const classMatch = line.match(/^(?:export\s+)?class\s+(\w+)(?:\s+extends\s+\w+)?(?:\s+implements\s+[\w,\s]+)?/);
    if (classMatch) {
      declarations.push({
        name: classMatch[1],
        type: "class",
        params: "",
        file: filePath,
        line: lineNum,
        signature: `class ${classMatch[1]}`,
      });
      continue;
    }

    // export const/let/var Name = (箭头函数或普通函数)
    const constFuncMatch = line.match(
      /^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*(?::\s*[^=]+)?\s*=\s*(?:async\s+)?(?:\([^)]*\)|[\w]+)\s*=>/
    );
    if (constFuncMatch) {
      declarations.push({
        name: constFuncMatch[1],
        type: "arrow-function",
        params: "",
        file: filePath,
        line: lineNum,
        signature: `const ${constFuncMatch[1]} = (...) => ...`,
      });
      continue;
    }

    // export const/let/var Name（非函数）
    const constMatch = line.match(/^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*[=;]/);
    if (constMatch && !constFuncMatch) {
      declarations.push({
        name: constMatch[1],
        type: "constant",
        params: "",
        file: filePath,
        line: lineNum,
        signature: `const ${constMatch[1]}`,
      });
    }
  }

  return declarations;
}

// ─── 重复检测 ──────────────────────────────────────────────

function findDuplicates(declarations) {
  const groups = {};

  for (const decl of declarations) {
    const key = decl.name;
    if (!groups[key]) {
      groups[key] = [];
    }
    groups[key].push(decl);
  }

  // 过滤出有多个定义的组
  const duplicates = [];
  for (const [name, decls] of Object.entries(groups)) {
    if (decls.length < 2) continue;

    // 检查是否真的是不同的定义（不同文件或不同签名）
    const uniqueSigs = new Set(decls.map((d) => d.signature));
    const uniqueFiles = new Set(decls.map((d) => d.file));

    // 同一文件内多个同名定义
    if (uniqueFiles.size === 1) {
      duplicates.push({
        name,
        count: decls.length,
        uniqueSignatures: uniqueSigs.size,
        severity: "HIGH",
        reason: "同一文件内多个同名定义",
        declarations: decls,
      });
      continue;
    }

    // 不同文件但签名相同（可能的重复实现）
    if (uniqueSigs.size === 1 && uniqueFiles.size >= 2) {
      duplicates.push({
        name,
        count: decls.length,
        uniqueSignatures: uniqueSigs.size,
        severity: "MEDIUM",
        reason: "不同文件中相同签名的定义（可能的重复实现）",
        declarations: decls,
      });
      continue;
    }

    // 不同文件且签名不同（可能是重载、接口冲突或命名冲突）
    if (uniqueSigs.size > 1) {
      duplicates.push({
        name,
        count: decls.length,
        uniqueSignatures: uniqueSigs.size,
        severity: "MEDIUM",
        reason: "不同文件中不同签名的定义（可能是重载或命名冲突）",
        declarations: decls,
      });
    }
  }

  // 按严重度和出现次数排序
  duplicates.sort((a, b) => {
    if (a.severity !== b.severity) return a.severity === "HIGH" ? -1 : 1;
    return b.count - a.count;
  });

  return duplicates;
}

// ─── 报告输出 ──────────────────────────────────────────────

function printReport(duplicates, scannedFiles, targetDir) {
  console.log(`\n${"=".repeat(60)}`);
  console.log(`  重复定义扫描报告`);
  console.log(`  目录: ${targetDir}`);
  console.log(`  扫描文件: ${scannedFiles} 个`);
  console.log(`  发现重复: ${duplicates.length} 组`);
  console.log(`${"=".repeat(60)}\n`);

  if (duplicates.length === 0) {
    console.log("✅ 未发现重复定义问题\n");
    return;
  }

  // HIGH severity
  const high = duplicates.filter((d) => d.severity === "HIGH");
  const medium = duplicates.filter((d) => d.severity === "MEDIUM");

  if (high.length > 0) {
    console.log(`🔴 HIGH — 同一文件内多个同名定义 (${high.length} 组)\n`);
    for (const dup of high) {
      console.log(`  ${dup.name} (${dup.count} 个定义, ${dup.uniqueSignatures} 种签名)`);
      for (const decl of dup.declarations) {
        const relFile = path.relative(process.cwd(), decl.file);
        console.log(`    ${relFile}:${decl.line} — ${decl.signature}`);
      }
      console.log("");
    }
  }

  if (medium.length > 0) {
    console.log(`🟡 MEDIUM — 跨文件重复/冲突定义 (${medium.length} 组)\n`);
    for (const dup of medium.slice(0, 20)) {
      console.log(`  ${dup.name} (${dup.count} 个定义, ${dup.uniqueSignatures} 种签名) — ${dup.reason}`);
      for (const decl of dup.declarations) {
        const relFile = path.relative(process.cwd(), decl.file);
        console.log(`    ${relFile}:${decl.line} — ${decl.signature}`);
      }
      console.log("");
    }
    if (medium.length > 20) {
      console.log(`  ... 及其他 ${medium.length - 20} 组\n`);
    }
  }

  // 统计摘要
  const typeStats = {};
  for (const dup of duplicates) {
    for (const decl of dup.declarations) {
      typeStats[decl.type] = (typeStats[decl.type] || 0) + 1;
    }
  }
  console.log(`${"─".repeat(60)}`);
  console.log(`统计: ${Object.entries(typeStats).map(([t, c]) => `${t}: ${c}`).join(", ")}`);
  console.log("");
}

// ─── 主逻辑 ────────────────────────────────────────────────

function main() {
  const args = process.argv.slice(2);
  const targetDir = args.find((a) => !a.startsWith("--")) || "src";
  const minIdx = args.indexOf("--min-occurrences");
  const minOccurrences = minIdx >= 0 ? parseInt(args[minIdx + 1]) || DEFAULT_MIN_OCCURRENCES : DEFAULT_MIN_OCCURRENCES;

  // 解析为绝对路径
  const absDir = path.resolve(targetDir);
  if (!fs.existsSync(absDir)) {
    console.error(`❌ 目录不存在: ${absDir}`);
    process.exit(1);
  }

  console.log(`扫描目录: ${absDir}`);
  const files = scanDirectory(absDir);
  console.log(`找到 ${files.length} 个源文件`);

  // 提取所有声明
  const allDeclarations = [];
  for (const file of files) {
    try {
      const content = fs.readFileSync(file, "utf-8");
      const decls = extractDeclarations(content, file);
      allDeclarations.push(...decls);
    } catch {
      // skip unreadable files
    }
  }

  console.log(`提取 ${allDeclarations.length} 个声明`);

  // 查找重复
  const duplicates = findDuplicates(allDeclarations).filter((d) => d.count >= minOccurrences);

  // 输出报告
  printReport(duplicates, files.length, targetDir);

  // 退出码：有 HIGH 问题返回 1
  const hasHigh = duplicates.some((d) => d.severity === "HIGH");
  process.exit(hasHigh ? 1 : 0);
}

main();
