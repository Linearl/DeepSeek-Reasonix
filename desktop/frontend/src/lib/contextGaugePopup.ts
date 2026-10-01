// Task 442 — the composer gauge popup's data model: composition segments,
// the provider gate for quota cards, and the "更多" navigation request.
//
// Pure functions only — the popup component does rendering and fetching; every
// decision that acceptance tests need to pin lives here.

import type { DictKey } from "../locales/en";
import type { ContextCompositionInfo } from "./types";

/**
 * The popup's 「更多 >」 entry opens the full detail surface. It travels as a
 * window event because the gauge lives deep inside the composer while the
 * opener (right dock / settings page) is App-level state; App.tsx owns the
 * listener, this module owns the event name and the destination decision.
 */
export const OPEN_CONTEXT_OVERVIEW_EVENT = "reasonix:open-context-overview";

export type ContextOverviewTarget = "right-dock" | "settings-usage";

/**
 * The creation layout hides the right dock's 概览 tab (App coerces "context"
 * back to "files" there), so 「更多」in that layout goes to the settings usage
 * page instead — every other layout opens the right dock's overview.
 */
export function contextOverviewTarget(layoutStyle: string): ContextOverviewTarget {
  return layoutStyle === "creation" ? "settings-usage" : "right-dock";
}

export function requestContextOverview(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent(OPEN_CONTEXT_OVERVIEW_EVENT));
}

/**
 * The OpenCode Go quota card is provider-conditional, not a lab switch: it
 * shows whenever the active session's model ref is an opencode-go provider.
 * The Anthropic/Responses/DeepSeek routes are separate presets over the same
 * subscription, so the prefix carries them (opencode-go-anthropic, ...).
 */
export function isOpencodeGoProvider(providerName: string | null | undefined): boolean {
  const name = (providerName ?? "").trim();
  return name === "opencode-go" || name.startsWith("opencode-go-");
}

export type CompositionSegmentKey =
  | "message"
  | "builtinTools"
  | "skills"
  | "systemPrompt"
  | "other"
  | "mcpTools";

export interface CompositionSegment {
  key: CompositionSegmentKey;
  labelKey: DictKey;
  tokens: number;
  /** Percent of the composition, one decimal, segments sum to exactly 100. */
  share: number;
}

// Display order follows the dispatch's category list. "other" is reserved for
// content the host cannot attribute to a named segment; today every byte of a
// request lands in a named bucket, so it renders at 0% until a host gains a
// real other-source — the legend keeps the six categories the user asked for.
const SEGMENT_DEFS: ReadonlyArray<{
  key: CompositionSegmentKey;
  labelKey: DictKey;
  tokens: (c: ContextCompositionInfo) => number;
}> = [
  { key: "message", labelKey: "context.segment.message", tokens: (c) => c.messageTokens },
  { key: "builtinTools", labelKey: "context.segment.builtinTools", tokens: (c) => c.builtinToolTokens },
  { key: "skills", labelKey: "context.segment.skills", tokens: (c) => c.skillTokens },
  { key: "systemPrompt", labelKey: "context.segment.systemPrompt", tokens: (c) => c.systemPromptTokens },
  { key: "other", labelKey: "context.segment.other", tokens: () => 0 },
  { key: "mcpTools", labelKey: "context.segment.mcpTools", tokens: (c) => c.mcpToolTokens },
];

/**
 * normalizeShares turns token counts into one-decimal percentages that sum to
 * exactly 100.0. Largest-remainder distribution puts each leftover tenth on
 * the segment that lost the most to flooring, so the segmented bar can never
 * disagree with its own legend (rounding drift is a display bug, not data).
 * The denominator is the segment sum — shares describe the actual
 * composition even if a host ever reports a total the segments do not reach.
 */
export function normalizeShares(tokens: number[]): number[] {
  const total = tokens.reduce((sum, value) => sum + Math.max(0, value), 0);
  if (total <= 0) return tokens.map(() => 0);
  const tenths = tokens.map((value) => Math.floor((Math.max(0, value) / total) * 1000));
  let assigned = tenths.reduce((sum, value) => sum + value, 0);
  const remainderOrder = tokens
    .map((value, index) => ({ index, rem: (Math.max(0, value) / total) * 1000 - tenths[index] }))
    .sort((a, b) => b.rem - a.rem || a.index - b.index);
  let leftover = 1000 - assigned;
  for (let pass = 0; pass < 2 && leftover > 0; pass += 1) {
    for (const { index } of remainderOrder) {
      if (leftover <= 0) break;
      tenths[index] += 1;
      leftover -= 1;
      assigned += 1;
    }
  }
  // Float noise can overshoot the target by a tenth; take it back from the
  // segments that gained one, so the sum still reads 100.0.
  for (let pass = 0; pass < 2 && leftover < 0; pass += 1) {
    for (let i = remainderOrder.length - 1; i >= 0 && leftover < 0; i -= 1) {
      const index = remainderOrder[i].index;
      if (tenths[index] <= 0) continue;
      tenths[index] -= 1;
      leftover += 1;
      assigned -= 1;
    }
  }
  return tenths.map((value) => value / 10);
}

/**
 * compositionSegments returns the popup's six legend rows. An absent or empty
 * composition (host predates the accessor, or no session yet) returns [] and
 * the popup hides the segmented bar instead of drawing zeros.
 */
export function compositionSegments(
  composition: ContextCompositionInfo | null | undefined,
): CompositionSegment[] {
  if (!composition || composition.totalTokens <= 0) return [];
  const tokens = SEGMENT_DEFS.map((def) => Math.max(0, def.tokens(composition)));
  if (tokens.every((value) => value === 0)) return [];
  const shares = normalizeShares(tokens);
  return SEGMENT_DEFS.map((def, index) => ({
    key: def.key,
    labelKey: def.labelKey,
    tokens: tokens[index],
    share: shares[index],
  }));
}
