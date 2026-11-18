// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1699-sidebar-width-reflow-anchor.test.tsx
//
// tl 1699 — 展开/收起侧栏（会话宽度 re-flow）后滚动位置漂移（tl 1255 机制的两态缺口）。
//
// 机制沿用 tl 1255（ResizeObserver widthChanged → 同步 commitViewportGeometry：
// tail 路 re-pin bottom + reader 路 block anchor 重放），但实测仍漂。三个洞：
//
//   洞 1（贴底态无补偿）：几何将要变化的补偿入口 beginAnchorRestore 的守卫是
//        `intent !== "reader" → return null`——贴底态被整个挡掉。于是渲染层
//        的坐标系释放（虚拟化窗口的 origin release）发生时，没有任何补偿，
//        内容在视口下平移 → 贴底态漂走、也不再跟随输出。
//        修法：意图只决定补偿的「目标」，不再决定「有没有补偿」——贴底态开
//        出尾部补偿，reflow 后无条件回写 bottom。
//
//   洞 2（贴底判定的时机）：tail 的「是否贴底」判定若发生在 re-flow 之后的
//        layout 上，读到的是浏览器 scroll anchoring 已经改过的 scrollTop
//        （内容变高 → 距离底部变远 → 读数「没贴底」）→ tail 意图被降级成
//        reader → re-pin 不再触发。
//        修法：宽度补偿的目标取自 reflow 之前的冻结状态（kernel 的粘性
//        atBottom 判定 + 冻结的逻辑锚），并且 re-flow 期间的 scroll 观察
//        不再改写意图（observeNativeScroll 的 reflowed 分支）。
//
//   洞 3（753 漂移门）：复核结论——门保留，宽度结构补偿不豁免。tl 1255 的 E
//        腿明确钉住「真实超门漂移经同步路径仍被 753 拒绝+重锚」，753 的语义
//        是「不瞬移」：宁可停在读者当前看到的像素，也不把视口搬到别处。
//        本件因此不动门本身（数值口径与锚点主语义都不动），只保证宽度补偿
//        的「目标」取自 reflow 前——不再被 re-flow 之后的测量污染。
//
// 验收矩阵：
//   A  贴底态 + 手势在场：re-wrap 期间的 scroll 事件不得降级 tail 意图；
//      手势结束后 tail 必须回到底部（洞 2）。
//   B  读者态 + 大 re-wrap（> 漂移门）：宽度补偿必须落笔，首行不变（洞 3）。
//   C  贴底态的几何补偿入口必须开出补偿并回写底部（洞 1）。
//   D  753 语义不弱化：普通 restore 路径仍拒写陈旧锚；宽度路径上真实超门的
//      位移也仍被拒（tl 1255 E 腿的语义，本件不动门）。

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

// ── DOM 全局（动态 import hook 之前就位，tl 1255 同款）────────────────
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

// ── 滚动容器几何 stub（tl 1255 同款）────────────────────────────────────
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
 * re-flow 的一帧（生产顺序）：
 *   1. 浏览器进入新的一帧（几何读取缓存按帧失效 → 读到的是本帧的事实）；
 *   2. 布局变了（宽度变 → 行重新折行 → 内容变高）；
 *   3. 浏览器的 scroll anchoring 改了 scrollTop → 产生一个 scroll 事件
 *      （scroll 事件在「滚动步骤」里派发，早于同帧的 ResizeObserver 交付）；
 *   4. ResizeObserver 交付（布局后、绘制前）→ 同步补偿。
 * 修前：第 3 步把 tail 意图降级（洞 2），第 4 步的补偿因此不再回写底部。
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
    // 手势在场（滚轮租约 / 手指未抬起）时的 scroll 观察。
    await act(async () => { hookRef.current!.onScroll(); });
  }
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

process.stdout.write("\n[tl 1699] sidebar width reflow: tail keeps the bottom, reader keeps the first line\n");

// ── A: 贴底态 + 手势在场——re-wrap 的 scroll 事件不得降级 tail 意图 ──────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699-a");
  flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "A: the view starts pinned at the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "A: the intent is tail");

  // 手势在场（滚轮租约还活着），侧栏展开：内容上面的行重新折行 → 内容变高
  // 600px；浏览器的 scroll anchoring 把 scrollTop 抬高 100px（虚拟化重建了
  // DOM 节点，锚定只能部分兜住）→ 视口离底部 500px → 「没贴底」。
  hookRef.current!.beginGesture();
  await rewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1600 }, "blk-b": { top: 1600, height: 3000 } },
    height: 4600,
    anchorShift: 100,
    gesture: true,
  });
  eq(hookRef.current!.kernel.intent, "tail", "A: the rewrap's scroll event does not demote the tail intent");
  eq(hookRef.current!.kernel.anchor.kind, "tail", "A: the logical anchor stays the tail");

  // 手势结束 → 尾部补贴必须把视图拉回底部。
  await act(async () => { hookRef.current!.endGesture(); });
  flushAll();
  bumpFrame();
  flushAll();
  eq(bottomDistance(), 0, `A: the tail re-pins to the bottom once the gesture ends (bottomDistance=${bottomDistance()})`);
  await unmount();
}

