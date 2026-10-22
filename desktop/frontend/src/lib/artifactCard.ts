// 任务 944：render_artifact 产物卡片的纯逻辑层（无 React / 无 bridge 依赖，
// 可独立单测）。数据流：工具 Execute 返回 renderArtifactResult JSON（task 944
// Go 侧），前端从 tool item 的 args/output 解析出卡片数据；output 在工具行
// 归档后被清空（compactArchivedToolItems），全量数据经 ToolResultForTab 按需
// 拉回（与 ToolCard 的 useArchivedToolData 同一通道）。
// 任务 1054：调用面双路径——直接工具行（name=render_artifact，兼容保留）与
// use_capability 能力代理行（name=use_capability + capability_id=
// tool:render_artifact，fork 主路径）。代理行 output 是内层工具输出原样透传，
// result 解析与直接路径共用；差异只在 args（信封 arguments 内层）与行判定。

import type { Item } from "./useController";

type ToolItem = Extract<Item, { kind: "tool" }>;

export const RENDER_ARTIFACT_TOOL = "render_artifact";

// 任务 1054：render_artifact 不在 provider-visible 直接工具面（只进 capability
// 目录），模型经 use_capability 能力代理调用——工具行 name=use_capability、
// capability_id="tool:render_artifact"（目录 id 规范 tool:<name>，见
// internal/agent/usecapability.go resolveCall）、目标工具自身参数在信封
// arguments 内层。判定族（isRenderArtifactToolItem / artifactCardData /
// supersededArtifactIds）必须同时认两条路径，否则卡片对代理主路径恒不出现。
export const USE_CAPABILITY_TOOL = "use_capability";

const RENDER_ARTIFACT_CAPABILITY_ID = `tool:${RENDER_ARTIFACT_TOOL}`;

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
 * the host result (authoritative; the proxy path passes the inner tool's
 * output through verbatim, so the same parse serves both paths); falls back
 * to the call args so a card can still render (open actions intact, size/
 * preview absent) while the archived payload is being fetched or when the
 * fetch failed. Proxy rows (use_capability) keep the target tool's own
 * parameters in the envelope's inner `arguments` (任务 1054). */
export function artifactCardData(item: ToolItem, archivedOutput?: string): ArtifactCardData | null {
  const fromResult = parseArtifactResult(item.output ?? "") ?? parseArtifactResult(archivedOutput ?? "");
  if (fromResult) return fromResult;
  const args = item.name === USE_CAPABILITY_TOOL
    ? parseArtifactArgs(renderArtifactProxyInnerArgs(item) ?? "")
    : parseArtifactArgs(item.args);
  if (!args) return null;
  return {
    path: args.path,
    title: args.title ?? basenameOf(args.path),
    kind: normalizeKind(args.kind, args.path),
    note: args.note,
  };
}

/** Decomposed use_capability call envelope (model-authored). `id`/`action`
 * undefined when the member is absent; `innerArgs` is the target tool's own
 * arguments re-serialized as JSON (the member may arrive as an object or as a
 * stringified JSON object — the Go resolver tolerates the same two shapes,
 * internal/agent/usecapability_mcp_arguments.go task 212/457). */
type UseCapabilityEnvelope = {
  action?: string;
  id?: string;
  innerArgs: string;
};

function parseUseCapabilityEnvelope(args: string): UseCapabilityEnvelope | null {
  if (!args) return null;
  let envelope: Record<string, unknown>;
  try {
    envelope = JSON.parse(args) as Record<string, unknown>;
  } catch {
    return null;
  }
  if (!envelope || typeof envelope !== "object" || Array.isArray(envelope)) return null;
  const inner = envelope.arguments;
  const innerArgs = typeof inner === "string" ? inner
    : inner && typeof inner === "object" && !Array.isArray(inner) ? JSON.stringify(inner)
    : "{}";
  return {
    action: typeof envelope.action === "string" ? envelope.action : undefined,
    id: typeof envelope.capability_id === "string" ? envelope.capability_id.trim() : undefined,
    innerArgs,
  };
}

export function isRenderArtifactToolItem(item: Item): item is ToolItem {
  if (item.kind !== "tool") return false;
  if (item.name === RENDER_ARTIFACT_TOOL) return true;
  return isRenderArtifactProxyItem(item);
}

/** Proxy path (任务 1054): a use_capability tool row whose call targets
 * render_artifact. The host-resolved wire `capabilityId` is authoritative —
 * it survives args archival and covers resolver repairs (task 798 nested-id
 * auto-promotion leaves the model-authored envelope without a top-level id).
 * The envelope parse still gates the action: list/search/inspect rows on this
 * capability carry the same id but never produce an artifact payload. */
function isRenderArtifactProxyItem(item: ToolItem): boolean {
  if (item.name !== USE_CAPABILITY_TOOL) return false;
  const wireId = typeof item.capabilityId === "string" ? item.capabilityId.trim() : "";
  const envelope = parseUseCapabilityEnvelope(item.args);
  const id = wireId || envelope?.id || "";
  if (id !== RENDER_ARTIFACT_CAPABILITY_ID) return false;
  if (envelope) return envelope.action === "call";
  // Envelope lost to archive/truncation (args sliced past JSON validity):
  // trust the host-resolved wire id. An archived discovery row on this
  // capability is the only misjudge window, and it degrades to the honest
  // "data lost" placeholder, never a dead open-action card.
  return item.dataArchived === true || item.truncated === true;
}

/** Inner (target tool) arguments of a render_artifact proxy call, for the
 * card-data args fallback. Null when the row is not a confirmed render_artifact
 * proxy call or its envelope was archived past JSON validity. */
function renderArtifactProxyInnerArgs(item: ToolItem): string | null {
  if (item.name !== USE_CAPABILITY_TOOL) return null;
  const envelope = parseUseCapabilityEnvelope(item.args);
  if (!envelope || envelope.action !== "call") return null;
  const wireId = typeof item.capabilityId === "string" ? item.capabilityId.trim() : "";
  const id = wireId || envelope.id || "";
  return id === RENDER_ARTIFACT_CAPABILITY_ID ? envelope.innerArgs : null;
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
