// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1255-sidebar-transition-frame-sync.test.tsx
//
// tl 1255 — tl 1243 修复后用户实测侧栏展开/收起滚动条仍跳（返修）。
//
// 真实触发源（本次复现的机制）：Windows 路径侧栏开合不是单步重排，而是
// `.layout { transition: grid-template-columns 0.16s ease }` 的连续宽度动画
// （styles.css；darwin 0.28s 同理）——每帧重排一次，ResizeObserver 每帧交付。
//
// tl 1243 缺口（owner 归因）：宽度补偿链有两级 rAF 延迟（观察者内部
// afterCurrentGenerationPaint 一级 + settleGeometry 的 geometryWork 再一级），
// 写笔落后交付 1~2 个绘制帧。滚动画期间每帧重排先以未补偿位置上屏——用户
// 看到的就是持续跳动。tl 1243 的 41 断言全绿是因为手控 RO + 每步 flushAll
// 只断言终态，中间帧从未被断言。
//
// 修复契约（本件）：RO 交付发生在「布局后、绘制前」——宽度变化交付必须
// 同步落笔（commitViewportGeometry(covered, true) 现成 beforePaint 机制），
// 写入当帧即生效；且交付本身是「本帧布局已变」的证据，同帧更早的
// frameSnapshot 合并缓存读必须失效（滚动锚定/钳制会让同帧早读的 scrollTop
// 与交付时事实发散，毒化 753 漂移门判定）。
//
// 验收矩阵：
//   A  tail 用户场景复现：10 步连续宽度动画（0.16s ease 的逐帧离散化）
//      逐帧断言底部距离=0（不 flush、不 bumpFrame——断言的就是本帧绘制前
//      补偿已落笔）；展开/收起三轮。修前：交付被 rAF 延迟吞掉，逐帧断言红。
//   B  reader 用户场景复现：同动画逐帧断言 scrollTop=块新 top+offsetPx
//      （视口下内容不变），意图不降级、永不写 Infinity。
//   C  单次交付同步契约：一次宽度交付、零 flush，写已落定。
//   D  同帧早读失效：rAF 相位的 settle 读缓存后，重排+浏览器滚动锚定模拟
//      （scrollTop 与缓存读发散 > 漂移门），交付时必须读新鲜事实放行补偿；
//      缓存未失效则误判 anchor-drift 拒写（红）。
//   E  回归：高度-only 保持 267 R3（延迟交付+仅 tail 补贴、reader 零写入）；
//      首帧观察分类无变化；真实超门漂移经同步路径仍被 753 拒绝+重锚。

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

// ── DOM 全局（在动态 import hook 之前就位，tl 1243 同款）────────────────
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

// ── 滚动容器几何 stub（tl 1243 同款）────────────────────────────────────
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
 * 滚动画的一帧（tl 1255 核心）：宽度步进 + RO 交付。真实浏览器里交付发生在
 * 「布局后、绘制前」——不 bumpFrame、不 flushAll，紧随其后的断言就是
 * 「本帧绘制前补偿必须已落笔」。修前交付被两级 rAF 延迟吞掉，断言红。
 */
async function fireAnimationFrameOfTransition(width: number): Promise<void> {
  await act(async () => { FakeResizeObserver.fireAll(width, geom.client); });
}

