// 任务 1060：转录选中文字 → 路径候选的形态识别（纯前端预筛）。
//
// 职责边界：这里只回答「这段文字长不长得像一个路径」（盘符/UNC/开头斜杠/
// 含路径分隔符），不回答「存不存在」——存在性与可开性由后端探针
// ResolveOpenablePathForTab 权威判定（与 opener 家族同款门禁）。
// 本模块宁可漏判不可误判：任何拿不准的形态都返回 null（菜单隐藏是无害默认），
// 与 localPathLinks 的识别精神一致但按「整段选区即候选」收紧。

export interface SelectionPathCandidate {
  /** 形态清洗后的路径候选，交给探针与打开动作使用。 */
  path: string;
}

// Windows 常见上限 260、长路径 32k；1024 已远超正常路径，纯护栏。
const MAX_PATH_CANDIDATE_LENGTH = 1024;

// 成对包裹符号：选区经常连引号/强调记号一起带上。只剥一对，剥完重验，
// 不递归——把内容剥伤比漏剥更糟。
const WRAPPING_PAIRS: ReadonlyArray<readonly [string, string]> = [
  ['"', '"'],
  ["'", "'"],
  ["`", "`"],
  ["「", "」"],
  ["《", "》"],
  ["（", "）"],
  ["\u201c", "\u201d"],
  ["\u2018", "\u2019"],
  ["**", "**"],
  ["*", "*"],
  ["__", "__"],
  ["_", "_"],
];

// 句尾噪声：路径后面接句读是选区常态（双击收尾、拖选带标点）。
// 逐个从尾部剥；`)` 仅在括号不配平时剥（"C:\Program Files (x86)" 的
// 收尾括号属于路径本身，与 localPathLinks 同一条平衡规则）。
const TRAILING_NOISE = "。，、；：！？…,.;:!?)]}》」\u2019\u201d`*_";

function countChar(text: string, ch: string): number {
  let n = 0;
  for (const c of text) if (c === ch) n += 1;
  return n;
}

function stripWrappingPair(text: string): string {
  for (const [open, close] of WRAPPING_PAIRS) {
    if (text.length >= open.length + close.length && text.startsWith(open) && text.endsWith(close)) {
      return text.slice(open.length, text.length - close.length);
    }
  }
  return text;
}

function stripTrailingNoise(text: string): string {
  let out = text;
  for (;;) {
    const last = out.charAt(out.length - 1);
    if (!last || !TRAILING_NOISE.includes(last)) break;
    if (last === ")") {
      // 收尾括号在括号配平时属于路径（unbalanced 才是句读）。
      if (countChar(out, ")") <= countChar(out, "(")) break;
    }
    out = out.slice(0, -1);
  }
  return out;
}

const DRIVE_RE = /^[A-Za-z]:[/\\]/;
const UNC_RE = /^\\\\[^\\]+\\/; // \\server\share 至少要凑齐服务器与共享名
const SLASH_UNC_RE = /^\/\/[^/]+\//; // 斜杠形式 UNC（//server/share），后端 FromSlash 归一
const POSIX_ABS_RE = /^\//;

function absoluteShape(text: string): boolean {
  if (DRIVE_RE.test(text) || UNC_RE.test(text) || SLASH_UNC_RE.test(text)) return true;
  // 开头斜杠按绝对候选放行（macOS/Linux 原生形）；Windows 侧由后端
  // 归一门禁裁决——无法落盘的形态探针自然拒绝，前端不越权判死。
  if (POSIX_ABS_RE.test(text)) return true;
  return false;
}

/**
 * 整段选区的路径形态识别。非路径文字返回 null；路径候选返回清洗后的文本。
 * 判定顺序：trim → 剥一对包裹符号 → 剥句尾噪声 → 形态分类
 * （绝对形，或含路径分隔符的相对候选）。
 */
export function selectionPathCandidate(raw: string): SelectionPathCandidate | null {
  let text = raw.trim();
  if (!text || text.length > MAX_PATH_CANDIDATE_LENGTH) return null;
  // 多行选区不是单一路径（路径不含换行）。
  if (/[\r\n]/.test(text)) return null;
  text = stripWrappingPair(text).trim();
  text = stripTrailingNoise(text).trim();
  if (!text || text.length > MAX_PATH_CANDIDATE_LENGTH) return null;
  // URL 带.scheme 分隔，明确不在本件范围（转录链接另有 file:// 链路面）。
  if (text.includes("://")) return null;
  // 家目录缩写在无 shell 语义的前端无法定位，不猜。
  if (text.startsWith("~")) return null;
  if (absoluteShape(text)) return { path: text };
  // 相对候选：至少含一个路径分隔符（含盘符外冒号的文本交给后端语法门禁）。
  if (text.includes("/") || text.includes("\\")) return { path: text };
  return null;
}
