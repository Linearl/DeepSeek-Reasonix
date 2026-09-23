// Run: node node_modules/tsx/dist/cli.mjs src/__tests__/task267-tail-follow.test.ts
//
// Task 267 — 转录贴底维持链接线（点 ↓ 露底后不再弹回）:
//   R1b (main)   — a gesture no longer demotes intent/anchor; only a REAL
//                   displacement (observeNativeScroll) does. A non-displacing
//                   tap must keep the tail follow alive.
//   subsidy      — tail-sync dropped during a gesture is re-armed at
//                   endUserGesture, and the geometry commit re-arms whenever
//                   its subsidy write did not land.
//   hard metric  — after a non-displacing gesture the scroll write to the
//                   bottom is Infinity (tail-follow), never a block top: the
//                   "bounces back to the last user message" signature.
// Also pins: a real upward displacement still hands the view to the reader
// (gestures must keep working), and a reader end never gets yanked downward.

import { TranscriptKernel, type TranscriptKernelClock, type TranscriptKernelEvent } from "../lib/transcriptKernel";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

class FakeClock implements TranscriptKernelClock {
  time = 0;
  sequence = 0;
  frames = new Map<number, FrameRequestCallback>();
  timers = new Map<number, { at: number; callback: () => void }>();
  now = () => this.time;
  requestAnimationFrame = (callback: FrameRequestCallback) => { const id = ++this.sequence; this.frames.set(id, callback); return id; };
  cancelAnimationFrame = (id: number) => { this.frames.delete(id); };
  setTimeout = (callback: () => void, delay: number) => { const id = ++this.sequence; this.timers.set(id, { at: this.time + delay, callback }); return id as unknown as ReturnType<typeof setTimeout>; };
  clearTimeout = (handle: ReturnType<typeof setTimeout>) => { this.timers.delete(handle as unknown as number); };
  flushFrames() { const frames = [...this.frames.values()]; this.frames.clear(); frames.forEach((callback) => callback(this.time)); }
}

type Snapshot = Parameters<TranscriptKernel["beginUserGesture"]>[0];
function snapshot(scrollTop: number, blocks: Array<{ key: string; top: number; bottom: number }>): Snapshot {
  return { scrollTop, scrollHeight: 4000, clientHeight: 800, visibleBlocks: blocks };
}
// The view is parked at the bottom: the tail fills the viewport, the "last
// user message" block straddles the top edge — the exact bounce-back scene.
const atBottom = (scrollTop: number): Snapshot => snapshot(scrollTop, [
  { key: "user-last", top: scrollTop - 120, bottom: scrollTop + 60 },
  { key: "tail-block", top: scrollTop + 60, bottom: scrollTop + 760 },
]);

function harness() {
  const clock = new FakeClock();
  const events: TranscriptKernelEvent[] = [];
  const writes: Array<{ offset: number; owner: string; intent: string }> = [];
  const kernel = new TranscriptKernel({ clock, emit: (event) => events.push(event) });
  kernel.connectWriter((request) => {
    writes.push({ offset: request.offset, owner: request.owner, intent: request.intent });
    return { accepted: true, offset: request.offset, changed: request.offset !== 0 };
  });
  kernel.replaceSurface("t267");
  return { clock, kernel, events, writes };
}

