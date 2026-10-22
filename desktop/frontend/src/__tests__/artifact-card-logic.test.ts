// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/artifact-card-logic.test.ts
//
// 任务 944：产物卡片纯逻辑层单测——类型分流（扩展名推断/显式覆盖）、工具
// args/result 解析（含归档降级）、同 path 幂等归并（supersededArtifactIds）、
// 尺寸格式化。无 React / 无 bridge。
// 任务 1054：use_capability 能力代理路径（fork 主路径）——行判定（信封
// action=call + capability_id=tool:render_artifact / wire capabilityId 兜底）、
// 卡片数据（output 透传 + 信封内层 arguments 兜底）、代理+直接混排归并。

import {
  artifactCardData,
  artifactKindForPath,
  formatArtifactSize,
  isRenderArtifactToolItem,
  parseArtifactArgs,
  parseArtifactResult,
  supersededArtifactIds,
  RENDER_ARTIFACT_TOOL,
  USE_CAPABILITY_TOOL,
} from "../lib/artifactCard";
import type { Item } from "../lib/useController";

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

function toolItem(id: string, args: string, output: string, status: Item extends never ? never : "done" | "running" | "error" = "done", error?: string): Item {
  return {
    kind: "tool",
    id,
    name: RENDER_ARTIFACT_TOOL,
    args,
    readOnly: true,
    status,
    output: output || undefined,
    error,
  } as Item;
}

console.log("\nartifact card logic");

// ── 类型分流（扩展名推断）─────────────────────────────────────────────────
ok(artifactKindForPath("report.md") === "md", ".md → md");
ok(artifactKindForPath("REPORT.MD") === "md", "大写 .MD → md");
ok(artifactKindForPath("notes.markdown") === "md", ".markdown → md");
ok(artifactKindForPath("page.html") === "html", ".html → html");
ok(artifactKindForPath("page.HTM") === "html", ".htm → html");
ok(artifactKindForPath("paper.pdf") === "pdf", ".pdf → pdf");
ok(artifactKindForPath("chart.png") === "image", ".png → image");
ok(artifactKindForPath("photo.JPEG") === "image", ".jpeg → image");
ok(artifactKindForPath("dump.txt") === "text", ".txt → text");
ok(artifactKindForPath("data.csv") === "text", ".csv → text（未知扩展回退 text）");
ok(artifactKindForPath("noextension") === "text", "无扩展名 → text");
ok(artifactKindForPath("C:\\a\\b\\weekly.md") === "text" === false, "Windows 全路径正确取扩展名");

// ── args / result 解析 ───────────────────────────────────────────────────
const argsParsed = parseArtifactArgs(JSON.stringify({ path: "D:\\out\\r.md", title: "周报", kind: "auto", note: "本周" }));
ok(argsParsed?.path === "D:\\out\\r.md" && argsParsed?.title === "周报" && argsParsed?.note === "本周", "args 解析出 path/title/note");
ok(parseArtifactArgs("") === null, "空 args → null");
ok(parseArtifactArgs("not json") === null, "非法 JSON（归档截断）→ null");
ok(parseArtifactArgs(JSON.stringify({ title: "无路径" })) === null, "缺 path → null");

const resultParsed = parseArtifactResult(JSON.stringify({
  ok: true, path: "D:\\out\\r.md", title: "周报", kind: "md", note: "本周", size: 2048, preview: "# 标题\n内容", previewTruncated: false,
}));
ok(resultParsed?.kind === "md" && resultParsed?.size === 2048 && resultParsed?.preview === "# 标题\n内容", "result 解析出完整卡片数据（含预览/大小）");
ok(parseArtifactResult(JSON.stringify({ ok: false, path: "x" })) === null, "ok=false → null");
ok(parseArtifactResult("") === null, "空 output → null");
ok(parseArtifactResult("nope") === null, "非法 output → null");

// result 的 kind=auto 按 path 回退推断（历史数据兼容）。
const autoKind = parseArtifactResult(JSON.stringify({ ok: true, path: "/a/b.html", kind: "auto" }));
ok(autoKind?.kind === "html", "result kind=auto 按扩展名回退");

// ── artifactCardData（result 优先，args 兜底）────────────────────────────
const doneItem = toolItem("t1",
  JSON.stringify({ path: "D:\\out\\r.md", title: "周报" }),
  JSON.stringify({ ok: true, path: "D:\\out\\r.md", title: "周报", kind: "md", size: 100, preview: "x" }));
const card = artifactCardData(doneItem as Extract<Item, { kind: "tool" }>);
ok(card?.kind === "md" && card.size === 100 && card.preview === "x", "有 output：result 优先（带 size/preview）");

