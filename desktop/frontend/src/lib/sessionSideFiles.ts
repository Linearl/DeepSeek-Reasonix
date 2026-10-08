// Session-side file grouping (task 114; revised by task 629): artifacts vs
// references.
//
// Artifacts = files the session wrote — created AND modified (task 629 user
// ruling 2026-10-08 supersedes the task-114 create-only mapping; the panel
// copy defines 产物 as "files the session wrote"). References = files the
// session read. Still distinct from the per-turn task-113 edit list by design.
//
// bash-written files ARE collected too (task 659, the C phase of task 629):
// bash produces no previewed diff, so the collector instead extracts EXPLICIT
// literal output targets from the command text — redirect operators and tee
// arguments (bashWriteTargets below). Variables, devices, fd dups, globs and
// heredoc bodies never become paths: a wrong artifact costs more than a miss.

import type { ToolItem } from "./transcriptRows";

/** Structural subset of transcript items used for artifact/reference grouping. */
export type SessionSideItem = {
  kind: string;
  name?: string;
  args?: string;
  /** Task 452: stable collapsed subject persisted by the host for every
   *  rehydrated tool card (desktop/app.go historyToolSubject). Hydrated items
   *  carry no args (argumentsArchived), so for file tools the subject IS the
   *  path — the only durable carrier after an app restart. */
  subject?: string;
  fileDiff?: ToolItem["fileDiff"];
};

export interface SessionSideFile {
  path: string;
  /** Last tool name that touched this path. */
  via: string;
}

export interface SessionSideFiles {
  artifacts: SessionSideFile[];
  references: SessionSideFile[];
}

const READ_TOOLS = new Set([
  "read_file",
  "view_image",
  "notebook_read",
]);

const WRITE_CREATE_TOOLS = new Set([
  "write_file",
]);

const WRITE_MODIFY_TOOLS = new Set([
  "edit_file",
  "multi_edit",
  "notebook_edit",
  "delete_range",
  "delete_symbol",
  "move_file",
]);

function parsePathArgs(args: string | undefined): { path?: string; source?: string; dest?: string } {
  if (!args) return {};
  try {
    const parsed = JSON.parse(args) as Record<string, unknown>;
    const path = typeof parsed.path === "string" ? parsed.path : undefined;
    const source = typeof parsed.source_path === "string" ? parsed.source_path : typeof parsed.file_path === "string" ? parsed.file_path : undefined;
    const dest = typeof parsed.destination_path === "string" ? parsed.destination_path : undefined;
    return { path, source, dest };
  } catch {
    return {};
  }
}

// Task 452: hydrated tool items carry no args (the host archives tool
// arguments for every persisted call except todo_write — desktop/app.go
// historyToolCall), so args-only parsing made the lists permanently empty in
// any rehydrated session. The host still persists a collapsed subject that,
// for every path-bearing tool, IS the path (historyToolSubject default branch;
// move_file encodes "source -> destination"). Args win when present; the
// subject only fills the gaps.
function subjectPaths(subject: string | undefined): { path?: string; source?: string; dest?: string } {
  const value = subject?.trim();
  if (!value) return {};
  const arrow = value.indexOf(" -> ");
  if (arrow > 0) {
    return { source: value.slice(0, arrow).trim(), dest: value.slice(arrow + 4).trim() };
  }
  return { path: value };
}

function pushUnique(list: SessionSideFile[], path: string | undefined, via: string, seen: Set<string>) {
  if (!path || !path.trim()) return;
  const key = path.trim();
  if (seen.has(key)) return;
  seen.add(key);
  list.push({ path: key, via });
}

// Task 629: unified diffs rendered by internal/diff always open with
// "--- a/<path>" then "+++ b/<path>" (go-udiff writes the labels verbatim).
// Anchoring on that first header pair keeps a diff BODY line that happens to
// start with "+++" from being mistaken for the header. Returns undefined for
// the "(diff omitted: …)" placeholders, which carry no header — the path is
// then unknown and we skip rather than guess.
function fileDiffPath(diff: string | undefined): string | undefined {
  if (!diff) return undefined;
  const lines = diff.split("\n");
  if (lines.length < 2 || !lines[0].startsWith("--- a/")) return undefined;
  const value = lines[1].startsWith("+++ b/") ? lines[1].slice("+++ b/".length).trim() : "";
  return value || undefined;
}

type ShellToken = { text: string; kind: "word" | "op" | "control"; quoted: boolean };

