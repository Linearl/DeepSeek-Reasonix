// Run: LANG=en node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/task931-scroll-rebound.test.ts
//
// Task 931 —— transcript 滚动回弹：陈旧块锚穿透 753 首对齐豁免面。
//   AC1 手势内继续滚动后，deferredStructural 恢复用「当前锚」而非 defer 时刻冻结锚
//      （endUserGesture 不再把 anchorValue 回拨到手势起点）。
//   AC2 被动流式 restore 携带陈旧锚（首对齐、与 alignedAnchor 无匹配）且目标
//      拉离当前视口超一屏 → 拒绝写回、outcome=anchor-stale、就地重锚、intent 保持 reader。
//   AC3 AC8 角例保留：surface 恢复出的块锚即使走被动 restore（显式恢复事务已过期）
//      首对齐仍放行。
//   AC4 显式 jump 大偏差仍写；小位移被动首对齐仍写。
//   源码钉：beginAnchorRestore 标 passive；endUserGesture 恢复用当前锚。

import { readFileSync } from "node:fs";
import {
  TranscriptKernel,
  type TranscriptKernelClock,
  type TranscriptKernelEvent,
  type TranscriptVisibleBlock,
  type TranscriptWriteRequest,
} from "../lib/transcriptKernel";

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
  setTimeout = (callback: () => void, delay: number) => {
    const id = ++this.sequence;
    this.timers.set(id, { at: this.time + delay, callback });
    return id as unknown as ReturnType<typeof setTimeout>;
  };
  clearTimeout = (handle: ReturnType<typeof setTimeout>) => { this.timers.delete(handle as unknown as number); };
  flushFrames() {
    const frames = [...this.frames.values()];
    this.frames.clear();
    frames.forEach((callback) => callback(this.time));
  }
  advance(ms: number) {
    this.time += ms;
    const ready = [...this.timers].filter(([, timer]) => timer.at <= this.time);
    ready.forEach(([id, timer]) => {
      this.timers.delete(id);
      timer.callback();
    });
  }
}

type Snapshot = Parameters<TranscriptKernel["beginUserGesture"]>[0];
function snapshot(scrollTop: number, blocks: Array<{ key: string; top: number; bottom: number }>): Snapshot {
  return { scrollTop, scrollHeight: 12_000, clientHeight: 400, visibleBlocks: blocks };
}
const viewportOf = (scrollTop: number, blocks: TranscriptVisibleBlock[]) => ({
  scrollTop, clientHeight: 400, visibleBlocks: blocks,
});

function harness(session = "t931") {
  const clock = new FakeClock();
  const events: TranscriptKernelEvent[] = [];
  const writes: TranscriptWriteRequest[] = [];
  const kernel = new TranscriptKernel({ clock, emit: (event) => events.push(event) });
  kernel.connectWriter((request) => {
    writes.push(request);
    return { accepted: true, offset: Number.isFinite(request.offset) ? request.offset : 11_600, changed: true };
  });
  kernel.replaceSurface(session);
  return { clock, events, kernel, writes };
}

console.log("\ntask 931: stale-anchor rebound — deferred resume uses current anchor; passive first alignment gated");

{
  // AC1: gesture-deferred structural resume must not revert the logical anchor.
  // The user parks on "user-msg", keeps scrolling mid-gesture to "step-3"; a
  // display-change arrives during the gesture and defers. On release the old
  // code resumed with the frozen user-msg anchor and the next correction
  // teleported the viewport back to the gesture start.
  const { kernel, writes } = harness("ac1");
  const atUserMsg = snapshot(500, [
    { key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 4_000 },
  ]);
  kernel.beginUserGesture(atUserMsg);
  kernel.observeNativeScroll(atUserMsg);
  ok(kernel.anchor.kind === "block" && kernel.anchor.blockKey === "user-msg",
    "AC1 setup: reader anchored on user-msg");

  const deferred = kernel.begin("display-change", kernel.anchor);
  ok(deferred === null, "AC1 setup: structural begin during gesture defers");

  // The reader keeps scrolling inside the same gesture.
  const further = snapshot(900, [
    { key: "step-3", top: 880, bottom: 960 }, { key: "tail-block", top: 960, bottom: 4_000 },
  ]);
  kernel.observeNativeScroll(further);
  ok(kernel.anchor.kind === "block" && kernel.anchor.blockKey === "step-3",
    "AC1: mid-gesture displacement advances the logical anchor");

  const resumed = kernel.endUserGesture();
  ok(kernel.anchor.kind === "block" && kernel.anchor.blockKey === "step-3",
    "AC1: gesture release resumes with the CURRENT anchor (no revert to the defer-time anchor)");
  ok(resumed?.status === "active", "AC1: the deferred structural transaction resumes");

  kernel.advanceGeometry();
  const written = resumed !== null && kernel.correctAnchor(resumed, (key) => key === "step-3" ? 880 : 480, viewportOf(900, [
    { key: "step-3", top: 880, bottom: 960 }, { key: "tail-block", top: 960, bottom: 4_000 },
  ]));
  ok(written && writes[writes.length - 1]?.offset === 900,
    "AC1: the resumed correction writes the reader's actual position (900), not the gesture start (500)");
}