// ── R1b: a non-displacing tap keeps the tail follow ─────────────────────────
{
  const { clock, kernel, writes } = harness();
  ok(kernel.intent === "tail" && kernel.anchor.kind === "tail", "surface starts parked on the tail");

  // Pointerdown on the transcript (copy button / expand reasoning) with NO
  // scroll event afterwards — the old code demoted intent+anchor here.
  kernel.beginUserGesture(atBottom(3200), "native");
  ok(kernel.intent === "tail" && kernel.anchor.kind === "tail",
    "gesture entry no longer demotes a tail view to reader (R1b)");
  kernel.endUserGesture();
  ok(kernel.intent === "tail" && kernel.anchor.kind === "tail",
    "a non-displacing gesture ends still owning the tail");

  // The subsidy the gesture swallowed re-arms on the next frame and writes
  // the bottom — the hard metric: Infinity, never a block top.
  kernel.scheduleTailSync();
  clock.flushFrames();
  const afterTap = writes.length;
  ok(afterTap >= 1 && writes[writes.length - 1]?.offset === Number.POSITIVE_INFINITY,
    "the re-armed subsidy writes the bottom (Infinity) after the tap");
  ok(writes.every((write) => write.offset === Number.POSITIVE_INFINITY || write.owner !== "tail-follow"),
    "no write ever targets a block top — the bounce-back signature is gone");

  // Streaming growth right after the tap: geometry commit's tail-sync works
  // again because intent stayed tail.
  kernel.advanceGeometry();
  const transaction = kernel.begin("tail-sync", { kind: "tail" });
  ok(transaction !== null, "a geometry commit can open tail-sync right after a non-displacing tap");
  if (transaction) {
    kernel.advanceGeometry();
    ok(kernel.correctAnchor(transaction, () => 9999) === true,
      "correctAnchor follows the tail (writes Infinity) instead of pinning the captured block");
  }
  const blockWrites = writes.filter((write) => Number.isFinite(write.offset));
  ok(blockWrites.length === 0, "no finite block-top scroll write exists anywhere in the run");
}

// ── a real displacement still hands the view to the reader ─────────────────
{
  const { clock, kernel, writes } = harness();
  kernel.beginUserGesture(atBottom(3200), "native");
  // The user actually scrolls up 400px: scroll fires, bottom gap > 4px.
  kernel.observeNativeScroll(atBottom(2800));
  ok(kernel.intent === "reader" && kernel.anchor.kind === "block",
    "a real upward displacement demotes to reader with a block anchor");
  kernel.endUserGesture();
  clock.flushFrames();
  ok(kernel.intent === "reader", "the reader intent survives gesture end (read-history is respected)");
  ok(writes.length === 0, "no subsidy write yanks a reading user back to the bottom");
}

// ── scrolling back to the bottom re-promotes the tail (existing path kept) ─
{
  const { kernel } = harness();
  kernel.beginUserGesture(atBottom(3200), "native");
  kernel.observeNativeScroll(atBottom(2800));
  ok(kernel.intent === "reader", "scrolled away from the bottom");
  kernel.observeNativeScroll(atBottom(3998)); // within BOTTOM_THRESHOLD_PX
  ok(kernel.intent === "tail" && kernel.anchor.kind === "tail",
    "scrolling back within 4px of the bottom re-promotes the tail during the gesture");
  kernel.endUserGesture();
}

// ── gesture-time writes stay gated (no regression of the lease window) ─────
{
  const { clock, kernel, writes } = harness();
  kernel.beginUserGesture(atBottom(3200), "native");
  kernel.scheduleTailSync();
  clock.flushFrames();
  ok(writes.length === 0, "scheduleTailSync declines while a finger is down (gesture gate intact)");
  kernel.advanceGeometry();
  const transaction = kernel.begin("tail-sync", { kind: "tail" });
  ok(transaction === null, "tail-sync cannot open mid-gesture (gate intact)");
}

// ── selection gestures keep an anchor edge without demoting tail ───────────
{
  const { kernel } = harness();
  kernel.beginUserGesture(atBottom(3200), "selection");
  ok(kernel.intent === "tail" && kernel.anchor.kind === "tail",
    "a selection gesture in a tail view keeps the tail anchor edge");
  kernel.endUserGesture();
  ok(kernel.intent === "tail", "selection end leaves the tail follow intact");
}

// ── endUserGesture defers/replays structural work exactly as before ────────
{
  const { kernel, writes } = harness();
  kernel.beginUserGesture(atBottom(3200), "native");
  // A composer resize arrives mid-gesture: still deferred, still replayed.
  const deferredResult = kernel.begin("composer-resize", { kind: "block", blockKey: "user-last", offsetPx: 40 });
  ok(deferredResult === null, "structural kinds still defer behind a gesture");
  const resumed = kernel.endUserGesture();
  ok(resumed !== null && resumed.kind === "composer-resize",
    "the deferred structural transaction replays at gesture end");
  void writes;
}

console.log(`\n${passed} checks passed, ${failed} failed`);
if (failed > 0) process.exit(1);
