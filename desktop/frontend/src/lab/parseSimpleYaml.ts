// 任务 724 — 实验室布局 yaml 的最小解析器。
//
// 为什么不引入 js-yaml：bundle 棘轮（JS 基线 553KB）+ 零新依赖纪律。实验室
// 布局文件只用到 yaml 的一个小正则子集——块映射、块列表、流式序列 `[a, b]`、
// 标量（词 / 引号串 / true|false|整数）——本文件手写这个子集，~150 行，任何
// 超出子集的语法直接报错（报错走 724 安全网：回退内置默认布局，不白屏）。
//
// 支持的语法（全部）：
//   key: value            —— 标量或流式序列
//   key:                  —— 嵌套块（下一行缩进更深）或 null
//   - item / - key: v     —— 列表（项为标量或映射，映射续行与首键对齐）
//   # comment             —— 整行注释与行尾 ` #` 注释（引号内 # 不剥）
// 明确不支持：多行字符串、锚点/引用、标签、多文档。用到即解析失败。

/** Parsed yaml values: scalars, arrays and plain objects only. */
export type SimpleYamlValue = string | number | boolean | null | SimpleYamlValue[] | { [key: string]: SimpleYamlValue };

export class YamlParseError extends Error {}

interface YamlLine {
  /** Leading-space count of the content (after comment strip). */
  indent: number;
  /** Comment-stripped, right-trimmed, left-trimmed text. */
  text: string;
  /** 1-based source line number, for error messages. */
  no: number;
}

function stripComment(raw: string): string {
  let inSingle = false;
  let inDouble = false;
  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i];
    if (ch === "'" && !inDouble) inSingle = !inSingle;
    else if (ch === '"' && !inSingle) inDouble = !inDouble;
    else if (ch === "#" && !inSingle && !inDouble && (i === 0 || /\s/.test(raw[i - 1] ?? ""))) {
      return raw.slice(0, i);
    }
  }
  return raw;
}

function parseScalar(token: string): SimpleYamlValue {
  const t = token.trim();
  if (t === "" || t === "null" || t === "~") return null;
  if (t.length >= 2 && ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'")))) {
    return t.slice(1, -1);
  }
  if (t === "true") return true;
  if (t === "false") return false;
  if (/^-?\d+$/.test(t)) return Number(t);
  if (t.startsWith("[")) {
    if (!t.endsWith("]")) throw new YamlParseError(`unclosed flow sequence: ${t}`);
    const inner = t.slice(1, -1).trim();
    if (inner === "") return [];
    return inner.split(",").map((part) => parseScalar(part));
  }
  if (t.includes(": ")) throw new YamlParseError(`flow mappings are not supported: ${t}`);
  return t;
}

const KEY_LINE = /^([^:]+):(?:\s+(.*))?$/;

/** Parse the yaml subset into JS values. Throws YamlParseError on anything
 * outside the grammar — callers treat every throw as "fall back to default". */
export function parseSimpleYaml(src: string): SimpleYamlValue {
  if (/[\t\v\f]/.test(src)) throw new YamlParseError("tab characters are not allowed (indent with spaces)");
  const lines: YamlLine[] = [];
  src.split(/\r?\n/).forEach((raw, idx) => {
    const noComment = stripComment(raw).replace(/\s+$/, "");
    if (noComment.trim() === "") return;
    const indent = noComment.length - noComment.trimStart().length;
    lines.push({ indent, text: noComment.trim(), no: idx + 1 });
  });
  let pos = 0;

  function parseBlock(childOfIndent: number): SimpleYamlValue {
    if (pos >= lines.length) return null;
    const line = lines[pos];
    if (line.indent <= childOfIndent) return null;
    if (line.text === "-" || line.text.startsWith("- ")) return parseList(line.indent);
    const out: { [key: string]: SimpleYamlValue } = {};
    parseMapEntries(out, line.indent);
    return out;
  }

  function parseMapEntries(out: { [key: string]: SimpleYamlValue }, indent: number): void {
    while (pos < lines.length) {
      const line = lines[pos];
      if (line.indent < indent) return;
      if (line.indent > indent) throw new YamlParseError(`line ${line.no}: unexpected deeper indent`);
      if (line.text === "-" || line.text.startsWith("- ")) {
        throw new YamlParseError(`line ${line.no}: list item where a "key: value" was expected`);
      }
      const m = KEY_LINE.exec(line.text);
      if (!m) throw new YamlParseError(`line ${line.no}: expected "key: value"`);
      const key = m[1].trim();
      if (key in out) throw new YamlParseError(`line ${line.no}: duplicate key "${key}"`);
      pos += 1;
      const rest = (m[2] ?? "").trim();
      if (rest === "") {
        out[key] = pos < lines.length && lines[pos].indent > indent ? parseBlock(indent) : null;
      } else {
        out[key] = parseScalar(rest);
      }
    }
  }

  function parseList(indent: number): SimpleYamlValue[] {
    const out: SimpleYamlValue[] = [];
    while (pos < lines.length && lines[pos].indent === indent && (lines[pos].text === "-" || lines[pos].text.startsWith("- "))) {
      const line = lines[pos];
      if (line.text === "-") {
        pos += 1;
        out.push(pos < lines.length && lines[pos].indent > indent ? parseBlock(indent) : null);
        continue;
      }
      const itemText = line.text.slice(1).trimStart();
      const contentCol = line.indent + (line.text.length - itemText.length);
      const mapStart = KEY_LINE.exec(itemText);
      if (!mapStart || itemText.startsWith("[")) {
        pos += 1;
        out.push(parseScalar(itemText));
        continue;
      }
      // "- key: value" — the item is a map whose first pair is inline; the
      // sibling keys continue at the content column.
      pos += 1;
      const item: { [key: string]: SimpleYamlValue } = {};
      const key = mapStart[1].trim();
      const rest = (mapStart[2] ?? "").trim();
      if (rest === "") {
        item[key] = pos < lines.length && lines[pos].indent > contentCol ? parseBlock(contentCol) : null;
      } else {
        item[key] = parseScalar(rest);
      }
      while (pos < lines.length && lines[pos].indent === contentCol && lines[pos].text !== "-" && !lines[pos].text.startsWith("- ")) {
        parseMapEntries(item, contentCol);
      }
      out.push(item);
    }
    return out;
  }

  const root: { [key: string]: SimpleYamlValue } = {};
  if (lines.length > 0) parseMapEntries(root, lines[0].indent);
  if (pos < lines.length) throw new YamlParseError(`line ${lines[pos].no}: unconsumed content (indentation mismatch)`);
  return root;
}
