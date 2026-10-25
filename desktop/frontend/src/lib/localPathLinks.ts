// Local-path linkification for chat markdown (issue #7426).
//
// AI replies frequently print local file paths as plain text (Windows drive
// paths, UNC paths, file:/// URLs). GFM autolink literals only recognize
// http(s)/www/email, so these render as inert text. This module rewrites
// matching text nodes into markdown links whose href is a file:/// URL; the
// link click handler (RichMarkdownLink) routes those to the native
// OpenLocalPath binding instead of the system browser.

import { visit } from "unist-util-visit";
import type { InlineCode, Parent, Link, Root, Text } from "mdast";
import { hasDisallowedWindowsPathSyntax, isLocalFileHref } from "./localFileUrl";
import { unescapeRefPath } from "./refToken";

// Sentence punctuation is excluded from path characters: Windows forbids `：`
// in file names, and the CJK/ASCII punctuation set below almost never appears
// inside a real path. Excluding it at match time is far more reliable than
// trimming a trailing marker after the fact ("D:\x\y.md。已生成" must stop at
// `。`). `.` is deliberately kept (extensions) and only stripped when it is
// actually trailing.
const SENT_PUNCT = "，。；、！？,;!?（）";

// One path character: either a backslash-escaped space/tab (kept, matching the
// @path grammar in lib/refToken), or any non-whitespace char that is not a
// quote, angle bracket, pipe, `?`, `*`, colon or sentence punctuation.
// The `\\[ \t]` alternative must come FIRST: in a `(?:A|B)+` loop the engine
// backtracks by dropping repetitions, not by retrying the alternative at the
// same position, so a trailing `\ ` would otherwise never be consumed.
const PATH_CHAR = String.raw`(?:\\[ \t]|[^\s<>"|?*:：${SENT_PUNCT}])`;

// file URLs are matched whole (their `:` and `.` are legal there). Drive and
// UNC prefix boundaries are checked in JavaScript rather than with regular
// expression lookbehind: macOS 12's WebKit rejects `(?<!...)` while loading the
// whole lazy Markdown chunk, before any message is rendered. URL candidates are
// validated with the shared parser below so malformed file URLs stay inert.
const FILE_RE = new RegExp(String.raw`file://[^\s<>"|?*${SENT_PUNCT}]+`, "g");
const DRIVE_RE = new RegExp(String.raw`[A-Za-z]:[\\/]${PATH_CHAR}+`, "g");
// Space-separated continuations of a drive/UNC path (task 1138): the initial
// regex stops at an unescaped space ("C:\Program" in `C:\Program Files\app.exe`),
// but the acceptance bar requires space-bearing paths to linkify. After the
// initial match, absorb further space-separated segments that themselves look
// path-like — a segment counts only when it contains a separator (`\` `/`) or
// a file extension (`report.md`). Bare words ("see", "已生成") never continue
// a path, so prose after a path keeps the match short (precision first).
const SPACE_SEG_RE = new RegExp(String.raw`[ \t]+${PATH_CHAR}+`);
const SEG_HAS_SEPARATOR_RE = /[\\/]/;
const SEG_HAS_EXTENSION_RE = /\.[A-Za-z0-9]{1,12}$/;
// UNC share paths: the plugin runs on parsed markdown text nodes, where
// CommonMark backslash escaping has already folded `\\` into `\`. A share
// therefore arrives as `\nas\share\docs\report.md` (single leading
// backslash); linkify restores the `\\` prefix so the native opener receives
// the real UNC form. The leading `\` is mandatory (the fold always leaves
// one). The JavaScript prefix guard keeps `C:\nas` or a plain `a\b` from being
// mistaken for a share start without requiring WebKit lookbehind support.
const UNC_RE = new RegExp(
  String.raw`\\(?!\\)[^\s\\<>"|?*:：${SENT_PUNCT}]+\\${PATH_CHAR}+`,
  "g",
);

