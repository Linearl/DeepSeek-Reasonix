#!/usr/bin/env node
// 任务 637 — 注释泄漏防再发守卫（604 同族扩大化的机制性收口）。
//
// 背景（604 + 637 两轮用户截图实锤）：设置 → 实验室的卡片区域把**代码注释**
// 当描述渲染给了用户。机制有二：
//   1. JSX children 位置的裸 `//` 行——在 JSX 里那不是注释，是会被渲染的
//      文本（SettingsPanel.tsx 曾有 5 处：604 三处 Task 254/254/277 +
//      637 两处 Task 342/377）。
//   2. locale 描述值本身携带注释特征——`//` 注释语法片段，或以
//      "Task N:"/"任务 N：" 署名开头（实现注释口吻，不是面向用户的文案）。
//
// 本守卫扫两类面，规则如下：
//   R1 代码面：src/**/*.tsx 的 JSX 文本子节点中出现行首 `//`（即会被渲染
//      的行注释）。
//   R2 文案面：locales/{zh,en,zh-TW}.ts 的字符串值 + public/fork-features.yaml
//      的标量值中，出现注释语法（`//`，URL 的 `://` 除外；或 `/*`）。
//   R3 文案面：同上取值中，值以 `Task N` / `任务 N` / `任務 N` 署名开头
//      （任务号属于 tasklist/changelog，不属于用户可见文案）。
//
// 用法：
//   node scripts/check-comment-leak.mjs              扫描现状，有泄漏 exit 1
//   node scripts/check-comment-leak.mjs --self-test  用内嵌违规样例自检规则
//
// 规则函数同时导出，供 src/__tests__/task637-comment-leak.test.ts 断言
// 「故意违规样例必须报错」。

import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { pathToFileURL } from "node:url";
import ts from "typescript";

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "..");

// ── 规则（纯函数，导出供测试） ────────────────────────────────────────────

/** R1: JSX 文本子节点里行首 `//` = 会被渲染的行注释。 */
export function jsxTextLeaksComment(jsxText) {
  return /(^|\n)[ \t]*\/\//.test(jsxText);
}

/** R2: 文案值里出现注释语法。`://`（URL 协议分隔）不算；`//` 与 `/*` 算。 */
export function valueContainsCommentSyntax(value) {
  if (/(^|[^:])\/\//.test(value)) return true;
  if (/\/\*/.test(value)) return true;
  return false;
}

/** R3: 文案值以任务号署名开头（Task N / 任务 N / 任務 N）。 */
export function valueStartsWithTaskAttribution(value) {
  return /^\s*(?:Task|任务|任務)\s*\d/.test(value);
}

function ruleNameFor(value) {
  if (valueContainsCommentSyntax(value)) return "R2 注释语法";
  if (valueStartsWithTaskAttribution(value)) return "R3 任务号署名";
  return null;
}

// ── R1: AST 扫描 tsx 的 JSX 文本子节点 ───────────────────────────────────

function listTsxFiles(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) listTsxFiles(p, out);
    else if (entry.name.endsWith(".tsx")) out.push(p);
  }
  return out;
}

/** 豁免：刻意展示的代码样例（主题预览的语法高亮 island 等）。判定依据是
 * 祖先元素的 className 含 `syntax--comment`——文本被明确按「代码注释样例」
 * 造型时是有意渲染，不属于泄漏。 */
function insideDeliberateCodeSample(node) {
  let cur = node.parent;
  while (cur) {
    if ((ts.isJsxElement(cur) || ts.isJsxSelfClosingElement(cur))) {
      const element = ts.isJsxElement(cur) ? cur.openingElement : cur;
      for (const attr of element.attributes.properties) {
        if (ts.isJsxAttribute(attr) && attr.name.text === "className" && attr.initializer && ts.isStringLiteral(attr.initializer)) {
          if (attr.initializer.text.includes("syntax--comment")) return true;
        }
      }
    }
    cur = cur.parent;
  }
  return false;
}

