// Run: tsx --import ./scripts/css-stub-register.mjs src/__tests__/tl1241-landing-diagnosis.test.tsx
//
// tl 1241 — 贴底随机失效打点①（诊断通道验收面）。
//
// 打点形态（协调线派单：不新建打点，复用 ScrollDiagnosticPanel 录制通道）：
//   a) surface-swap 切面快照：每次切面一条事件，携带 restore 裁决消费的
//      三元组（切走时刻 atBottom/intent + 切回读取的 bottomBySession +
//      恢复 anchor kind）——tl1219 §1 的「陈旧快照」家族从静态推断变成
//      导出 trace 里的可复现实据；
//   b) 采样三字段：kernelAtBottom / kernelIntent / bottomBySession 并入
//      ScrollDiagnosticPanel 的 diagnosticSample（与实测 atBottom 刻意
//      区分——二者的差就是病灶读数）。
// 本文件断言：schema 白名单放行/拦截（字段不登记即静默丢弃的教训）+
// hook 层全链（probe sink → 单例 recorder → 导出 payload）+ hook 层
// restoreLanding 实测兜底的接线（wrapper 必须把当前几何递给权威）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  createTranscriptScrollDiagnostics,
  transcriptScrollDiagnostics,
} from "../lib/transcriptScrollDiagnostics";
import type { TranscriptKernel, TranscriptViewportSnapshot } from "../lib/transcriptKernel";

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

// --- F/G: schema 白名单（独立 recorder 工厂实例，不碰单例）。 ---
{
  const recorder = createTranscriptScrollDiagnostics({ now: () => 0, randomID: () => "fixed-id" });
  recorder.start();
  recorder.record("surface-swap", {
    surface: "tab:A:0",
    surfaceFrom: "tab:B:2",
    atBottomFrom: false,
    intentFrom: "reader",
    bottomBySession: false,
    restoredAnchorKind: "block",
    surfaceBad: "this field is not whitelisted",
  });
  const payload = recorder.stop();
  const swap = payload.events.find((event) => event.type === "surface-swap");
  ok(swap, "F: the surface-swap event survives the sanitizer");
  eq(swap?.surface, "tab:A:0", "F: surface key survives");
  eq(swap?.surfaceFrom, "tab:B:2", "F: surfaceFrom key survives");
  eq(swap?.atBottomFrom, false, "F: atBottomFrom (the stale verdict written at switch-away) survives");
  eq(swap?.intentFrom, "reader", "F: intentFrom survives");
  eq(swap?.bottomBySession, false, "F: bottomBySession (the switch-back verdict input) survives");
  eq(swap?.restoredAnchorKind, "block", "F: restoredAnchorKind survives");
  eq((swap as unknown as Record<string, unknown>).surfaceBad, undefined,
    "F: an unregistered field is dropped (no silent prose smuggling)");

  const samplerRecorder = createTranscriptScrollDiagnostics({ now: () => 0, randomID: () => "fixed-id" });
  samplerRecorder.start();
  samplerRecorder.record("sample", {
    scrollTop: 700,
    kernelAtBottom: true,
    kernelIntent: "reader",
    bottomBySession: false,
  });
  const samplerPayload = samplerRecorder.stop();
  const sample = samplerPayload.events.find((event) => event.type === "sample");
  ok(sample, "G: the sample event survives the sanitizer");
  eq(sample?.kernelAtBottom, true, "G: kernelAtBottom (conditional snapshot) survives");
  eq(sample?.kernelIntent, "reader", "G: kernelIntent survives");
  eq(sample?.bottomBySession, false, "G: bottomBySession snapshot survives");
  eq(sample?.scrollTop, 700, "G: sample geometry fields unaffected");
}

// --- H/I: hook 层（jsdom harness，复用 tl1204 切面测试基建）。 ---
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
// defaultEnvironment (diagnostics manifest) reads computed styles via the bare
// global — mirror it like the other window builtins above.
(globalThis as unknown as { getComputedStyle: typeof dom.window.getComputedStyle }).getComputedStyle =
  dom.window.getComputedStyle.bind(dom.window);