/** 0.16s ease 滚动画的逐帧离散化：800↔620，10 步，块高随宽度单调变化。 */
function rewrapBlocks(width: number): { blocks: Record<string, BlockGeom>; height: number } {
  const extra = Math.round((800 - width) / 180 * 600); // 800→0px，620→600px
  const blocks = {
    "blk-a": { top: 0, height: 1000 + extra },
    "blk-b": { top: 1000 + extra, height: 3000 + extra * 3 },
  };
  return { blocks, height: blocks["blk-b"].top + blocks["blk-b"].height };
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

process.stdout.write("\n[tl 1255] sidebar grid-column transition: same-frame width compensation\n");

// ── A: tail 用户场景复现——10 步连续宽度动画逐帧贴底，三轮 ────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1255-a");
  flushAll();
  eq(geom.top, bottomOf(), "A: mount lands at the bottom");
  await fireInitialObservation();

  for (let round = 1; round <= 3; round += 1) {
    // 展开：800→620 十步滚动画。
    for (let step = 1; step <= 10; step += 1) {
      const width = 800 - step * 18;
      geom.clientW = width;
      const rewrapped = rewrapBlocks(width);
      geom.blocks = rewrapped.blocks;
      geom.height = rewrapped.height;
      await fireAnimationFrameOfTransition(width);
      eq(bottomDistance(), 0, `A round ${round} expand step ${step}: viewport bottom stays pinned in the same frame (bottomDistance=${bottomDistance()})`);
    }
    ok(writes.some((write) => write.kind === "pinTail"), `A round ${round}: expand re-pins rode the single writer (pinTail)`);

    // 收起：620→800 十步滚动画。
    for (let step = 1; step <= 10; step += 1) {
      const width = 620 + step * 18;
      geom.clientW = width;
      const rewrapped = rewrapBlocks(width);
      geom.blocks = rewrapped.blocks;
      geom.height = rewrapped.height;
      geom.top = Math.min(geom.top, bottomOf());
      await fireAnimationFrameOfTransition(width);
      eq(bottomDistance(), 0, `A round ${round} collapse step ${step}: viewport bottom stays pinned in the same frame`);
    }
    ok(writes.some((write) => write.kind === "pinTail"), `A round ${round}: collapse re-pins rode the single writer`);
  }
  eq(hookRef.current!.kernel.intent, "tail", "A: tail intent survives every round");
  await unmount();
}

// ── B: reader 用户场景复现——逐帧块锚重放（视口下内容不变）───────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1255-b");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200; // 用户真实滚动到位
  scrollUpTo(hookRef.current!.kernel, 1200);
  writes.length = 0; // 清掉 reader 建立前的 tail 阶段写（含初始观察的补贴），只看 reader 腿
  eq(hookRef.current!.kernel.intent, "reader", "B: scrolled up parks as reader");
  eq((hookRef.current!.kernel.anchor as { blockKey: string }).blockKey, "blk-b", "B: anchored to blk-b");
  eq((hookRef.current!.kernel.anchor as { offsetPx: number }).offsetPx, 200, "B: anchor offset 1200-1000=200");

  for (let round = 1; round <= 2; round += 1) {
    for (let step = 1; step <= 10; step += 1) {
      const width = round % 2 === 1 ? 800 - step * 18 : 620 + step * 18;
      geom.clientW = width;
      const rewrapped = rewrapBlocks(width);
      geom.blocks = rewrapped.blocks;
      geom.height = rewrapped.height;
      geom.top = Math.min(geom.top, bottomOf());
      await fireAnimationFrameOfTransition(width);
      const expected = geom.blocks["blk-b"]!.top + 200;
      eq(geom.top, expected, `B round ${round} step ${step}: reading spot replayed to the rewrapped block position (${expected})`);
      eq(hookRef.current!.kernel.intent, "reader", `B round ${round} step ${step}: intent stays reader`);
      ok(!writes.some((write) => write.requestedOffset === Number.POSITIVE_INFINITY), `B round ${round} step ${step}: never yanked toward the bottom (no Infinity write)`);
    }
  }
  await unmount();
}

// ── C: 单次交付同步契约——零 flush，写已落定 ─────────────────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1255-c");
  flushAll();
  await fireInitialObservation();

  writes.length = 0;
  geom.clientW = 620;
  const rewrapped = rewrapBlocks(620);
  geom.blocks = rewrapped.blocks;
  geom.height = rewrapped.height;
  await fireAnimationFrameOfTransition(620);
  eq(bottomDistance(), 0, "C: a single width delivery commits before any frame flush (sync at delivery)");
  // tail 锚的补偿写经 correctAnchor 的 tail 分支落笔（owner=tail-follow/pinTail）；
  // owner=composer-resize 是 reader 块锚重放的标注（B 场景已钉）。
  eq(writes.length, 1, "C: exactly the sync compensation write raced in");
  eq(writes[0]?.kind, "pinTail", "C: the write is the tail re-pin");
  await unmount();
}

