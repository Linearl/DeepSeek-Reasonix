#!/usr/bin/env node
/**
 * Capability Gate — PreToolUse Hook（参考实现）
 *
 * Write/Edit 前自动检测：新标识符是否与已有能力重复。
 * 支持命名风格模糊转换（snake_case ↔ camelCase ↔ PascalCase）。
 *
 * 适配说明：
 *   1. 修改 CAPABILITY_MAP 指向你的能力索引文件
 *   2. 修改 SEARCH_DIR 指向你的源码目录
 *   3. 修改 CONFIG_PATH 指向你的配置文件
 *
 * 匹配级别：
 *   HIGH: 精确匹配/前缀匹配/codebase grep → exit 2 阻断
 *   LOW:  命名风格转换匹配 → exit 2 告警（低级别）
 *   NONE: 无匹配 → exit 0 放行
 */

import fs from "fs";
import path from "path";
import { execSync } from "child_process";

// ─── 路径常量（需适配） ──────────────────────────────────────
const PROJECT_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const CAPABILITY_MAP = process.env.CAPABILITY_MAP_PATH
  || path.join(PROJECT_ROOT, "docs", "2.tech", "CAPABILITY_MAP.md");
const SEARCH_DIR = process.env.CAPABILITY_SEARCH_DIR
  || path.join(PROJECT_ROOT, "src");
const CONFIG_PATH = process.env.DIFF_GATE_CONFIG
  || path.join(PROJECT_ROOT, ".claude", "diff-gate-config.json");

const CODE_EXTS = new Set([".ts", ".tsx", ".js", ".jsx", ".py", ".go", ".rs", ".java", ".c", ".cpp", ".h"]);

// ─── 配置加载 ────────────────────────────────────────────────
function loadConfig() {
  try {
    const raw = fs.readFileSync(CONFIG_PATH, "utf-8");
    const parsed = JSON.parse(raw);
    return parsed.capabilityGate || { fuzzyMatch: true, fuzzyMatchStyles: ["snake_case", "camelCase", "PascalCase"] };
  } catch {
    return { fuzzyMatch: true, fuzzyMatchStyles: ["snake_case", "camelCase", "PascalCase"] };
  }
}

// ─── 命名风格转换 ────────────────────────────────────────────
function toSnakeCase(str) {
  return str
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/([A-Z])([A-Z][a-z])/g, "$1_$2")
    .toLowerCase();
}

function toCamelCase(str) {
  return str.replace(/_([a-z0-9])/g, (_, c) => c.toUpperCase());
}

function toPascalCase(str) {
  const camel = str.includes("_") ? toCamelCase(str) : str;
  return camel.charAt(0).toUpperCase() + camel.slice(1);
}

function getVariants(identifier) {
  const snake = toSnakeCase(identifier);
  const camel = toCamelCase(snake);
  const pascal = toPascalCase(snake);
  return [...new Set([snake, camel, pascal, identifier])];
}

// ─── 标识符提取 ──────────────────────────────────────────────
function extractExportIdentifiers(content) {
  if (!content) return [];
  const identifiers = [];
  const pattern = /export\s+(?:async\s+)?(?:function|class|const|let|var)\s+(\w+)/g;
  let match;
  while ((match = pattern.exec(content)) !== null) {
    identifiers.push(match[1]);
  }
  return identifiers;
}

