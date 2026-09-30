// jumpPreview: pure content + placement model for the question navigator's
// hover preview card (task 149). Deliberately free of DOM/React so the card
// structure (bold lead + body lines + tool tags) and the edge-flip rules are
// unit-testable without jsdom.
//
// Data seam: the input is structurally satisfied by QuestionAnchor today
// (text + loaded). If the navigation data path later attaches optional
// `title` / `body` / `tools` fields to the anchor, the card picks them up
// with no further UI changes (the component forwards the anchor as-is).

export type JumpPreviewSource = {
  text: string;
  title?: string;
  body?: string;
  tools?: readonly string[];
  loaded?: boolean;
};

export type JumpPreviewContent = {
  /** Bold lead line of the card. */
  title: string;
  /** Remaining body lines, already cleaned and capped for rendering. */
  bodyLines: string[];
  /** Normalized, deduplicated tool names (capped) shown as small tags. */
  tools: string[];
  /** True for not-yet-loaded aggregated turns: title-only compact card. */
  placeholder: boolean;
};

export type JumpPreviewFlip = "none" | "down" | "up";

/** Tool tags shown per card; extra tools are dropped to keep the card light. */
export const JUMP_PREVIEW_MAX_TOOLS = 4;
/** Body lines rendered per card; visual overflow is the CSS line-clamp's job. */
export const JUMP_PREVIEW_MAX_BODY_LINES = 8;
/** Perf cap per body line; long lines wrap, this only bounds worst-case work. */
export const JUMP_PREVIEW_MAX_BODY_LINE_CHARS = 160;
export const JUMP_PREVIEW_TITLE_MAX_CHARS = 120;
export const JUMP_PREVIEW_PLACEHOLDER_MAX_CHARS = 120;
/** Keep-out gap between the card and the rail's top/bottom edge. */
export const JUMP_PREVIEW_EDGE_MARGIN = 8;
/** First-paint estimate before the real card is measured (title + body + tags). */
export const JUMP_PREVIEW_ESTIMATED_HEIGHT = 96;

// A split only fires on sentence punctuation followed by whitespace, the end
// of the text, or a CJK character — so "3.14" or "v1.2" never produce a break,
// while Chinese sentences (no space after 。！？) still split.
const SENTENCE_END = /[.!?。！？;；](\s+|$|(?=[\u4e00-\u9fff\u3400-\u4dbf]))/;
const TITLE_CORE_MIN_CHARS = 4;
const REST_MIN_CHARS = 8;

function cleanLine(line: string): string {
  return line.replace(/\s+/g, " ").trim();
}

/** Deduplicate (case-insensitive), trim and cap tool names. */
export function normalizeJumpPreviewTools(tools: readonly string[] | undefined): string[] {
  if (!tools || tools.length === 0) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of tools) {
    const name = typeof raw === "string" ? raw.trim() : "";
    if (!name) continue;
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(name);
    if (out.length >= JUMP_PREVIEW_MAX_TOOLS) break;
  }
  return out;
}

function capBodyLines(lines: string[]): string[] {
  return lines
    .slice(0, JUMP_PREVIEW_MAX_BODY_LINES)
    .map((line) => line.slice(0, JUMP_PREVIEW_MAX_BODY_LINE_CHARS));
}

/**
 * Split a compacted one-liner into a bold lead sentence and the remainder.
 * Returns null when either half is too small to read as its own row, so
 * short questions stay a single bold line instead of a lonely fragment.
 */
export function splitLeadSentence(text: string): { title: string; rest: string } | null {
  const match = SENTENCE_END.exec(text);
  if (!match) return null;
  const title = text.slice(0, match.index + match[0].length).trim();
  const rest = text.slice(match.index + match[0].length).trim();
  const core = title.replace(/[.!?。！？;；]+$/, "").trim();
  if (core.length < TITLE_CORE_MIN_CHARS || rest.length < REST_MIN_CHARS) return null;
  return { title, rest };
}

export function buildJumpPreviewContent(source: JumpPreviewSource): JumpPreviewContent {
  const text = typeof source?.text === "string" ? source.text : "";
  const placeholder = source.loaded === false;
  const explicitTitle = cleanLine(source.title ?? "");
  const lines = text.split(/\r?\n/).map(cleanLine).filter((line) => line.length > 0);

  // Not-yet-loaded aggregated turns have no real content: title-only card.
  if (placeholder) {
    const title = (explicitTitle || lines[0] || "").slice(0, JUMP_PREVIEW_PLACEHOLDER_MAX_CHARS);
    return { title, bodyLines: [], tools: [], placeholder: true };
  }

  const tools = normalizeJumpPreviewTools(source.tools);
  const explicitBody = typeof source.body === "string" ? source.body : "";

  let title = explicitTitle;
  let bodyLines: string[] = [];
  if (explicitBody) {
    bodyLines = capBodyLines(explicitBody.split(/\r?\n/).map(cleanLine).filter((line) => line.length > 0));
  } else if (lines.length > 1) {
    title = title || lines[0];
    bodyLines = capBodyLines(lines.slice(1));
  } else if (!title) {
    const single = lines[0] ?? "";
    const split = splitLeadSentence(single);
    if (split) {
      title = split.title;
      bodyLines = capBodyLines([split.rest]);
    } else {
      title = single;
    }
  }

  return {
    title: title.slice(0, JUMP_PREVIEW_TITLE_MAX_CHARS),
    bodyLines,
    tools,
    placeholder: false,
  };
}

/**
 * Vertical placement of the preview card inside the rail container.
 *
 * The card is centered on the hovered rail position by default; near the
 * rail's top edge it flips to grow downward from the anchor, near the bottom
 * edge it flips to grow upward, and in both cases it is clamped so the card
 * never leaves the container. When the card is taller than the container the
 * top margin wins, keeping the headline readable.
 */
export function jumpPreviewPlacement(
  anchorY: number,
  cardHeight: number,
  containerHeight: number,
  margin = JUMP_PREVIEW_EDGE_MARGIN,
): { top: number; flip: JumpPreviewFlip } {
  const height = Number.isFinite(cardHeight) && cardHeight > 0 ? cardHeight : 0;
  const container = Number.isFinite(containerHeight) && containerHeight > 0 ? containerHeight : 0;
  const anchor = Number.isFinite(anchorY) ? Math.max(0, anchorY) : 0;
  if (height <= 0 || container <= 0) {
    return { top: container > 0 ? Math.min(anchor, container) : anchor, flip: "none" };
  }
  const lower = Math.max(0, margin);
  const upper = Math.max(lower, container - margin - height);
  const clamp = (value: number) => Math.max(lower, Math.min(value, upper));
  const centered = anchor - height / 2;
  if (centered >= lower && centered + height <= container - margin) {
    return { top: centered, flip: "none" };
  }
  if (centered < lower) {
    // Not enough room above: grow downward from the anchor point.
    return { top: clamp(Math.max(anchor, lower)), flip: "down" };
  }
  // Not enough room below: keep the card bottom at the anchor and grow upward.
  return { top: clamp(anchor - height), flip: "up" };
}
