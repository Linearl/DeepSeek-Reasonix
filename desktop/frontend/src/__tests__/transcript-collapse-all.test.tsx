// Run: tsx src/__tests__/transcript-collapse-all.test.tsx
//
// Task 269: concise three-fix + the collapse-all backstop, pinned end to end
// on the transcript side (the Composer button dispatches this exact event):
//   A1 concise outranks the whole-turn exemption (folds stay closed mid-run)
//   A2 an already-hydrated session experience is not overwritten by the
//      legacy reasoning-display hydrate (double-hydrate race, R2)
//   A3 before hydration the compatibility mirror decides, not "standard"
//   B  reasonix:collapse-all-folds collapses everything and the running
//      reconcile tick cannot spring it back (userOverridden pin, R3 reset)

import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntask 269: concise fix + collapse-all");

const harness = await createTranscriptHarness({
  // Task 269 A3: seed the compatibility mirror so the pre-hydrate window
  // (no hydrate call yet) must read "concise" from storage, not default to
  // standard and live-expand the fold.
  storage: { "reasonix-session-experience": "concise" },
});
const { container } = harness;
const { act } = await import("react");
const sessionExperience = await harness.loadModule<{
  getSessionExperience: () => string;
  hydrateSessionExperience: (v: unknown) => void;
  isSessionExperienceHydrated: () => boolean;
}>("/src/lib/sessionExperience.ts");
const reasoning = await harness.loadModule<{
  hydrateReasoningDisplayMode: (mode: unknown, explicit?: boolean) => void;
}>("/src/lib/reasoningDisplayPreference.ts");

try {
  // A3: the harness seeds the compatibility mirror; before any hydrate the
  // reader must honour it instead of defaulting to standard (which would
  // live-expand the running fold the user asked to keep closed).
  ok(sessionExperience.isSessionExperienceHydrated() === false, "starts un-hydrated");
  ok(sessionExperience.getSessionExperience() === "concise", "A3: pre-hydrate reads the mirror, not standard");

  // A2: chain B (explicit legacy reasoning flag) must not flip an
  // already-authoritative session experience back to standard.
  sessionExperience.hydrateSessionExperience("concise");
  reasoning.hydrateReasoningDisplayMode("auto", true);
  ok(sessionExperience.getSessionExperience() === "concise",
    "A2: the explicit reasoning hydrate does not overwrite an already-hydrated experience");

  // A1 + B: render a RUNNING turn under concise — both the answer-streamed
  // case (turn has outside content) and a purely-in-flight one stay closed.
  const answered: Item[] = [
    { kind: "user", id: "u1", text: "ask" },
    { kind: "assistant", id: "a1", text: "streaming answer", reasoning: "inner thought", streaming: true, workDurationMs: 1_000 },
  ];
  const inFlight: Item[] = [
    { kind: "user", id: "u2", text: "ask two" },
    { kind: "assistant", id: "a2", text: "", reasoning: "still thinking", streaming: true, workDurationMs: 100 },
  ];
  await harness.render(inFlight, { running: true });
  ok(!container.querySelector(".turn-collapse--open"), "A1: concise keeps the not-yet-answered fold closed while running");

  await harness.render(answered, { running: true });
  ok(!container.querySelector(".turn-collapse--open"), "A1: concise keeps the answering fold closed while running");

  // B: the backstop starts from an OPEN fold — driven by a FRESH segment
  // under standard instead of a DOM click (click -> handleFoldToggle ->
  // beginStructural hangs the jsdom harness). A fresh key has no stored
  // entry, so defaultFoldOpen runs: standard + running opens it; the tier
  // switch alone would not — the switch branch deliberately keeps the old
  // value so a concise-collapsed fold stays collapsed across to standard.
  sessionExperience.hydrateSessionExperience("standard");
  const fresh: Item[] = [
    { kind: "user", id: "u3", text: "ask three" },
    { kind: "assistant", id: "a3", text: "streaming more", reasoning: "third thought", streaming: true, workDurationMs: 900 },
  ];
  await harness.render(fresh, { running: true });
  await harness.flush();
  ok(container.querySelector(".turn-collapse--open"), "standard opens a fresh running fold (precondition)");
  await act(async () => {
    window.dispatchEvent(new CustomEvent("reasonix:collapse-all-folds"));
  });
  await harness.flush();
  ok(!container.querySelector(".turn-collapse--open"), "B: collapse-all closes every work process");
  // The next reconcile passes (segmentStates/flush) must not spring it back —
  // this is the userOverridden pin; without it the running branch re-opens.
  await harness.settle();
  await harness.render(fresh, { running: true });
  await harness.flush();
  ok(!container.querySelector(".turn-collapse--open"), "B: the running reconcile tick cannot spring the collapse back");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
