// Run: tsx src/__tests__/transcript-derived-cache.test.ts
//
// The cross-tab derivation cache exists because switching back to a tab hands
// over the SAME items array (App keeps `state.items` per tab, and Transcript
// receives it by reference). The contract is therefore: identical inputs reuse
// the identical derived objects, and every input change still invalidates.

import { cachedSubcallsByParent, cachedTranscriptRowBlocks, cachedTurnModels } from "../lib/transcriptDerivedCache";
import { EMPTY_FOLDS, NO_LIVE, type TranscriptLiveFlags } from "../lib/transcriptRows";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function syntheticItems(turns: number, toolsPerTurn: number): Item[] {
  const items: Item[] = [];
  let seq = 0;
  for (let turn = 0; turn < turns; turn += 1) {
    items.push({ kind: "user", id: `u${seq++}`, text: `prompt ${turn}` });
    items.push({ kind: "assistant", id: `a${seq++}`, text: `answer ${turn}`, reasoning: "", streaming: false });
    for (let tool = 0; tool < toolsPerTurn; tool += 1) {
      items.push({
        kind: "tool",
        id: `t${seq++}`,
        name: "bash",
        args: "",
        readOnly: false,
        status: "done",
        dataArchived: true,
      });
    }
  }
  return items;
}

console.log("\ntranscript derived cache contract");

{
  const items = syntheticItems(3, 2);
  const live: TranscriptLiveFlags = { id: "a1", hasAnswerText: true, hasReasoning: false };

  // 1. The tab-switch case: same array + same presence signature -> same object.
  const first = cachedTurnModels(items, live, false, false);
  ok(first.length === 3, "the model array covers every turn");
  ok(cachedTurnModels(items, live, false, false) === first, "the same items array reuses the model array");

  // A structurally equal but distinct array must not alias the first one.
  ok(cachedTurnModels(syntheticItems(3, 2), live, false, false) !== first, "a distinct items array derives its own models");

  // 2. Every input buildTurnModels reads invalidates.
  ok(cachedTurnModels(items, live, true, false) !== first, "a running flip rebuilds");
  ok(cachedTurnModels(items, NO_LIVE, false, false) !== first, "a live presence change rebuilds");
  ok(cachedTurnModels(items, { ...live, hasAnswerText: false }, false, false) !== first, "an answer-text presence flip rebuilds");
  ok(cachedTurnModels(items, live, false, true) !== first, "a hideReasoning flip rebuilds");

  // 3. The most recent derivation is what a repeat lookup returns.
  const afterFlip = cachedTurnModels(items, NO_LIVE, false, false);
  ok(cachedTurnModels(items, NO_LIVE, false, false) === afterFlip, "the newest derivation is the one reused");

  // 4. Subcall grouping is keyed on the items array too.
  const grouped = cachedSubcallsByParent(items);
  ok(cachedSubcallsByParent(items) === grouped, "subcall grouping is reused for the same items");
  ok(cachedSubcallsByParent(syntheticItems(3, 2)) !== grouped, "a distinct items array regroups");

  // 5. Row blocks follow the model array identity plus the dep identities.
  const models = cachedTurnModels(items, NO_LIVE, false, false);
  const options = {
    folds: EMPTY_FOLDS,
    sessionExperience: "standard",
    hasOlderHistory: false,
    creationMode: false,
    turnForUser: () => undefined,
    hasCheckpointForTurn: () => false,
    subcallsByParent: grouped,
  } as never;
  const deps = [1, "a", false];
  const blocks = cachedTranscriptRowBlocks(models, options, deps);
  ok(blocks.length === 3, "one block per turn");
  ok(cachedTranscriptRowBlocks(models, options, [1, "a", false]) === blocks, "equal deps reuse the block array");
  ok(cachedTranscriptRowBlocks(models, options, [1, "b", false]) !== blocks, "a changed dep rebuilds the blocks");
  ok(cachedTranscriptRowBlocks(cachedTurnModels(items, live, false, false), options, deps) !== blocks, "a different model array rebuilds the blocks");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
