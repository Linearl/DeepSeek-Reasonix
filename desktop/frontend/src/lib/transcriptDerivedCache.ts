// Cross-tab reuse for the transcript derivation chain.
//
// A tab's `items` survive a switch (the per-tab state map keeps them), and the
// model only reads `live` through its PRESENCE flags — never the streamed text
// (see the contract note in transcriptRows.ts). So the same `items` array with
// the same presence signature must derive identical models.
//
// React's useMemo only remembers its most recent inputs, and switching A -> B ->
// A invalidates every step of the chain, so models, subcall grouping and row
// blocks were rebuilt from scratch on every return to a tab. These caches key on
// the array identity instead: switching back hands over the very same `items`
// array, so the chain short-circuits. WeakMap keys let a discarded transcript
// release its derived structures immediately.

import {
  buildTranscriptRowBlocks,
  buildTurnModels,
  type BuildRowsOptions,
  type ToolItem,
  type TranscriptLiveFlags,
  type TurnModel,
} from "./transcriptRows";
import type { TimelineBlock } from "./transcriptTimeline";
import type { Item } from "./useController";

type TurnModelEntry = { signature: string; models: TurnModel[] };
type SubcallEntry = { byParent: Map<string, ToolItem[]> };
type BlockEntry = { deps: readonly unknown[]; blocks: TimelineBlock[] };

const modelsByItems = new WeakMap<readonly Item[], TurnModelEntry>();
const subcallsByItems = new WeakMap<readonly Item[], SubcallEntry>();
const blocksByModels = new WeakMap<readonly TurnModel[], BlockEntry>();

// Every input buildTurnModels reads. The live flags carry presence only, so no
// streamed content can change the result behind this signature's back.
function turnModelSignature(live: TranscriptLiveFlags, running: boolean, hideReasoning: boolean): string {
  return [
    live.id ?? "",
    live.hasAnswerText ? 1 : 0,
    live.hasReasoning ? 1 : 0,
    live.reasoningComplete ? 1 : 0,
    running ? 1 : 0,
    hideReasoning ? 1 : 0,
  ].join("|");
}

export function cachedTurnModels(
  items: readonly Item[],
  live: TranscriptLiveFlags,
  running: boolean,
  hideReasoning: boolean,
): TurnModel[] {
  const signature = turnModelSignature(live, running, hideReasoning);
  const entry = modelsByItems.get(items);
  if (entry && entry.signature === signature) return entry.models;
  const models = buildTurnModels(items, live, running, hideReasoning);
  modelsByItems.set(items, { signature, models });
  return models;
}

/** Same grouping buildTranscriptRowBlocks consumes; keyed on the items array. */
export function cachedSubcallsByParent(items: readonly Item[]): Map<string, ToolItem[]> {
  const entry = subcallsByItems.get(items);
  if (entry) return entry.byParent;
  const byParent = new Map<string, ToolItem[]>();
  for (const item of items) {
    if (item.kind !== "tool" || !item.parentId) continue;
    const children = byParent.get(item.parentId) ?? [];
    children.push(item);
    byParent.set(item.parentId, children);
  }
  subcallsByItems.set(items, { byParent });
  return byParent;
}

/**
 * Row blocks for a model array. The models come from cachedTurnModels, so an
 * unchanged array means an unchanged derivation; `deps` carries everything else
 * the builder reads (fold state, checkpoints, creation mode, ...) and is
 * compared by identity.
 */
export function cachedTranscriptRowBlocks(
  models: readonly TurnModel[],
  options: BuildRowsOptions,
  deps: readonly unknown[],
): TimelineBlock[] {
  const entry = blocksByModels.get(models);
  if (entry && entry.deps.length === deps.length && entry.deps.every((dep, index) => dep === deps[index])) {
    return entry.blocks;
  }
  const blocks = buildTranscriptRowBlocks(models, options);
  blocksByModels.set(models, { deps: [...deps], blocks });
  return blocks;
}
