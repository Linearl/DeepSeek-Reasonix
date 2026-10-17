// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/artifact-card-logic.test.ts
//
// 任务 944：产物卡片纯逻辑层单测——类型分流（扩展名推断/显式覆盖）、工具
// args/result 解析（含归档降级）、同 path 幂等归并（supersededArtifactIds）、
// 尺寸格式化。无 React / 无 bridge。

import {
  artifactCardData,
  artifactKindForPath,
  formatArtifactSize,
  parseArtifactArgs,
  parseArtifactResult,
  supersededArtifactIds,
  RENDER_ARTIFACT_TOOL,
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

// ── 幂等归并（同 path 只留最新卡）────────────────────────────────────────
const mdPayload = (path: string) => JSON.stringify({ ok: true, path, kind: "md" });
const argsPayload = (path: string) => JSON.stringify({ path });

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

// ── 尺寸格式化 ───────────────────────────────────────────────────────────
ok(formatArtifactSize(512) === "512 B", "512 B");
ok(formatArtifactSize(2048) === "2.0 KB", "2048 → 2.0 KB");
ok(formatArtifactSize(5 * 1024 * 1024) === "5.0 MB", "5MB → 5.0 MB");
ok(formatArtifactSize(-1) === "", "非法值 → 空串");

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
