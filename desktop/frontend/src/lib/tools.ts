// Per-tool presentation helpers. The kernel forwards every tool call the same way
// (name + raw-JSON args + output); these turn that generic payload into the
// recognizable one-liner, inline diff, and collapsed outcome each tool deserves —
// the recognizable "card" vocabulary the desktop uses. Kept pure (no React, no
// highlight.js) so ToolCard stays a renderer and the main bundle stays light.

import { diffLines } from "./diff";
import { t } from "./i18n";
import { extToLang } from "./lang";
import type { DictKey } from "../locales/en";

export interface ToolDiff {
  original: string;
  modified: string;
  lang: string;
  label?: string; // multi_edit labels each step ("edit 1", …)
}

export interface ToolFileDiff {
  diff: string;
  added: number;
  removed: number;
}

function parse(args: string): Record<string, unknown> {
  try {
    const value: unknown = JSON.parse(args);
    return value !== null && typeof value === "object" && !Array.isArray(value)
      ? value as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

function str(a: Record<string, unknown>, key: string): string {
  return typeof a[key] === "string" ? (a[key] as string) : "";
}

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

export function fileDiffFromWire(value?: { diff?: unknown; added?: unknown; removed?: unknown }): ToolFileDiff | undefined {
  const diff = typeof value?.diff === "string" ? value.diff : "";
  const added = num(value?.added);
  const removed = num(value?.removed);
  if (!diff && added === 0 && removed === 0) return undefined;
  return { diff, added, removed };
}

export function summarizeFileDiff(fileDiff?: ToolFileDiff): string {
  if (!fileDiff || (fileDiff.added === 0 && fileDiff.removed === 0)) return "";
  return `+${fileDiff.added} -${fileDiff.removed}`;
}

// subjectOf pulls the most informative one-liner out of a call's args — the
// command for bash, the pattern for search, the path for file tools, the
// description for a sub-task — so the collapsed row reads at a glance.
export function subjectOf(name: string, args: string): string {
  const a = parse(args);
  switch (name) {
    case "bash":
      return str(a, "command");
    case "grep":
    case "glob":
      return str(a, "pattern") || str(a, "path");
    case "web_fetch":
      return str(a, "url");
    case "web_search":
      return str(a, "query");
    case "task":
      return str(a, "description") || str(a, "prompt");
    case "run_skill":
      return str(a, "name");
    case "move_file": {
      const src = str(a, "source_path");
      const dst = str(a, "destination_path");
      return src && dst ? `${src} -> ${dst}` : src || dst;
    }
    case "remember":
      return str(a, "name") || str(a, "description");
    case "todo_write":
    case "exit_plan_mode":
      return ""; // these get dedicated cards, not a subject line
    case "use_capability":
      // Stable MCP proxy: show the capability id as the collapsed subject so
      // Planner/Delivery cards read as "MCP · mcp-tool:server/tool".
      return str(a, "capability_id") || str(a, "action");
    default:
      return str(a, "path") || str(a, "file_path");
  }
}

// tl1475: while a call's arguments are still streaming, the payload is half-
// written JSON — subjectOf's strict parse yields nothing and the pending card
// would show no subject at all. streamingToolSubject scans the excerpt's HEAD
// segment (everything before the backend's "\n…" head/tail separator, so
// content fragments in the tail can never masquerade as a subject key) for the
// tool's subject key and reads the string value tolerantly: a value whose
// closing quote is still in flight counts too, rendered as far as it got.
// Best-effort by design — anything ambiguous yields "" (missing subject), a
// wrong subject is never shown.
export function streamingToolSubject(name: string, preview: string): string {
  const head = preview.split("\n…", 1)[0] ?? preview;
  for (const key of streamingSubjectKeys(name)) {
    const value = partialStringField(head, key);
    if (value) return value;
  }
  return "";
}

// Mirrors subjectOf's key mapping for the streaming phase.
function streamingSubjectKeys(name: string): string[] {
  switch (name) {
    case "bash": return ["command"];
    case "grep":
    case "glob": return ["pattern", "path"];
    case "web_fetch": return ["url"];
    case "web_search": return ["query"];
    case "task": return ["description", "prompt"];
    case "run_skill": return ["name"];
    case "move_file": return ["source_path", "destination_path"];
    case "remember": return ["name", "description"];
    case "todo_write":
    case "exit_plan_mode": return []; // dedicated cards, no subject line
    case "use_capability": return ["capability_id", "action"];
    default: return ["path", "file_path"];
  }
}

// partialStringField reads `"key"` : "value…" out of possibly-truncated JSON.
// Every candidate match must be shaped like a real field (closing quote,
// optional whitespace, colon, optional whitespace, opening quote) before its
// value is read, so keys that merely appear inside an earlier value are
// rejected. Standard JSON escapes are decoded; a truncated \u sequence yields
// only the decoded prefix. Returns "" when no shaped field is present.
function partialStringField(head: string, key: string): string {
  const token = `"${key}"`;
  let from = 0;
  while (from <= head.length - token.length) {
    const at = head.indexOf(token, from);
    if (at < 0) return "";
    from = at + token.length;
    let cursor = from;
    while (cursor < head.length && (head[cursor] === " " || head[cursor] === "\t")) cursor += 1;
    if (head[cursor] !== ":") continue;
    cursor += 1;
    while (cursor < head.length && (head[cursor] === " " || head[cursor] === "\t")) cursor += 1;
    if (head[cursor] !== '"') continue;
    cursor += 1;
    let out = "";
    while (cursor < head.length) {
      const ch = head[cursor];
      if (ch === '"') return out;
      if (ch === "\\") {
        const esc = head[cursor + 1];
        if (esc === undefined) return out; // truncated escape: show what decoded
        switch (esc) {
          case '"': out += '"'; cursor += 2; break;
          case "\\": out += "\\"; cursor += 2; break;
          case "/": out += "/"; cursor += 2; break;
          case "n": out += "\n"; cursor += 2; break;
          case "t": out += "\t"; cursor += 2; break;
          case "r": out += "\r"; cursor += 2; break;
          case "b": out += "\b"; cursor += 2; break;
          case "f": out += "\f"; cursor += 2; break;
          case "u": {
            const hex = head.slice(cursor + 2, cursor + 6);
            if (hex.length < 4 || !/^[0-9a-fA-F]{4}$/.test(hex)) return out; // truncated \u
            out += String.fromCharCode(parseInt(hex, 16));
            cursor += 6;
            break;
          }
          default: return out; // unknown escape in flight: stop at the safe prefix
        }
        continue;
      }
      out += ch;
      cursor += 1;
    }
    return out; // unterminated value: still streaming — return the prefix
  }
  return "";
}

// diffsFor returns the before/after pairs a writer tool's card renders inline:
// edit_file is one pair, write_file is an all-add (empty original), multi_edit is
// one pair per step. Returns [] for non-writers, so the card folds args/output
// away instead.
export function diffsFor(name: string, args: string): ToolDiff[] {
  const a = parse(args);
  const lang = extToLang(str(a, "path") || str(a, "file_path"));
  if (name === "edit_file") {
    if (typeof a.old_string === "string" && typeof a.new_string === "string") {
      return [{ original: a.old_string, modified: a.new_string, lang }];
    }
  }
  if (name === "write_file" && typeof a.content === "string") {
    return [{ original: "", modified: a.content, lang }];
  }
  if (name === "multi_edit" && Array.isArray(a.edits)) {
    const out: ToolDiff[] = [];
    (a.edits as unknown[]).forEach((e, i) => {
      const step = e as Record<string, unknown>;
      if (typeof step?.old_string === "string" && typeof step?.new_string === "string") {
        out.push({ original: step.old_string, modified: step.new_string, lang, label: `edit ${i + 1}` });
      }
    });
    return out;
  }
  return [];
}

export function languageForToolArgs(args: string): string {
  const a = parse(args);
  return extToLang(str(a, "path") || str(a, "file_path"));
}

export type TodoStatus = "pending" | "in_progress" | "completed";

export interface Todo {
  content: string;
  status: TodoStatus | string;
  activeForm?: string;
  level?: number; // 0 = phase, 1 = sub-step of the phase above it
  // Task 152 tree: step_id of the parent task; empty/absent = root (or a flat
  // level 0/1 item, whose parent comes from level adjacency). step_id below
  // stays the only stable identity — parent_id merely references one.
  parent_id?: string;
  step_id?: string;
}

// parseTodos pulls the task list out of a todo_write call's args.
export function parseTodos(args: string): Todo[] {
  try {
    const a = JSON.parse(args) as { todos?: Todo[] };
    return Array.isArray(a.todos) ? a.todos : [];
  } catch {
    return [];
  }
}

function plusMinus(original: string, modified: string): { add: number; del: number } {
  let add = 0;
  let del = 0;
  for (const r of diffLines(original, modified)) {
    if (r.type === "add") add++;
    else if (r.type === "del") del++;
  }
  return { add, del };
}

// lineCount counts lines, ignoring a single trailing newline so "a\n" reads as 1.
function lineCount(s: string): number {
  if (!s) return 0;
  const t = s.endsWith("\n") ? s.slice(0, -1) : s;
  return t === "" ? 0 : t.split("\n").length;
}

function nonEmptyLines(s: string): number {
  return s.split("\n").filter((l) => l.trim() !== "").length;
}

// countOf renders a localized "N <noun>" using the singular/plural key pair (zh
// collapses both to one form). Lives here, not the dict, so the counted phrasing
// stays a translation concern.
function countOf(n: number, one: DictKey, other: DictKey): string {
  return t(n === 1 ? one : other, { n });
}

function hasReplaceAllEdit(edits: Record<string, unknown>[]): boolean {
  return edits.some((e) => e?.replace_all === true);
}

function multiEditAppliedSummary(output: string): string {
  const match = output.match(/:\s*(\d+)\s+edits applied \((\d+)\s+total replacements\)/);
  if (!match) return "";
  const edits = Number(match[1]);
  const replacements = Number(match[2]);
  if (!Number.isFinite(edits) || !Number.isFinite(replacements)) return "";
  return `${countOf(edits, "tool.editOne", "tool.editOther")} · ${countOf(replacements, "tool.replacementOne", "tool.replacementOther")}`;
}

// summarize derives the one-line outcome shown under a finished card (the "⎿"
// secondary line) — counts from the args for writers, from the output for
// readers. "" means there's nothing worth a summary line.
export function summarize(name: string, args: string, output?: string, error?: string): string {
  if (error) return "";
  const a = parse(args);
  switch (name) {
    case "write_file":
      return typeof a.content === "string" ? countOf(lineCount(a.content), "tool.lineOne", "tool.lineOther") : "";
    case "edit_file": {
      if (typeof a.old_string === "string" && typeof a.new_string === "string") {
        const { add, del } = plusMinus(a.old_string, a.new_string);
        return `+${add} -${del}`;
      }
      return "";
    }
    case "multi_edit": {
      const edits = Array.isArray(a.edits) ? (a.edits as Record<string, unknown>[]) : [];
      if (hasReplaceAllEdit(edits)) {
        return output ? multiEditAppliedSummary(output) : "";
      }
      let add = 0;
      let del = 0;
      for (const e of edits) {
        if (typeof e?.old_string === "string" && typeof e?.new_string === "string") {
          const pm = plusMinus(e.old_string, e.new_string);
          add += pm.add;
          del += pm.del;
        }
      }
      return `${countOf(edits.length, "tool.editOne", "tool.editOther")} · +${add} -${del}`;
    }
  }

  if (!output) return "";
  switch (name) {
    case "read_file": {
      if (output.startsWith("(empty file)")) return t("tool.emptyFile");
      const arrows = (output.match(/→/g) || []).length;
      return countOf(arrows || lineCount(output), "tool.lineOne", "tool.lineOther");
    }
    case "grep":
      return countOf(nonEmptyLines(output), "tool.matchOne", "tool.matchOther");
    case "glob":
      return countOf(nonEmptyLines(output), "tool.fileOne", "tool.fileOther");
    case "ls":
      return countOf(nonEmptyLines(output), "tool.entryOne", "tool.entryOther");
    case "web_fetch":
      return output.split("\n", 1)[0].slice(0, 80);
    case "bash":
      return output.trim() === "" ? t("tool.noOutput") : countOf(lineCount(output), "tool.lineOne", "tool.lineOther");
    default:
      return "";
  }
}
