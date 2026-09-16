// toolPayloadPreview — UTF-8-safe bounded previews for tool payload text
// (task 137). Used where a live card wants a readable prefix instead of the
// full output or the archive path that drops the body entirely.
//
// Truncation never splits a multi-byte code point or a surrogate pair: the
// prefix is walked by code points until the byte budget or block budget is
// reached. The result is display text only — session data is not mutated.

import { TOOL_PREVIEW_MAX_BLOCKS, TOOL_PREVIEW_MAX_BYTES } from "./resourceBudgets";
import { noteToolPreviewTruncation } from "./sessionDiagnostics";

export type ToolPreviewResult = {
  text: string;
  truncated: boolean;
  /** UTF-8 bytes kept (approximate when the input was already truncated). */
  bytes: number;
  /** Logical blocks kept (newline-separated segments). */
  blocks: number;
  originalBytes: number;
  originalBlocks: number;
};

function utf8Length(ch: string): number {
  const code = ch.codePointAt(0) ?? 0;
  if (code <= 0x7f) return 1;
  if (code <= 0x7ff) return 2;
  if (code <= 0xffff) return 3;
  return 4;
}

function countBlocks(text: string): number {
  if (!text) return 0;
  let blocks = 1;
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) blocks += 1;
  }
  return blocks;
}

/**
 * Returns a display-safe prefix of `raw` within the byte and block budgets.
 * An empty/undefined input yields an empty, non-truncated result.
 */
export function toolPayloadPreview(
  raw: string | undefined | null,
  opts?: { maxBytes?: number; maxBlocks?: number },
): ToolPreviewResult {
  const text = raw ?? "";
  const maxBytes = Math.max(0, opts?.maxBytes ?? TOOL_PREVIEW_MAX_BYTES);
  const maxBlocks = Math.max(1, opts?.maxBlocks ?? TOOL_PREVIEW_MAX_BLOCKS);
  const originalBytes = utf8ByteLength(text);
  const originalBlocks = countBlocks(text);
  if (originalBytes <= maxBytes && originalBlocks <= maxBlocks) {
    return { text, truncated: false, bytes: originalBytes, blocks: originalBlocks, originalBytes, originalBlocks };
  }
  noteToolPreviewTruncation();
  let bytes = 0;
  let blocks = 1;
  let end = 0;
  for (const ch of text) {
    const size = utf8Length(ch);
    if (bytes + size > maxBytes) break;
    if (ch === "\n") {
      if (blocks + 1 > maxBlocks) break;
      blocks += 1;
    }
    bytes += size;
    end += ch.length;
  }
  const prefix = text.slice(0, end);
  return {
    text: prefix,
    truncated: true,
    bytes,
    blocks,
    originalBytes,
    originalBlocks,
  };
}

/** Exact UTF-8 byte length of a JS string (code-point walk). */
export function utf8ByteLength(text: string): number {
  let n = 0;
  for (const ch of text) n += utf8Length(ch);
  return n;
}

/**
 * utf8Prefix returns at most maxBytes of text without splitting a code point.
 * Unlike toolPayloadPreview it does not apply a block budget.
 */
export function utf8Prefix(text: string, maxBytes: number): string {
  if (maxBytes <= 0) return "";
  let bytes = 0;
  let end = 0;
  for (const ch of text) {
    const size = utf8Length(ch);
    if (bytes + size > maxBytes) break;
    bytes += size;
    end += ch.length;
  }
  return text.slice(0, end);
}