{
  // AC2: the rebound itself — a passive streaming restore carries a stale
  // anchor that was never aligned on this viewport (alignedAnchor null), and
  // its target is far from where the reader actually is. Pre-931 the first
  // alignment was exempt and the write teleported the view back.
  const { events, kernel, writes } = harness("ac2");
  const atUserMsg = snapshot(500, [
    { key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 4_000 },
  ]);
  kernel.beginUserGesture(atUserMsg);
  kernel.observeNativeScroll(atUserMsg);
  kernel.endUserGesture();
  ok(kernel.intent === "reader" && kernel.anchor.kind === "block" && kernel.anchor.blockKey === "user-msg",
    "AC2 setup: reader anchored on user-msg, never aligned yet");

  // Unobserved native momentum: the viewport walked far away without any
  // observeNativeScroll call (no gesture ⇒ anchor stays stale).
  const writesBefore = writes.length;
  kernel.advanceGeometry();
  const stale = kernel.begin("restore", kernel.anchor, { passive: true });
  const refused = stale !== null && kernel.correctAnchor(stale, (key) => key === "user-msg" ? 480 : undefined, viewportOf(2_200, [
    { key: "step-9", top: 2_180, bottom: 2_260 }, { key: "tail-block", top: 2_260, bottom: 8_000 },
  ]));
  ok(refused === false, "AC2: the stale passive first alignment is refused (no teleport)");
  ok(writes.length === writesBefore, "AC2: refusing performs zero writes");
  ok(stale?.status === "cancelled", "AC2: the stale transaction reaches a terminal state");
  ok(events.some((event) => event.outcome === "anchor-stale"), "AC2: the refusal is observable as outcome=anchor-stale");
  ok(kernel.anchor.kind === "block" && kernel.anchor.blockKey === "step-9",
    "AC2: the kernel re-anchors to the block the reader is actually looking at");
  ok(kernel.intent === "reader", "AC2: the reader intent survives the refusal");
}

{
  // AC3 (AC8 corner): replaceSurface restored a block anchor; the explicit
  // restore transaction expired before the block was measurable, so the FIRST
  // correction rides a passive restore — it must still write (session-restore
  // semantics), and the exemption is consumed.
  const { kernel, writes } = harness("ac3-leave");
  const atUserMsg = snapshot(500, [
    { key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 4_000 },
  ]);
  kernel.beginUserGesture(atUserMsg);
  kernel.observeNativeScroll(atUserMsg);
  kernel.endUserGesture();

  kernel.replaceSurface("ac3-back"); // saves user-msg anchor under ac3-leave
  kernel.replaceSurface("ac3-leave"); // restores it — the session-restore path
  ok(kernel.anchor.kind === "block" && kernel.anchor.blockKey === "user-msg",
    "AC3 setup: switching back restores the saved block anchor");

  const writesBefore = writes.length;
  kernel.advanceGeometry();
  const restore = kernel.begin("restore", kernel.anchor, { passive: true });
  const written = restore !== null && kernel.correctAnchor(restore, (key) => key === "user-msg" ? 5_000 : undefined, viewportOf(0, [
    { key: "user-msg", top: 5_000, bottom: 5_400 },
  ]));
  ok(written && writes.length === writesBefore + 1,
    "AC3: the surface-restored anchor's first passive correction still restores (AC8 kept)");

  // The exemption is consumed: a second passive restore with the same key far
  // from the viewport is gated like any stale anchor.
  kernel.advanceGeometry();
  const writesBeforeSecond = writes.length;
  const second = kernel.begin("restore", kernel.anchor, { passive: true });
  const secondWritten = second !== null && kernel.correctAnchor(second, (key) => key === "user-msg" ? 5_000 : undefined, viewportOf(3_000, [
    { key: "step-40", top: 2_980, bottom: 3_060 },
  ]));
  ok(secondWritten === false && writes.length === writesBeforeSecond,
    "AC3: the exemption is one-shot — the next far passive restore is gated");
}

