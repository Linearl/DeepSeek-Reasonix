// tl 1184: tab-switch scroll position must not degrade to "reader" when the
// user was parked at the bottom while output was streaming.
//
// Root cause this locks down: during streaming the transcript keeps growing, so
// a scroll event that arrives after the user reached the bottom compares an OLD
// scrollTop against a NEW scrollHeight, concludes atBottom=false, demotes the
// intent to "reader" and stores a BLOCK anchor. Switching away and back then
// restored that block anchor and parked the view mid-transcript.
//
// Two directions are asserted, and the second one matters just as much as the
// first: a user who deliberately scrolled UP must come back exactly where they
// left, NOT be yanked to the bottom.
import { TranscriptKernel, type TranscriptViewportSnapshot } from "../lib/transcriptKernel";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function snapshot(scrollTop: number, scrollHeight = 1000, clientHeight = 500): TranscriptViewportSnapshot {
  return {
    scrollTop,
    scrollHeight,
    clientHeight,
    visibleBlocks: [
      { key: "blk-early", top: 0, bottom: 200 },
      { key: "blk-mid", top: 200, bottom: 600 },
      { key: "blk-tail", top: 600, bottom: 1000 },
    ],
  };
}

// Distance from the bottom: scrollHeight - clientHeight - scrollTop.
// atBottom iff <= 4 (BOTTOM_THRESHOLD_PX).
const AT_BOTTOM_TOP = 500; // 1000 - 500 - 500 = 0 -> at bottom
const SCROLLED_UP_TOP = 100; // 1000 - 500 - 100 = 400 -> reader

function freshKernel() {
  return new TranscriptKernel({ clock: {
    now: () => 0,
    requestAnimationFrame: () => 1,
    cancelAnimationFrame: () => {},
    setTimeout: () => 0 as unknown as ReturnType<typeof setTimeout>,
    clearTimeout: () => {},
  } });
}

process.stdout.write("\n[tl 1184] tab-switch bottom restoration\n");

// --- Direction 1: parked at bottom, anchor contaminated by streaming growth ---
{
  const k = freshKernel();
  k.replaceSurface("tab-A");

  // User reaches the bottom and keeps the tail follow alive.
  k.beginUserGesture(snapshot(AT_BOTTOM_TOP));
  k.observeNativeScroll(snapshot(AT_BOTTOM_TOP));
  ok(k.intent === "tail", "D1: after reaching bottom, intent is tail");

  // Streaming growth arrives: same scrollTop, taller content -> the naive
  // atBottom computation now says "not at bottom" and demotes to reader.
  k.beginUserGesture(snapshot(AT_BOTTOM_TOP, 1600));
  k.observeNativeScroll(snapshot(AT_BOTTOM_TOP, 1600));
  ok(k.intent === "reader", "D1: growth alone can still demote (documents the bug window)");

  // Switch away and back. The growth-demoted (reader/block) state must NOT win:
  // the tab was last genuinely parked at the bottom, so we come back in tail.
  k.replaceSurface("tab-B"); // parks tab-A; note tab-A's atBottomValue is now false
  k.replaceSurface("tab-A");
  ok(
    k.intent === "tail",
    "D1: switching back restores tail for a tab that was at the bottom before growth",
  );
  ok(k.anchor.kind === "tail", "D1: restored anchor is tail, not the stale block");
}

// --- Direction 2: deliberately scrolled up must stay scrolled up ---
{
  const k = freshKernel();
  k.replaceSurface("tab-A");

  // User scrolls up on purpose.
  k.beginUserGesture(snapshot(SCROLLED_UP_TOP));
  k.observeNativeScroll(snapshot(SCROLLED_UP_TOP));
  ok(k.intent === "reader", "D2: scrolling up sets reader");

  const anchorBefore = k.anchor;

  k.replaceSurface("tab-B");
  k.replaceSurface("tab-A");

  ok(k.intent === "reader", "D2: reader survives the round trip (must NOT be yanked to bottom)");
  ok(
    anchorBefore.kind !== "tail" ? k.anchor.kind !== "tail" : true,
    "D2: reader anchor is not converted into tail",
  );
}

// --- Direction 3: first ever visit of a session still follows the tail ---
{
  const k = freshKernel();
  k.replaceSurface("brand-new");
  ok(k.intent === "tail", "D3: first visit defaults to tail follow");
}

// --- Direction 4 (tl 1184b): SECOND round trip must not yank the reader -------
// User-reported failure: "来回切换 2 次，第一次正常，第二次还是有问题".
// Root cause: replaceSurface's else-branch never set atBottomValue, so the field
// carried the PREVIOUS surface's verdict into the restored one (lastScrollTop is
// a single global, so a bottom-parked neighbour leaks its `true` in). On the
// second switch back the kernel wrongly believed the reader was at the tail.
{
  const k = freshKernel();
  // tab-A: reader parked mid-transcript.
  k.replaceSurface("tab-A");
  k.beginUserGesture(snapshot(SCROLLED_UP_TOP));
  k.observeNativeScroll(snapshot(SCROLLED_UP_TOP));
  ok(k.intent === "reader", "D4: tab-A parked as reader");

  // tab-B: parked at the bottom (this is what poisons the shared state).
  k.replaceSurface("tab-B");
  k.observeNativeScroll(snapshot(AT_BOTTOM_TOP));
  ok(k.intent === "tail", "D4: tab-B parked at tail");

  // First round trip back to tab-A — this one already worked before the fix.
  k.replaceSurface("tab-A");
  ok(k.intent === "reader", "D4: first switch back keeps reader");
  k.beginUserGesture(snapshot(SCROLLED_UP_TOP));
  k.observeNativeScroll(snapshot(SCROLLED_UP_TOP));
  ok(k.intent === "reader", "D4: first return still reader after restore scroll");

  // Second round trip — the reported failure window.
  k.replaceSurface("tab-B");
  k.observeNativeScroll(snapshot(AT_BOTTOM_TOP));
  k.replaceSurface("tab-A");
  ok(k.intent === "reader", "D4: SECOND switch back must still be reader (regression)");
  ok(k.anchor.kind !== "tail", "D4: second restore anchor is not tail");
  k.beginUserGesture(snapshot(SCROLLED_UP_TOP));
  k.observeNativeScroll(snapshot(SCROLLED_UP_TOP));
  ok(k.intent === "reader", "D4: second return still reader after restore scroll");
}

// --- Direction 5 (tl 1184b): bottom-parked tab stays bottom across two trips -
{
  const k = freshKernel();
  k.replaceSurface("tab-C");
  k.observeNativeScroll(snapshot(AT_BOTTOM_TOP)); // at bottom
  k.replaceSurface("tab-D");
  k.beginUserGesture(snapshot(SCROLLED_UP_TOP));
  k.observeNativeScroll(snapshot(SCROLLED_UP_TOP)); // D is a reader
  k.replaceSurface("tab-C");
  ok(k.intent === "tail", "D5: first switch back restores tail for bottom-parked tab");
  k.replaceSurface("tab-D");
  k.replaceSurface("tab-C");
  ok(k.intent === "tail", "D5: second switch back still restores tail (no reader leak)");
}

process.stdout.write(`\n[tl 1184] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