// ─── CAPABILITY_MAP 搜索 ─────────────────────────────────────
function searchCapabilityMap(identifier, config) {
  if (!fs.existsSync(CAPABILITY_MAP)) return { exact: [], fuzzy: [] };

  const content = fs.readFileSync(CAPABILITY_MAP, "utf-8");
  const lines = content.split("\n");
  const variants = getVariants(identifier);
  const exact = [];
  const fuzzy = [];

  for (const line of lines) {
    if (!line.includes("|")) continue;

    // 从签名列提取函数名
    const sigMatch = line.match(/`(\w+)\s*\(/);
    if (!sigMatch) continue;
    const existingName = sigMatch[1];

    const fileMatch = line.match(/\|([^|]+)\.(?:ts|js)/);
    const descMatch = line.match(/\|\s*([^|]+)\s*\|\s*$/);
    const hit = {
      file: fileMatch ? fileMatch[1].trim().split("/").pop() + "." + (line.includes(".ts") ? "ts" : "js") : "",
      name: existingName,
      description: descMatch ? descMatch[1].trim() : "",
    };

    // 精确匹配（含命名风格变体）
    if (variants.includes(existingName)) {
      exact.push(hit);
      continue;
    }

    // 前缀匹配
    if (existingName.startsWith(identifier) || identifier.startsWith(existingName)) {
      exact.push({ ...hit, description: `前缀相似: ${existingName}` });
      continue;
    }

    // 命名风格模糊匹配
    if (config.fuzzyMatch) {
      const existingVariants = getVariants(existingName);
      const overlap = variants.filter((v) => existingVariants.includes(v));
      if (overlap.length > 0 && !exact.some((e) => e.name === existingName)) {
        fuzzy.push({ ...hit, description: `命名风格相似: ${overlap[0]}` });
      }
    }
  }

  return { exact, fuzzy };
}

// ─── 代码库搜索 ──────────────────────────────────────────────
function searchCodebase(identifier) {
  const variants = getVariants(identifier);
  const results = [];

  for (const variant of variants) {
    try {
      const output = execSync(
        `grep -rn "export.*\\b${variant}\\b" --include="*.ts" --include="*.js" "${SEARCH_DIR}" 2>/dev/null`,
        { encoding: "utf-8", timeout: 5000 }
      );
      const hits = output.trim().split("\n").filter(Boolean);
      for (const hit of hits) {
        if (!results.some((r) => r === hit)) results.push(hit);
      }
    } catch { /* grep 未找到匹配 */ }
  }

  return results;
}

// ─── 主逻辑 ──────────────────────────────────────────────────
function main() {
  const args = process.argv.slice(2);

  if (args.includes("--status")) {
    console.log(JSON.stringify({
      capabilityMapExists: fs.existsSync(CAPABILITY_MAP),
      searchDirExists: fs.existsSync(SEARCH_DIR),
    }));
    return;
  }

  let inputData = "";
  try { inputData = fs.readFileSync(0, "utf-8"); } catch { return; }

  let toolInput;
  try { toolInput = JSON.parse(inputData); } catch { return; }

  const filePath = toolInput.file_path || "";
  const ext = path.extname(filePath).toLowerCase();
  if (!CODE_EXTS.has(ext)) process.exit(0);

  const content = toolInput.content || toolInput.new_string || "";
  const identifiers = extractExportIdentifiers(content);
  if (identifiers.length === 0) process.exit(0);

  const config = loadConfig();
  const highWarnings = [];
  const lowWarnings = [];

  for (const id of identifiers) {
    const { exact, fuzzy } = searchCapabilityMap(id, config);
    const codeHits = searchCodebase(id);
    const externalCodeHits = codeHits.filter((hit) => !hit.startsWith(filePath + ":"));

    if (exact.length > 0 || externalCodeHits.length > 0) {
      highWarnings.push({ id, mapHits: exact, codeHits: externalCodeHits });
    } else if (fuzzy.length > 0) {
      lowWarnings.push({ id, mapHits: fuzzy });
    }
  }

  if (highWarnings.length === 0 && lowWarnings.length === 0) process.exit(0);

  let msg = "";

  if (highWarnings.length > 0) {
    msg += `HIGH: 检测到高置信度重复——新建标识符可能与已有能力冲突\n\n`;
    for (const w of highWarnings) {
      msg += `标识符: "${w.id}"\n`;
      if (w.mapHits.length > 0) {
        msg += `  CAPABILITY_MAP 匹配:\n`;
        for (const hit of w.mapHits) msg += `    - ${hit.file} ${hit.description}\n`;
      }
      if (w.codeHits.length > 0) {
        msg += `  代码库 export 匹配:\n`;
        for (const hit of w.codeHits.slice(0, 3)) msg += `    - ${hit}\n`;
      }
      msg += `\n`;
    }
    msg += `请确认：\n`;
    msg += `1. 是否可以复用已有实现？\n`;
    msg += `2. 如果确实需要新建，请确认命名不会造成混淆\n`;
    msg += `3. 确认后继续写入\n`;
  }

  if (lowWarnings.length > 0) {
    if (highWarnings.length > 0) msg += `\n`;
    msg += `LOW: 命名风格相似（低置信度）——以下标识符与已有能力在命名风格上相似\n\n`;
    for (const w of lowWarnings) {
      msg += `标识符: "${w.id}"\n`;
      if (w.mapHits.length > 0) {
        msg += `  相似能力:\n`;
        for (const hit of w.mapHits) msg += `    - ${hit.name} (${hit.file}) ${hit.description}\n`;
      }
      msg += `\n`;
    }
    msg += `此为低级别提示，不影响写入。如需保持命名一致性可参考已有命名。\n`;
  }

  console.log(msg);
  process.exit(2);
}

main();
