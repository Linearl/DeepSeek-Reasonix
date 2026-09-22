// Run: npx tsx src/__tests__/hydrate-resident-snapshot.test.ts
//
// Task 232: a reset-surface hydrate empties the transcript surface (items and
// prefix), which used to make the next switch read as "nothing cached"
// (no-reusable-cache + "resident items empty" veto) and pay a full reload.
// The emptied surface now reuses the transcript-store LRU snapshot:
// hydratedHistoryApplyMode applies it (replace) instead of skipping, and
// hasResidentSnapshotForEmptySurface gates the decision by session identity.

import assert from "node:assert/strict";
import {
  hasResidentSnapshotForEmptySurface,
  hydratedHistoryApplyMode,
} from "../lib/hydrateHistoryApply";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

type MinimalState = {
  items: unknown[];
  historyPrefixCount?: number;
  meta?: { sessionPath?: string };
};

const state = (over: Partial<MinimalState>): MinimalState => ({
  items: [],
  historyPrefixCount: 0,
  ...over,
});

function testHasResidentSnapshotForEmptySurface() {
  process.stdout.write("hasResidentSnapshotForEmptySurface\n");

  const emptied = state({
    items: [],
    meta: { sessionPath: `C:\\s\\20260922-0500.1-model.jsonl` },
  });
  ok(
    hasResidentSnapshotForEmptySurface(emptied as never, `C:\\s\\20260922-0500.1-MODEL.jsonl`) === true,
    "reset-emptied surface with matching (case/spelling-tolerant) identity reuses the snapshot",
  );

  ok(
    hasResidentSnapshotForEmptySurface(
      state({ items: [{}], meta: { sessionPath: `C:\\s\\a.jsonl` } }) as never,
      `C:\\s\\a.jsonl`,
    ) === false,
    "a non-empty surface stays on the regular reusable-transcript contract",
  );

  ok(
    hasResidentSnapshotForEmptySurface(
      state({ meta: { sessionPath: `C:\\s\\other.jsonl` } }) as never,
      `C:\\s\\a.jsonl`,
    ) === false,
    "a different session identity must not adopt the snapshot",
  );

  ok(
    hasResidentSnapshotForEmptySurface(state({ meta: {} }) as never, `C:\\s\\a.jsonl`) === false,
    "no stored identity: play safe and refetch",
  );

  ok(
    hasResidentSnapshotForEmptySurface(
      state({ meta: { sessionPath: `C:\\s\\a-recovery-9f2.jsonl` } }) as never,
      `C:\\s\\a.jsonl`,
    ) === false,
    "recovery copies never match",
  );
}

function testHydratedHistoryApplyModeLocalSnapshot() {
  process.stdout.write("hydratedHistoryApplyMode (local-snapshot branch)\n");

  const emptied = state({ items: [] });
  const projection = { items: [{ kind: "user" }] } as never;

  ok(
    hydratedHistoryApplyMode(true, true, false, emptied as never, projection) === "replace",
    "skipHistory onto an emptied surface with a local snapshot applies it (replace)",
  );

  ok(
    hydratedHistoryApplyMode(true, false, false, emptied as never, undefined) === "skip",
    "skipHistory without any snapshot still skips (caller owns the surface)",
  );

  const cached = state({ items: [{ kind: "user" }], historyPrefixCount: 3 });
  ok(
    hydratedHistoryApplyMode(true, true, false, cached as never, projection) === "skip",
    "a populated surface keeps the existing skip behaviour",
  );
}

testHasResidentSnapshotForEmptySurface();
testHydratedHistoryApplyModeLocalSnapshot();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
