// Run: node --import tsx src/__tests__/jump-preview.test.ts
//
// Pure-logic tests for the question rail's rich hover card (task 149):
// content structure (bold lead / body lines / tool tags) and the
// edge-flip placement. No DOM involved — see lib/jumpPreview.ts.

import {
  buildJumpPreviewContent,
  JUMP_PREVIEW_MAX_BODY_LINES,
  JUMP_PREVIEW_MAX_BODY_LINE_CHARS,
  JUMP_PREVIEW_MAX_TOOLS,
  jumpPreviewPlacement,
  normalizeJumpPreviewTools,
  splitLeadSentence,
} from "../lib/jumpPreview";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  const same = JSON.stringify(actual) === JSON.stringify(expected);
  if (same) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\njump preview content");

// ── 多行正文 ────────────────────────────────────────────────
{
  const content = buildJumpPreviewContent({ text: "修复登录问题\n先看后端日志\n再检查前端表单" });
  eq(content.title, "修复登录问题", "multi-line input promotes its first line to the bold title");
  eq(content.bodyLines, ["先看后端日志", "再检查前端表单"], "multi-line input keeps the remaining lines as body");
  eq(content.tools, [], "a plain question carries no tool tags");
  eq(content.placeholder, false, "a loaded question is not a placeholder");
}

// ── 单行句子切分（压缩锚点文本的粗体导语 + 剩余正文）─────────
{
  const content = buildJumpPreviewContent({ text: "修好登录。另外把失败用例也过一遍" });
  eq(content.title, "修好登录。", "a Chinese one-liner splits at the sentence boundary");
  eq(content.bodyLines, ["另外把失败用例也过一遍"], "the sentence remainder becomes the body line");
}
{
  const content = buildJumpPreviewContent({ text: "Fix the login bug? Also rerun the failing tests" });
  eq(content.title, "Fix the login bug?", "an English one-liner splits at the question mark");
  eq(content.bodyLines, ["Also rerun the failing tests"], "the English remainder becomes the body line");
}
{
  const content = buildJumpPreviewContent({ text: "修好登录。看下" });
  eq(content.title, "修好登录。看下", "a tiny remainder stays in the title instead of a lonely fragment");
}
{
  const content = buildJumpPreviewContent({ text: "e.g. fix the login bug and then rerun everything" });
  eq(content.title, "e.g. fix the login bug and then rerun everything", "an abbreviation lead is never treated as the sentence boundary");
}
{
  eq(splitLeadSentence("版本号 3.14 发布计划细节"), null, "decimal numbers never count as a sentence boundary");
  eq(splitLeadSentence("一句短话。"), null, "a boundary without a remainder does not split");
}

// ── 无边界 → 整行做标题 ─────────────────────────────────────
{
  const content = buildJumpPreviewContent({ text: "帮我把这个构建跑绿" });
  eq(content.title, "帮我把这个构建跑绿", "a boundary-free one-liner stays the whole title");
  eq(content.bodyLines, [], "a boundary-free one-liner has no body");
}

// ── 显式 title/body（未来数据通路接通后的形态）──────────────
{
  const content = buildJumpPreviewContent({ text: "ignored", title: "显式标题", body: "第一行\n第二行" });
  eq(content.title, "显式标题", "an explicit title wins over text derivation");
  eq(content.bodyLines, ["第一行", "第二行"], "an explicit body overrides text derivation");
}
{
  const content = buildJumpPreviewContent({ text: "第一行\n第二行", title: "显式标题" });
  eq(content.title, "显式标题", "an explicit title survives multi-line text");
  eq(content.bodyLines, ["第二行"], "body still derives from the lines after the first");
}

// ── 工具标记 ────────────────────────────────────────────────
{
  eq(
    normalizeJumpPreviewTools(["bash", " Bash ", "READ", "", "edit", "grep", "glob", "web"]),
    ["bash", "READ", "edit", "grep"],
    "tool names are trimmed, deduplicated case-insensitively and capped",
  );
  eq(normalizeJumpPreviewTools(undefined), [], "a missing tool list yields no tags");
  eq(normalizeJumpPreviewTools(["  ", ""]), [], "blank tool names are dropped");
  eq(JUMP_PREVIEW_MAX_TOOLS, 4, "the tool cap stays at four tags per card");

  const content = buildJumpPreviewContent({ text: "跑下构建", tools: ["bash", "bash", "read"] });
  eq(content.tools, ["bash", "read"], "the built content carries the normalized tool tags");
}

