// Run:
//   corepack pnpm --dir desktop/frontend exec tsx src/__tests__/q2-transcript-measurement-benchmark.ts
//
// Q2 probe (task wt-zcode-q2, 2026-10-04): cost of the transcript measurement
// versions around a write_file result on this build (main-v2-stable tip,
// measurement caches from 7ef98c7c4 present). Synthetic, privacy-safe.

import { historyMessagesToItems, initialState, reducer, type Item } from "../lib/useController";
import type { HistoryMessage } from "../lib/types";
import { buildTranscriptRowBlocks, buildTurnModels, EMPTY_FOLDS, NO_LIVE } from "../lib/transcriptRows";
import { transcriptRowMeasurementVersion } from "../lib/transcriptRows";

const TURNS = 300;
const OUTPUT = "x".repeat(1024);
const WRITE_BYTES = 5561;

function syntheticHistory(): HistoryMessage[] {
  const messages: any[] = [];
  const writeBody = "y".repeat(WRITE_BYTES);
  for (let turn = 0; turn < TURNS; turn += 1) {
    messages.push({ role: "user", content: `prompt ${turn}` });
    const last = turn === TURNS - 1;
    messages.push({
      role: "assistant",
      content: `answer ${turn}`,
      toolCalls: [{
        id: `call_${turn}`,
        name: "write_file",
        arguments: last ? JSON.stringify({ path: `docs/file-${turn}.md`, content: writeBody }) : `{"command":"synthetic ${turn}"}`,
      }],
    });
    messages.push({
      role: "tool",
      toolCallId: `call_${turn}`,
      toolName: "write_file",
      content: last ? `wrote ${WRITE_BYTES} bytes to docs/file-${turn}.md` : OUTPUT,
    });
  }
  return messages as HistoryMessage[];
}

function time<T>(fn: () => T): { value: T; ms: number } {
  const start = performance.now();
  const value = fn();
  return { ms: performance.now() - start, value };
}

function rowsFor(items: Item[]) {
  return buildTranscriptRowBlocks(buildTurnModels(items, NO_LIVE, false), {
    folds: EMPTY_FOLDS, sessionExperience: "standard", hasOlderHistory: true, creationMode: false,
    turnForUser: (item) => (item.historyTurn ?? 1) - 1,
  });
}

function main() {
  const messages = syntheticHistory();
  const converted = time(() => historyMessagesToItems(messages, "q2"));
  const reduced = time(() => reducer(initialState, { type: "history", messages }));
  const items = reduced.value.items;
  console.log(`fixture: turns=${TURNS} messages=${messages.length} items=${items.length} historyConvertMs=${converted.ms.toFixed(1)} reducerMs=${reduced.ms.toFixed(1)}`);

  // 1. Cold: first full build + first per-row measurement (all caches empty).
  const coldBuild = time(() => {
    const blocks = rowsFor(items);
    const rows = blocks.flatMap((b) => b.rows);
    for (const row of rows) transcriptRowMeasurementVersion(row);
    return rows.length;
  });
  console.log(`cold: build+measure all rows: rows=${coldBuild.value} ms=${coldBuild.ms.toFixed(1)}`);

  // 2. Warm: same items, rebuild (new row objects, same item objects) — the
  // per-event shape when an unrelated state update lands.
  const warm = time(() => {
    const blocks = rowsFor(items);
    for (const b of blocks) for (const row of b.rows) transcriptRowMeasurementVersion(row);
  });
  console.log(`warm rebuild (unchanged items): ms=${warm.ms.toFixed(1)}`);

  // 3. The write_file result lands: its tool item is replaced (new object).
  // Rebuild + measure — only that one item recomputes.
  const idx = items.length - 1;
  const changed = [...items];
  changed[idx] = { ...(changed[idx] as any), output: `wrote ${WRITE_BYTES} bytes to docs/file-${TURNS - 1}.md` } as Item;
  const afterWrite = time(() => {
    const blocks = rowsFor(changed);
    for (const b of blocks) for (const row of b.rows) transcriptRowMeasurementVersion(row);
  });
  console.log(`rebuild after write_file result (1 item replaced): ms=${afterWrite.ms.toFixed(1)}`);

  // 4. Scale reference: cold measurement if the caches were missing entirely
  // (the 20260910 root-cause shape), approximated by a fresh module state is
  // not possible across calls; instead measure 10 repeated cold passes of the
  // single changed row to get the per-item recompute cost.
  const perItem: number[] = [];
  for (let i = 0; i < 10; i += 1) {
    const it = [...items];
    it[idx] = { ...(it[idx] as any), output: `wrote ${WRITE_BYTES} bytes v${i}` } as Item;
    const blocks = rowsFor(it);
    const rows = blocks.flatMap((b) => b.rows);
    const start = performance.now();
    for (const row of rows) transcriptRowMeasurementVersion(row);
    perItem.push(performance.now() - start);
  }
  console.log(`rebuild after write (x10 repeats): ms=[${perItem.map((m) => m.toFixed(1)).join(", ")}]`);
}

main();