// ── B: 读者态 + 手势在场——re-wrap 的 scroll 事件不得降级读者意图，首行不变 ──
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699-b");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  eq(hookRef.current!.kernel.intent, "reader", "B: scrolled up parks as reader");
  eq((hookRef.current!.kernel.anchor as { blockKey: string }).blockKey, "blk-b", "B: anchored to blk-b");
  eq((hookRef.current!.kernel.anchor as { offsetPx: number }).offsetPx, 200, "B: anchor offset 1200-1000=200");

  // 手势在场 + 侧栏展开一帧：上面的行重新折行（blk-a 1000→1240，blk-b 的
  // top 随之移动），浏览器的 scroll anchoring 被虚拟化重建的 DOM 击穿
  // （scrollTop 没动 → 视口下的内容已经平移），再产生一个 scroll 事件。
  hookRef.current!.beginGesture();
  writes.length = 0;
  await rewrapFrame({
    width: 780,
    blocks: { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3000 } },
    height: 4240,
    anchorShift: 0,
    gesture: true,
  });
  eq(hookRef.current!.kernel.intent, "reader", "B: the rewrap's scroll event does not demote the reader intent");
  eq((hookRef.current!.kernel.anchor as { blockKey: string }).blockKey, "blk-b", "B: the logical anchor stays the pre-rewrap block");

  // 手势结束 → 宽度补偿必须按 re-wrap 前的锚落笔（blk-b 的新 top + offset）。
  await act(async () => { hookRef.current!.endGesture(); });
  flushAll();
  bumpFrame();
  flushAll();
  ok(writes.some((write) => write.owner === "composer-resize"), "B: the width compensation rode the composer-resize owner");
  eq(geom.top, 1440, `B: the reader's first line is preserved (expected 1240+200=1440, got ${geom.top})`);
  await unmount();
}

// ── C: 贴底态的几何补偿入口必须开出补偿并回写底部（洞 1）──────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699-c");
  flushAll();
  await fireInitialObservation();
  eq(hookRef.current!.kernel.intent, "tail", "C: the intent is tail");
  eq(bottomDistance(), 0, "C: the view starts pinned at the bottom");

  // 渲染层的坐标系释放：内容变高 600px，视口下的内容平移（贴底态失守）。
  geom.height = 4600;
  geom.blocks = { "blk-a": { top: 0, height: 1600 }, "blk-b": { top: 1600, height: 3000 } };
  geom.top = 3500; // 锚定没兜住：视口停在原处，离底部 600px
  writes.length = 0;

  // 渲染层预告「几何要变了」→ 补偿入口必须开出一个补偿（不再是 null）。
  const transaction = hookRef.current!.beginAnchorRestore();
  ok(transaction != null, "C: the tail view opens a compensation for the incoming geometry change");
  await act(async () => { hookRef.current!.commitViewportGeometry(); });
  flushAll();
  eq(bottomDistance(), 0, `C: the tail is re-pinned after the geometry change (bottomDistance=${bottomDistance()})`);
  await unmount();
}

// ── D: 753 语义不弱化——普通 restore 路径仍拒写陈旧锚 ───────────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1699-d");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200;
  scrollUpTo(hookRef.current!.kernel, 1200);
  bumpFrame();
  // 建立对齐基准（一次小 re-wrap 的补偿）。
  await rewrapFrame({
    width: 780,
    blocks: { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3000 } },
    height: 4240,
    anchorShift: 0,
    gesture: false,
  });
  eq(geom.top, 1440, "D: the small rewrap compensated (aligned on blk-b)");

  // 普通 restore 路径（不是宽度补偿）遇到真的陈旧锚：门必须拒写。
  writes.length = 0;
  hookRef.current!.beginAnchorRestore();
  geom.blocks = { "blk-a": { top: 0, height: 3600 }, "blk-b": { top: 3600, height: 2000 } };
  geom.height = 5600;
  geom.top = Math.min(geom.top, bottomOf());
  await act(async () => { hookRef.current!.commitViewportGeometry(); });
  ok(!writes.some((write) => write.owner === "restore"), "D: a genuinely stale anchor restore is still refused (753 semantics kept)");
  eq(geom.top, 1440, "D: the viewport stays where the reader is (no teleport)");

  // 宽度路径上的真实超门位移同样被拒（tl 1255 E 腿的语义）：本件不动门。
  writes.length = 0;
  await rewrapFrame({
    width: 760,
    blocks: { "blk-a": { top: 0, height: 3600 }, "blk-b": { top: 3600, height: 2000 } },
    height: 5600,
    anchorShift: 0,
    gesture: false,
  });
  ok(!writes.some((write) => write.owner === "composer-resize"), "D: an over-threshold shift on the width path is still refused (753 semantics kept)");
  eq(geom.top, 1440, "D: the width path keeps the reader's pixels (no teleport)");
  await unmount();
}

process.stdout.write(`\n[tl 1699] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
