// Task 399: in-conversation Ctrl+F search over transcript rows.
//
// The transcript is virtualized (block windowing), so searching the DOM would
// silently miss rows that are not mounted. This module searches the row MODEL
// instead: every row contributes the text it renders (folded process segments
// contribute through their header row), matches are keyed by blockKey+rowKey,
// and the jump reuses the sanctioned mountBlock + jumpToBlock path so the
// single scroll-writer contract stays intact.
//
// Keep this file free of React: it is pure data logic so tests can drive it
// without a render harness.

import type { TranscriptRow, ToolItem } from "./transcriptRows";
import type { TimelineBlock } from "./transcriptTimeline";
import type { Item } from "./useController";

/** One row of searchable text beyond which we clip (head 80% + tail 20%).
 *  Tool outputs are the long tail; the tail slice keeps end-of-output errors
 *  like stack traces findable, which is where users actually search. */
const FIND_FIELD_CLIP = 50_000;

/** Row-level hit cap so a 10k-row session with a one-letter query cannot
 *  materialize an unbounded array on every keystroke. */
export const TRANSCRIPT_FIND_HIT_CAP = 500;

export interface TranscriptFindHit {
  blockKey: string;
  rowKey: string;
  /** Occurrences inside this row (row-level navigation, count for display). */
  occurrences: number;
}

export interface TranscriptFindResult {
  hits: TranscriptFindHit[];
  /** True when more rows matched than `hits.length` (display shows "N+"). */
  capped: boolean;
}

export interface TranscriptFindEntry {
  blockKey: string;
  rowKey: string;
  /** Lowercased, clipped row text. */
  text: string;
}

/** Find-highlight payload threaded down to row views. `null` = find closed,
 *  which keeps the stable identity that memoized rows depend on. */
export type TranscriptFindHighlight = { hits: ReadonlySet<string>; active: string | null } | null;

export const EMPTY_FIND_INDEX: readonly TranscriptFindEntry[] = [];
export const EMPTY_FIND_RESULT: TranscriptFindResult = { hits: [], capped: false };

function clip(text: string): string {
  if (text.length <= FIND_FIELD_CLIP) return text;
  const head = Math.ceil(FIND_FIELD_CLIP * 0.8);
  const tail = FIND_FIELD_CLIP - head;
  return `${text.slice(0, head)}\n…${text.slice(text.length - tail)}`;
}

function join(parts: Array<string | undefined | null>): string {
  return parts.filter((part): part is string => Boolean(part)).join("\n");
}

function toolFindText(item: ToolItem): string {
  return join([item.name, item.args, item.summary, item.subject, item.searchSummary, item.error, item.output]);
}

function noticeFindText(item: Extract<Item, { kind: "notice" }>): string {
  return join([item.title, item.text, item.detail]);
}

function extensionFindText(item: Extract<Item, { kind: "extension" }>): string {
  const card = item.card;
  return join([
    card.title,
    card.text,
    card.markdown,
    ...(card.fields ?? []).flatMap((field) => [field.key, field.value]),
  ]);
}

/** Text for one item inside a process fold (matches what the fold renders:
 *  assistant items reach folds stripped to their reasoning). */
function itemFindText(item: Item): string {
  switch (item.kind) {
    case "user":
      return item.text;
    case "assistant":
      return item.reasoning;
    case "tool":
      return toolFindText(item);
    case "phase":
      return item.text;
    case "notice":
      return noticeFindText(item);
    case "compaction":
      return item.summary;
    case "extension":
      return extensionFindText(item);
    default:
      return "";
  }
}

/** The text a row contributes to search. For a CLOSED process fold the body
 *  rows do not exist, so the header row carries its segment's text — that way
 *  folded tool output stays findable and the hit lands (visibly) on the
 *  header. When the fold is open the body rows search themselves, so the
 *  header contributes nothing and nothing is counted twice. */
