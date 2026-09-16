// Session-side file grouping (task 114): artifacts vs references.
//
// Artifacts = files the session produced (created/written).
// References = files the session read. Distinct from "all modified files"
// (task 113 edit list) by design.

import type { ToolItem } from "./transcriptRows";

/** Structural subset of transcript items used for artifact/reference grouping. */
export type SessionSideItem = {
  kind: string;
  name?: string;
  args?: string;
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

function pushUnique(list: SessionSideFile[], path: string | undefined, via: string, seen: Set<string>) {
  if (!path || !path.trim()) return;
  const key = path.trim();
  if (seen.has(key)) return;
  seen.add(key);
  list.push({ path: key, via });
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
    const { path, source, dest } = parsePathArgs(item.args);
    if (READ_TOOLS.has(name)) {
      pushUnique(references, path ?? source, name, refSeen);
      continue;
    }
    if (WRITE_CREATE_TOOLS.has(name)) {
      pushUnique(artifacts, path, name, artSeen);
      continue;
    }
    if (WRITE_MODIFY_TOOLS.has(name)) {
      // Modified existing files are not "artifacts" (task 114 acceptance).
      // They still count as references when the session opened them first.
      pushUnique(references, path ?? source, name, refSeen);
      if (dest) pushUnique(artifacts, dest, name, artSeen);
      continue;
    }
    // fileDiff create kind also counts as an artifact.
    if (item.fileDiff?.diff && name !== "bash") {
      // Previewed whole-file create without a parseable path stays out.
    }
  }
  return { artifacts, references };
}

export function formatReferenceListForPrompt(files: SessionSideFile[], max = 20): string {
  const paths = files.slice(0, max).map((f) => f.path);
  if (paths.length === 0) return "";
  const more = files.length > paths.length ? `\n… +${files.length - paths.length} more` : "";
  return `Files already referenced in this session (do not re-read unless content may have changed):\n${paths.map((p) => `- ${p}`).join("\n")}${more}`;
}