// Quote-aware shell word splitter: just enough structure to tell redirect
// operators and control characters from literal words — deliberately NOT a
// full shell parser. Anything ambiguous resolves to "not a write target".
function tokenizeShellWords(command: string): ShellToken[] {
  const toks: ShellToken[] = [];
  let cur = "";
  let quoted = false;
  const flush = () => {
    if (cur !== "") toks.push({ text: cur, kind: "word", quoted });
    cur = "";
    quoted = false;
  };
  const n = command.length;
  let i = 0;
  while (i < n) {
    const c = command[i];
    if (c === "\\" && i + 1 < n) {
      // backslash escape outside quotes
      cur += command[i + 1];
      i += 2;
      continue;
    }
    if (c === '"' || c === "'") {
      const close = command.indexOf(c, i + 1);
      const end = close === -1 ? n : close + 1;
      cur += command.slice(i, end);
      quoted = true;
      i = end;
      continue;
    }
    if (/\s/.test(c)) {
      flush();
      i += 1;
      continue;
    }
    if (c === "&" && command[i + 1] === ">") {
      // both-streams redirect &> / &>>
      flush();
      let j = i + 1;
      while (command[j] === ">") j += 1;
      toks.push({ text: command.slice(i, j), kind: "op", quoted: false });
      i = j;
      continue;
    }
    if (c === "&" || c === "|" || c === ";" || c === "(" || c === ")") {
      flush();
      if ((c === "&" || c === "|") && command[i + 1] === c) {
        toks.push({ text: c + c, kind: "control", quoted: false });
        i += 2;
      } else {
        toks.push({ text: c, kind: "control", quoted: false });
        i += 1;
      }
      continue;
    }
    if (c === "<" || c === ">") {
      flush();
      let text: string;
      if (c === "<" && command[i + 1] === "<") {
        text = command[i + 2] === "<" ? "<<<" : "<<";
      } else if (c === ">" && command[i + 1] === ">") {
        text = ">>";
      } else if (c === ">" && command[i + 1] === "|") {
        text = ">|";
      } else if (c === ">" && command[i + 1] === "&") {
        text = ">&";
      } else {
        text = c;
      }
      toks.push({ text, kind: "op", quoted: false });
      i += text.length;
      continue;
    }
    cur += c;
    i += 1;
  }
  flush();
  return toks;
}

// Literal-path check for a redirect/tee target: one layer of matching quotes
// stripped, everything runtime-shaped rejected. op is the operator the target
// belongs to ("tee" for tee arguments); only ">&" plus digits is an fd dup.
function validWriteTarget(raw: string, op: string): string | undefined {
  let value = raw.trim();
  if (
    value.length >= 2 &&
    ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))
  ) {
    value = value.slice(1, -1).trim();
  }
  if (!value) return undefined;
  if (value.startsWith("&")) return undefined; // fd dup: 2>&1
  if (op === ">&" && /^\d+$/.test(value)) return undefined; // >&2
  if (value.startsWith("(")) return undefined; // process substitution
  if (value.includes("$") || value.includes("`")) return undefined; // shell-expanded at run time
  if (value.startsWith("/dev/")) return undefined;
  if (value.toLowerCase() === "nul") return undefined;
  if (value.includes("*") || value.includes("?")) return undefined; // glob — not concrete
  if (value.includes("(") || value.includes(")")) return undefined; // ambiguous punctuation
  return value;
}

// Task 659 (bash write collection — C phase of task 629): bash produces no
// previewed diff, so its write targets are extracted from the command text
// itself, and only from EXPLICIT, literal output targets: the redirect
// operators (> >> >| &> &>> >& [N]> [N]>>) and tee file arguments. Everything
// the shell resolves at run time stays out — $vars, backticks, fd dups (&1,
// >&2), /dev and NUL devices, process substitution, globs — a wrong path in
// the artifacts list costs more than a missed one (the task-629 red line).
// Heredoc bodies are dropped wholesale: everything from the first unquoted
// "<<" operator to the end is cut, so a body line like "x > y" inside
// cat <<EOF cannot masquerade as a redirect. The cut is token-level rather
// than line-level because hydrated subjects flatten newlines (clipSingleLine
// on the host collapses them). Accepted misses by design: sed -i / cp / mv
// style writes and paths carried in variables.
export function bashWriteTargets(command: string | undefined): string[] {
  if (!command) return [];
  const toks = tokenizeShellWords(command);
  const out: string[] = [];
  const seen = new Set<string>();
  let teeMode = false;
  for (let k = 0; k < toks.length; k++) {
    const tok = toks[k];
    if (tok.kind === "control") {
      teeMode = false;
      continue;
    }
    if (tok.kind === "op") {
      if (tok.text === "<<") return out; // heredoc: drop everything after it
      const operand = toks[k + 1];
      if (operand && operand.kind === "word") {
        if (tok.text.includes(">")) {
          const path = validWriteTarget(operand.text, tok.text);
          if (path !== undefined && !seen.has(path)) {
            seen.add(path);
            out.push(path);
          }
        }
        k += 1; // input files, here-strings and fd dups are consumed too
      }
      continue;
    }
    // word: a pure-digit word right before an operator is an fd prefix (2>, 2>>)
    if (/^\d+$/.test(tok.text) && toks[k + 1]?.kind === "op") continue;
    if (teeMode) {
      if (!tok.quoted && tok.text.startsWith("-")) continue; // tee option (-a, --append)
      const path = validWriteTarget(tok.text, "tee");
      if (path !== undefined && !seen.has(path)) {
        seen.add(path);
        out.push(path);
      }
      continue;
    }
    if (tok.text === "tee" || tok.text.endsWith("/tee")) teeMode = true;
  }
  return out;
}