// Task 1158: workspace-relative paths ("docs/issues/design/x.md") linkify
// against the session's project root. A relative token must carry at least
// one forward slash AND end in a file extension — "and/or", "3/4" and dates
// ("2026/10/25") stay inert. Segment characters exclude the slash set, the
// scheme/drive breakers (`:` kills "https://" and "C:"), and shell/config
// noise that historically rides in front of real paths (`~`, `=`, `#`, `%`,
// quotes, backticks): `path=docs/x.md` then matches only `docs/x.md`.
const REL_SEG_CHAR = String.raw`[^\s<>"'|?:：${SENT_PUNCT}/\\~=#[%\`]`;
const REL_RE = new RegExp(String.raw`${REL_SEG_CHAR}+(?:/${REL_SEG_CHAR}+)+`, "g");
const REL_HAS_EXTENSION_RE = /\.[A-Za-z0-9]{1,12}$/;
// CJK prose routinely touches a path with no space ("见docs/x.md"), and the
// first segment cannot be told apart from a real CJK directory name. Rule:
// the FIRST segment must be CJK-free (precision first — such tokens stay
// plain text and the backend probe would reject them anyway), while CJK in
// LATER segments is accepted ("docs/设计稿/首页.png" is a real shape).
const REL_CJK_RE = /[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]/;
// A rel match is only a whole token when the preceding character cannot
// continue it. ASCII token characters (including both slashes) reject — that
// is what keeps the tail of "https://x/a/b.png" from linkifying; CJK and
// other non-ASCII neighbours are allowed because Chinese prose routinely
// touches a path with no space in between.
const REL_PREFIX_RE = /[A-Za-z0-9._/\\-]/;

const DRIVE_PREFIX_RE = /[:/A-Za-z]/;
const UNC_PREFIX_RE = /[\\/\w:；：，。、！？（）]/;
// A file URL must start a URI-like token. Without this guard, the matcher can
// start in the middle of `profile://...` or `http://file://...` and produce a
// clickable suffix that was never a local path.
const FILE_PREFIX_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./\\:+-?#&=";

function hasValidPrefixBoundary(text: string, start: number, kind: "file" | "drive" | "unc" | "rel"): boolean {
  if (start === 0) return true;
  const previous = text[start - 1];
  if (kind === "file") return !FILE_PREFIX_CHARS.includes(previous);
  if (kind === "rel") return !REL_PREFIX_RE.test(previous);
  return kind === "drive" ? !DRIVE_PREFIX_RE.test(previous) : !UNC_PREFIX_RE.test(previous);
}

// Trailing closers that are more likely sentence punctuation than file name
// characters. A trailing `)` is only stripped when parens are unbalanced —
// "C:\Program Files (x86)" keeps its closing paren, "D:\x\y.md)." loses it.
const TRAIL_STRIP_RE = /[.)\]]+$/;

function stripTrailingClosers(raw: string): string {
  const stripped = raw.replace(TRAIL_STRIP_RE, "");
  if (stripped === raw) return raw;
  // A trailing `)` may be a real file-name character inside balanced parens
  // ("C:\Program Files (x86)") — only strip it when parens are unbalanced,
  // i.e. the removed part contains a `)` with no matching opener.
  if (raw.slice(stripped.length).includes(")")) {
    const open = (raw.match(/\(/g) ?? []).length;
    const close = (raw.match(/\)/g) ?? []).length;
    if (close <= open) return raw;
  }
  return stripped;
}

// Extends a drive/UNC match across single spaces into spaced path segments
// ("C:\Program" + " Files\app.exe"). Each absorbed segment must independently
// carry path signal (separator or extension); the walk stops at the first
// bare word, so trailing prose never joins the path. file:/// URLs are not
// extended — a literal space in a file URL is already invalid form.
function spacedPathEnd(text: string, start: number): number {
  let end = start;
  for (;;) {
    const seg = SPACE_SEG_RE.exec(text.slice(end));
    if (seg === null) return end;
    const segment = seg[0].trim();
    const segEnd = end + seg[0].length;
    if (!SEG_HAS_SEPARATOR_RE.test(segment) && !SEG_HAS_EXTENSION_RE.test(segment)) return end;
    // An alternate-data-stream colon ("C:\a b.md:payload") ends the walk; the
    // caller's colon guard then rejects the whole candidate.
    if (text[segEnd] === ":" && !/\s/.test(text[segEnd + 1] ?? "")) return segEnd;
    end = segEnd;
  }
}

export type LocalPathKind = "file" | "drive" | "unc" | "rel";

