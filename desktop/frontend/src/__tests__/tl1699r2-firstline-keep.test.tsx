// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1699r2-firstline-keep.test.tsx
//
// tl 1699r2 — 非贴底首行漂移返修（tl 1699 首轮只保住了贴底态）。
//
// 首轮的缺口（本件的根因，两个叠加机制）：
//   ① 首行锚粒度不足：宽度补偿的恢复目标是 block 级锚 {blockKey, offsetPx}——
//      offsetPx 是「视口顶距块顶的像素」。rewrap 改变块内折行，同一像素偏移
//      指向不同文本行 → 首行漂移。首轮的 firstVisibleAnchor 兜底同为 block 级；
//      首轮 B 腿的 harness 用块矩形建模（无行概念），断言「块顶+offset」= 假绿。
//   ② 753 漂移门拒绝 fresh 首行恢复：大宽度变化时补偿位移 Δ 超过
//      max(320, clientHeight) → 门拒写 + 重锚当前可见首行 = 把漂移后的像素
//      定格成「正确」。
//
// 修法（两洞一线）：
//   行级首行锚（LineAnchor）：DOM Range 持「视口顶切割的那一字符」——rewrap
//   改变折行，不改文本内容与节点身份，Range 跨布局稳定。捕获用
//   caretRangeFromPoint（平台原语；.transcript 是 overflow-anchor: none，
//   内核独占锚定语义，所以浏览器的滚动锚定不会替我们做行级保持，必须显式做）。
//   恢复时重测 Range 在新布局的位置，换算成 block 锚写入既有补偿通道，并带
//   freshFirstLine 标记——753 门对 fresh 行级目标豁免（它永远指向读者此刻
//   看着的行，不可能是「瞬移回离开已久的消息」）；块级降级目标不带标记、
//   门保持原样（tl 1255 E 腿字节不变）。数值口径（ANCHOR_DRIFT_MIN_PX /
//   max(..., clientHeight)）不动。
//
// 验收矩阵（两态 + 降级 + 回归钉）：
//   R1 中部停驻（视口顶伸入块内 200px=第 8 行），开侧栏 rewrap 且块内行数
//      变化（3000→2800）：改前恢复到「块顶+旧 offset」=1700（首行漂一行），
//      改后恢复到「捕获行新位置」=1675（首行不动）→ red-to-green 主钉。
//   R2 大宽度变化（Δ=3000 ≫ 门 500）：fresh 行级恢复照常落笔（门豁免），
//      首行不动。
//   R3 caret API 不可用（降级链）：块级恢复 1700 = 首轮行为，门语义不变。
//   R3b 降级链 + 超门位移：门仍拒（no composer-resize write）——豁免只给
//      fresh 行级目标，tl 1255 E 腿字节保持。
//   R4 贴底态 + rewrap：无条件回底（洞 1 不回归）；贴底时不捕获行锚。
//   R5 transition 逐帧 rewrap：每帧行级保持（捕获→恢复→重捕获链式自洽），
//      视口顶行的视口位置逐帧为 0。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { TranscriptKernel, TranscriptViewportSnapshot } from "../lib/transcriptKernel";
import type { useTranscriptKernel } from "../lib/useTranscriptKernel";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";
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

// ── DOM 全局（动态 import hook 之前就位，tl 1255/1699 同款）────────────
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

// ── 写探针 sink（验收读数来源）──────────────────────────────────────────
const writes: TranscriptScrollWriteRecord[] = [];
(window as unknown as { __REASONIX_TRANSCRIPT_SCROLL_WRITE__?: (write: TranscriptScrollWriteRecord) => void })
  .__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => writes.push(write);

// ── 滚动容器几何 stub + 行模型 ─────────────────────────────────────────
// 块仍是矩形 stub（top/height），但每块内放一个「行探针」span（含 500 字符
// 文本节点）。行高恒定 ROW_H；caretRangeFromPoint / Range.getClientRects 的
// 假实现按行模型应答：
//   捕获（caretRangeFromPoint）：视口顶+1 落在第几行 → Range{文本节点, 行号}，
//     并把「捕获时的行号与块行数」记进 lineSim（模拟真实 Range 锚定的是字符、
//     捕获时刻的布局）。
//   重测（getClientRects）：同一锚在「当前」布局中的行号 k' = round(k × 新行数/旧行数)
//     ——文本均匀折行的近似，正是真实 Range 跨 rewrap 的行为（同一文本行，
//     行序号因上方折行变化而移动）。块顶随 geom.blocks 实时取值。
const ROW_H = 25;
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
function rowsOf(block: BlockGeom): number {
  return Math.max(1, Math.round(block.height / ROW_H));
}