function scanJsxTextComments(onViolation) {
  const files = listTsxFiles(join(frontendRoot, "src"));
  for (const file of files) {
    const source = readFileSync(file, "utf8");
    const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    const visit = (node) => {
      if (ts.isJsxText(node) && jsxTextLeaksComment(node.getText()) && !insideDeliberateCodeSample(node)) {
        const line = sf.getLineAndCharacterOfPosition(node.getStart()).line + 1;
        onViolation({
          rule: "R1 JSX文本行注释",
          file,
          line,
          snippet: node.getText().trim().replace(/\s+/g, " ").slice(0, 100),
        });
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return files.length;
}

// ── R2/R3: locale 文件（TS 对象字面量字符串值） ──────────────────────────

const LOCALE_FILES = ["src/locales/zh.ts", "src/locales/en.ts", "src/locales/zh-TW.ts"];

function scanLocaleValues(onViolation) {
  let values = 0;
  for (const rel of LOCALE_FILES) {
    const file = join(frontendRoot, rel);
    const sf = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const visit = (node) => {
      if (ts.isPropertyAssignment(node) && ts.isStringLiteral(node.name) && ts.isStringLiteral(node.initializer)) {
        const value = node.initializer.text;
        values += 1;
        const rule = ruleNameFor(value);
        if (rule) {
          onViolation({
            rule,
            file,
            line: sf.getLineAndCharacterOfPosition(node.getStart()).line + 1,
            key: node.name.text,
            snippet: value.slice(0, 100),
          });
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return values;
}

// ── R2/R3: fork-features.yaml 标量值（行扫描，刻意不引 YAML 依赖） ────────

function scanYamlValues(onViolation) {
  const file = join(frontendRoot, "public", "fork-features.yaml");
  const lines = readFileSync(file, "utf8").split(/\r?\n/);
  let values = 0;
  for (let i = 0; i < lines.length; i += 1) {
    const raw = lines[i];
    const trimmed = raw.trim();
    if (trimmed === "" || trimmed.startsWith("#")) continue;
    const match = /^-?\s*([A-Za-z_][\w-]*):\s+(.+)$/.exec(trimmed);
    if (!match) continue;
    let value = match[2].trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    values += 1;
    const rule = ruleNameFor(value);
    if (rule) {
      onViolation({ rule, file, line: i + 1, key: match[1], snippet: value.slice(0, 100) });
    }
  }
  return values;
}

// ── 自检样例（验收要求：故意违规样例必须报错） ────────────────────────────

const SELF_TEST_CASES = [
  // [名称, 规则函数, 输入, 期望(是否必须报违规)]
  ["R1 拦截 JSX 裸行注释（637 实锤形态）", jsxTextLeaksComment, "\n    // Task 342: WebView2 CDP debug endpoint.\n    ", true],
  ["R1 放行正常 JSX 文本", jsxTextLeaksComment, "\n    点一下时钟图标，历史提问任你挑\n    ", false],
  ["R2 拦截值内 // 注释", valueContainsCommentSyntax, "跳过崩溃报告 // see clean()", true],
  ["R2 放行 URL 的 ://", valueContainsCommentSyntax, "详见 https://example.com/docs", false],
  ["R2 放行单斜杠路径", valueContainsCommentSyntax, "写入 logs/desktop/cdp-endpoint.txt", false],
  ["R3 拦截 Task N: 署名（604/637 实锤形态）", valueStartsWithTaskAttribution, "Task 377: skips crash reports for clean-shutdown lifecycle residue.", true],
  ["R3 拦截 任务 N：署名", valueStartsWithTaskAttribution, "任务 377：开启后跳过正常关机残留的崩溃报告。", true],
  ["R3 拦截 任務 N：署名", valueStartsWithTaskAttribution, "任務 377：開啟後跳過正常關機殘留的崩潰報告。", true],
  ["R3 放行句中提及（非开头）", valueStartsWithTaskAttribution, "授权随任务 24 小时有效。", false],
  ["R3 放行普通文案", valueStartsWithTaskAttribution, "开启后跳过正常关机残留的崩溃报告。", false],
];

function runSelfTest() {
  let failed = 0;
  for (const [name, rule, input, shouldFlag] of SELF_TEST_CASES) {
    const flagged = rule(input);
    const ok = flagged === shouldFlag;
    if (!ok) failed += 1;
    process.stdout.write(`  ${ok ? "PASS" : "FAIL"}  ${name}（期望${shouldFlag ? "报" : "不报"}，实际${flagged ? "报" : "不报"}）\n`);
  }
  if (failed > 0) {
    process.stdout.write(`self-test: ${failed} 例失败\n`);
    return false;
  }
  process.stdout.write(`self-test: ${SELF_TEST_CASES.length} 例全过\n`);
  return true;
}

// ── CLI ──────────────────────────────────────────────────────────────────

export function scanAll(onViolation) {
  const tsxFiles = scanJsxTextComments(onViolation);
  const localeValues = scanLocaleValues(onViolation);
  const yamlValues = scanYamlValues(onViolation);
  return { tsxFiles, localeValues, yamlValues };
}

function main() {
  if (process.argv.includes("--self-test")) {
    process.exit(runSelfTest() ? 0 : 1);
  }
  const violations = [];
  const { tsxFiles, localeValues, yamlValues } = scanAll((v) => violations.push(v));
  for (const v of violations) {
    const where = `${v.file.replace(frontendRoot, "").replace(/^[\\/]/, "")}:${v.line}`;
    const key = v.key ? ` [${v.key}]` : "";
    process.stdout.write(`LEAK ${v.rule} ${where}${key} :: ${v.snippet}\n`);
  }
  process.stdout.write(
    `checked ${tsxFiles} tsx files, ${localeValues} locale values, ${yamlValues} yaml values — ${violations.length} leak(s)\n`,
  );
  process.exit(violations.length > 0 ? 1 : 0);
}

const isMain = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;
if (isMain) main();
