// Run: tsx src/__tests__/tl1204-scroll-ssot-authority.test.ts
//
// tl 1204 — 切 tab 恢复/滚动 SSOT 权威状态机（kernel 层验收面）。
//
// 族病灶（1184 上漂 / 1187 不贴底 / 1196 三形态 = 同一病灶不同时序外露）：
// atBottom/anchor 来源 4+ 处、恢复链 4 条竞争看时序。本件收敛为：
//   权威单点：kernel.restoreLanding（tail=同步贴底 / block=restore 落位）+
//             kernel.isViewportAtBottom（判定公式唯一份）；
//   恢复链两条：a) 同步贴底（replaceSurface / hydrate 完成帧）
//              b) 异步兜底（scheduleTailSync 单次 rAF，失败 emit
//                 tail-sync-fallback-exhausted 可见诊断）。
// 本文件在 kernel 层断言 1196 三形态的收敛（验收①④）与守卫（验收③）；
// 1184/1187 回归由同名测试文件保持（验收②），hook 层两路径在
// tl1204-tab-switch-hydrate-landing.test.tsx。

import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import {
  TranscriptKernel,
  TRANSCRIPT_BOTTOM_THRESHOLD_PX,
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

/** Fake clock whose rAF callbacks NEVER run by themselves — tests flush them
 * by hand. Anything already written before a flush is a synchronous landing. */
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
): { kernel: TranscriptKernel; writes: WriteLog[]; events: Array<{ outcome: string }> } {
  const writes: WriteLog[] = [];
  const events: Array<{ outcome: string }> = [];
  const kernel = new TranscriptKernel({ clock, emit: (event) => events.push({ outcome: event.outcome }) });
  kernel.connectWriter((request) => {
    const result = respond(request);
    writes.push({ request, result });
    return result;
  });
  return { kernel, writes, events };
}

const acceptAll = (request: TranscriptWriteRequest): TranscriptWriteResult => ({
  accepted: true,
  offset: Number.isFinite(request.offset) ? request.offset : 800,
  changed: true,
});

process.stdout.write("\n[tl 1204] scroll SSOT authority (kernel layer)\n");

// --- F1 (1196 form 1, fast path): a bottom-parked session lands on the
// switch-back tick itself — the write exists BEFORE any rAF flush, so no
// intermediate scroll frame can ever be painted (acceptance ④). ---
{
  const { clock, frames } = manualClock();
  const { kernel, writes } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("tab-A");
  kernel.beginUserGesture(snapshot(500));
  kernel.observeNativeScroll(snapshot(500)); // 1000-500-500=0 → at bottom
  kernel.endUserGesture();
  eq(kernel.intent, "tail", "F1: A parked at the bottom owns the tail");

  kernel.replaceSurface("tab-B"); // switch away
  kernel.replaceSurface("tab-A"); // switch back — the authority must land NOW
  const framesBeforeLanding = frames.length;
  eq(kernel.restoreLanding(), true, "F1: the authority lands the restored surface synchronously");
  eq(kernel.intent, "tail", "F1: the restored verdict is tail (bottomBySession)");
  const tailWrites = writes.filter((write) => write.request.owner === "tail-follow");
  eq(tailWrites.length, 1, "F1: exactly one synchronous tail write, before any rAF");
  eq(frames.length - framesBeforeLanding, 0, "F1: zero NEW rAF fallbacks armed — the sync landing sufficed");
  ok(tailWrites[0] != null && Number.isFinite(tailWrites[0]!.request.offset) === false,
    "F1: the landing requested the bottom floor (non-finite offset)");
}

