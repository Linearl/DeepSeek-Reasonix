// Run: tsx src/__tests__/context-metrics-narrow-css.test.ts
//
// tl1129: 246px 窄态 rail 下概览指标静默截断回归钉。会话指标「费用不可估算」
// 「196分5秒」与用量分析「总计 107,164,532」曾被 nowrap+ellipsis × 多列 grid
// 硬裁（mini-stat/source-metric 数值列），且多数读数无悬浮 title —— 静默截断。
// 修复两层：数值列允许换行（overflow-wrap:anywhere）；面板体挂容器查询，
// ≤319px 时指标网格单列展开（每项拿全行宽，完整可读）。JSDOM 无布局引擎，
// 沿 context-capacity-badge-css.test.ts（任务 665 同族问题）的静态样式断言。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
// tl1191: mode-scoped rules live in src/styles/mode-*.css now.
const styles = [
  readFileSync(resolve(testDir, "../styles.css"), "utf8"),
  readFileSync(resolve(testDir, "../styles/mode-classic.css"), "utf8"),
  readFileSync(resolve(testDir, "../styles/mode-workbench.css"), "utf8"),
  readFileSync(resolve(testDir, "../styles/mode-creation.css"), "utf8"),
].join("\n").replace(/\/\*[\s\S]*?\*\//g, "");

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(Object.is(actual, expected), `${label} (actual=${String(actual)}, expected=${String(expected)})`);
}

function matchingBlocks(selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(styles)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

console.log("\ncontext metrics narrow container css (tl1129 截断消除)");

// ── 数值列允许换行（基础段 + creation 覆写段 + 用量分析数值列） ──────────
const miniStrong = matchingBlocks(".context-panel__mini-stat strong").join("\n");
ok(/overflow-wrap:\s*anywhere/.test(miniStrong), "mini-stat 数值列允许任意断行（overflow-wrap:anywhere）");
ok(!/white-space:\s*nowrap/.test(miniStrong), "mini-stat 数值列不再 nowrap（「费用不可估算」「196分5秒」不硬裁）");
ok(!/text-overflow:\s*ellipsis/.test(miniStrong), "mini-stat 数值列不再省略号截断");

const creationMiniStrong = matchingBlocks(".app--creation .context-panel__mini-stat strong").join("\n");
ok(/overflow-wrap:\s*anywhere/.test(creationMiniStrong), "creation 覆写段数值列同步允许断行");
ok(!/white-space:\s*nowrap/.test(creationMiniStrong), "creation 覆写段数值列不再 nowrap");

const sourceStrong = matchingBlocks(".context-panel__source-metric strong").join("\n");
ok(/overflow-wrap:\s*anywhere/.test(sourceStrong), "用量分析数值列（总计/输入/输出）允许断行");
ok(!/white-space:\s*nowrap/.test(sourceStrong), "用量分析数值列不再 nowrap（「总计 107,16…」不硬裁）");

// nowrap 组选择器不得再卷入数值列（旧行为：source-metric strong 挂在
// head/span 的 ellipsis 组里）。
const sourceStrongEllipsisBlocks = matchingBlocks(".context-panel__source-metric strong").filter((block) => /text-overflow:\s*ellipsis/.test(block));
eq(sourceStrongEllipsisBlocks.length, 0, "数值列不再出现在任何 ellipsis 规则中（组选择器已拆分）");
const labelBlocks = matchingBlocks(".context-panel__mini-stat-label");
ok(
  labelBlocks.length > 0 && labelBlocks.every((block) => /white-space:\s*nowrap/.test(block)),
  "label 列保持 ellipsis 语义不变（截断回归只限数值列）",
);

// ── 容器查询：窄容器单列展开 ─────────────────────────────────────────────
const bodyBlocks = matchingBlocks(".context-panel__body").join("\n");
ok(/container-type:\s*inline-size/.test(bodyBlocks), "面板体挂 container-type:inline-size（面板宽度不随视口，媒体查询不可用）");
ok(/container-name:\s*context-panel-body/.test(bodyBlocks), "容器具名 context-panel-body");

const containerQuery = styles.match(/@container context-panel-body \(max-width:\s*319px\)\s*\{[\s\S]*?\n\}/);
ok(containerQuery !== null, "@container ≤319px 单列规则块存在");
const containerBody = containerQuery ? containerQuery[0] : "";
ok(/\.context-panel__summary-rows/.test(containerBody), "容器查询覆盖会话指标网格（summary-rows）");
ok(/\.app--creation \.context-panel__summary-rows/.test(containerBody), "creation 高特异性覆写在容器查询内对齐（否则 creation 仍钉 2 列）");
ok(/\.context-panel__source-summary/.test(containerBody), "容器查询覆盖用量分析三列摘要（source-summary）");
ok(/\.context-panel__source-metrics/.test(containerBody), "容器查询覆盖明细展开四列（source-metrics）");
ok(/\.context-panel__source-legend/.test(containerBody), "容器查询覆盖来源图例（source-legend）");
ok(/nth-child\(even\)[^{]*\{[^}]*border-left:\s*0/.test(containerBody), "单列后奇偶行分隔竖线/边距重置（对称行）");

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