const archivedItem = toolItem("t2", JSON.stringify({ path: "D:\\out\\r.txt" }), "");
const archivedCard = artifactCardData(archivedItem as Extract<Item, { kind: "tool" }>, JSON.stringify({ ok: true, path: "D:\\out\\r.txt", kind: "text", size: 5, preview: "abc" }));
ok(archivedCard?.preview === "abc" && archivedCard.size === 5, "output 归档：ToolResultForTab 全量数据接管");

const argsOnlyItem = toolItem("t3", JSON.stringify({ path: "D:\\out\\r.md", title: "T" }), "");
const argsOnlyCard = artifactCardData(argsOnlyItem as Extract<Item, { kind: "tool" }>);
ok(argsOnlyCard?.path === "D:\\out\\r.md" && argsOnlyCard?.kind === "md" && argsOnlyCard?.size === undefined, "result 缺失：args 兜底（无 size/preview，卡片仍可渲染）");

// title 缺省取文件名。
const noTitle = artifactCardData(toolItem("t4", JSON.stringify({ path: "C:/a/weekly report.md" }), "") as Extract<Item, { kind: "tool" }>);
ok(noTitle?.title === "weekly report.md", "title 缺省 = 文件名");

// ── 代理路径（任务 1054：use_capability + capability_id=tool:render_artifact）──
const mdPayload = (path: string) => JSON.stringify({ ok: true, path, kind: "md" });
const argsPayload = (path: string) => JSON.stringify({ path });

function proxyItem(id: string, envelope: Record<string, unknown>, output: string, extra: Record<string, unknown> = {}): Item {
  return {
    kind: "tool",
    id,
    name: USE_CAPABILITY_TOOL,
    args: JSON.stringify(envelope),
    readOnly: true,
    status: "done",
    output: output || undefined,
    ...extra,
  } as Item;
}

// 行判定：信封解析（action=call + capability_id 精确匹配）。
ok(isRenderArtifactToolItem(proxyItem("p1",
  { action: "call", capability_id: "tool:render_artifact", arguments: { path: "D:\\out\\r.md" } }, "")), "代理行（action=call + capability_id=tool:render_artifact）判为 render_artifact");
ok(!isRenderArtifactToolItem(proxyItem("p2",
  { action: "inspect", capability_id: "tool:render_artifact" }, "")), "同 id 的 inspect 行不判为 render_artifact（目录发现不是渲染调用）");
ok(!isRenderArtifactToolItem(proxyItem("p3",
  { action: "call", capability_id: "tool:grep", arguments: {} }, "")), "其他 capability 的代理行不判为 render_artifact");
ok(!isRenderArtifactToolItem(proxyItem("p4",
  { action: "call", arguments: { path: "x" } }, "")), "无 capability_id 且无 wire id → 不判（保守）");
ok(isRenderArtifactToolItem(proxyItem("p5",
  { action: "call", capability_id: "tool:render_artifact ", arguments: { path: "x" } }, "")), "capability_id 带尾随空白（Go 侧 TrimSpace 同款容忍）仍命中");

// 行判定：wire capabilityId（host 解析，覆盖归档截断与 798 嵌套 id 提升）。
ok(isRenderArtifactToolItem({
  kind: "tool", id: "p6", name: USE_CAPABILITY_TOOL, args: "", readOnly: true, status: "done",
  capabilityId: "tool:render_artifact", dataArchived: true,
} as Item), "args 归档截断 + wire capabilityId 命中 → 判定成立");
ok(!isRenderArtifactToolItem({
  kind: "tool", id: "p7", name: USE_CAPABILITY_TOOL, args: "", readOnly: true, status: "done",
  capabilityId: "tool:grep", dataArchived: true,
} as Item), "wire id 指其他能力 → 不判");
ok(!isRenderArtifactToolItem({
  kind: "tool", id: "p8", name: USE_CAPABILITY_TOOL, args: "{截断", readOnly: true, status: "done",
  capabilityId: "tool:render_artifact",
} as Item), "wire id 命中但未归档且信封不可解析 → 不判（保守）");
ok(isRenderArtifactToolItem(proxyItem("p9",
  { action: "call", arguments: { capability_id: "tool:render_artifact", path: "D:\\out\\r.md" } }, "",
  { capabilityId: "tool:render_artifact" })), "798 嵌套 id（信封无顶层 id，wire 解析兜底）→ 判定成立");

// 卡片数据：output 是内层工具输出原样透传 → result 解析两路径共用。
const proxyResultCard = artifactCardData(proxyItem("d1",
  { action: "call", capability_id: "tool:render_artifact", arguments: { path: "D:\\out\\r.md", title: "周报" } },
  JSON.stringify({ ok: true, path: "D:\\out\\r.md", kind: "md", size: 2048, preview: "# x" })));
