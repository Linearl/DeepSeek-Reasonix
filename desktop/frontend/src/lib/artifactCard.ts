// 任务 944：render_artifact 产物卡片的纯逻辑层（无 React / 无 bridge 依赖，
// 可独立单测）。数据流：工具 Execute 返回 renderArtifactResult JSON（task 944
// Go 侧），前端从 tool item 的 args/output 解析出卡片数据；output 在工具行
// 归档后被清空（compactArchivedToolItems），全量数据经 ToolResultForTab 按需
// 拉回（与 ToolCard 的 useArchivedToolData 同一通道）。

import type { Item } from "./useController";

type ToolItem = Extract<Item, { kind: "tool" }>;

export const RENDER_ARTIFACT_TOOL = "render_artifact";

export type ArtifactKind = "md" | "text" | "html" | "pdf" | "image";

/** One parsed artifact card payload. `path` is the gate that every open
 * action re-validates on the native side — the frontend never trusts it. */
export type ArtifactCardData = {
  path: string;
  title: string;
  kind: ArtifactKind;
  note?: string;
  size?: number;
  preview?: string;
  previewTruncated?: boolean;
};

const IMAGE_EXTENSIONS = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico", ".avif"]);

/** Frontend mirror of the Go-side auto kind resolution (internal/tool/
 * builtin/render_artifact.go artifactKindForPath). Only used when the stored
 * payload carries kind="auto" (history written before resolution landed in
 * the result). */
export function artifactKindForPath(path: string): ArtifactKind {
  const dot = path.lastIndexOf(".");
  const ext = dot >= 0 ? path.slice(dot).toLowerCase() : "";
  if (ext === ".md" || ext === ".markdown" || ext === ".mdx") return "md";
  if (ext === ".html" || ext === ".htm") return "html";
  if (ext === ".pdf") return "pdf";
  if (IMAGE_EXTENSIONS.has(ext)) return "image";
  return "text";
}

function normalizeKind(value: unknown, path: string): ArtifactKind {
  if (value === "md" || value === "text" || value === "html" || value === "pdf" || value === "image") return value;
  return artifactKindForPath(path);
}

function str(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function num(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

/** Parse the tool CALL arguments (model-authored). Degrades to null when the
 * args were archived/truncated past JSON validity — the caller then depends on
 * the ToolResultForTab payload. */
export function parseArtifactArgs(args: string): { path: string; title?: string; kind?: string; note?: string } | null {
  if (!args) return null;
  try {
    const parsed = JSON.parse(args) as Record<string, unknown>;
    const path = str(parsed?.path);
    if (!path) return null;
    return { path, title: str(parsed?.title), kind: str(parsed?.kind), note: str(parsed?.note) };
  } catch {
    return null;
  }
}

/** Parse the tool RESULT payload (host-authored, gate-validated). This is the
 * authoritative source: resolved absolute path, resolved kind, size, preview. */
export function parseArtifactResult(output: string): ArtifactCardData | null {
  if (!output) return null;
  try {
    const parsed = JSON.parse(output) as Record<string, unknown>;
    if (parsed?.ok !== true) return null;
    const path = str(parsed.path);
    if (!path) return null;
    return {
      path,
      title: str(parsed.title) ?? basenameOf(path),
      kind: normalizeKind(parsed.kind, path),
      note: str(parsed.note),
      size: num(parsed.size),
      preview: str(parsed.preview),
      previewTruncated: parsed.previewTruncated === true,
    };
  } catch {
    return null;
  }
}

function basenameOf(path: string): string {
  const cut = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
  return cut >= 0 ? path.slice(cut + 1) : path;
}

/** Build the card payload for one settled render_artifact tool item. Prefers
 * the host result (authoritative); falls back to the call args so a card can
 * still render (open actions intact, size/preview absent) while the archived
 * payload is being fetched or when the fetch failed. */
export function artifactCardData(item: ToolItem, archivedOutput?: string): ArtifactCardData | null {
  const fromResult = parseArtifactResult(item.output ?? "") ?? parseArtifactResult(archivedOutput ?? "");
  if (fromResult) return fromResult;
  const args = parseArtifactArgs(item.args);
  if (!args) return null;
  return {
    path: args.path,
    title: args.title ?? basenameOf(args.path),
    kind: normalizeKind(args.kind, args.path),
    note: args.note,
  };
}

export function isRenderArtifactToolItem(item: Item): item is ToolItem {
  return item.kind === "tool" && item.name === RENDER_ARTIFACT_TOOL;
}

/** Idempotent merge (草图 §3.1「同一 path 重复调用=更新该卡，不刷屏」): among
 * settled render_artifact items, only the LAST call per normalized path keeps
 * a card; earlier same-path calls are superseded and render as nothing. Live
 * and running calls never supersede anything (a running call may still fail).
 * Returns the set of item ids whose card is superseded by a later call. */
export function supersededArtifactIds(items: readonly Item[]): ReadonlySet<string> {
  const latestByPath = new Map<string, string>();
  const superseded = new Set<string>();
  for (const item of items) {
    if (!isRenderArtifactToolItem(item)) continue;
    if (item.status !== "done" || item.error) continue;
    const data = artifactCardData(item);
    if (!data) continue;
    // Windows 路径大小写不敏感；分隔符归一后比较。
    const key = data.path.replace(/\\/g, "/").toLowerCase();
    const prior = latestByPath.get(key);
    if (prior) superseded.add(prior);
    latestByPath.set(key, item.id);
  }
  return superseded;
}

/** Bytes → compact human size for the card header ("12.3 KB"). */
export function formatArtifactSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
