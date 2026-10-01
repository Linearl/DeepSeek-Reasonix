// Task 436: line estimation for the fold gate — moved out of
// components/Message.tsx so lightweight consumers (task 446's hover peek in
// ComposerGuidanceShelf) can reuse the estimator without dragging the whole
// Message/Markdown module graph into the shelf's lazy chunk.
//
// Threshold 14 estimated lines: a user bubble renders ≈22px per line, so 14
// lines ≈ 300px — well above the 200px clamp (folding has to visibly pay off)
// while staying inside the 12–16 line band where anything readable on one
// screen is never folded. Estimated lines weight CJK/fullwidth chars double
// because they render ≈2× wider than Latin glyphs at the bubble's font size.
export const USER_MSG_FOLD_LINE_THRESHOLD = 14;
export const USER_MSG_FOLD_CLAMP_HEIGHT_PX = 200;
const USER_MSG_FOLD_UNITS_PER_LINE = 110;

export function estimateUserMessageLines(text: string): number {
  if (!text) return 0;
  let total = 0;
  for (const line of text.split(/\r?\n/)) {
    let units = 0;
    for (const ch of line) units += ch.charCodeAt(0) > 0x2e7f ? 2 : 1;
    total += Math.max(1, Math.ceil(units / USER_MSG_FOLD_UNITS_PER_LINE));
  }
  return total;
}