function rect(top: number, height: number): DOMRect {
  return { top, bottom: top + height, left: 0, right: geom.clientW, width: geom.clientW, height, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
}

// 行模拟状态：文本节点 → {块 key, 捕获行号, 捕获时行数}
const lineSim = new WeakMap<Text, { blockKey: string; row: number; rows: number }>();
const spanByKey = new Map<string, HTMLSpanElement>();
/** 重测：锚定文本在「当前布局」的行盒（视口坐标）。 */
function lineBoxRect(text: Text): DOMRect | null {
  const sim = lineSim.get(text);
  if (!sim) return null;
  const block = geom.blocks[sim.blockKey];
  if (!block) return null;
  const currentRows = rowsOf(block);
  // 同一文本行在新布局中的行序号：k' = round(k × rows'/rows)。
  const newRow = Math.min(currentRows - 1, Math.max(0, Math.round((sim.row * currentRows) / sim.rows)));
  const clientTop = block.top - geom.top;
  return rect(clientTop + newRow * ROW_H, ROW_H);
}
// Range 测量按行模型应答（仅对行探针文本节点；其余走 jsdom 空实现）。
const rangePrototype = dom.window.Range.prototype as unknown as {
  getClientRects(): DOMRectList;
  getBoundingClientRect(): DOMRect;
};
const nativeGetClientRects = rangePrototype.getClientRects;
rangePrototype.getClientRects = function (): DOMRectList {
  const text = this.startContainer as Text;
  const box = lineBoxRect(text);
  if (box) return [box] as unknown as DOMRectList;
  return nativeGetClientRects.call(this);
};
const nativeGetBoundingClientRect = rangePrototype.getBoundingClientRect;
rangePrototype.getBoundingClientRect = function (): DOMRect {
  const text = this.startContainer as Text;
  const box = lineBoxRect(text);
  if (box) return box;
  return nativeGetBoundingClientRect.call(this);
};
// caretRangeFromPoint：视口顶+1 落在哪一块的第几行。
(dom.window.document as unknown as { caretRangeFromPoint?: unknown }).caretRangeFromPoint = (x: number, y: number) => {
  const hit = Object.entries(geom.blocks).find(([, block]) => {
    const clientTop = block.top - geom.top;
    return y >= clientTop && y < clientTop + block.height;
  });
  if (!hit) return null;
  const [blockKey, block] = hit;
  const span = spanByKey.get(blockKey);
  const text = span?.firstChild as Text | undefined;
  if (!text) return null;
  const clientTop = block.top - geom.top;
  const row = Math.max(0, Math.floor((y - clientTop) / ROW_H));
  lineSim.set(text, { blockKey, row, rows: rowsOf(block) });
  return { startContainer: text, startOffset: row, endContainer: text, endOffset: row };
};
/** R3 用：卸掉 caret 能力（模拟老引擎 / 无头测试 DOM）。 */
function removeCaretApi(): void {
  delete (dom.window.document as unknown as { caretRangeFromPoint?: unknown }).caretRangeFromPoint;
}

const geomBound = new WeakSet<Element>();
function ensureScrollerStubs(node: HTMLDivElement): void {
  if (geomBound.has(node)) return;
  geomBound.add(node);
  for (const key of Object.keys(geom.blocks)) {
    const block = document.createElement("div");
    block.dataset.transcriptBlockKey = key;
    block.getBoundingClientRect = () => rect(geom.blocks[key]!.top - geom.top, geom.blocks[key]!.height);
    // 行探针 span：Range/caret 锚定的宿主（500 字符保证任意行号可 set）。
    const probe = document.createElement("span");
    probe.dataset.lineProbe = key;
    probe.textContent = "x".repeat(500);
    block.appendChild(probe);
    spanByKey.set(key, probe);
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
 * re-flow 的一帧（生产顺序）：布局已变（宽度/块几何）、scrollTop 不动
 * （.transcript 是 overflow-anchor: none，浏览器不锚定）、RO 交付 → 同步补偿。
 */
async function rewrapFrame(options: {
  width: number;
  blocks: Record<string, BlockGeom>;
  height: number;
  anchorShift?: number;
}): Promise<void> {
  bumpFrame();
  geom.clientW = options.width;
  geom.blocks = options.blocks;
  geom.height = options.height;
  if (options.anchorShift) geom.top = Math.max(0, Math.min(geom.top + options.anchorShift, bottomOf()));
  await act(async () => { FakeResizeObserver.fireAll(options.width, geom.client); });
}

function snap(scrollTop: number): TranscriptViewportSnapshot {
  return {
    scrollTop,
    scrollHeight: geom.height,
    clientHeight: geom.client,
    visibleBlocks: Object.entries(geom.blocks).map(([key, block]) => ({ key, top: block.top, bottom: block.top + block.height })),
  };
}
/** 用户真实上翻到 top（位移观察 → reader + 当前布局的块锚）。 */
function scrollUpTo(kernel: TranscriptKernel, top: number): void {
  kernel.beginUserGesture(snap(top), "native");
  kernel.observeNativeScroll(snap(top));
  kernel.endUserGesture();
}
/** 让 hook 的滚动观察路径跑一遍 → capturePreReflowFacts 捕获行级首行锚。
 * 先 bumpFrame：生产里 scroll 事件必然落在新的一帧（读几何缓存按帧失效），
 * 不推帧会命中上一帧的合并缓存（tl 1699 首轮 harness 同款语义）。 */
async function observeScrollForLineCapture(): Promise<void> {
  bumpFrame();
  await act(async () => { hookRef.current!.onScroll(); });
}

process.stdout.write("\n[tl 1699r2] width reflow keeps the reader's first LINE (not just the block)\n");

// ── R1: 中部停驻 + 块内行数变化——首行按「行」保持（red-to-green 主钉）────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699r2-r1");
  flushAll();
  await fireInitialObservation();

  // 视口顶伸入 blk-b 内部 200px（= 第 8 行，行高 25）。
  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  eq(hookRef.current!.kernel.intent, "reader", "R1: scrolled up parks as reader");
  await observeScrollForLineCapture();

  // 开侧栏一帧：blk-a 变高（blk-b top 1000→1500）、blk-b 行数变少
  // （3000→2800，120 行→112 行）。捕获的第 8 行在新布局位于块内 175px 处
  // （行序号 8×112/120≈7.47→7）。
  await rewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1500 }, "blk-b": { top: 1500, height: 2800 } },
    height: 4300,
  });
  // 行级目标：1500 + 175 = 1675（捕获行回到视口顶 0px 处）。
  // 块级目标（首轮）：1500 + 200 = 1700（首行向上漂 25px = 一行）。
  eq(geom.top, 1675, `R1: the first line is kept at the line level (expected 1675, got ${geom.top})`);
  ok(writes.some((write) => write.owner === "composer-resize"), "R1: the keep-alive rode the composer-resize owner");
  // 首行视觉位置核对：捕获行的新行盒顶 - 视口顶 = 0。
  const firstLineClientTop = (1500 - geom.top) + 7 * ROW_H;
  eq(firstLineClientTop, 0, "R1: the captured line's box sits exactly at the viewport top after the rewrap");
  await unmount();
}

