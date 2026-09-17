// Task 160: the "load older" button is the default entry point for older history.
// It has to work while the viewport already sits at scrollTop === 0 — the case the
// scroll trigger can never serve, because a parked viewport emits no further
// scroll events and the direction signal has nothing left to measure.
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

console.log("\ntranscript load-older button");
const harness = await createTranscriptHarness({ deterministic: true, viewportHeight: 800, rowHeight: 24 });
try {
  const calls: Array<string | undefined> = [];
  const onLoadOlderHistory = async (_turn?: number, trigger?: string) => {
    calls.push(trigger);
    return true;
  };

  await harness.render(turns(20), {
    geometrySessionKey: "load-older",
    hasOlderHistory: true,
    historyStartTurn: 5,
    historyTotalTurns: 40,
    onLoadOlderHistory,
  });
  await harness.settle();
  const scroll = harness.scrollElement();
  // Park the viewport at the top: that is the state the scroll trigger can never
  // serve (no further scroll events, no scrollTop delta left to measure), so it is
  // exactly the state the button has to work in.
  await act(async () => { scroll.scrollTop = 0; });
  check(scroll.scrollTop === 0, "the viewport is parked at scrollTop 0");
  const button = harness.container.querySelector<HTMLButtonElement>(".chat-older");
  check(Boolean(button), "the load-older button renders at the transcript top");
  const topBeforeClick = scroll.scrollTop;
  await act(async () => { button?.click(); });
  await harness.flush();
  check(calls.length === 1 && calls[0] === "viewport-user",
    `clicking it requests a user-driven history load (${JSON.stringify(calls)})`);
  check(topBeforeClick === 0 && scroll.scrollTop === topBeforeClick, "the request needed no scrollTop delta at all");

  // The gates the scroll path uses stay in force: the button must not become a way
  // around them (task 160 explicitly keeps hasOlderHistory / loading / running).
  calls.length = 0;
  await harness.render(turns(20), {
    geometrySessionKey: "load-older-loading",
    hasOlderHistory: true,
    loadingOlderHistory: true,
    onLoadOlderHistory,
  });
  await harness.settle();
  check(harness.container.querySelector(".chat-older") === null, "an in-flight load hides the button behind the loading row");
  check(harness.container.querySelector(".transcript__older-status") != null, "the loading row still reports progress");

  await harness.render(turns(20), {
    geometrySessionKey: "load-older-running",
    hasOlderHistory: true,
    running: true,
    onLoadOlderHistory,
  });
  await harness.settle();
  check(harness.container.querySelector(".chat-older") === null, "a running turn keeps the button gated, exactly like the scroll path");

  await harness.render(turns(20), {
    geometrySessionKey: "load-older-exhausted",
    hasOlderHistory: false,
    onLoadOlderHistory,
  });
  await harness.settle();
  check(harness.container.querySelector(".chat-older") === null, "no older history means no load-older button");

  await harness.render(turns(20), {
    geometrySessionKey: "load-older-unwired",
    hasOlderHistory: true,
  });
  await harness.settle();
  check(harness.container.querySelector(".chat-older") === null, "without an onLoadOlderHistory handler there is nothing to call");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
