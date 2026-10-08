// Session-side file grouping (task 114; revised by task 629): artifacts vs
// references.
//
// Artifacts = files the session wrote — created AND modified (task 629 user
// ruling 2026-10-08 supersedes the task-114 create-only mapping; the panel
// copy defines 产物 as "files the session wrote"). References = files the
// session read. Still distinct from the per-turn task-113 edit list by design.
//
// bash-written files are NOT collected yet: bash produces no previewed diff
// and its command text is not parsed (false artifacts cost more than misses;
// task 629 deferred that to a dedicated follow-up).

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