// --- F2 (1196 form 2, full-reload path): geometry not ready → the write is
// refused and the single rAF fallback is armed; the hydrate-complete frame
// lands synchronously through the same authority (with block geometry). ---
{
  const { clock, frames } = manualClock();
  let ready = false;
  const { kernel, writes } = kernelWithWriter(clock, (request) => ready
    ? acceptAll(request)
    : { accepted: false, offset: 0, reason: "invalid-geometry" });
  kernel.replaceSurface("tab-R");
  kernel.beginUserGesture(snapshot(500));
  kernel.observeNativeScroll(snapshot(500));
  kernel.endUserGesture(); // tail, parked at the bottom
  kernel.replaceSurface("tab-H");
  const framesBeforeFallback = frames.length;
  eq(kernel.restoreLanding(), false, "F2: with geometry unavailable the sync landing is refused");
  eq(frames.length - framesBeforeFallback, 1, "F2: exactly ONE rAF fallback is armed (chain b), not a retry storm");

  // Hydrate completes: real blocks mounted, geometry measurable. The
  // hydrate-complete frame asks the authority again — synchronously.
  ready = true;
  const landed = kernel.restoreLanding();
  eq(landed, true, "F2: the hydrate-complete frame lands synchronously");
  const last = writes[writes.length - 1];
  ok(last != null && last.request.owner === "tail-follow", "F2: the landing rode the tail owner");
  eq(kernel.intent, "tail", "F2: intent stays tail through the reload landing");
}

// --- F3 (1196 form 3, reader must not be dragged): a reader-parked session
// comes back through the SAME authority and lands on its BLOCK anchor — no
// pinTail write may exist anywhere on this path. ---
{
  const { clock, frames } = manualClock();
  const { kernel, writes } = kernelWithWriter(clock, acceptAll);
  kernel.replaceSurface("tab-R");
  kernel.beginUserGesture(snapshot(100));
  kernel.observeNativeScroll(snapshot(100)); // 400px above the bottom → reader
  kernel.endUserGesture();
  eq(kernel.anchor.kind, "block", "F3: the reader parked on a block anchor");
  const blockKey = kernel.anchor.kind === "block" ? kernel.anchor.blockKey : "";

  kernel.replaceSurface("tab-X");
  kernel.replaceSurface("tab-R");
  eq(kernel.intent, "reader", "F3: the restored verdict is reader (1184b derivation)");
  const framesBeforeReader = frames.length;
  eq(kernel.restoreLanding(), true, "F3: the authority stages the restore transaction");
  ok(kernel.activeTransaction?.kind === "restore", "F3: the staged transaction is a restore, not a tail-sync");
  ok(!writes.some((write) => write.request.owner === "tail-follow"),
    "F3: zero tail writes for the returning reader");
  eq(frames.length - framesBeforeReader, 0, "F3: no rAF tail fallback armed for a reader");

  // The hydrate-complete frame variant: block geometry supplied → the same
  // authority lands the reader on the measured block position in-tick.
  // offset = measured top (100) + the captured offsetPx (scrollTop 100 - block
  // top 0 = 100) = 200.
  const landedOffset = kernel.restoreLanding((key) => (key === blockKey ? 100 : undefined));
  eq(landedOffset, true, "F3: with block geometry the reader lands synchronously");
  const landing = writes[writes.length - 1];
  ok(landing != null && landing.request.owner === "restore" && landing.request.offset === 200,
    "F3: the landing wrote blockTop + captured offsetPx through the restore owner");
}

// --- F4: the rAF fallback is the LAST chain — when even it is refused, the
// exhaustion is VISIBLE on the diagnostic stream (acceptance: 失败可见诊断). ---
{
  const { clock, frames } = manualClock();
  const refuse = (_request: TranscriptWriteRequest): TranscriptWriteResult =>
    ({ accepted: false, offset: 0, reason: "test-refused" });
  const { kernel, events } = kernelWithWriter(clock, refuse);
  kernel.replaceSurface("tab-T");
  kernel.scheduleTailSync(); // chain b directly
  eq(frames.length, 1, "F4: the fallback armed exactly one rAF");
  frames.shift()?.();
  ok(events.some((event) => event.outcome === "tail-sync-fallback-exhausted"),
    "F4: the exhausted fallback is visible as tail-sync-fallback-exhausted");
  eq(events.filter((event) => event.outcome === "tail-sync-fallback-exhausted").length, 1,
    "F4: the exhaustion emits once (no loop)");
}