export interface LocalPathSegment {
  /** Raw text to render (keeps the original spelling, escapes intact). */
  text: string;
  /** When present, this segment is a clickable local path. */
  path?: string;
  /** Which recognizer produced `path`; "rel" resolves against a project root. */
  kind?: LocalPathKind;
}

/**
 * Splits `text` into plain segments and clickable local-path segments.
 * Pure function — unit tests cover the full recognition matrix here.
 */
export function linkifyLocalPaths(text: string): LocalPathSegment[] {
  const matches: Array<{ start: number; end: number; raw: string; kind: LocalPathKind }> = [];
  const patterns: Array<[RegExp, LocalPathKind]> = [
    [FILE_RE, "file"],
    [DRIVE_RE, "drive"],
    [UNC_RE, "unc"],
    [REL_RE, "rel"],
  ];
  for (const [re, kind] of patterns) {
    re.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(text)) !== null) {
      const match = m;
      let matchEnd = match.index + match[0].length;
      if (kind !== "file" && kind !== "rel") matchEnd = spacedPathEnd(text, matchEnd);
      if (!hasValidPrefixBoundary(text, match.index, kind)) {
        continue;
      }
      // Absolute recognizers must win before the relative one is even
      // considered — a rel match overlapping an earlier drive/UNC/file match
      // is a fragment of that absolute path ("x/y.md" inside "C:/x/y.md").
      if (matches.some((p) => !(matchEnd <= p.start || match.index >= p.end))) {
        continue;
      }
      if (kind === "file" && !isLocalFileHref(stripTrailingClosers(match[0]))) {
        continue;
      }
      // The extension gate reads the STRIPPED token: "docs/x.md)" is a path
      // followed by a closer, "and/or)" is still not a path.
      const relToken = stripTrailingClosers(match[0]);
      if (kind === "rel") {
        if (!REL_HAS_EXTENSION_RE.test(relToken)) {
          continue;
        }
        const firstSegment = relToken.slice(0, relToken.indexOf("/"));
        if (REL_CJK_RE.test(firstSegment)) {
          continue;
        }
      }
      // FILE_RE stops before a question mark so it cannot consume query
      // syntax. Do not linkify the safe-looking prefix of a file URL that has
      // a raw query suffix; localPathFromHref intentionally rejects queries.
      if (kind === "file" && text[matchEnd] === "?") {
        continue;
      }
      // Do not turn the safe-looking prefix of an alternate data stream into
      // a link (for example, C:\\report.md in C:\\report.md:payload).
      if (kind !== "file" && text[matchEnd] === ":" && !/\s/.test(text[matchEnd + 1] ?? "")) {
        continue;
      }
      // RegExpExecArray has no start/end — compare via index/length.
      matches.push({ start: match.index, end: matchEnd, raw: text.slice(match.index, matchEnd), kind });
      if (match.index === re.lastIndex) re.lastIndex += 1; // guard against zero-width
    }
  }
  matches.sort((a, b) => a.start - b.start);

  const segments: LocalPathSegment[] = [];
  let cursor = 0;
  for (const m of matches) {
    if (m.start > cursor) segments.push({ text: text.slice(cursor, m.start) });
    // UNC text arrives with a single leading backslash (markdown folded the
    // `\\` escape); restore the real UNC prefix for the native opener.
    const path = m.kind === "unc" ? "\\" + stripTrailingClosers(m.raw) : stripTrailingClosers(m.raw);
    const decodedPath = unescapeRefPath(path);
    if (path && !hasDisallowedWindowsPathSyntax(decodedPath)) {
      segments.push({ text: m.raw, path: decodedPath, kind: m.kind });
    } else {
      segments.push({ text: m.raw });
    }
    cursor = Math.max(cursor, m.end);
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor) });
  return segments;
}

/**
 * Builds the href for a clickable local path. Uses file:/// with forward
 * slashes (the standard Windows form) and percent-encodes non-ASCII and `#`.
 * Valid file URLs are preserved verbatim so authority-form UNC URLs are not
 * rewritten into a different path.
 */