// ── R2: 大宽度变化（Δ ≫ 漂移门）——fresh 行级恢复不被 753 拒 ─────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699r2-r2");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  await observeScrollForLineCapture();

  // 大宽度展开：blk-a 1000→4000（补偿位移 |4200-1200|=3000 ≫ max(320,500)）。
  await rewrapFrame({
    width: 400,
    blocks: { "blk-a": { top: 0, height: 4000 }, "blk-b": { top: 4000, height: 3000 } },
    height: 7000,
  });
  // 行级目标 = 4000 + 8×25 = 4200（块高不变，捕获行仍在块内 200px 处）。
  eq(geom.top, 4200, `R2: the fresh line target writes through the gate (expected 4200, got ${geom.top})`);
  const write = writes.find((entry) => entry.owner === "composer-resize");
  ok(write != null, "R2: the over-threshold keep-alive still wrote (freshFirstLine exemption)");
  await unmount();
}

// ── R3: caret API 不可用——降级到首轮块级行为 ────────────────────────────
{
  resetGeom();
  removeCaretApi();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699r2-r3");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  await observeScrollForLineCapture();

  await rewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1500 }, "blk-b": { top: 1500, height: 2800 } },
    height: 4300,
  });
  // 块级降级：1500 + 200 = 1700（tl 1699 首轮语义，门内放行 Δ=500 ≤ 门）。
  eq(geom.top, 1700, `R3: the degraded block-level fallback matches the first round (expected 1700, got ${geom.top})`);
  await unmount();
}

