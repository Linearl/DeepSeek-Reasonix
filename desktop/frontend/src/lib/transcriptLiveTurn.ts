import { userRowKey, type AssistantItem, type TranscriptRow, type TurnModel } from "./transcriptRows";

// #9579: the live region keeps only the most recent rows (plus everything
// streaming); rows beyond this budget fall back into the virtual list body
// so an uncollapsed long turn stacks naturally instead of crowding the
// user message. Folded turns render compact headers, so a small budget
// leaves their behaviour unchanged.
const LIVE_TAIL_ROW_KEEP = 5;

// The streaming turn renders in Virtuoso's Footer, outside its measured size
// tree. Keep the active user's row in history and preserve every later row's
// visual order in the live region.
export interface TranscriptLiveSplit {
  historyRows: TranscriptRow[];
  liveRows: TranscriptRow[];
  /** True while the live region may need to render its status row. */
  liveActive: boolean;
}

export function assistantAnswerOnly(item: AssistantItem): AssistantItem {
  return { ...item, reasoning: "", reasoningComplete: true, reasoningDurationMs: undefined };
}

export function resolveLiveTurnGrowthFloor(
  previousRowCount: number,
  nextRowCount: number,
  previousHeight: number,
  heldHeight: number | null,
): number | null {
  if (nextRowCount <= previousRowCount || previousHeight <= 0) return heldHeight;
  return Math.max(previousHeight, heldHeight ?? 0);
}

function firstRowKeyForModel(model: TurnModel): string | undefined {
  for (const segment of model.segments) {
    if (segment.displayItems.length > 0) return `ph:${segment.key}`;
    const outside = segment.outsideItems[0];
    if (!outside) continue;
    return outside.kind === "extension" ? `x:${outside.id}` : outside.kind === "notice" ? `n:${outside.id}` : `a:${outside.id}`;
  }
  return undefined;
}

export function splitTranscriptLiveRows(
  models: readonly TurnModel[],
  rows: readonly TranscriptRow[],
  liveId: string | undefined,
  running: boolean,
): TranscriptLiveSplit {
  let activeIndex = -1;
  if (liveId) activeIndex = models.findIndex((model) => model.turnItems.some((item) => item.id === liveId));
  if (activeIndex < 0 && running && models.length > 0) activeIndex = models.length - 1;
  if (activeIndex < 0) return { historyRows: [...rows], liveRows: [], liveActive: false };
  const active = models[activeIndex];
  const activeUser = active.user;
  if (!activeUser) {
    const firstKey = firstRowKeyForModel(active);
    const firstIndex = firstKey ? rows.findIndex((row) => row.key === firstKey) : -1;
    if (!firstKey || firstIndex < 0) return { historyRows: [...rows], liveRows: [], liveActive: true };
    return { historyRows: rows.slice(0, firstIndex), liveRows: rows.slice(firstIndex), liveActive: true };
  }
  const userIndex = rows.findIndex((row) => row.key === userRowKey(activeUser.id));
  if (userIndex < 0) return { historyRows: [...rows], liveRows: [], liveActive: false };
  // #9579: when the active turn is not folded, pulling *every* post-user row
  // into the Footer live block crowds dozens of expanded tool rows right
  // under the user message instead of letting them stack naturally in the
  // conversation flow. Keep only the most recent rows in the live region
  // (queued follow-up user rows are part of that tail, preserving #9540's
  // visual order); completed rows move back into the virtual list body,
  // which is exactly where they end up once the turn settles. Folded turns
  // render compact headers, so a small budget leaves their behaviour
  // unchanged.
  const tailStart = Math.max(userIndex + 1, rows.length - LIVE_TAIL_ROW_KEEP);
  return {
    historyRows: rows.slice(0, tailStart),
    liveRows: rows.slice(tailStart),
    liveActive: true,
  };
}