export function localPathHref(path: string): string {
  if (isLocalFileHref(path)) return path;
  if (path.startsWith("file:///")) {
    try {
      path = decodeURIComponent(path.slice("file:///".length));
    } catch {
      // Malformed escapes: keep the literal text rather than failing.
    }
  }
  return "file:///" + encodeURI(path.replace(/\\/g, "/")).replace(/#/g, "%23");
}

// Task 1158: workspace-relative paths cannot masquerade as file:/// URLs —
// that scheme means "absolute" to every downstream gate. They ride in a
// dedicated scheme instead; RelativePathMarkdownLink resolves them through
// the per-tab backend probe (ResolveOpenablePathForTab) before opening.
const RELPATH_SCHEME = "relpath://";

export function relPathHref(path: string): string {
  return RELPATH_SCHEME + encodeURI(path.replace(/\\/g, "/")).replace(/#/g, "%23");
}

/** Inverse of relPathHref. Non-relpath hrefs (and absolute shapes) return null. */
export function relativePathFromHref(href?: string): string | null {
  if (!href || !href.startsWith(RELPATH_SCHEME)) return null;
  try {
    const path = decodeURIComponent(href.slice(RELPATH_SCHEME.length));
    if (!path || path.startsWith("/") || path.startsWith("\\") || /^[A-Za-z]:/.test(path)) return null;
    return path;
  } catch {
    return null;
  }
}

export function isRelativePathHref(href?: string): boolean {
  return relativePathFromHref(href) !== null;
}

/**
 * Remark plugin: rewrites plain-text nodes containing local paths into link
 * nodes (text + link alternation), so the markdown renderer hands them to
 * RichMarkdownLink which routes them to the native opener.
 *
 * Task 1138 follow-up: an inline code span whose ENTIRE value is one
 * recognizable absolute path linkifies too. LLM answers overwhelmingly print
 * paths inside backticks, and the DSH reference renders such spans as chips.
 * Only the whole-span form qualifies (precision first): mixed content like
 * `path=C:\x\y.md` stays inert, and relative paths never match because the
 * recognizer only accepts drive/UNC/file-URL shapes. The chip itself carries
 * the inline-code styling (.md-rich-link--local), so no code semantics are
 * visibly lost.
 *
 * Collection and replacement are separate passes: replacing during the visit
 * would re-visit the freshly inserted link children and nest anchors.
 */
export function remarkLocalPathLinks() {
  return (tree: Root) => {
    const plan: Array<{ parent: Parent; index: number; nodes: Array<Text | Link> }> = [];
    visit(tree, "text", (node: Text, index: number | undefined, parent) => {
      if (parent === undefined || parent === null || index === undefined || index === null) return;
      // Skip link internals: rewriting their text would nest <a> elements
      // (mdast forbids links inside links) and double-fire on click.
      if (parent.type === "link" || parent.type === "linkReference") return;
      const segments = linkifyLocalPaths(node.value);
      if (segments.length === 1 && segments[0].path === undefined) return;
      plan.push({
        parent,
        index,
        nodes: segments.map<Text | Link>((seg) =>
          seg.path !== undefined
            ? {
                type: "link",
                // "rel" rides the relpath:// scheme — RelativePathMarkdownLink
                // resolves it against the session root via the backend probe.
                url: seg.kind === "rel" ? relPathHref(seg.path) : localPathHref(seg.path),
                title: null,
                children: [{ type: "text", value: seg.text }],
              }
            : { type: "text", value: seg.text },
        ),
      });
    });
    visit(tree, "inlineCode", (node: InlineCode, index: number | undefined, parent) => {
      if (parent === undefined || parent === null || index === undefined || index === null) return;
      // Same nesting guard: a link label like [`C:\x\y.md`](u) already sits in
      // an anchor; rewriting it would nest <a> elements.
      if (parent.type === "link" || parent.type === "linkReference") return;
      const segments = linkifyLocalPaths(node.value);
      if (segments.length !== 1 || segments[0].path === undefined) return;
      plan.push({
        parent,
        index,
        nodes: [{
          type: "link",
          url: segments[0].kind === "rel" ? relPathHref(segments[0].path) : localPathHref(segments[0].path),
          title: null,
          children: [{ type: "text", value: node.value }],
        }],
      });
    });
    // Back-to-front keeps earlier indices valid within the same parent.
    for (let i = plan.length - 1; i >= 0; i--) {
      const { parent, index, nodes } = plan[i];
      parent.children.splice(index, 1, ...nodes);
    }
  };
}