// ESM import 提升：静态 import 会先于上面的全局注入执行——hook 模块顶层的
// rAF 帧计数器会在「全局 requestAnimationFrame 尚不存在」时判空跳过，
// transcriptFrame 恒 0 → readSnapshot 的同帧缓存永久命中首次几何 →
// restoreLanding 的实测永远读到陈旧值。必须先注入全局再加载模块。
const { useTranscriptKernel } = await import("../lib/useTranscriptKernel");

// 复用滚动容器：scrollTop 保留上一个会话的值（真实 tab 切换语义）。
const geom = { height: 0, client: 500, top: 0, heightReady: true };
function attachGeometry(el: HTMLElement): void {
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => geom.client });
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => (geom.heightReady ? geom.height : Number.NaN) });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => geom.top,
    set: (value: number) => { geom.top = value; },
  });
}
const bottomOf = () => geom.height - geom.client;

type Handle = {
  kernel: TranscriptKernel;
  restoreLanding: () => void;
  commitViewportGeometry: (covered?: boolean, beforePaint?: boolean) => void;
};
let handle: Handle | null = null;
const geomBound = new WeakSet<Element>();

function Harness({ sessionKey, geometryRevision }: { sessionKey: string; geometryRevision: string }) {
  const hook = useTranscriptKernel({ sessionKey, geometryRevision });
  handle = {
    kernel: hook.kernel,
    restoreLanding: hook.restoreLanding,
    commitViewportGeometry: hook.commitViewportGeometry,
  };
  return <div
    ref={(node) => {
      hook.scrollRef.current = node;
      if (node && !geomBound.has(node)) { geomBound.add(node); attachGeometry(node); }
    }}
  />;
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
let root: Root = createRoot(rootElement);

async function render(sessionKey: string, revision: string): Promise<void> {
  await act(async () => { root.render(<Harness sessionKey={sessionKey} geometryRevision={revision} />); });
}
async function flushFrames(count = 2): Promise<void> {
  for (let index = 0; index < count; index += 1) {
    await act(async () => { await new Promise<void>((resolve) => dom.window.requestAnimationFrame(() => resolve())); });
  }
}

function snap(top: number, height: number): TranscriptViewportSnapshot {
  return { scrollTop: top, scrollHeight: height, clientHeight: geom.client, visibleBlocks: [{ key: "blk-0", top: 0, bottom: height }] };
}
function scrollUpTo(kernel: TranscriptKernel, top: number, height: number): void {
  kernel.beginUserGesture(snap(top, height), "native");
  kernel.observeNativeScroll(snap(top, height));
  kernel.endUserGesture();
}

// jsdom 的 rAF 帧计数不与 readSnapshot 的同帧缓存协同（帧号可能长期不动，
// 缓存返回首次读的几何）。geom 变更后先做一次同步几何提交，把缓存刷新成
// 当前真实值，随后的 restoreLanding 同帧命中即读到真实几何。
function syncGeometry(): void {
  handle!.commitViewportGeometry(undefined, true);
}

process.stdout.write("\n[tl 1241] landing diagnosis (hook layer, full probe chain)\n");

// --- H: wrapper 必须把当前几何递给权威——失真态 + 容器实测贴底 → 贴底；
// 实测不贴底（reader 中部）→ block 落位不动。 ---
{
  geom.heightReady = true;
  geom.height = 1200; geom.top = 300;
  await render("tab:A", "rev-a");
  scrollUpTo(handle!.kernel, 300, 1200); // A parks as reader at 300 (stale-false verdict + block anchor)

  geom.height = 2000; geom.top = 300;
  await render("tab:B", "rev-b"); // switch away: bottomBySession(A)=false, anchors(A)=block
  await flushFrames();

  // 切回，容器实测停在底部（scrollTop 保留在 A 的底部位置）
  geom.height = 1200; geom.top = 700; // 1200-500-700 = 0 → measured at bottom
  await render("tab:A", "rev-a");
  await flushFrames(1);
  syncGeometry();

  handle!.restoreLanding(); // the hydrate-complete frame entry
  // 判据是 verdict 翻转而不是「同步写发生」：实测贴底时视口已在底部，
  // 同步写可被同 geometry revision 去重拒绝（无需位移）——kernel 级 S2
  // 已用接受型 writer 证明写入链路，这里证明的是 wrapper 的接线。
  eq(geom.top, bottomOf(), "H: the view is at the bottom (wrapper handed the measurement to the authority)");
  eq(handle!.kernel.anchor.kind, "tail", "H: the stale block snapshot was demoted to tail");
  eq(handle!.kernel.intent, "tail", "H: the intent follows the measurement");
  eq(handle!.kernel.atBottom, true, "H: the verdict is refreshed by the measurement");

  // 红线反例：全新的失真态会话（不复用已被刷新的 A）、实测停在中部
  // → block 落位，绝不贴底。
  geom.height = 2000; geom.top = 400;
  await render("tab:R", "rev-r");
  scrollUpTo(handle!.kernel, 400, 2000); // R parks as reader at 400
  geom.height = 1200; geom.top = 100;
  await render("tab:B", "rev-b");
  await flushFrames(1);
  geom.height = 2000; geom.top = 400;
  await render("tab:R", "rev-r");
  await flushFrames(1);
  syncGeometry();
  handle!.restoreLanding();
  eq(geom.top, 400, "H-red: measured mid-transcript keeps the reader position (no tail force)");
  eq(handle!.kernel.anchor.kind, "block", "H-red: the block anchor survives a mid-transcript measurement");

  // 几何不可得（hydrate 中间态）：无实测 → 行为与修复前一致（不贴底不崩）。
  geom.heightReady = false; geom.height = 2400; geom.top = 100;
  await render("tab:B", "rev-b");
  await flushFrames(1);
  geom.heightReady = true;
  eq(handle!.restoreLanding(), true, "H: unavailable geometry degrades to the pre-fix path without crashing");
}

// --- I: 全链 surface-swap —— hook 的切面记录经 probe sink 进单例
// recorder，导出 payload 即见切面裁决三元组。 ---
{
  // 全新 root = 全新 kernel 实例，切面序列从零开始（surfaceFrom 首条为空）。
  await act(async () => { root.unmount(); });
  root = createRoot(rootElement);
  transcriptScrollDiagnostics.start();
  geom.heightReady = true;
  geom.height = 1200; geom.top = 450;
  await render("diag:A", "rev-1");
  // 450：与 H 段残留的 lastScrollTop 必然不同，保证这次观测是位移观测
  // （否则 displaced 门控会跳过 verdict——那本身就是漏判B的机制）。
  scrollUpTo(handle!.kernel, 450, 1200); // diag:A parks as reader at 450

  geom.height = 2000; geom.top = 450;
  await render("diag:B", "rev-2"); // switch away
  await flushFrames();

  geom.height = 1200; geom.top = 450;
  await render("diag:A", "rev-1"); // switch back
  await flushFrames();

  const payload = transcriptScrollDiagnostics.stop();
  transcriptScrollDiagnostics.reset();
  const swaps = payload.events.filter((event) => event.type === "surface-swap");
  eq(swaps.length, 3, "I: one surface-swap event per switch (mount + away + back)");
  eq(swaps[0]?.surface, "diag:A", "I: first swap names the mounted surface");
  eq(swaps[0]?.surfaceFrom, undefined, "I: the first swap has no outgoing surface");
  eq(swaps[1]?.surface, "diag:B", "I: second swap names B");
  eq(swaps[1]?.surfaceFrom, "diag:A", "I: second swap names the outgoing surface");
  eq(swaps[1]?.atBottomFrom, false, "I: the switch-away verdict is the stale false (leak A made visible)");
  eq(swaps[1]?.intentFrom, "reader", "I: the switch-away intent is reader");
  eq(swaps[2]?.surface, "diag:A", "I: third swap is the switch-back");
  eq(swaps[2]?.bottomBySession, false, "I: the switch-back verdict input is the recorded stale false");
  eq(swaps[2]?.restoredAnchorKind, "block", "I: the restored anchor kind is block (the corruption path)");
}

await act(async () => { root.unmount(); });
dom.window.close();

process.stdout.write(`\n[tl 1241 diagnosis] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
