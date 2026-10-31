// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1303-unapplied-merge.test.tsx
//
// tl1303 防风暴: a dead run holding several guidance messages makes the Go
// flush emit ONE counted user notice (code=unapplied_steer, no marker) plus
// per-item restore-only signals (code=unapplied_steer,
// detail=unapplied_steer:restore-only, ItemID wired for the Go-side durable
// inbox parking). The restore signal must NOT render a transcript warning —
// its facts already live in the counted notice. Failing open (older backend,
// missing marker) keeps the pre-tl1303 per-item warnings visible.

import { initialState, reducer } from "../lib/useController";
import type { Item, State } from "../lib/useController";
import type { WireEvent } from "../lib/types";

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

function ev(e: Record<string, unknown>): { type: "event"; e: WireEvent } {
  return { type: "event", e: { turnId: "t-tl1303", ...e } as unknown as WireEvent };
}

function noticeItems(s: State): Array<Extract<Item, { kind: "notice" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "notice" }> => it.kind === "notice");
}

function main() {
  // 1. Restore-only signal: never renders.
  {
    const s = reducer(initialState, ev({ kind: "notice", level: "warn", code: "unapplied_steer", detail: "unapplied_steer:restore-only", text: "Guidance was not applied ...:\nitem text", itemId: "item-1" }));
    ok(noticeItems(s).length === 0, "restore-only unapplied notice is kept out of the transcript");
  }

  // 2. Merged counted notice: renders once, carrying the count and both texts.
  {
    let s = reducer(initialState, ev({ kind: "notice", level: "warn", code: "unapplied_steer", text: "Guidance was not applied ...:\n×2\nfirst\nguidance\nsecond\nguidance" }));
    const items = noticeItems(s);
    ok(items.length === 1, "the merged counted notice renders exactly once");
    ok(items[0]?.code === "unapplied_steer" && items[0]?.level === "warn", "the merged notice keeps its code and warn level");
  }

  // 3. Legacy single notice (no marker detail): unchanged face.
  {
    const s = reducer(initialState, ev({ kind: "notice", level: "warn", code: "unapplied_steer", text: "Guidance was not applied ...:\nsolo guidance", itemId: "item-solo" }));
    const items = noticeItems(s);
    ok(items.length === 1, "a legacy single unapplied notice still renders");
    ok(items[0]?.text.includes("solo guidance"), "the legacy notice localizes the guidance body");
  }

  // 4. Replay idempotency: a restore-only replay leaves no residue and does
  // not advance the transcript; a merged notice renders regardless of order.
  {
    let s = reducer(initialState, ev({ kind: "notice", level: "warn", code: "unapplied_steer", detail: "unapplied_steer:restore-only", text: "x", itemId: "item-1", seq: 7 }));
    s = reducer(s, ev({ kind: "notice", level: "warn", code: "unapplied_steer", detail: "unapplied_steer:restore-only", text: "x", itemId: "item-1", seq: 7 }));
    ok(noticeItems(s).length === 0, "replayed restore-only signals stay invisible");
    s = reducer(s, ev({ kind: "notice", level: "warn", code: "unapplied_steer", text: "...:\n×1\nbody" }));
    ok(noticeItems(s).length === 1, "the counted notice renders after suppressed restore-only signals");
  }

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
