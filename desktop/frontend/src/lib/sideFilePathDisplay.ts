// tl1468: display-layer path shaping for the side-files dock lists (产物/参考).
//
// The lists store the CANONICAL path (tool-arg / persisted-subject shape —
// relative workspace shapes stay relative, absolute shapes stay absolute);
// every operation (copy / open / reveal / reference inject) keeps consuming
// that canonical path. Only what the row renders goes through here.
//
// Rules, by entry source:
// 1. workspace-relative shape ("out/report.txt")  -> shown as-is (it already
//    reads as workspace-relative);
// 2. absolute inside the workspace root            -> relative to the root
//    ("C:\ws\out\a.txt" with root "C:\ws" -> "out\a.txt");
// 3. absolute inside the session directory         -> relative to that
//    directory (generated files that live next to the session .jsonl);
// 4. absolute elsewhere                            -> tail segments within a
//    char budget, "…"-prefixed when segments were dropped — the FILENAME is
//    always the last thing shown (prefix collapse beats tail truncation; the
//    20261106 screenshot complaint was precisely a prefix-only "C:\Users\…");
//    a short absolute path fits the budget and is shown in full.
//
// Containment is judged AFTER Windows folding — separators normalized to one
// kind and ASCII case folded (the tl1414 lesson: case-sensitive compares
// silently miss Windows-equivalent paths).

/** tl1437's path-shape recognition, moved here so the display layer and the
 *  copy-absolute resolve share one definition (drive letter / UNC / leading
 *  slash). */
export function looksAbsolutePath(path: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(path) || path.startsWith("/") || path.startsWith("\\\\");
}

/** Directory of a file path as plain string surgery (no filesystem calls):
 *  everything before the last separator; "" when there is none. Used to turn
 *  the session's .jsonl path into the session directory base. */
export function parentDirectory(path: string): string {
  const value = path.trim();
  if (!value) return "";
  const cut = Math.max(value.lastIndexOf("/"), value.lastIndexOf("\\"));
  return cut > 0 ? value.slice(0, cut) : "";
}

/** Split into path segments, dropping empty pieces from repeated/leading/trailing
 *  separators, and fold for Windows-insensitive comparison (tl1414). */
function foldedSegments(p: string): string[] {
  return p
    .replace(/[\\/]+/g, "\\")
    .toLowerCase()
    .split("\\")
    .filter((segment) => segment.length > 0);
}

/** The separator the original path spells out (first one wins) — the display
 *  keeps the entry's native style instead of re-slash-ing everything. */
function nativeSeparator(path: string): string {
  const slash = path.indexOf("/");
  const back = path.indexOf("\\");
  if (slash === -1) return "\\";
  if (back === -1) return "/";
  return back < slash ? "\\" : "/";
}

/** Relative-to-base display when `path` sits under `base` (both absolute
 *  shapes), else "". Segment-wise so a boundary never half-matches
 *  ("C:\ws2" is NOT under "C:\ws"). */
function relativeUnderBase(path: string, base: string): string {
  const baseTrim = base.trim();
  if (!baseTrim || !looksAbsolutePath(baseTrim) || !looksAbsolutePath(path)) return "";
  const pathSegments = foldedSegments(path);
  const baseSegments = foldedSegments(baseTrim);
  if (baseSegments.length === 0 || pathSegments.length <= baseSegments.length) return "";
  for (let i = 0; i < baseSegments.length; i++) {
    if (pathSegments[i] !== baseSegments[i]) return "";
  }
  // Slice the ORIGINAL string so case and separators survive untouched: find
  // the same segment boundary on the raw text.
  const rawSegments = path.trim().split(/[\\/]+/).filter((segment) => segment.length > 0);
  const rawSegmentsBase = baseTrim.split(/[\\/]+/).filter((segment) => segment.length > 0);
  const consumed = rawSegments.slice(0, rawSegmentsBase.length).reduce((sum, segment) => sum + segment.length, 0)
    + rawSegmentsBase.length - 1; // separators between consumed segments
  return path.trim().slice(consumed).replace(/^[\\/]+/, "");
}

/** Tail-segment display for absolute paths outside every known base: walk
 *  segments from the filename backwards while they fit the budget; prefix
 *  "…" when anything was dropped. The filename is never dropped. */
const TAIL_BUDGET = 44;

function tailDisplay(path: string): string {
  const trimmed = path.trim();
  if (trimmed.length <= TAIL_BUDGET) return trimmed;
  const separator = nativeSeparator(trimmed);
  const rawSegments = trimmed.split(/[\\/]+/).filter((segment) => segment.length > 0);
  let shown = rawSegments[rawSegments.length - 1];
  for (let i = rawSegments.length - 2; i >= 0; i--) {
    const candidate = rawSegments[i].length + separator.length + shown.length;
    if (candidate > TAIL_BUDGET) break;
    shown = rawSegments[i] + separator + shown;
  }
  const dropped = rawSegments.length - shown.split(separator).length;
  return dropped > 0 ? `…${separator}${shown}` : shown;
}

/**
 * The string a side-files row renders for `path`. See the module header for
 * the per-source rules; workspace beats session directory when both contain
 * the entry (the workspace context is the more informative frame).
 */
export function sideFileDisplayPath(path: string, workspaceRoot: string, sessionPath: string): string {
  const value = path.trim();
  if (!value) return "";
  if (!looksAbsolutePath(value)) return value; // workspace-relative shape: already short
  const inWorkspace = relativeUnderBase(value, workspaceRoot);
  if (inWorkspace) return inWorkspace;
  const sessionDir = parentDirectory(sessionPath);
  const inSessionDir = relativeUnderBase(value, sessionDir);
  if (inSessionDir) return inSessionDir;
  return tailDisplay(value);
}
