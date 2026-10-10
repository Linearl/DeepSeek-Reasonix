// Run: LANG=en node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/task753-scroll-anchor.test.ts
//
// Task 753 — 根因②：滚动锚三修（定位报告 §2.4）。
//   AC7  锚漂移护栏：reader 锚已对齐过一次后，锚块位移超过一屏的复位被拒绝
//        并重锚到当前视口顶块（"跳回用户消息不跟底"的直接观感治法）；
//        首次对齐（新 capture / 显式 jump / 会话恢复）永远豁免（AC8 语义）。
//   AC6  native-clamp 不再滞留 1s TTL：写入被原生钳制的事务立即 finish，
//        下一帧 tail-sync 可以立刻重试（折叠动画期间跟底瞬断治法）。
//   AC9  原生 wheel 手势后备：kernel 层 renewNativeGesture 建立/续租手势租约
//        （jsdom 无布局引擎，hook 接线由源码钉覆盖——React 合成 onWheelCapture
//        在 jsdom 不委托，恰是真实 WebView 缺口的镜像，行为级只能钉 kernel）。
//   源码钉：useTranscriptKernel 的 wheel listener 在 deltaY≠0 时续租、
//        settleGeometry 给 correctAnchor 传视口上下文（防回退）。

import { readFileSync } from "node:fs";
import {
  TranscriptKernel,
  type TranscriptKernelClock,
  type TranscriptKernelEvent,
  type TranscriptVisibleBlock,
  type TranscriptViewportSnapshot,
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
  return { scrollTop, scrollHeight: 4_000, clientHeight: 400, visibleBlocks: blocks };
}
const viewportOf = (scrollTop: number, blocks: TranscriptVisibleBlock[]) => ({
  scrollTop, clientHeight: 400, visibleBlocks: blocks,
});

function harness(writerReply?: (request: TranscriptWriteRequest) => { accepted: boolean; reason?: string }) {
  const clock = new FakeClock();
  const events: TranscriptKernelEvent[] = [];
  const writes: TranscriptWriteRequest[] = [];
  const kernel = new TranscriptKernel({ clock, emit: (event) => events.push(event) });
  kernel.connectWriter((request) => {
    writes.push(request);
    if (writerReply) return { accepted: writerReply(request).accepted, offset: request.offset, changed: true, reason: writerReply(request).reason };
    return { accepted: true, offset: Number.isFinite(request.offset) ? request.offset : 3_600, changed: true };
  });
  kernel.replaceSurface("t753");
  return { clock, events, kernel, writes };
}

/** The real reader-anchor chain: gesture in, REAL upward displacement, gesture
 * out — capture pins the viewport-top block ("user-msg" here). */
function parkReaderOnUserMessage(kernel: TranscriptKernel) {
  const blocks = [{ key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 1_200 }];
  kernel.beginUserGesture(snapshot(500, blocks), "native");
  kernel.observeNativeScroll(snapshot(500, blocks));
  kernel.endUserGesture();
}

console.log("\ntask 753-2: anchor drift guard, native-clamp fast finish, wheel lease");

{
  // AC7 main scene: the reader is parked on "user-msg" (top 480, the anchor
  // captured at scrollTop 500 with offsetPx 20). A large collapse ABOVE the
  // anchor pulls its top to 30 — re-applying the stale anchor would put the
  // viewport at 50, a 450px teleport away from where the reader actually is.
  const { events, kernel, writes } = harness();
  parkReaderOnUserMessage(kernel);
  ok(kernel.intent === "reader" && kernel.anchor.kind === "block" && kernel.anchor.blockKey === "user-msg",
    "setup: the reader anchor is the captured viewport-top block");

  // First alignment: legitimate, exempt, records the aligned top.
  kernel.advanceGeometry();
  const first = kernel.begin("display-change", kernel.anchor);
  const firstBlocks = [{ key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 1_200 }];
  const firstWritten = first !== null && kernel.correctAnchor(first, (key) => key === "user-msg" ? 480 : 560, viewportOf(500, firstBlocks));
  ok(firstWritten, "AC7: the first alignment of a fresh anchor still writes");
  ok(first?.status === "committed", "AC7: the exempt alignment commits (baseline recorded for the next round)");

  // A large collapse above the anchor: user-msg moves 480 -> 30 while the
  // reader stayed at scrollTop 500. The stale restore must be refused.
  kernel.advanceGeometry();
  const second = kernel.begin("display-change", kernel.anchor);
  const driftBlocks = [{ key: "user-msg", top: 30, bottom: 110 }, { key: "answer-block", top: 520, bottom: 900 }];
  const writeCountBefore = writes.length;
  const refused = second !== null && kernel.correctAnchor(second, (key) => key === "user-msg" ? 30 : key === "answer-block" ? 520 : 560, viewportOf(500, driftBlocks));
  ok(refused === false, "AC7: the drifted-anchor restore is refused (no write)");
  ok(writes.length === writeCountBefore, "AC7: refusing the restore does not touch the viewport");
  ok(second?.status === "cancelled", "AC7: the refused transaction is finished (not held)");
  ok(events.some((event) => event.outcome === "anchor-drift"), "AC7: the refusal is observable as outcome=anchor-drift");
  ok(kernel.anchor.kind === "block" && kernel.anchor.blockKey === "answer-block" && kernel.anchor.offsetPx === -20,
    "AC7: the kernel re-anchors to the block the reader is actually looking at");
  ok(kernel.intent === "reader", "AC7: the reader intent survives the re-anchor (no tail demotion)");
}

