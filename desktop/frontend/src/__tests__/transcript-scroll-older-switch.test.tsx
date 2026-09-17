// Task 160: the scroll-driven history trigger is an experiment
// (`experimental_auto_load_older`). These assertions pin the contract the switch
// promises: off (the default) means no scroll-triggered load at all, on means an
// upward gesture at the top loads once — and no programmatic scroll ever counts.
import { act } from "react";
import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;
function check(condition: unknown, label: string): void {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function turns(count: number): Item[] {
  return Array.from({ length: count }, (_, index) => [
    { kind: "user", id: `user-${index}`, text: `question ${index}`, historyTurn: index + 1 } as Item,
    { kind: "assistant", id: `answer-${index}`, text: `answer ${index}`, reasoning: "", streaming: false } as Item,
  ]).flat();
}

console.log("\ntranscript scroll-to-load-older switch");
const harness = await createTranscriptHarness({ deterministic: true, viewportHeight: 800, rowHeight: 24 });
try {
  const preference = await harness.loadModule<{
    setAutoLoadOlderEnabled: (next: boolean) => void;
    isAutoLoadOlderEnabled: () => boolean;
  }>("/src/lib/autoLoadOlderPreference.ts");

  const calls: Array<string | undefined> = [];
  const onLoadOlderHistory = async (_turn?: number, trigger?: string) => {
    calls.push(trigger);
    return true;
  };

  // The trigger only serves a viewport that is already parked at the top, and the
  // kernel can leave the viewport at the tail between gestures, so each upward
  // wheel starts from the top explicitly.
  const wheelUp = async () => {
    await act(async () => {
      harness.scrollElement().scrollTop = 0;
      harness.scrollElement().dispatchEvent(new harness.dom.window.WheelEvent("wheel", { deltaY: -140, bubbles: true }));
    });
    await harness.flush();
  };

  await harness.render(turns(20), {
    geometrySessionKey: "auto-load-older",
    hasOlderHistory: true,
    onLoadOlderHistory,
  });
  await harness.settle();
  check(preference.isAutoLoadOlderEnabled() === false, "the experiment is off by default");
  // Probe: confirm the wheel event actually reaches the scroll element in this
  // harness before blaming the handler for not reacting.
  const wheelDeltas: unknown[] = [];
  harness.scrollElement().addEventListener("wheel", (event) => { wheelDeltas.push((event as WheelEvent).deltaY); });
  await act(async () => { harness.scrollElement().scrollTop = 0; });
  check(harness.scrollElement().scrollTop === 0, "the viewport is parked at the top");
  await wheelUp();
  check(calls.length === 0, `switch off: scrolling up at the top loads nothing (${JSON.stringify(calls)})`);

  await act(async () => { preference.setAutoLoadOlderEnabled(true); });
  await harness.flush();
  await wheelUp();
  check(wheelDeltas.length > 0 && wheelDeltas[0] === -140,
    `probe: the dispatched wheel reached the element with its deltaY (${JSON.stringify(wheelDeltas)})`);
  check(calls.length === 1 && calls[0] === "viewport-user",
    `switch on: one upward wheel at the top requests a user-driven load (${JSON.stringify(calls)})`);

  // A downward wheel is not a history intent.
  await act(async () => {
    harness.scrollElement().dispatchEvent(new harness.dom.window.WheelEvent("wheel", { deltaY: 140, bubbles: true }));
  });
  await harness.flush();
  check(calls.length === 1, "a downward wheel does not load older history");

  // Programmatic scrolling must never trigger a load: it emits no wheel or key
  // input, which is what the widened trigger keys off (verification item 3).
  const before = calls.length;
  await act(async () => {
    harness.scrollElement().scrollTop = 0;
    harness.scrollElement().dispatchEvent(new Event("scroll"));
  });
  await harness.settle();
  check(calls.length === before, "a programmatic scroll back to the top loads nothing");

  await act(async () => {
    harness.scrollElement().dispatchEvent(new harness.dom.window.KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
  });
  await harness.flush();
  check(calls.length === before + 1, "switch on: ArrowUp at the top is an equivalent entry point");

  await act(async () => { preference.setAutoLoadOlderEnabled(false); });
  await harness.flush();
  await act(async () => { harness.scrollElement().scrollTop = 0; });
  await wheelUp();
  check(calls.length === before + 1, "turning the switch back off stops the scroll trigger immediately");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