// Live bash items carry args {command}; hydrated items carry the clipped
// command text as subject (historyToolSubject). Args win when present — same
// priority as the path tools above.
function bashCommandText(args: string | undefined, subject: string | undefined): string | undefined {
  if (args) {
    try {
      const parsed = JSON.parse(args) as Record<string, unknown>;
      if (typeof parsed.command === "string" && parsed.command.trim()) return parsed.command;
    } catch {
      // hydrated or malformed args — fall through to the subject
    }
  }
  const value = subject?.trim();
  return value || undefined;
}

/**
 * Aggregate artifacts and references from transcript tool items.
 * Order is first-seen; later tools do not reshuffle earlier entries.
 */
export function collectSessionSideFiles(items: readonly SessionSideItem[]): SessionSideFiles {
  const artifacts: SessionSideFile[] = [];
  const references: SessionSideFile[] = [];
  const artSeen = new Set<string>();
  const refSeen = new Set<string>();
  for (const item of items) {
    if (item.kind !== "tool" || !item.name) continue;
    const name = item.name;
    const fromArgs = parsePathArgs(item.args);
    const fromSubject = subjectPaths(item.subject);
    // Task 452: args first (live sessions), subject fills the hydrated gaps.
    const path = fromArgs.path ?? fromSubject.path;
    const source = fromArgs.source ?? fromSubject.source;
    const dest = fromArgs.dest ?? fromSubject.dest;
    // Task 629: a previewed diff proves the call wrote a file whatever its
    // name — the host attaches FileDiff only to non-read-only tools that
    // implement Previewer (internal/agent/agent.go withPreviewFileDiffs), so
    // reads can never carry one. The path comes from the diff header; bash is
    // barred by name for defense in depth (it has no Previewer today).
    const diffFallback = name !== "bash" ? fileDiffPath(item.fileDiff?.diff) : undefined;
    if (READ_TOOLS.has(name)) {
      pushUnique(references, path ?? source, name, refSeen);
      continue;
    }
    if (WRITE_CREATE_TOOLS.has(name)) {
      pushUnique(artifacts, path ?? diffFallback, name, artSeen);
      continue;
    }
    if (WRITE_MODIFY_TOOLS.has(name)) {
      if (name === "move_file") {
        // A rename leaves the destination behind and the source gone: the
        // surviving path is the artifact, the old path stays a reference.
        pushUnique(artifacts, dest, name, artSeen);
        pushUnique(references, path ?? source, name, refSeen);
        continue;
      }
      // Task 629 (user ruling 2026-10-08): modified files ARE artifacts —
      // the panel defines 产物 as "files the session wrote", and an edit
      // writes. Supersedes the task-114 reference-only mapping; reads still
      // land in references unchanged.
      pushUnique(artifacts, path ?? source ?? diffFallback, name, artSeen);
      continue;
    }
    // Task 659: bash has no Previewer and never carries a fileDiff, so its
    // write targets come from explicit redirect/tee paths in the command text
    // (live args.command; the persisted command text as subject when
    // hydrated). The fileDiff fallback above stays barred for bash — a diff
    // on a bash item would be host-bug spillover, not proof of a write.
    if (name === "bash") {
      for (const target of bashWriteTargets(bashCommandText(item.args, item.subject))) {
        pushUnique(artifacts, target, name, artSeen);
      }
      continue;
    }
    // Whitelist-external writers (future Previewer tools): the diff header is
    // the only trustworthy path source — args of an unknown tool may point at
    // an output dir or an unrelated param, so they are not consulted here.
    if (diffFallback) pushUnique(artifacts, diffFallback, name, artSeen);
  }
  return { artifacts, references };
}
export function formatReferenceListForPrompt(files: SessionSideFile[], max = 20): string {
  const paths = files.slice(0, max).map((f) => f.path);
  if (paths.length === 0) return "";
  const more = files.length > paths.length ? `\n… +${files.length - paths.length} more` : "";
  return `Files already referenced in this session (do not re-read unless content may have changed):\n${paths.map((p) => `- ${p}`).join("\n")}${more}`;
}