export function extractRowFindText(row: TranscriptRow): string {
  switch (row.kind) {
    case "user":
      return join([row.item.text, row.item.submitText]);
    case "answer":
      return row.item.text;
    case "reasoning":
      return row.item.reasoning;
    case "tool":
      return toolFindText(row.item);
    case "tool-batch":
    case "tool-group":
      return row.items.map(toolFindText).join("\n");
    case "phase":
      return row.item.text;
    case "process-notice":
    case "notice":
      return noticeFindText(row.item);
    case "compaction":
      return row.item.summary;
    case "extension":
      return extensionFindText(row.item);
    case "turn-actions":
      return row.text;
    case "process-header":
      return row.open ? "" : row.segment.displayItems.map(itemFindText).join("\n");
    case "older-history":
    default:
      return "";
  }
}

// Rows are stable objects between keystrokes (blocks are memoized), so the
// extraction + lowercasing cost is paid once per row lifetime instead of once
// per keystroke.
const rowTextCache = new WeakMap<object, string>();

export function buildTranscriptFindIndex(blocks: readonly TimelineBlock[]): readonly TranscriptFindEntry[] {
  const entries: TranscriptFindEntry[] = [];
  for (const block of blocks) {
    for (const row of block.rows) {
      let text = rowTextCache.get(row);
      if (text === undefined) {
        text = clip(extractRowFindText(row)).toLowerCase();
        rowTextCache.set(row, text);
      }
      if (text) entries.push({ blockKey: block.key, rowKey: row.key, text });
    }
  }
  return entries;
}

/** Case-insensitive literal substring search (browser-find semantics: no
 *  regex, no word boundaries). Returns row-level hits in document order. */
export function searchTranscriptFind(
  index: readonly TranscriptFindEntry[],
  query: string,
  hitCap: number = TRANSCRIPT_FIND_HIT_CAP,
): TranscriptFindResult {
  const needle = query.trim().toLowerCase();
  if (!needle || index.length === 0) return EMPTY_FIND_RESULT;
  const hits: TranscriptFindHit[] = [];
  for (let i = 0; i < index.length; i += 1) {
    const entry = index[i];
    let occurrences = 0;
    let at = entry.text.indexOf(needle);
    while (at !== -1) {
      occurrences += 1;
      at = entry.text.indexOf(needle, at + needle.length);
    }
    if (occurrences > 0) hits.push({ blockKey: entry.blockKey, rowKey: entry.rowKey, occurrences });
    if (hits.length >= hitCap) {
      // Keep the flag honest: only rows AFTER the cap could still be new hits.
      const more = index.slice(i + 1).some((rest) => rest.text.indexOf(needle) !== -1);
      return { hits, capped: more };
    }
  }
  return { hits, capped: false };
}

/** Wrap-around prev/next step. `current` may be out of range or -1 (no active
 *  hit yet): next starts at the first hit, prev at the last. */
export function stepFindHit(current: number, total: number, direction: 1 | -1): number {
  if (total <= 0) return -1;
  if (current < 0 || current >= total) return direction > 0 ? 0 : total - 1;
  return (current + direction + total) % total;
}

/** Ctrl+F pressed with focus inside a code block must stay with the code
 *  editor's own search (LineNumberCode handles it in its capture path) — the
 *  two shortcut domains are isolated here. Duck-typed on `closest` instead of
 *  `instanceof Element` so cross-realm targets (jsdom in tests) behave the
 *  same as browser ones. */
export function shouldIgnoreFindShortcutTarget(target: unknown): boolean {
  if (!target || typeof target !== "object") return false;
  const closest = (target as { closest?: unknown }).closest;
  if (typeof closest !== "function") return false;
  try {
    return (target as { closest: (selector: string) => unknown }).closest(".code-block__wrap") != null;
  } catch {
    return false;
  }
}

/** Attribute selector for a row/block key (keys are ids but may contain
 *  quotes/backslashes once downstream data feeds them). */
export function findAttributeSelector(name: string, value: string): string {
  const escaped = value.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  return `[${name}="${escaped}"]`;
}