{
  // AC7/AC8 exemption: a FIRST alignment of a different anchor with a huge
  // gap (explicit jump / fresh capture / surface restore) must still write.
  const { kernel } = harness();
  const blocks = [{ key: "far-target", top: 5_000, bottom: 5_400 }];
  kernel.advanceGeometry();
  const jump = kernel.begin("restore", { kind: "block", blockKey: "far-target", offsetPx: 0 });
  const written = jump !== null && kernel.correctAnchor(jump, (key) => key === "far-target" ? 5_000 : undefined, viewportOf(0, blocks));
  ok(written, "AC8: a first alignment with a viewport-sized gap still restores (restore/jump semantics kept)");
}

{
  // Small drift stays a normal correction: within the threshold the anchor
  // still follows and the baseline refreshes.
  const { kernel } = harness();
  parkReaderOnUserMessage(kernel);
  kernel.advanceGeometry();
  const first = kernel.begin("display-change", kernel.anchor);
  kernel.correctAnchor(first!, (key) => key === "user-msg" ? 480 : 560, viewportOf(500, [
    { key: "user-msg", top: 480, bottom: 560 }, { key: "tail-block", top: 560, bottom: 1_200 },
  ]));
  kernel.advanceGeometry();
  const second = kernel.begin("display-change", kernel.anchor);
  const written = second !== null && kernel.correctAnchor(second, (key) => key === "user-msg" ? 380 : 560, viewportOf(500, [
    { key: "user-msg", top: 380, bottom: 460 }, { key: "tail-block", top: 560, bottom: 1_200 },
  ]));
  ok(written, "AC7: drift within one viewport is still a normal correction");
  kernel.advanceGeometry();
  const third = kernel.begin("display-change", kernel.anchor);
  const thirdWritten = third !== null && kernel.correctAnchor(third, (key) => key === "user-msg" ? 370 : 560, viewportOf(500, [
    { key: "user-msg", top: 370, bottom: 450 }, { key: "tail-block", top: 560, bottom: 1_200 },
  ]));
  ok(thirdWritten, "AC7: the refreshed baseline keeps following small drifts");
}

{
  // Backward compatibility: no viewport context -> pre-753 behavior exactly
  // (a drifted anchor still writes; the race matrix keeps running unchanged).
  const { kernel, writes } = harness();
  parkReaderOnUserMessage(kernel);
  kernel.advanceGeometry();
  const first = kernel.begin("display-change", kernel.anchor);
  kernel.correctAnchor(first!, (key) => key === "user-msg" ? 480 : 560);
  kernel.advanceGeometry();
  const second = kernel.begin("display-change", kernel.anchor);
  const writeCountBefore = writes.length;
  const written = second !== null && kernel.correctAnchor(second, (key) => key === "user-msg" ? 30 : 560);
  ok(written && writes.length === writeCountBefore + 1, "compat: without viewport context the correction keeps the pre-753 behavior");
}

{
  // AC6: a native-clamped write finishes the transaction immediately instead
  // of holding it for the 1s TTL — the next frame can retry.
  const { clock, events, kernel, writes } = harness(() => ({ accepted: false, reason: "native-clamp" }));
  kernel.scrollToTail();
  ok(kernel.activeTransaction === null, "AC6: the clamped tail-sync is finished immediately (no TTL hold)");
  ok(events.some((event) => event.outcome === "native-clamp"), "AC6: the clamp is observable as outcome=native-clamp");
  kernel.scheduleTailSync();
  clock.flushFrames();
  ok(writes.length >= 2, "AC6: the next frame's tail-sync retry is not blocked by the old transaction");
  ok(kernel.activeTransaction === null, "AC6: the retried transaction also settles instead of stacking");
}

{
  // AC9 (kernel half): the native gesture lease can be established and times
  // out on its own — this is the exact entry the raw wheel channel now uses.
  const { clock, kernel } = harness();
  let ended = false;
  kernel.renewNativeGesture(snapshot(500, [{ key: "b", top: 480, bottom: 560 }]), 320, () => { ended = true; });
  ok(kernel.userGestureActive && kernel.nativeGestureLeaseActive, "AC9: a raw native entry establishes the gesture lease");
  clock.advance(320);
  ok(ended && !kernel.userGestureActive && !kernel.nativeGestureLeaseActive, "AC9: the lease expires idle and ends the gesture cleanly");
}

{
  // Source pins: the hook wiring must keep the wheel fallback and the
  // correction context (regression guard against silent reverts).
  const hookSource = readFileSync(new URL("../lib/useTranscriptKernel.ts", import.meta.url), "utf8");
  const wheelBranch = hookSource.slice(hookSource.indexOf("const onWheel = (event: WheelEvent)"), hookSource.indexOf("element.addEventListener(\"wheel\""));
  ok(wheelBranch.includes("renewGestureLease()") && wheelBranch.includes("event.deltaY !== 0"), "AC9 source pin: the native wheel listener renews the gesture lease for any deltaY direction");
  ok(hookSource.includes("kernel.correctAnchor(transaction, (key) => blockTop(element, key), geometry ? {"), "AC7 source pin: settleGeometry hands correctAnchor the viewport context");
}

if (failed > 0) {
  throw new Error(`${failed} task 753-2 checks failed`);
}
console.log(`  ${passed} checks passed`);