// --- F5: the bottom verdict is the kernel's single formula. ---
{
  const kernel = new TranscriptKernel({ clock: manualClock().clock });
  eq(kernel.isViewportAtBottom({ scrollTop: 500, scrollHeight: 1000, clientHeight: 500 }), true,
    "F5: flush bottom reads true");
  eq(kernel.isViewportAtBottom({ scrollTop: 497, scrollHeight: 1000, clientHeight: 500 }), true,
    "F5: within the shared threshold reads true");
  eq(kernel.isViewportAtBottom({ scrollTop: 492, scrollHeight: 1000, clientHeight: 500 }), false,
    "F5: beyond the shared threshold reads false");
  eq(TRANSCRIPT_BOTTOM_THRESHOLD_PX, 4, "F5: the exported threshold is the historic 4px");
}

// --- G: source guard (acceptance ③) — the real sources satisfy the restore-
// landing contract, and the guard PROVABLY trips on injected violations. ---
{
  // Non-literal specifier on purpose: it loads the plain-.mjs guard script
  // through the native ESM loader and types as any for tsc.
  const guardPath = "../../scripts/check-restore-landing-sites.mjs";
  const guard = (await import(guardPath)) as {
    auditRestoreLandingSites: (sources: Record<string, string>) => string[];
  };
  const root = dirname(dirname(fileURLToPath(import.meta.url)));
  const read = (relative: string) => readFileSync(join(root, relative), "utf8");
  const realSources = {
    "lib/transcriptKernel.ts": read("lib/transcriptKernel.ts"),
    "lib/useTranscriptKernel.ts": read("lib/useTranscriptKernel.ts"),
    "components/Transcript.tsx": read("components/Transcript.tsx"),
    "components/ScrollDiagnosticPanel.tsx": read("components/ScrollDiagnosticPanel.tsx"),
    "lib/frontendDiagnostics.ts": read("lib/frontendDiagnostics.ts"),
  };
  const realViolations = guard.auditRestoreLandingSites(realSources);
  eq(realViolations.length, 0, `G: real sources satisfy the contract (${realViolations.join(" | ") || "clean"})`);

  // Lower-bound self-proof: a re-grown #9567-style chain MUST trip the guard.
  const withRemountChain = {
    ...realSources,
    "components/Transcript.tsx": `${realSources["components/Transcript.tsx"]!}\n// injected violation\nsetScrollMode("tail-follow");\nscrollToBottom();\n`,
  };
  const remountViolations = guard.auditRestoreLandingSites(withRemountChain);
  ok(remountViolations.length > 0 && remountViolations.some((violation) => violation.includes("Transcript.tsx")),
    "G: injecting the deleted remount chain trips the guard (lower bound 1)");

  // Lower-bound self-proof 2: an inline tail-sync recovery combo in the hook.
  const withInlineCombo = {
    ...realSources,
    "lib/useTranscriptKernel.ts": `${realSources["lib/useTranscriptKernel.ts"]!}\n// injected violation\nkernel.begin("tail-sync", { kind: "tail" });\n`,
  };
  const inlineViolations = guard.auditRestoreLandingSites(withInlineCombo);
  ok(inlineViolations.length > 0 && inlineViolations.some((violation) => violation.includes('inline kernel.begin("tail-sync")')),
    "G: injecting an inline tail-sync combo trips the guard (lower bound 2)");

  // Lower-bound self-proof 3: a second render-layer verdict formula.
  const withFormulaCopy = {
    ...realSources,
    "lib/useTranscriptKernel.ts": `${realSources["lib/useTranscriptKernel.ts"]!}\n// injected violation\nconst copy = geometry.scrollHeight - geometry.clientHeight - geometry.scrollTop <= 4;\n`,
  };
  const formulaViolations = guard.auditRestoreLandingSites(withFormulaCopy);
  ok(formulaViolations.length > 0 && formulaViolations.some((violation) => violation.includes("render-layer copy")),
    "G: injecting a formula copy trips the guard (lower bound 3)");
}

process.stdout.write(`\n[tl 1204] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