// ── R3b: 降级链 + 超门位移——门仍拒（豁免只给 fresh 行级目标）────────────
// 构造镜像 tl 1255 E 腿：先小步 rewrap 建立对齐基准（gate 的 sameAnchorAligned
// 需要 alignedAnchor 在场），再让锚块位置剧变——降级块级目标不带 fresh 标记，
// 超门位移仍被拒、保像素、重锚可见首行。
{
  resetGeom();
  removeCaretApi();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699r2-r3b");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  await observeScrollForLineCapture();

  // 小步重排建立 alignedAnchor（块级补偿，门内放行）。
  await rewrapFrame({
    width: 780,
    blocks: { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3000 } },
    height: 4240,
  });
  eq(geom.top, 1440, "R3b setup: the small rewrap compensated (aligned on blk-b)");

  // 锚块位置剧变（blk-b top 1240→3600，位移 2360 ≫ 门 500）：降级块级目标
  // 仍被门拒——fresh 行级豁免不外溢到块级降级链。
  writes.length = 0;
  await rewrapFrame({
    width: 760,
    blocks: { "blk-a": { top: 0, height: 3600 }, "blk-b": { top: 3600, height: 2000 } },
    height: 5600,
  });
  ok(!writes.some((entry) => entry.owner === "composer-resize"), "R3b: the degraded block target is still gated (tl 1255 leg E kept)");
  eq(geom.top, 1440, "R3b: the viewport keeps the reader's pixels (no teleport)");
  const anchor = hookRef.current!.kernel.anchor as { kind: string; blockKey?: string };
  ok(anchor.kind === "block" && anchor.blockKey === "blk-a", "R3b: the anchor re-based to the visible block (753 semantics)");
  await unmount();
}

// ── R4: 贴底态 + rewrap——无条件回底（洞 1 不回归），贴底不捕获行锚 ─────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699r2-r4");
  flushAll();
  await fireInitialObservation();
  eq(hookRef.current!.kernel.intent, "tail", "R4: the intent is tail");
  eq(bottomDistance(), 0, "R4: the view starts pinned at the bottom");

  // 内容变高 600px、视口停在原处（贴底失守）→ 宽度补偿必须无条件回底。
  geom.height = 4600;
  geom.blocks = { "blk-a": { top: 0, height: 1600 }, "blk-b": { top: 1600, height: 3000 } };
  geom.top = 3500;
  writes.length = 0;
  await rewrapFrame({ width: 620, blocks: geom.blocks, height: 4600 });
  eq(bottomDistance(), 0, `R4: the tail re-pins unconditionally after the rewrap (bottomDistance=${bottomDistance()})`);
  ok(writes.some((entry) => entry.kind === "pinTail"), "R4: the re-pin rode the tail write");
  await unmount();
}

// ── R5: transition 逐帧 rewrap——每帧行级保持（重捕获链式自洽）────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699r2-r5");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  await observeScrollForLineCapture();

  // 帧 1：blk-a 1000→1240（blk-b top 1000→1240），补偿写 1240+200=1440。
  await rewrapFrame({
    width: 780,
    blocks: { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3000 } },
    height: 4240,
  });
  eq(geom.top, 1440, `R5 frame 1: the first line is kept (expected 1440, got ${geom.top})`);
  // 帧 2（新宽度下重捕获后再 rewrap）：blk-a 1240→1480，补偿写 1480+200=1680。
  await rewrapFrame({
    width: 760,
    blocks: { "blk-a": { top: 0, height: 1480 }, "blk-b": { top: 1480, height: 3000 } },
    height: 4480,
  });
  eq(geom.top, 1680, `R5 frame 2: the next frame keeps the re-captured line (expected 1680, got ${geom.top})`);
  // 每帧后视口顶行的视口位置恒为 0（首行视觉零漂移）。
  const frame2LineClientTop = (1480 - geom.top) + 8 * ROW_H;
  eq(frame2LineClientTop, 0, "R5: the viewport-top line's box sits at the viewport top in every frame");
  ok(writes.filter((entry) => entry.owner === "composer-resize").length >= 2, "R5: every transition frame got its own keep-alive write");
  await unmount();
}

process.stdout.write(`\n[tl 1699r2] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
