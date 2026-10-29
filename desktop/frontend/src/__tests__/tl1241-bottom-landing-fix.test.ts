// Run: tsx src/__tests__/tl1241-bottom-landing-fix.test.ts
//
// tl 1241 — 贴底随机失效的漏判A最小修（kernel 层验收面）。
//
// 病灶（tl1219 报告 §1）：atBottomValue 是三条件门控的条件快照
// （手势 + 非 writeTop + 有位移，transcriptKernel.observeNativeScroll），
// programmatic 贴底写入（跳底按钮 / tail-follow）全部被门控早退吃掉，
// 切走时刻 bottomBySession 记下陈旧值；tl1204 删掉无条件贴底兜底后，
// restoreLanding 由 anchor 快照单点裁决 → 快照失真即随机不贴底。
//
// 修法（协调线 07:1x 派单，最小档）：restoreLanding 增加实测视口入参——
// 实测贴底则显式归 tail（「当前确实贴底」作为当前证据，取代陈旧快照）。
// 红线：实测不贴底（reader 停中部）绝不归 tail——1196 形态 2/3 的
// reader 驻留保护原样保留；不传实测时行为与修复前逐字节一致。

import {
  TranscriptKernel,
  type TranscriptKernelClock,
  type TranscriptViewportSnapshot,
  type TranscriptWriteRequest,
  type TranscriptWriteResult,
} from "../lib/transcriptKernel";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const good = actual === expected;
  ok(good, good ? label : `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
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

function manualClock() {
  const frames: Array<() => void> = [];
  const clock: TranscriptKernelClock = {
    now: () => 0,
    requestAnimationFrame: (callback) => { frames.push(callback); return frames.length; },
    cancelAnimationFrame: () => {},
    setTimeout: () => 0 as unknown as ReturnType<typeof setTimeout>,
    clearTimeout: () => {},
  };
  return { clock, frames, flushOne: () => { const callback = frames.shift(); callback?.(); } };
}

type WriteLog = { request: TranscriptWriteRequest; result: TranscriptWriteResult };
function kernelWithWriter(
  clock: TranscriptKernelClock,
  respond: (request: TranscriptWriteRequest) => TranscriptWriteResult,
): { kernel: TranscriptKernel; writes: WriteLog[] } {
  const writes: WriteLog[] = [];
  const kernel = new TranscriptKernel({ clock, emit: () => {} });
  kernel.connectWriter((request) => {
    const result = respond(request);
    writes.push({ request, result });
    return result;
  });
  return { kernel, writes };
}

const acceptAll = (request: TranscriptWriteRequest): TranscriptWriteResult => ({
  accepted: true,
  offset: Number.isFinite(request.offset) ? request.offset : 800,
  changed: true,
});

/** 失真态构造：会话 A 的 kernel 快照 = atBottomValue=false + anchor=block
 * （合法 reader 停中部），切走写入 bottomBySession=false。 */
function parkReaderMidTranscript(kernel: TranscriptKernel): void {
  kernel.beginUserGesture(snapshot(300));
  kernel.observeNativeScroll(snapshot(300)); // 1000-500-300=200 > 4 → reader
  kernel.endUserGesture();
}

process.stdout.write("\n[tl 1241] bottom-landing stale-snapshot fix (kernel layer)\n");

// --- S4: 漏判A本体读数——跳底按钮（programmatic）后条件快照仍陈旧 false。
// 这就是 atBottomValue 与实测可背离的证据（打点①的价值佐证）。 ---
{
  const { clock } = manualClock();
  const { kernel } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("A");
  parkReaderMidTranscript(kernel);
  eq(kernel.atBottom, false, "S4: reader park reads atBottom=false");
  kernel.scrollToTail(); // jump-to-bottom button path (programmatic write)
  eq(kernel.anchor.kind, "tail", "S4: the jump flips the anchor to tail");
  eq(kernel.atBottom, false,
    "S4: the conditional snapshot stays stale-false after the programmatic bottom write (leak A, unchanged by design)");
}

// --- E: 只读三元组 getter（打点①的 kernel 侧出口）。 ---
{
  const { clock } = manualClock();
  const { kernel } = kernelWithWriter(clock, acceptAll);
  eq(kernel.surface, "", "E: fresh kernel reports an empty surface");
  kernel.replaceSurface("A");
  eq(kernel.surface, "A", "E: surface getter reports the active session");
  eq(kernel.peekBottomSnapshot("A"), undefined, "E: no bottom snapshot before any switch-away");
  parkReaderMidTranscript(kernel);
  kernel.replaceSurface("B");
  eq(kernel.peekBottomSnapshot("A"), false, "E: switch-away recorded the stale verdict for A");
  eq(kernel.peekBottomSnapshot("B"), undefined, "E: B has no snapshot yet");
}

// --- S2: 修复本体——切回时快照失真（anchor=block），但容器实测贴底
// → restoreLanding 以实测为准归 tail，同步贴底。 ---
{
  const { clock, frames } = manualClock();
  const { kernel, writes } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("A");
  parkReaderMidTranscript(kernel);
  kernel.replaceSurface("B"); // switch away: bottomBySession(A)=false, anchors(A)=block
  kernel.replaceSurface("A"); // switch back: else branch restores the stale block anchor
  eq(kernel.anchor.kind, "block", "S2: the stale block anchor is what the authority holds");

  const framesBefore = frames.length;
  // 容器实测贴底（scrollTop 保留在底部：切走前的 tail-follow 写入或用户手动回底）
  const landed = kernel.restoreLanding(undefined, { scrollTop: 500, scrollHeight: 1000, clientHeight: 500 });
  eq(landed, true, "S2: the measured-at-bottom restore lands synchronously");
  eq(kernel.anchor.kind, "tail", "S2: measured-at-bottom demotes the stale block anchor to tail");
  eq(kernel.intent, "tail", "S2: the intent follows the measurement");
  eq(kernel.atBottom, true, "S2: the verdict is refreshed to true by the measurement");
  eq(frames.length, framesBefore, "S2: no rAF fallback was needed (sync landing)");
  eq(writes.some((write) => write.request.owner === "tail-follow" && write.result.accepted),
    true, "S2: the landing rode a tail write");
}

// --- S2b（红线）: 同一失真态 + 实测不贴底（reader 停中部）→ 绝不归 tail，
// block 落位原样保留（1196 形态 2/3 的 reader 驻留保护）。 ---
{
  const { clock } = manualClock();
  const { kernel, writes } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("A");
  parkReaderMidTranscript(kernel);
  kernel.replaceSurface("B");
  kernel.replaceSurface("A");

  const writesBefore = writes.length;
  // blk-mid top=200, capture offset=300-200=100 → the restore re-applies 300.
  const landed = kernel.restoreLanding((key) => (key === "blk-mid" ? 200 : undefined), { scrollTop: 300, scrollHeight: 1000, clientHeight: 500 });
  eq(landed, true, "S2b: the reader restore still lands via its block anchor");
  eq(kernel.anchor.kind, "block", "S2b: NOT at the bottom measured -> the anchor stays block (no tail force)");
  eq(kernel.intent, "reader", "S2b: the reader intent is untouched");
  ok(!writes.slice(writesBefore).some((write) => write.request.owner === "tail-follow"),
    "S2b: no tail write exists on the measured-mid restore path");
  const blockWrite = writes.slice(writesBefore).find((write) => write.request.owner === "restore");
  eq(blockWrite?.request.offset, 300, "S2b: the block restore re-applied the reader position");
}

// --- S2c: 不传实测入参 = 行为与修复前一致（opt-in 门控，非无条件贴底）。 ---
{
  const { clock } = manualClock();
  const { kernel, writes } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("A");
  parkReaderMidTranscript(kernel);
  kernel.replaceSurface("B");
  kernel.replaceSurface("A");

  const writesBefore = writes.length;
  const landed = kernel.restoreLanding((key) => (key === "blk-mid" ? 200 : undefined));
  eq(landed, true, "S2c: without a measurement the block restore still runs");
  eq(kernel.anchor.kind, "block", "S2c: no measurement -> no tail demotion (pre-fix behavior byte-identical)");
  const blockWrite = writes.slice(writesBefore).find((write) => write.request.owner === "restore");
  eq(blockWrite?.request.offset, 300, "S2c: the block landing position is unchanged");
}

// --- S3: 多轮切走切回贴底稳定（实测兜底逐轮生效）。 ---
{
  const { clock } = manualClock();
  const { kernel } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("A");
  for (let round = 1; round <= 3; round += 1) {
    // A 用户坐在底部（实测贴底的位置）
    kernel.beginUserGesture(snapshot(500));
    kernel.observeNativeScroll(snapshot(500)); // 1000-500-500=0 → tail
    kernel.endUserGesture();
    eq(kernel.anchor.kind, "tail", `S3 r${round}: A parked at the bottom owns the tail`);

    kernel.replaceSurface("B"); // switch away
    // B 的生命周期里 kernel 快照被 B 的内容改写（模拟任意失真源）
    parkReaderMidTranscript(kernel);
    kernel.replaceSurface("A"); // switch back

    const landed = kernel.restoreLanding(undefined, { scrollTop: 500, scrollHeight: 1000, clientHeight: 500 });
    eq(landed, true, `S3 r${round}: switch-back lands synchronously`);
    eq(kernel.anchor.kind, "tail", `S3 r${round}: switch-back verdict is tail (stability across rounds)`);
  }
}

process.stdout.write(`\n[tl 1241 kernel] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
