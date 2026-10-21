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
  | "images"
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
// real other-source — the legend keeps the categories the user asked for.
// 任务1040 adds "images": screenshots price by resolution (not content bytes),
// and without their own bucket the bar showed an all-"消息" composition while
// the headline billed ~2K tokens per image — the two readouts could never be
// reconciled. Older hosts omit imageTokens → Math.max(0, undefined) = 0, the
// segment disappears (same as "other") and the bar degrades gracefully.
const SEGMENT_DEFS: ReadonlyArray<{
  key: CompositionSegmentKey;
  labelKey: DictKey;
  /** Hidden until the host reports a non-zero value — this is what drops the
   * reserved "other" row (and the images row on a pre-1040 host) instead of
   * rendering a misleading 0.0%. */
  tokens: (c: ContextCompositionInfo) => number;
}> = [
  { key: "message", labelKey: "context.segment.message", tokens: (c) => c.messageTokens },
  { key: "images", labelKey: "context.segment.images", tokens: (c) => c.imageTokens ?? 0 },
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
 * The denominator defaults to the segment sum; pass an explicit totalTokens to
 * make shares reflect the host-reported total (任务1025 缺口6: 分段条百分比
 * 分母改用 totalTokens，消息 94.8% → 81.8% 对齐主读数)。
 */
export function normalizeShares(tokens: number[], totalTokens?: number): number[] {
  const segmentSum = tokens.reduce((sum, value) => sum + Math.max(0, value), 0);
  const explicitTotal = totalTokens ?? 0;
  const total = explicitTotal > 0 ? Math.max(explicitTotal, segmentSum) : segmentSum;
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
 * compositionSegments returns the popup's legend rows. An absent or empty
 * composition (host predates the accessor, or no session yet) returns [] and
 * the popup hides the segmented bar instead of drawing zeros. Zero-token
 * buckets (the reserved "other" row today, "images" on a pre-1040 host) are
 * dropped rather than rendered as a misleading 0.0% row.
 *
 * 任务1025（缺口6）：分母改用 totalTokens（主读数口径），并在分项和小于
 * totalTokens 时补一个「其他」桶承载差额，使 Σtokens = totalTokens 且
 * Σshare = 100%——分段条百分比不再用分项和当分母（消息 94.8% → 81.8%）。
 */
export function compositionSegments(
  composition: ContextCompositionInfo | null | undefined,
): CompositionSegment[] {
  if (!composition || composition.totalTokens <= 0) return [];
  const visible = SEGMENT_DEFS.filter((def) => Math.max(0, def.tokens(composition)) > 0);
  const tokens = visible.map((def) => Math.max(0, def.tokens(composition)));
  if (tokens.length === 0) return [];
  const segmentSum = tokens.reduce((sum, value) => sum + value, 0);
  const totalTokens = Math.max(composition.totalTokens, segmentSum);
  // 补「其他」桶承载分项和与 totalTokens 的差额（缺口6：Σtokens = totalTokens）。
  const remainder = totalTokens - segmentSum;
  const rows: CompositionSegment[] = visible.map((def, index) => ({
    key: def.key,
    labelKey: def.labelKey,
    tokens: tokens[index],
    share: 0,
  }));
  if (remainder > 0) {
    rows.push({ key: "other", labelKey: "context.segment.other", tokens: remainder, share: 0 });
  }
  const allTokens = rows.map((row) => row.tokens);
  const shares = normalizeShares(allTokens, totalTokens);
  return rows.map((row, index) => ({ ...row, share: shares[index] }));
}