ok(proxyResultCard?.kind === "md" && proxyResultCard.size === 2048 && proxyResultCard.preview === "# x", "代理行 output 透传 → result 解析命中（含 size/preview）");

// 卡片数据：output 缺失 → 信封内层 arguments 兜底（对象形态）。
const proxyArgsCard = artifactCardData(proxyItem("d2",
  { action: "call", capability_id: "tool:render_artifact", arguments: { path: "D:\\out\\r.md", title: "周报" } }, ""));
ok(proxyArgsCard?.path === "D:\\out\\r.md" && proxyArgsCard?.title === "周报" && proxyArgsCard?.kind === "md" && proxyArgsCard?.size === undefined, "代理行 output 缺失 → 内层 arguments 兜底（卡片仍可渲染）");

// 卡片数据：内层 arguments 字符串化形态（Go task 212/457 同款容忍）。
const proxyStringifiedCard = artifactCardData(proxyItem("d3",
  { action: "call", capability_id: "tool:render_artifact", arguments: JSON.stringify({ path: "D:\\out\\r.md" }) }, ""));
ok(proxyStringifiedCard?.path === "D:\\out\\r.md", "内层 arguments 字符串化形态 → 兜底解析命中");

// 卡片数据：非 call 动作不产出兜底数据。
const proxyInspectCard = artifactCardData(proxyItem("d4",
  { action: "inspect", capability_id: "tool:render_artifact" }, JSON.stringify({ action: "inspect" })));
ok(proxyInspectCard === null, "inspect 行无卡片数据（回退占位符而非死卡）");

// ── 幂等归并：代理 + 直接混排（同 path 只留最新卡）────────────────────────
const merged = supersededArtifactIds([
  toolItem("a1", argsPayload("D:/out/r.md"), mdPayload("D:/out/r.md")),
  toolItem("a2", argsPayload("D:/out/other.md"), mdPayload("D:/out/other.md")),
  toolItem("a3", argsPayload("D:\\out\\r.md"), mdPayload("D:\\out\\r.md")),
] as Item[]);
ok(merged.has("a1") && !merged.has("a3"), "同 path（斜杠/大小写归一）旧调用被归并，最新保留");
ok(!merged.has("a2"), "不同 path 的卡不受归并影响");

const noMergeOnFailure = supersededArtifactIds([
  toolItem("b1", argsPayload("D:/out/r.md"), mdPayload("D:/out/r.md")),
  toolItem("b2", argsPayload("D:/out/r.md"), "", "error", "boom"),
] as Item[]);
ok(!noMergeOnFailure.has("b1"), "失败的后续调用不归并前卡（错误可见）");

const runningNoMerge = supersededArtifactIds([
  toolItem("c1", argsPayload("D:/out/r.md"), mdPayload("D:/out/r.md")),
  toolItem("c2", argsPayload("D:/out/r.md"), "", "running"),
] as Item[]);
ok(!runningNoMerge.has("c1"), "运行中的调用不归并（可能失败）");

// 任务 1054：代理行参与同一归并——代理+直接混排同 path，旧卡让位。
const mixedMerge = supersededArtifactIds([
  proxyItem("m1", { action: "call", capability_id: "tool:render_artifact", arguments: argsPayload("D:/out/r.md") }, mdPayload("D:/out/r.md")),
  toolItem("m2", argsPayload("D:/out/r.md"), mdPayload("D:/out/r.md")),
] as Item[]);
ok(mixedMerge.has("m1") && !mixedMerge.has("m2"), "代理+直接混排同 path 归并（旧代理卡让位，最新保留）");

const proxyOnlyMerge = supersededArtifactIds([
  proxyItem("m3", { action: "call", capability_id: "tool:render_artifact", arguments: argsPayload("D:/out/r.md") }, mdPayload("D:/out/r.md")),
  proxyItem("m4", { action: "call", capability_id: "tool:render_artifact", arguments: argsPayload("D:\\OUT\\R.md") }, mdPayload("D:\\OUT\\R.md")),
] as Item[]);
ok(proxyOnlyMerge.has("m3") && !proxyOnlyMerge.has("m4"), "纯代理行同 path（大小写/斜杠归一）幂等归并仍生效");

// ── 尺寸格式化 ───────────────────────────────────────────────────────────
ok(formatArtifactSize(512) === "512 B", "512 B");
ok(formatArtifactSize(2048) === "2.0 KB", "2048 → 2.0 KB");
ok(formatArtifactSize(5 * 1024 * 1024) === "5.0 MB", "5MB → 5.0 MB");
ok(formatArtifactSize(-1) === "", "非法值 → 空串");

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
