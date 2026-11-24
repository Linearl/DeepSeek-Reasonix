// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1776-stream-append-tail-bounce.test.tsx
//
// tl 1776 — 贴底判定缺口：流式输出时贴底往上回弹（P2，包112 勘误确认从未实现）。
//
// 现场机制（用户报 + 截图，间歇性）：
//   1. 贴底 tail 视图流式追加（工作过程卡片/思考中等在最后一行输出下方长出）。
//      流式跟随引擎每 chunk 写一次底；写与写之间 scrollHeight 已长、scrollTop
//      还在旧底——这个窗口是 follow-lag，不是读者意图。
//   2. 尾写产生的 scroll 事件（echo）恰落在这个窗口时（快流式下是稳态），
//      useTranscriptKernel.onScroll → capturePreReflowFacts 把
//      atBottom=false（+首可见行锚）采样进 preReflowRef——「重排前事实」被
//      follow-lag 污染。
//   3. 此后任一宽度变化（侧栏 hover 过渡 / 窗口缩放 / 停靠切换 / 滚动条
//      出现）触发 ResizeObserver widthChanged → tl 1699 宽度补偿走
//      resolveWidthTarget：kernel.intent=tail 但 facts.atBottom=false →
//      按行/块锚定目标 beginWidthCompensation → begin() 把 intent 降级为
//      reader，视口被写回被捕获的旧位置（可见的上弹）。
//   4. intent=reader 后流式补贴（settleGeometry 的 tail-sync 闸）永久不再
//      触发 → 视口停在旧行，新内容在下方不可见，需手动滚动（手势观察
//      重新推导 intent=tail 后恢复跟随）。
//
// 修法（本件）：尾底见证（tail-floor witness）。TranscriptViewportWriter 是
//   滚动写入的唯一生产所有者——它记录最近一次 tail 类写入瞄准的 floor；
//   facts 采样（capturePreReflowFacts / refreshPreReflowFacts）在
//   kernel.intent=tail 且 scrollTop 仍坐在该 floor 上（±4px 贴底容差）时，
//   把「测得不在底」判定为 follow-lag 而非读者意图（atBottom=true，且不再
//   采样行锚）。真正的读者位移（上滚离开 floor，如租约过期的动量滚）距
//   floor > 4px，见证不命中——tl 1740 语义逐字节保持。
//
// 验收矩阵：
//   R  贴底 tail + 流式追加竞态采样 + 宽度重排：intent 保持 tail、回底、
//      后续流式继续跟随（修复前：intent 降 reader、视口弹回旧位置并停跟）；
//      见证命中落诊断打点 tail-floor-witness。
//   M  贴底 tail + 真实读者上滚位移（无手势租约的动量尾部）+ 宽度重排：
//      读者位置保持、不被拽回底（修复前后一致——tl 1740 零回退）。
//   W  干净贴底（无流式竞态采样）+ 宽度重排：无条件回底（tl 1699 A 腿
//      语义保持；facts 缺席时读 kernel.atBottom 的路径）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { useTranscriptKernel } from "../lib/useTranscriptKernel";
import { setTranscriptScrollDiagnosticSink } from "../lib/transcriptScrollProbe";
import { TranscriptTestClock } from "./transcript-test-clock";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const good = actual === expected;
  ok(good, good ? label : `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── DOM 全局（动态 import hook 之前就位，tl 1740 同款）────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;

type FrameCallback = (time: number) => void;
let frameCallbacks: FrameCallback[] = [];
globalThis.requestAnimationFrame = (callback: FrameCallback): number => {
  frameCallbacks.push(callback);
  return frameCallbacks.length;
};
globalThis.cancelAnimationFrame = (handle: number): void => {
  frameCallbacks = frameCallbacks.filter((_, index) => index !== handle - 1);
};
function bumpFrame(): void {
  const callbacks = frameCallbacks;
  frameCallbacks = [];
  callbacks.forEach((callback) => callback(0));
}

// ── 手控 ResizeObserver（jsdom 无布局；hook 的 RO 效果由这里驱动）────────
type ROEntry = { target: Element; contentRect: { width: number; height: number } };
class FakeResizeObserver {
  static all: FakeResizeObserver[] = [];
  private callback: (entries: ROEntry[]) => void;
  private observed = new Set<Element>();
  constructor(callback: (entries: ROEntry[]) => void) {
    this.callback = callback;
    FakeResizeObserver.all.push(this);
  }
  observe(element: Element): void { this.observed.add(element); }
  unobserve(element: Element): void { this.observed.delete(element); }
  disconnect(): void { this.observed.clear(); }
  fire(width: number, height: number): void {
    for (const element of this.observed) this.callback([{ target: element, contentRect: { width, height } }]);
  }
  static fireAll(width: number, height: number): void {
    FakeResizeObserver.all.forEach((observer) => observer.fire(width, height));
  }
  static reset(): void { FakeResizeObserver.all = []; }
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = FakeResizeObserver;

// ── 诊断流收集（见证命中打点的读数来源）───────────────────────────────
type DiagnosticEvent = { type: string; outcome?: string; intent?: string };
const diagnostics: DiagnosticEvent[] = [];
setTranscriptScrollDiagnosticSink((type, fields) => {
  diagnostics.push({ type, outcome: fields.outcome as string | undefined, intent: fields.intent as string | undefined });
});

// ── 滚动容器几何 stub（tl 1740 同款）────────────────────────────────
type BlockGeom = { top: number; height: number };
const ORIGINAL_BLOCKS: Record<string, BlockGeom> = {
  "blk-a": { top: 0, height: 1000 },
  "blk-b": { top: 1000, height: 3000 },
};
const geom = {
  client: 500,
  clientW: 800,
  height: 4000,
  top: 0,
  blocks: ORIGINAL_BLOCKS as Record<string, BlockGeom>,
};
const bottomOf = (): number => geom.height - geom.client;
const bottomDistance = (): number => geom.height - geom.client - geom.top;
function resetGeom(): void {
  geom.client = 500;
  geom.clientW = 800;
  geom.height = 4000;
  geom.top = 0;
  geom.blocks = ORIGINAL_BLOCKS;
}

function rect(top: number, height: number): DOMRect {
  return { top, bottom: top + height, left: 0, right: geom.clientW, width: geom.clientW, height, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
}
const geomBound = new WeakSet<Element>();
function ensureScrollerStubs(node: HTMLDivElement): void {
  if (geomBound.has(node)) return;
  geomBound.add(node);
  for (const key of Object.keys(geom.blocks)) {
    const block = document.createElement("div");
    block.dataset.transcriptBlockKey = key;
    block.getBoundingClientRect = () => rect(geom.blocks[key]!.top - geom.top, geom.blocks[key]!.height);
    node.appendChild(block);
  }
  node.dataset.transcriptBlockCount = String(Object.keys(geom.blocks).length);
  Object.defineProperties(node, {
    clientHeight: { configurable: true, get: () => geom.client },
    clientWidth: { configurable: true, get: () => geom.clientW },
    scrollHeight: { configurable: true, get: () => geom.height },
    scrollTop: {
      configurable: true,
      get: () => geom.top,
      set: (value: number) => { geom.top = value; },
    },
  });
  node.getBoundingClientRect = () => rect(0, geom.client);
}

// ── harness：真实 useTranscriptKernel + TranscriptTestClock ─────────────
type HookHandle = ReturnType<typeof useTranscriptKernel>;
let hookRef: { current: HookHandle | null } = { current: null };
let clock: TranscriptTestClock = new TranscriptTestClock();

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
let root: Root | null = null;

const { TranscriptKernelClockContext, useTranscriptKernel } = await import("../lib/useTranscriptKernel");

function Harness({ sessionKey, geometryRevision }: { sessionKey: string; geometryRevision: string }) {
  const hook = useTranscriptKernel({ sessionKey, geometryRevision });
  return <div
    ref={(node) => {
      if (node) ensureScrollerStubs(node);
      hook.setScroller(node);
      if (node) hookRef.current = hook;
    }}
  />;
}

async function mount(sessionKey: string): Promise<void> {
  hookRef.current = null;
  const container = document.createElement("div");
  rootElement.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root!.render(<TranscriptKernelClockContext.Provider value={clock}>
      <Harness key={sessionKey} sessionKey={sessionKey} geometryRevision="rev-1" />
    </TranscriptKernelClockContext.Provider>);
  });
}
async function unmount(): Promise<void> {
  await act(async () => { root?.unmount(); });
  root = null;
  hookRef.current = null;
}
function flushAll(): void {
  let guard = 0;
  while (clock.frames.size > 0 && guard < 32) {
    clock.flushFrames();
    guard += 1;
  }
}
/** 生产语义：observe() 后 ResizeObserver 总先交付一次初始尺寸（分类=无变化）。 */
async function fireInitialObservation(): Promise<void> {
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  flushAll();
}

/**
 * 流式追加的一拍（生产顺序）：内容在尾写之间长出（follow-lag 窗口），
 * 尾写的 echo scroll 观察恰在此刻读几何——scrollTop 坐在旧底、
 * scrollHeight 已含新卡片的增量。增量长在最后一个块的内容流里（生产里
 * 追加的工作过程卡片就是可见块）：块几何随 scrollHeight 一起长，否则 stub
 * 的可见块集不覆盖新 floor，settle 会按 kernel 真实防护逻辑报
 * blank-viewport 异常（与本件机制无关）。
 */
async function streamAppendEchoObservation(growthPx: number): Promise<void> {
  bumpFrame();
  geom.height += growthPx;
  const lastKey = Object.keys(geom.blocks)[Object.keys(geom.blocks).length - 1]!;
  geom.blocks[lastKey] = { top: geom.blocks[lastKey]!.top, height: geom.blocks[lastKey]!.height + growthPx };
  await act(async () => { hookRef.current!.onScroll(); });
}

/**
 * 宽度重排的一帧（tl 1699 生产顺序）：ResizeObserver 布局后同步交付 →
 * widthChanged 补偿分支。
 */
async function widthRewrapFrame(width: number): Promise<void> {
  bumpFrame();
  await act(async () => { FakeResizeObserver.fireAll(width, geom.client); });
}

process.stdout.write("\n[tl 1776] streaming append keeps a bottom-pinned view pinned through width rewraps\n");

// ── R: 流式追加竞态采样 + 宽度重排——intent 保持 tail、回底、继续跟随 ──
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  diagnostics.length = 0;
  await mount("t1776-r");
  flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "R: the view starts pinned at the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "R: the intent is tail");

  // 流式追加（工作过程卡片 +600px）：echo 观察落在 follow-lag 窗口——
  // 测得「不在底」（缺口=600），但 scrollTop 仍坐在尾写瞄准的 floor 上。
  await streamAppendEchoObservation(600);
  eq(bottomDistance(), 600, "R: the append leaves the follow-lag gap the echo observes");

  // 同一宽度纪元内的宽度重排（侧栏过渡帧 / 滚动条出现等）。
  await widthRewrapFrame(620);
  flushAll();
  eq(hookRef.current!.kernel.intent, "tail",
    "R: the follow-lag sample does not demote the tail intent through the width compensation");
  eq(bottomDistance(), 0, `R: the width compensation re-pins the bottom (bottomDistance=${bottomDistance()})`);

  // 后续流式继续：跟随必须活着（修复前 intent=reader 永久停跟）。
  await streamAppendEchoObservation(600);
  await act(async () => { hookRef.current!.commitViewportGeometry(); });
  flushAll();
  bumpFrame();
  flushAll();
  eq(bottomDistance(), 0, `R: streaming keeps following after the rewrap (bottomDistance=${bottomDistance()})`);
  eq(hookRef.current!.kernel.intent, "tail", "R: the intent stays tail through continued streaming");
  ok(diagnostics.some((event) => event.type === "tail-floor-witness"),
    "R: the witness rescue is recorded on the diagnostic stream (tail-floor-witness)");
  await unmount();
}

// ── M: 真实读者上滚位移 + 宽度重排——位置保持，不拽回底（tl 1740 保持）──
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  diagnostics.length = 0;
  await mount("t1776-m");
  flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "M: the view starts pinned at the bottom");

  // 读者动量尾部（手势租约已过期）：真实上滚位移 -120px。观察仍被
  // userGesture 闸吞掉（intent 不动），但 facts 采样按真实位置记录。
  bumpFrame();
  geom.top -= 120;
  await act(async () => { hookRef.current!.onScroll(); });
  eq(bottomDistance(), 120, "M: the reader travel leaves the floor");

  // 宽度重排：读者位置必须保持——见证不命中（距 floor 120px > 容差），
  // 行/块锚定语义与修复前逐字节一致。
  await widthRewrapFrame(620);
  flushAll();
  eq(bottomDistance(), 120, `M: the width compensation keeps the reader position (bottomDistance=${bottomDistance()})`);
  ok(bottomDistance() !== 0, "M: the reader is NOT dragged back to the bottom");
  await unmount();
}

// ── W: 干净贴底 + 宽度重排——无条件回底（tl 1699 A 腿保持）──────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  diagnostics.length = 0;
  await mount("t1776-w");
  flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "W: the view starts pinned at the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "W: the intent is tail");

  // 重排同时内容变高（重折行）：补偿按贴底事实回新底。
  geom.height = 4600;
  await widthRewrapFrame(620);
  flushAll();
  eq(hookRef.current!.kernel.intent, "tail", "W: the clean at-bottom rewrap keeps the tail intent");
  eq(bottomDistance(), 0, `W: the width compensation re-pins the new bottom (bottomDistance=${bottomDistance()})`);
  await unmount();
}

process.stdout.write(`\n[tl 1776] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
