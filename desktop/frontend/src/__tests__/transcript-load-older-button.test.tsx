// Task 160: the "load older" button is the default entry point for older history.
// It has to work while the viewport already sits at scrollTop === 0 — the case the
// scroll trigger can never serve, because a parked viewport emits no further
// scroll events and the direction signal has nothing left to measure.
//
// Task 448 (384 收尾 + B3) evolved the gate contract: `running` and
// `olderHistoryError` no longer hide the button — only the controller's own two
// gates (hasOlder / loading) do. The task-384 controller-layer unlock was
// unreachable while the component layer still refused; this pins both halves.
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

  // The two states the controller itself gates on stay in force: the button must
  // not become a way around hasOlder / loading (task 160). `running` and
  // `olderHistoryError` are NOT gates any more — task 448 (384 收尾 + B3).
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

  // 任务 448 / 384 收尾：running 不再闸组件层（滚动/按钮/跳转此前全部静默拒绝）。
  await harness.render(turns(20), {
    geometrySessionKey: "load-older-running",
    hasOlderHistory: true,
    running: true,
    onLoadOlderHistory,
  });
  await harness.settle();
  const runningButton = harness.container.querySelector<HTMLButtonElement>(".chat-older");
  check(Boolean(runningButton), "a running turn still offers the load-older button (task 448 B2)");
  calls.length = 0;
  await act(async () => { runningButton?.click(); });
  await harness.flush();
  check(calls.length === 1 && calls[0] === "viewport-user",
    `clicking it during a running turn requests the load (${JSON.stringify(calls)})`);

  // 任务 448 B3（zcode 借鉴：失败不进 error 态）：一次历史页失败不锁 UI ——
  // 按钮仍在，点它发新请求并由 history_older_start 清 error；失败行继续带原因。
  await harness.render(turns(20), {
    geometrySessionKey: "load-older-error",
    hasOlderHistory: true,
    olderHistoryError: "history identity changed",
    onLoadOlderHistory,
  });
  await harness.settle();
  const retryButton = harness.container.querySelector<HTMLButtonElement>(".chat-older");
  check(Boolean(retryButton), "a failed load keeps the load-older button visible (task 448 B3, no error lock-out)");
  check(harness.container.querySelector(".transcript__older-status") != null, "the failure row still reports the reason");
  calls.length = 0;
  await act(async () => { retryButton?.click(); });
  await harness.flush();
  check(calls.length === 1 && calls[0] === "viewport-user",
    `the button retries after a failure (${JSON.stringify(calls)})`);

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
