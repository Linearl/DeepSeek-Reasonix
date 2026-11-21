// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1740-reflow-wheel-intent.test.tsx
//
// tl 1740 — 侧栏宽度变化窗口内用户上滚被强制回底（P1：转录区失去读者控制权）。
//
// 现场机制（20261121 用户实测 + 协调线两轮复测确认）：
//   1. 侧栏展开/收起骑 `.layout { transition: grid-template-columns 0.16s }`，
//      scroller 每帧重折行（tl 1255 机制），~10 帧内 clientWidth 持续变化；
//   2. 窗口内的 scroll 观察被 useTranscriptKernel.onScroll 的 reflowed 判定
//      命中（live clientWidth 与 compensatedWidthRef 差 > 0.5px）；
//   3. transcriptKernel.observeNativeScroll 的 reflowed 分支（tl 1699 洞 2 的
//      保护）把整条观察吞掉——intent 留在 tail；
//   4. 同帧 ResizeObserver widthChanged 投递走 tl 1699 的宽度补偿，
//      resolveWidthTarget 按「重排前冻结的 facts.atBottom=true」选 tail 目标 →
//      无条件回底写——用户上滚被拽回底部。
//   用户实测临时规避：切宽度后立即补滚一次即可恢复非贴底（补滚落在稳态、
//   reflowed=false、观察放行、intent 切 reader）。
//
// 修法（任务书方案 1）：重排期间仍用滚轮真实位移推导 intent。
//   - wheel 上滚输入（deltaY < 0）是明确的读者意图信号，不携带锚定污染：
//     hook 的 onWheel 记录上滚时间戳，observeNativeScroll 的 reflowed 分支
//     见到未过期的上滚证据时放行观察、照常推导 intent/anchor；
//   - 无滚轮证据的 reflowed 观察保持吞（tl 1699 洞 2 的保护不变）；
//   - resolveWidthTarget 在 kernel.intent 已是 reader（真实上滚切过来的）
//     时不再按冻结 tail 回底——否则同帧补偿仍会把 reader 拽回底部；
//   - 两处早退都打诊断点（reflow-suppressed / reflow-wheel-intent）取证。
//
// 验收矩阵：
//   E  贴底 tail + reflow 窗口内 wheel 上滚：intent 切 reader、不回底；
//      手势结束后保持读者位置（临时规避不再需要——单次上滚直接生效）；
//      诊断流有 reflow-wheel-intent 放行打点。
//   G  贴底 tail + reflow 窗口内无滚轮证据的 scroll（tl 1699 洞 2 场景）：
//      观察仍吞（intent 留 tail）、手势结束回底；诊断流有 reflow-suppressed
//      早退打点（tl 1699 行为保持 + 本件取证）。

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

// ── DOM 全局（动态 import hook 之前就位，tl 1699 同款）────────────────
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

// ── 诊断流收集（早退原因打点的读数来源）────────────────────────────────
type DiagnosticEvent = { type: string; outcome?: string; intent?: string };
const diagnostics: DiagnosticEvent[] = [];
setTranscriptScrollDiagnosticSink((type, fields) => {
  diagnostics.push({ type, outcome: fields.outcome as string | undefined, intent: fields.intent as string | undefined });
});

// ── 滚动容器几何 stub（tl 1699 同款）────────────────────────────────────
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

/** 用户滚轮上滚：真实输入（生产顺序 wheel 事件先于其派生的 scroll 事件）。 */
function wheelUp(deltaY = -120): void {
  const element = hookRef.current!.scrollElement;
  element!.dispatchEvent(new dom.window.WheelEvent("wheel", { deltaY }));
}

/**
 * re-flow 的一帧（生产顺序，tl 1699 同款）：
 *   1. 布局变了（宽度变 → 行重新折行 → 内容变高）；
 *   2. 浏览器的 scroll anchoring 改了 scrollTop（anchorShift）；
 *   3. 手势在场时的 scroll 观察（可选）；
 *   4. ResizeObserver 交付（布局后、绘制前）→ 同步补偿。
 */
