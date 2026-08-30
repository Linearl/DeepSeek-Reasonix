// Run: tsx src/__tests__/delivery-parked-turn-done.test.ts
// #9601: a replaced turn's final_readiness outcome must not be silently
// dropped while a newer turn owns the tab — the backend has already recorded
// the awaiting_delivery badge, so the frontend parks the outcome and surfaces
// the acceptance card when the newer turn settles.

import { initialState, reducer } from "../lib/useController";
import type { WireFinalReadiness } from "../lib/types";

type ReducerState = ReturnType<typeof reducer>;

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

function deliveryCards(state: ReducerState) {
  return state.items.filter((item) => item.kind === "notice" && item.variant === "delivery");
}

const readiness: WireFinalReadiness = { missing: ["verify", "signoff"] } as WireFinalReadiness;

{
  process.stdout.write("\nreplaced turn's final_readiness parks until the newer turn settles\n");
  let state = reducer(initialState, { type: "user", text: "q0", seq: 0, submissionId: "s0" });
  state = reducer(state, { type: "turn_admitted", turnId: "turn-a", submissionId: "s0" });
  eq(state.activeTurnId, "turn-a", "the first turn owns the tab");
  // The user submits a replacement while turn-a is still running.
  state = reducer(state, { type: "user", text: "q1", seq: 1, submissionId: "s1" });
  state = reducer(state, { type: "turn_admitted", turnId: "turn-b", submissionId: "s1" });
  eq(state.activeTurnId, "turn-b", "the newer turn owns the tab");

  // Turn-a settles with a final_readiness outcome — parked, no card yet.
  state = reducer(state, { type: "event", e: { kind: "turn_done", turnId: "turn-a", outcome: "final_readiness", readiness, err: "delivery incomplete" } });
  eq(state.parkedDelivery !== undefined, true, "the replaced turn's readiness outcome is parked");
  eq(deliveryCards(state).length, 0, "no acceptance card renders while the newer turn is in flight");
  eq(state.activeTurnId, "turn-b", "the newer turn keeps ownership");

  // Turn-b settles normally — the parked card surfaces.
  state = reducer(state, { type: "event", e: { kind: "turn_done", turnId: "turn-b" } });
  eq(state.parkedDelivery, undefined, "the parked outcome is consumed");
  eq(deliveryCards(state).length, 1, "the acceptance card surfaces once the newer turn settles");
  eq(deliveryCards(state)[0]?.action, "continue_delivery", "the parked card is actionable");
}

{
  process.stdout.write("\nnon-readiness outcomes from a replaced turn are still dropped\n");
  let state = reducer(initialState, { type: "user", text: "q0", seq: 0, submissionId: "s0" });
  state = reducer(state, { type: "turn_admitted", turnId: "turn-a", submissionId: "s0" });
  state = reducer(state, { type: "user", text: "q1", seq: 1, submissionId: "s1" });
  state = reducer(state, { type: "turn_admitted", turnId: "turn-b", submissionId: "s1" });
  state = reducer(state, { type: "event", e: { kind: "turn_done", turnId: "turn-a", outcome: "completed" } });
  eq(state.parkedDelivery, undefined, "a completed outcome parks nothing");
  eq(deliveryCards(state).length, 0, "no acceptance card for a completed replaced turn");
}

{
  process.stdout.write("\na newer turn's own readiness card supersedes the parked one\n");
  let state = reducer(initialState, { type: "user", text: "q0", seq: 0, submissionId: "s0" });
  state = reducer(state, { type: "turn_admitted", turnId: "turn-a", submissionId: "s0" });
  state = reducer(state, { type: "user", text: "q1", seq: 1, submissionId: "s1" });
  state = reducer(state, { type: "turn_admitted", turnId: "turn-b", submissionId: "s1" });
  state = reducer(state, { type: "event", e: { kind: "turn_done", turnId: "turn-a", outcome: "final_readiness", readiness, err: "delivery incomplete" } });
  state = reducer(state, { type: "event", e: { kind: "turn_done", turnId: "turn-b", outcome: "final_readiness", readiness, err: "delivery incomplete" } });
  eq(state.parkedDelivery, undefined, "the parked outcome is dropped as superseded");
  eq(deliveryCards(state).length, 1, "exactly one acceptance card remains");
}

if (failed > 0) {
  console.error(`\n${failed} parked delivery test(s) failed; ${passed} passed.`);
  process.exit(1);
}
console.log(`\n${passed} parked delivery tests passed.`);
