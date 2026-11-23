// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1769-hover-full-name.test.ts
//
// tl1769（P3，20261123 用户截图报）对话 hover 缺完整对话名——验收逐条钉：
//   ① hover 提示含完整对话名（任意长度不截断可换行）：
//     - classic hover 卡：title 取完整 label（原先 preview 顶替 title，长名会话
//       hover 后仍看不到全名），预览降级为独立 preview 行（与名同文不重复）；
//     - compact 原生 tooltip：titleWithName = label 独占首行，预览/状态等事实
//       聚到次行（titleDetails 不含 label），职责行仍居末；
//     - CSS 钉：hover-card-title 无 ellipsis/line-clamp、anywhere 断词可换行。
//   ② 运行中状态下名称完整可读：三态（空闲/运行/异常）下 title 恒等于完整名，
//     状态标签是独立字段/独立行，永不拼进名称行（运行符号只存在于侧栏行的
//     CSS 点位 project-tree__topic-state，tooltip 内无任何运行符号）。
//   ③ 三态样式不破：idle/streaming/error 三态 statusLabel 各归其位（空/流式/
//     错误键），preview/purpose 行为不随状态漂移。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { projectTreeTopicHoverCardModel } from "../lib/projectTreeTopic";
import type { ProjectNode } from "../lib/types";

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
function ok(value: boolean, label: string) {
  eq(Boolean(value), true, label);
}

const testT = (key: string, vars?: Record<string, string | number>) => {
  if (key === "history.turnOne") return `${vars?.n ?? 1} turn`;
  if (key === "history.turnOther") return `${vars?.n ?? 0} turns`;
  return key;
};

const here = dirname(fileURLToPath(import.meta.url));
const LONG_NAME = "fork开发-新15/需求与计划2——这是一个特别长的会话名用来验证任意长度不截断";

const baseNode = (over: Partial<ProjectNode>): ProjectNode =>
  ({ key: "topic_x", kind: "topic", label: LONG_NAME, preview: "最新的消息预览片段", root: "/repo", topicId: "x", ...over }) as ProjectNode;

console.log("\ntl1769 hover full name — classic hover card model");

// ── 验收①：完整对话名为首行，任意长度不截断 ──
{
  const card = projectTreeTopicHoverCardModel(baseNode({}), testT, "my-project");
  eq(card.title, LONG_NAME, "idle: the card title is the FULL conversation name (preview no longer impersonates it)");
  eq(card.title.length, LONG_NAME.length, "idle: the full name is carried verbatim (no truncation at the model layer)");
  eq(card.preview, "最新的消息预览片段", "idle: the last-message preview survives as its own supplementary line");
  eq(card.statusLabel, "", "idle: no status line when idle");
  eq(card.purpose, "", "idle: no duty line when none registered");
}

// ── 验收②：运行中名称完整可读，状态独立成行不遮挡 ──
{
  const card = projectTreeTopicHoverCardModel(baseNode({ running: true, status: "streaming" }), testT, "my-project");
  eq(card.title, LONG_NAME, "running: the title is still the FULL conversation name");
  eq(card.statusLabel, "projectTree.status.streaming", "running: the streaming status is its own field (separate line in the card)");
  ok(!card.title.includes(card.statusLabel), "running: the status never leaks into the name line (spinner dot lives only on the sidebar row)");
  eq(card.preview, "最新的消息预览片段", "running: the preview line survives the running state");
}

// ── 验收③：异常态同纪律 ──
{
  const card = projectTreeTopicHoverCardModel(baseNode({ status: "error" }), testT, "my-project");
  eq(card.title, LONG_NAME, "error: the title is still the FULL conversation name");
  eq(card.statusLabel, "projectTree.status.error", "error: the error status is its own field");
  ok(!card.title.includes(card.statusLabel), "error: the status never leaks into the name line");
}

// ── 职责行共存 + 去重 + 兜底 ──
{
  const card = projectTreeTopicHoverCardModel(baseNode({}), testT, "my-project", "  维护工具引擎  ");
  eq(card.purpose, "维护工具引擎", "duty: trimmed purpose coexists with the full-name title and preview line");
  eq(
    projectTreeTopicHoverCardModel(baseNode({ preview: LONG_NAME }), testT, "my-project").preview,
    "",
    "dedupe: a preview identical to the name renders no duplicate line",
  );
  eq(
    projectTreeTopicHoverCardModel(baseNode({ label: "", topicId: "fallback" }), testT, "my-project").title,
    "最新的消息预览片段",
    "fallback: an unnamed session falls back to the preview before the id",
  );
  eq(
    projectTreeTopicHoverCardModel(baseNode({ label: "● 圆点开头的名字" }), testT, "my-project").title,
    "圆点开头的名字",
    "strip: the leading activity dot is stripped from the name (row-label precedent)",
  );
}

// ── 验收①（续）：compact 原生 tooltip 首行=完整对话名（接线源码钉）──
console.log("\ntl1769 hover full name — native tooltip wiring");
{
  const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");
  ok(treeSource.includes('const titleDetails = [node.preview || "", recoveryLabel, imSourceTitle, statusLabel, metaFull,'), "wiring: the details line no longer embeds the name (label left the joined array)");
  ok(treeSource.includes("const titleWithName = [label, titleDetails].filter(Boolean).join(\"\\n\")"), "wiring: the full name leads the native tooltip as its own first line");
  ok(treeSource.includes("titleWithSessionModel(titleWithName, modelHover"), "wiring: the model hover line composes on top of the name-first title");
  ok(treeSource.includes("title={classicTopics ? undefined : titleWithPurpose}"), "wiring: non-classic rows still fold the composed tooltip into the native title");
  ok(treeSource.includes('aria-label={classicTopics ? titleWithPurpose : undefined}'), "wiring: the accessible label keeps the same name-first composition");
}

// ── 验收①（续）：classic 卡渲染顺序 title → status → purpose → preview → meta ──
console.log("\ntl1769 hover full name — classic card render order");
{
  const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");
  const idxTitle = treeSource.indexOf("project-tree__hover-card-title");
  const idxStatus = treeSource.indexOf("project-tree__hover-card-status");
  const idxPurpose = treeSource.indexOf("project-tree__hover-card-purpose");
  const idxPreview = treeSource.indexOf("project-tree__hover-card-preview");
  const idxMeta = treeSource.indexOf("project-tree__hover-card-meta");
  ok(idxTitle > 0, "render: the card has a title line");
  ok(idxPreview > 0, "render: the card renders the supplementary preview line (tl1769)");
  ok(idxTitle < idxStatus && idxStatus < idxPurpose && idxPurpose < idxPreview && idxPreview < idxMeta, "render: name first, then status, duty, preview, meta (expectation: full name leads, duty/status follow)");
}

// ── 验收①（续）：CSS 不截断可换行 ──
console.log("\ntl1769 hover full name — css wrap discipline");
{
  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  const titleBlock = css.slice(css.indexOf(".project-tree__hover-card-title"), css.indexOf(".project-tree__hover-card-status"));
  ok(titleBlock.includes("overflow-wrap: anywhere"), "css: the title line breaks long words (any length wraps)");
  ok(!titleBlock.includes("text-overflow"), "css: the title line never ellipsizes");
  ok(!titleBlock.includes("line-clamp"), "css: the title line never line-clamps");
  const previewBlock = css.slice(css.indexOf(".project-tree__hover-card-preview"), css.indexOf("/* 938：「编辑职责」"));
  ok(previewBlock.includes("overflow-wrap: anywhere"), "css: the preview line breaks long words too (same discipline as the duty line)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