async function rewrapFrame(options: {
  width: number;
  blocks: Record<string, BlockGeom>;
  height: number;
  anchorShift: number;
  gesture: boolean;
}): Promise<void> {
  bumpFrame();
  geom.clientW = options.width;
  geom.blocks = options.blocks;
  geom.height = options.height;
  geom.top = Math.max(0, Math.min(geom.top + options.anchorShift, bottomOf()));
  if (options.gesture) {
    await act(async () => { hookRef.current!.onScroll(); });
  }
  await act(async () => { FakeResizeObserver.fireAll(options.width, geom.client); });
}

process.stdout.write("\n[tl 1740] reflow window: a real upward wheel keeps the reader in control\n");

// ── E: 贴底 tail + reflow 窗口内 wheel 上滚——intent 切 reader、不回底 ─────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  diagnostics.length = 0;
  await mount("t1740-e");
  flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "E: the view starts pinned at the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "E: the intent is tail");

  // 帧 1（侧栏动画开始）：宽度 800→620，内容变高 600px；锚定击穿把 scrollTop
  // 抬高 100px。widthChanged 补偿按冻结 facts（贴底 tail）回底——既有机制。
  await rewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1400 }, "blk-b": { top: 1400, height: 3200 } },
    height: 4600,
    anchorShift: 100,
    gesture: false,
  });
  eq(bottomDistance(), 0, "E: frame 1's width compensation re-pins the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "E: frame 1 keeps the tail intent");

  // 帧 2（动画未结束，宽度仍在变）：用户滚轮上滚。生产顺序——wheel 事件先到
  // （手势租约 + 上滚证据），浏览器执行位移（-120px），scroll 观察看到
  // reflowed=true（clientWidth 600 ≠ 补偿见证 620）。
  wheelUp(-120);
  bumpFrame();
  geom.clientW = 600;
  geom.blocks = { "blk-a": { top: 0, height: 1400 }, "blk-b": { top: 1400, height: 3250 } };
  geom.height = 4650;
  geom.top = Math.max(0, Math.min(geom.top + 50, bottomOf())) - 120;
  await act(async () => { hookRef.current!.onScroll(); });
  await act(async () => { FakeResizeObserver.fireAll(600, geom.client); });
  flushAll();
  eq(bottomDistance(), 120, `E: the wheel displacement lands (bottomDistance=${bottomDistance()})`);
  eq(hookRef.current!.kernel.intent, "reader", "E: the in-reflow upward wheel switches the intent to reader");
  eq(bottomDistance(), 120, "E: the width compensation does NOT drag the view back to the bottom");
  ok(diagnostics.some((event) => event.type === "kernel" && event.outcome === "reflow-wheel-intent"),
    "E: the pass-through observation is recorded on the diagnostic stream (reflow-wheel-intent)");

  // 手势结束：读者态必须保持（临时规避验证——单次上滚直接生效，无需补滚）。
  await act(async () => { hookRef.current!.endGesture(); });
  flushAll();
  bumpFrame();
  flushAll();
  eq(hookRef.current!.kernel.intent, "reader", "E: the gesture end does not switch back to tail");
  eq(bottomDistance(), 120, `E: the reader stays put after the gesture ends (bottomDistance=${bottomDistance()})`);
  await unmount();
}

// ── G: 无滚轮证据的 reflow 观察——tl 1699 洞 2 行为保持 + 早退打点 ──────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  diagnostics.length = 0;
  await mount("t1740-g");
  flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "G: the view starts pinned at the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "G: the intent is tail");

  // 手势在场（无 wheel 证据——锚定击穿产生的 scroll 观察），侧栏展开一帧。
  hookRef.current!.beginGesture();
  await rewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1400 }, "blk-b": { top: 1400, height: 3200 } },
    height: 4600,
    anchorShift: 100,
    gesture: true,
  });
  eq(hookRef.current!.kernel.intent, "tail", "G: a wheel-less reflow observation is still suppressed (tl 1699 hole 2 kept)");
  ok(diagnostics.some((event) => event.type === "kernel" && event.outcome === "reflow-suppressed"),
    "G: the suppressed observation is recorded on the diagnostic stream (reflow-suppressed)");

  // 手势结束 → 尾部补贴把视图拉回底部（tl 1699 A 腿语义保持）。
  await act(async () => { hookRef.current!.endGesture(); });
  flushAll();
  bumpFrame();
  flushAll();
  eq(bottomDistance(), 0, `G: the tail re-pins to the bottom once the gesture ends (bottomDistance=${bottomDistance()})`);
  await unmount();
}

process.stdout.write(`\n[tl 1740] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