// ── 未加载占位 ──────────────────────────────────────────────
{
  const content = buildJumpPreviewContent({ text: "Question 12 (click to load)", loaded: false, tools: ["bash"] });
  eq(content.placeholder, true, "an unloaded aggregated turn is a placeholder");
  eq(content.title, "Question 12 (click to load)", "a placeholder renders its hint text as the title");
  eq(content.bodyLines, [], "a placeholder has no body lines");
  eq(content.tools, [], "a placeholder shows no tool tags");
}

// ── 截断上限 ────────────────────────────────────────────────
{
  const manyLines = Array.from({ length: 20 }, (_, i) => `第${i + 1}行`).join("\n");
  const content = buildJumpPreviewContent({ text: `标题行\n${manyLines}` });
  eq(content.bodyLines.length, JUMP_PREVIEW_MAX_BODY_LINES, "body lines are capped for rendering");

  const longLine = "字".repeat(500);
  const capped = buildJumpPreviewContent({ text: `标题\n${longLine}` });
  eq(capped.bodyLines[0]?.length, JUMP_PREVIEW_MAX_BODY_LINE_CHARS, "each body line is length-capped");

  const longTitle = buildJumpPreviewContent({ text: "标".repeat(300) });
  eq(longTitle.title.length, 120, "the title is length-capped");
}

// ── 退化输入 ────────────────────────────────────────────────
{
  const empty = buildJumpPreviewContent({ text: "" });
  eq(empty.title, "", "empty text yields an empty title");
  eq(empty.bodyLines, [], "empty text yields no body");
}

console.log("\njump preview placement");

// 中点 → 居中不翻转
{
  const placement = jumpPreviewPlacement(120, 100, 240);
  eq(placement.flip, "none", "a mid-rail anchor centers the card without flipping");
  eq(placement.top, 70, "centering puts the anchor at the card's vertical middle");
}
// 恰好贴合上边界 → 仍视为居中
{
  const placement = jumpPreviewPlacement(58, 100, 240);
  eq(placement.flip, "none", "a card that exactly fits the top margin is not flipped");
  eq(placement.top, 8, "an exact fit keeps the margin-aligned top");
}
// 贴近顶端 → 向下翻转
{
  const placement = jumpPreviewPlacement(20, 100, 240);
  eq(placement.flip, "down", "an anchor near the top edge flips the card to grow downward");
  eq(placement.top, 20, "the flipped card starts at the anchor point");
  ok(placement.top >= 8, "the flipped card respects the top margin");
  ok(placement.top + 100 <= 232, "the downward card stays inside the container");
}
// 锚点在边距之内 → 夹到边距
{
  const placement = jumpPreviewPlacement(2, 100, 240);
  eq(placement.flip, "down", "an anchor inside the margin still flips downward");
  eq(placement.top, 8, "the card is clamped to the top margin");
}
// 贴近底端 → 向上翻转
{
  const placement = jumpPreviewPlacement(200, 100, 240);
  eq(placement.flip, "up", "an anchor near the bottom edge flips the card to grow upward");
  ok(placement.top + 100 <= 232, "the upward card stays inside the container");
  ok(placement.top >= 8, "the upward card respects the top margin");
  eq(placement.top, 100, "the upward card keeps its bottom edge at the anchor");
}
// 卡比容器高 → 顶部对齐夹紧
{
  const placement = jumpPreviewPlacement(120, 300, 240);
  eq(placement.flip, "down", "an oversized card falls into the downward branch");
  eq(placement.top, 8, "an oversized card is clamped to the top margin");
}
// 自定义边距
{
  const placement = jumpPreviewPlacement(6, 100, 240, 4);
  eq(placement.top, 6, "a custom margin lets the card hug the edge by that margin");
}
// 退化输入
{
  eq(jumpPreviewPlacement(50, 0, 240), { top: 50, flip: "none" }, "a zero-height card falls back to the anchor");
  eq(jumpPreviewPlacement(50, 100, 0), { top: 50, flip: "none" }, "a zero-height container falls back to the anchor");
  eq(jumpPreviewPlacement(Number.NaN, 100, 240), { top: 8, flip: "down" }, "a NaN anchor degrades to anchor 0 and parks the card at the top margin");
  eq(jumpPreviewPlacement(-5, 100, 240).flip, "down", "a negative anchor flips downward like a top-edge anchor");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