{
  // AC4: explicit intents keep their exemptions.
  const { kernel, writes } = harness("ac4");
  kernel.advanceGeometry();
  const jump = kernel.begin("jump", { kind: "block", blockKey: "far-question", offsetPx: 0 });
  const jumpWritten = jump !== null && kernel.correctAnchor(jump, (key) => key === "far-question" ? 9_000 : undefined, viewportOf(40, [
    { key: "tail-block", top: 0, bottom: 400 },
  ]));
  ok(jumpWritten && writes[writes.length - 1]?.offset === 9_000,
    "AC4: an explicit jump with a viewport-sized gap still writes");

  const { kernel: kernel2, writes: writes2 } = harness("ac4b");
  kernel2.beginUserGesture(snapshot(520, [{ key: "near-block", top: 480, bottom: 900 }]));
  kernel2.observeNativeScroll(snapshot(520, [{ key: "near-block", top: 480, bottom: 900 }]));
  kernel2.endUserGesture();
  kernel2.advanceGeometry();
  const near = kernel2.begin("restore", kernel2.anchor, { passive: true });
  const nearWritten = near !== null && kernel2.correctAnchor(near, (key) => key === "near-block" ? 480 : undefined, viewportOf(520, [
    { key: "near-block", top: 480, bottom: 900 },
  ]));
  ok(nearWritten && writes2[writes2.length - 1]?.offset === 520,
    "AC4: a small-displacement passive first alignment still writes (fresh capture unaffected)");
}

{
  // 753 regression: same-key repeat alignment with large drift still refuses
  // with the original anchor-drift outcome (the new anchor-stale outcome only
  // covers first alignments).
  const { events, kernel, writes } = harness("ac5");
  const atUserMsg = snapshot(500, [
    { key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 4_000 },
  ]);
  kernel.beginUserGesture(atUserMsg);
  kernel.observeNativeScroll(atUserMsg);
  kernel.endUserGesture();
  kernel.advanceGeometry();
  const first = kernel.begin("restore", kernel.anchor, { passive: true });
  kernel.correctAnchor(first!, (key) => key === "user-msg" ? 480 : undefined, viewportOf(500, [
    { key: "user-msg", top: 480, bottom: 560 },
  ]));
  kernel.advanceGeometry();
  const writesBefore = writes.length;
  const second = kernel.begin("restore", kernel.anchor, { passive: true });
  const refused = second !== null && kernel.correctAnchor(second, (key) => key === "user-msg" ? 30 : undefined, viewportOf(500, [
    { key: "answer-block", top: 520, bottom: 900 },
  ]));
  ok(refused === false && writes.length === writesBefore,
    "753 regression: same-key drifted repeat alignment is still refused without writes");
  ok(events.some((event) => event.outcome === "anchor-drift"),
    "753 regression: the same-key refusal keeps outcome=anchor-drift (distinct from anchor-stale)");
}

{
  // No viewport context: pre-931 byte-identical behavior (the gate needs the
  // viewport facts to decide).
  const { kernel, writes } = harness("compat");
  kernel.beginUserGesture(snapshot(500, [{ key: "user-msg", top: 480, bottom: 560 }]));
  kernel.observeNativeScroll(snapshot(500, [{ key: "user-msg", top: 480, bottom: 560 }]));
  kernel.endUserGesture();
  kernel.advanceGeometry();
  const restore = kernel.begin("restore", kernel.anchor, { passive: true });
  const written = restore !== null && kernel.correctAnchor(restore, (key) => key === "user-msg" ? 4_000 : undefined);
  ok(written && writes[writes.length - 1]?.offset === 4_020,
    "compat: without viewport context the correction keeps the pre-931 behavior");
}

{
  // Source pins: the hook must keep the passive wiring and the current-anchor
  // resume (regression guards against silent reverts).
  const hookSource = readFileSync(new URL("../lib/useTranscriptKernel.ts", import.meta.url), "utf8");
  ok(hookSource.includes('kernel.begin("restore", restoreAnchor, { passive: true })'),
    "source pin: beginAnchorRestore marks the streaming restore passive");
  const kernelSource = readFileSync(new URL("../lib/transcriptKernel.ts", import.meta.url), "utf8");
  ok(kernelSource.includes("this.begin(deferred.kind, this.anchorValue, { passive: deferred.passive })"),
    "source pin: endUserGesture resumes the deferred transaction with the CURRENT anchor");
  ok(!kernelSource.includes("kind, anchor }") && !/deferredStructural = \{ generation: [^}]*anchor/.test(kernelSource),
    "source pin: the deferred record no longer freezes the defer-time anchor");
}

if (failed > 0) {
  throw new Error(`${failed} task 931 checks failed`);
}
console.log(`  ${passed} checks passed`);
