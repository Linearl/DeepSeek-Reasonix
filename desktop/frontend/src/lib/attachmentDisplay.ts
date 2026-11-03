import { escapeRefPath, refTokenRe, unescapeRefPath } from "./refToken";

const attachmentRefRe = /@(\.reasonix\/attachments\/[^\s]+)/g;
const namedAttachmentRefRe = /(^|\s)@\[([^\]\r\n]+)\]\(([^)\s]+)\)/g;
const trailingPunctuationRe = /[.,;!?)\]}，。；！？）】]+$/;

// tl1347 兜底层：controller.ResolveRefs 注入的引用 XML 块（<image path="…">
// …</image> 等，模型侧表示，见 internal/control/refs.go appendRefBlock）只应
// 给模型看。后端 StripReferencedContextPrefix 负责常规剥离（history 路径）；
// 此处兜住仍漏进显示文本的残块——任何 image path 文本都不得原样进用户视图。
// 块体可多行，非贪婪匹配到对应闭合标签。
const injectedRefXmlRe = /<(image|file|dir|resource|audio) path="[^"]*">[\s\S]*?<\/\1>\n?/g;

export interface DisplayAttachment {
  path: string;
  name: string;
  kind: "image" | "file" | "folder";
  source: "attachment" | "workspace";
  ext: string;
}

function splitTrailingPunctuation(token: string): { core: string; suffix: string } {
  const m = token.match(trailingPunctuationRe);
  if (!m || m.index === undefined) return { core: token, suffix: "" };
  return { core: token.slice(0, m.index), suffix: m[0] };
}

export function baseName(path: string): string {
  const clean = path.replace(/[\\/]+$/, "");
  const slash = clean.lastIndexOf("/");
  const backslash = clean.lastIndexOf("\\");
  const idx = Math.max(slash, backslash);
  return idx >= 0 ? clean.slice(idx + 1) : clean;
}

function isImageAttachmentRef(path: string): boolean {
  const ext = attachmentExt(path);
  switch (ext) {
    case ".png":
    case ".jpg":
    case ".jpeg":
    case ".gif":
    case ".webp":
    case ".bmp":
    case ".svg":
    case ".tif":
    case ".tiff":
      return true;
    default:
      return false;
  }
}

function attachmentExt(path: string): string {
  const name = baseName(path).toLowerCase();
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? name.slice(dot) : "";
}

function cleanDisplayName(name: string): string {
  return name.replace(/\\([\[\]])/g, "$1").replace(/\s+/g, " ").trim();
}

export function replaceAttachmentRefsForDisplay(text: string): string {
  return text
    .replace(namedAttachmentRefRe, (_full, lead: string, label: string, token: string) => {
      const { core, suffix } = splitTrailingPunctuation(token);
      if (!core || !isDisplayReference(core)) return _full;
      const name = cleanDisplayName(label) || baseName(core) || "attachment";
      return `${lead}${isImageAttachmentRef(core) ? "[image]" : `[file:${name}]`}${suffix}`;
    })
    .replace(attachmentRefRe, (_full, token: string) => {
      const { core, suffix } = splitTrailingPunctuation(token);
      if (!core) return _full;
      if (isImageAttachmentRef(core)) return `[image]${suffix}`;
      const name = baseName(core) || "attachment";
      return `[file:${name}]${suffix}`;
    });
}

export function restoreAttachmentRefsForSubmit(text: string): string {
  return text.replace(namedAttachmentRefRe, (_full, lead: string, _label: string, token: string) => {
    const { core, suffix } = splitTrailingPunctuation(token);
    if (!core || !isDisplayReference(core)) return _full;
    return `${lead}@${core}${suffix}`;
  });
}

function displayRefName(name: string): string {
  return name.replace(/[\[\]\(\)\r\n]+/g, " ").replace(/\s+/g, " ").trim() || "attachment";
}

export function formatAttachmentRefForDisplay(attachment: Pick<DisplayAttachment, "path" | "name" | "source">): string {
  if (attachment.source === "attachment") {
    return `@[${displayRefName(attachment.name || baseName(attachment.path) || "attachment")}](${attachment.path})`;
  }
  return `@${escapeRefPath(attachment.path)}`;
}

// Whitespace in the path is escaped so the ref survives @-token parsing on
// submit; attachment-store paths are generated without spaces, so this is a
// no-op for them.
export function formatAttachmentRefForSubmit(attachment: Pick<DisplayAttachment, "path">): string {
  return `@${escapeRefPath(attachment.path)}`;
}

export function parseAttachmentRefsForDisplay(text: string): { text: string; attachments: DisplayAttachment[] } {
  const attachments: DisplayAttachment[] = [];
  // tl1347 兜底层：先剥模型侧注入的引用 XML 块（见 injectedRefXmlRe 注释）。
  let cleaned = text.replace(injectedRefXmlRe, "");
  if (cleaned !== text) {
    // 注入块剥除后，其前导标签（Referenced context:）已不携带内容，一并剥
    // 掉——与后端 StripReferencedContextPrefix 的输出对齐；仅限确实剥了块
    // 的文本，用户手打的同形文本不受影响。残留的连续空行收拢成单个段落间
    // 隔，避免剥除后留白堆叠。
    cleaned = cleaned.replace(/^Referenced context:[^\n]*\n?/, "").replace(/\n{3,}/g, "\n\n");
  }
  cleaned = cleaned
    .replace(namedAttachmentRefRe, (_full, lead: string, label: string, token: string) => {
      const { core, suffix } = splitTrailingPunctuation(token);
      if (!core || !isDisplayReference(core)) return _full;
      const name = cleanDisplayName(label) || baseName(core) || "attachment";
      attachments.push(displayAttachment(core, name));
      return lead + suffix;
    })
    .replace(refTokenRe(), (_full, lead: string, token: string) => {
      const { core, suffix } = splitTrailingPunctuation(token);
      const path = unescapeRefPath(core);
      if (!path || !isDisplayReference(path)) return _full;
      const name = baseName(path) || "attachment";
      const attachment = displayAttachment(path, name);
      attachments.push(attachment);
      return lead + suffix;
    })
    .replace(/[ \t]+([.,;!?)\]}，。；！？）】])/g, "$1")
    .replace(/[ \t]{2,}/g, " ")
    .trim();
  return { text: cleaned, attachments };
}

export function sortDisplayAttachments<T extends { kind: "image" | "file" | "folder" }>(attachments: T[]): T[] {
  return [...attachments].sort((a, b) => {
    if (a.kind === b.kind) return 0;
    if (a.kind === "image") return -1;
    if (b.kind === "image") return 1;
    return 0;
  });
}

function isDisplayReference(path: string): boolean {
  if (path.startsWith(".reasonix/attachments/")) return true;
  if (path.endsWith("/")) return true;
  if (path.includes("/")) return true;
  return attachmentExt(path) !== "";
}

function displayAttachment(path: string, name: string): DisplayAttachment {
  if (path.startsWith(".reasonix/attachments/")) {
    const kind = isImageAttachmentRef(path) ? "image" : "file";
    return {
      path,
      name,
      kind,
      source: "attachment",
      ext: attachmentExt(path).replace(/^\./, "").toUpperCase(),
    };
  }
  const isDir = path.endsWith("/");
  return {
    path,
    name,
    kind: isDir ? "folder" : "file",
    source: "workspace",
    ext: isDir ? "" : attachmentExt(path).replace(/^\./, "").toUpperCase(),
  };
}