// ── D: 同帧早读失效——滚动锚定模拟，漂移门必须用新鲜事实判定 ─────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1255-d");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);

  // 小步重排建立 alignedAnchor（门只挡同锚的后续对齐）；浏览器滚动锚定把
  // scrollTop 抬到 1440（内容稳定在视口下）。
  bumpFrame();
  geom.clientW = 780;
  geom.blocks = { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3000 } };
  geom.height = 4240;
  geom.top = 1440;
  await fireAnimationFrameOfTransition(780);
  eq(geom.top, 1440, "D round 1: small rewrap compensates (gate lets a legit correction through)");

  // 同帧早读：rAF 相位的 settle（如流式 tail 几何提交）先读一次 → 进合并缓存。
  bumpFrame();
  await act(async () => {
    hookRef.current!.commitViewportGeometry();
    flushAll();
  });

  // 重排 + 浏览器滚动锚定：blk-a 再长 600，锚定把 scrollTop 抬到 2040 ——
  // 与缓存读的 1440 发散 600 > 漂移门 max(320,500)。
  geom.clientW = 760;
  geom.blocks = { "blk-a": { top: 0, height: 1840 }, "blk-b": { top: 1840, height: 3000 } };
  geom.height = 4840;
  geom.top = 2040;
  writes.length = 0;
  await fireAnimationFrameOfTransition(760);
  const poisonedWrite = writes.find((write) => write.owner === "composer-resize");
  ok(poisonedWrite != null, "D: the delivery must read fresh same-frame facts, not a cached pre-reflow read");
  eq(poisonedWrite?.requestedOffset, 2040, "D: compensation target = fresh anchor top 1840 + offset 200");
  const dAnchor = hookRef.current!.kernel.anchor as { kind: string; blockKey?: string };
  ok(dAnchor.kind === "block" && dAnchor.blockKey === "blk-b", "D: anchor stays blk-b (no false anchor-drift re-base)");
  await unmount();
}

// ── E: 回归——267 R3 高度-only 语义 / 首帧观察 / 真实超门漂移仍拒 ────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1255-e");
  flushAll();
  await fireInitialObservation();
  ok(!writes.some((write) => write.owner === "composer-resize"), "E: the initial observation classifies as unchanged");

  // 高度-only（267 R3）：延迟交付语义不变，tail 拿补贴、不开结构事务。
  writes.length = 0;
  geom.client = 560;
  bumpFrame();
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  flushAll();
  flushAll();
  ok(!writes.some((write) => write.owner === "composer-resize"), "E: height-only opens no structural transaction");
  eq(bottomDistance(), 0, "E: tail still re-pins via the guarded subsidy (267 R3)");

  // reader + 高度-only：零写入。
  geom.top = 900;
  scrollUpTo(hookRef.current!.kernel, 900);
  writes.length = 0;
  geom.client = 640;
  bumpFrame();
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  flushAll();
  flushAll();
  eq(writes.length, 0, "E: height-only never writes under a reader (267 R3)");
  eq(geom.top, 900, "E: reader position untouched by a height-only change");

  // 恢复 800 宽布局并重建 blk-b 锚（镜像 tl 1243 D 的漂移腿前置序列）：
  geom.client = 500;
  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  bumpFrame();
  geom.clientW = 780;
  geom.blocks = { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3000 } };
  geom.height = 4240;
  geom.top = 1440;
  await fireAnimationFrameOfTransition(780);
  eq(geom.top, 1440, "E: small width rewrap establishes the aligned blk-b anchor");
  eq((hookRef.current!.kernel.anchor as { blockKey: string }).blockKey, "blk-b", "E: anchor is blk-b before the drift leg");

  // 真实超门漂移经同步路径仍被 753 拒绝：blk-b top 1240→3000（位移 1760 ≫ 500）。
  writes.length = 0;
  geom.clientW = 760;
  geom.blocks = { "blk-a": { top: 0, height: 3600 }, "blk-b": { top: 3600, height: 2000 } };
  geom.height = 5600;
  geom.top = Math.min(geom.top, bottomOf());
  await fireAnimationFrameOfTransition(760);
  ok(!writes.some((write) => write.owner === "composer-resize"), "E: a genuinely drifted anchor restore is refused, no write");
  eq(geom.top, 1440, "E: viewport stays where the reader is (no teleport)");
  const anchor = hookRef.current!.kernel.anchor as { kind: string; blockKey?: string; offsetPx?: number };
  ok(anchor.kind === "block" && anchor.blockKey === "blk-a" && anchor.offsetPx === 1440,
    "E: anchor re-based to the visible block (753 semantics)");
  await unmount();
}

process.stdout.write(`\n[tl 1255] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
