#!/usr/bin/env node
/**
 * Diff-Size Gate — PostToolUse Hook（参考实现）
 *
 * 累计代码变更行数，超阈值时输出审计指令。
 * 模型自主执行审计→修复→继续，或升级给用户。
 *
 * 适配说明：
 *   1. 修改 CONFIG_PATH 指向你的配置文件
 *   2. 修改 STATE_DIR 指向你的状态存储目录
 *   3. 调整 isCodeFile 中的扩展名和排除模式
 *
 * 用法：
 *   node diff-gate.mjs          ← 检查+更新计数（PostToolUse 调用）
 *   node diff-gate.mjs --reset  ← 重置计数器（审计完成后调用）
 *   node diff-gate.mjs --status ← 输出当前状态
 */

import fs from "fs";
import path from "path";
import { randomUUID } from "crypto";

// ─── 路径常量（需适配） ──────────────────────────────────────
const PROJECT_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const CONFIG_PATH = process.env.DIFF_GATE_CONFIG
  || path.join(PROJECT_ROOT, ".claude", "diff-gate-config.json");
const STATE_DIR = process.env.DIFF_GATE_STATE_DIR
  || path.join(PROJECT_ROOT, ".omc", "diff-gate");
const SESSION_ID_FILE = path.join(STATE_DIR, ".session-id");

// ─── 默认配置 ────────────────────────────────────────────────
const DEFAULT_CONFIG = {
  threshold: 500,
  codeExtensions: [".ts", ".tsx", ".js", ".jsx", ".py", ".go", ".rs", ".java"],
  excludePatterns: ["node_modules", "dist", ".git", "*.lock", "*.md"],
};

// ─── 配置加载 ────────────────────────────────────────────────
function loadConfig() {
  try {
    const raw = fs.readFileSync(CONFIG_PATH, "utf-8");
    return { ...DEFAULT_CONFIG, ...JSON.parse(raw) };
  } catch {
    return DEFAULT_CONFIG;
  }
}

// ─── 会话 ID（per-session 隔离） ─────────────────────────────
function getSessionId() {
  if (process.env.CLAUDE_SESSION_ID) return process.env.CLAUDE_SESSION_ID;

  try {
    const existing = fs.readFileSync(SESSION_ID_FILE, "utf-8").trim();
    if (existing) return existing;
  } catch { /* 生成新 ID */ }

  const id = randomUUID().slice(0, 12);
  try {
    fs.mkdirSync(STATE_DIR, { recursive: true });
    fs.writeFileSync(SESSION_ID_FILE, id, "utf-8");
  } catch { /* 不影响运行 */ }
  return id;
}

// ─── 状态管理 ────────────────────────────────────────────────
function getStatePath(sessionId) {
  return path.join(STATE_DIR, `session-${sessionId}.json`);
}

function loadState(sessionId) {
  const defaults = {
    sessionStart: new Date().toISOString(),
    codeLinesChanged: 0,
    modifiedFiles: [],
    auditCount: 0,
    lastAuditAt: null,
  };
  try {
    return { ...defaults, ...JSON.parse(fs.readFileSync(getStatePath(sessionId), "utf-8")) };
  } catch {
    return defaults;
  }
}

function saveState(sessionId, state) {
  try {
    fs.mkdirSync(STATE_DIR, { recursive: true });
    fs.writeFileSync(getStatePath(sessionId), JSON.stringify(state, null, 2), "utf-8");
  } catch (err) {
    console.error(`[diff-gate] 状态写入失败: ${err.message}`);
  }
}

// ─── 代码文件判定 ────────────────────────────────────────────
function isCodeFile(filePath, config) {
  if (!filePath) return false;
  const ext = path.extname(filePath).toLowerCase();
  if (!config.codeExtensions.includes(ext)) return false;

  const normalized = filePath.replace(/\\/g, "/");
  for (const pattern of config.excludePatterns) {
    if (pattern.startsWith("*")) {
      if (ext === pattern.slice(1)) return false;
    } else if (normalized.includes(pattern)) {
      return false;
    }
  }
  return true;
}

// ─── 行数计算 ────────────────────────────────────────────────
function countWriteLines(content) {
  return content ? content.split("\n").length : 0;
}

function countEditLines(oldStr, newStr) {
  const oldLines = oldStr ? oldStr.split("\n").length : 0;
  const newLines = newStr ? newStr.split("\n").length : 0;
  return Math.abs(newLines - oldLines);
}

// ─── 审计指令输出 ────────────────────────────────────────────
function outputAuditInstruction(state, threshold) {
  const fileList = state.modifiedFiles.map((f) => `  - ${f}`).join("\n");

  console.log(`
AUDIT_REQUIRED: 本对话累计 ${state.codeLinesChanged} 行代码变更（阈值: ${threshold}）

本对话修改的文件：
${fileList}

请执行审计：
1. 读取上述文件的 git diff，重点关注本对话变更
2. 检查是否有重复实现（grep 仓库相似函数名）
3. 检查接口一致性（同名函数签名是否一致）
4. 检查新增文件是否 > 800 行

审计完成后：
- 无问题 → node <diff-gate-path> --reset
- 有问题可自修 → 修复后 --reset
- 需用户决策 → 暂停并询问用户
`);
}

// ─── 主逻辑 ──────────────────────────────────────────────────
function main() {
  const args = process.argv.slice(2);

  if (args.includes("--reset")) {
    const sessionId = getSessionId();
    const state = loadState(sessionId);
    state.codeLinesChanged = 0;
    state.modifiedFiles = [];
    state.auditCount++;
    state.lastAuditAt = new Date().toISOString();
    saveState(sessionId, state);
    console.log(`[diff-gate] 计数器已重置（累计审计 ${state.auditCount} 次）`);
    return;
  }

  if (args.includes("--status")) {
    const sessionId = getSessionId();
    const state = loadState(sessionId);
    const config = loadConfig();
    console.log(JSON.stringify({ ...state, threshold: config.threshold, sessionId }, null, 2));
    return;
  }

  const config = loadConfig();
  if (config.threshold === -1) return; // 功能关闭

  let inputData = "";
  try { inputData = fs.readFileSync(0, "utf-8"); } catch { return; }

  let toolInput;
  try { toolInput = JSON.parse(inputData); } catch { return; }

  let filePath = "";
  let linesChanged = 0;

  if (toolInput.file_path) {
    filePath = toolInput.file_path;
    linesChanged = countWriteLines(toolInput.content);
  } else if (toolInput.old_string !== undefined && toolInput.new_string !== undefined) {
    filePath = toolInput.file_path || "";
    linesChanged = countEditLines(toolInput.old_string, toolInput.new_string);
  }

  if (!isCodeFile(filePath, config)) return;

  const sessionId = getSessionId();
  const state = loadState(sessionId);
  state.codeLinesChanged += linesChanged;
  if (!state.modifiedFiles.includes(filePath)) {
    state.modifiedFiles.push(filePath);
  }
  saveState(sessionId, state);

  if (state.codeLinesChanged >= config.threshold) {
    outputAuditInstruction(state, config.threshold);
  }
}

main();
