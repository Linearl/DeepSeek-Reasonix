// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1776-tail-bounce-stale-frame-facts.test.tsx
//
// tl 1776 — 贴底判定缺口：流式输出时贴底往上回弹（P2，用户 20261123 两截图报，间歇性）。
//
// 现场机制（20261123 报：贴底锚定后，下方新产出的流式工作过程——工具执行/思考中
// 卡片——显示不到，视口停在最后一行输出，需手动滚动）：
//   1. 流式追加帧（同一 transcriptFrame 内）：新内容 commit（scrollHeight H1→H2）
//      → settleGeometry 先 snapshot 后写底——快照缓存住「写前 scrollTop + 追加后
//      scrollHeight」的混合几何（读时为真，写后即假）；
//   2. 写底产生的 scroll 事件在同一帧内送达（渲染步先于下一 rAF tick）——
//      readSnapshot 的帧内合并缓存（frameSnapshot，task 861）原样返回该混合几何；
//   3. 链 A（passive 毒化→宽度补偿回拉）：onScroll 无条件 capturePreReflowFacts
//      （tl 1699），从混合几何读到 atBottom=false（实为贴底）→ preReflowRef 记入假
//      facts；随后流式进入静默期（工具执行中，无新 scroll 事件刷新 facts）→ 用户
//      侧栏悬停/窗口调整触发宽度重排 → resolveWidthTarget 按 facts.atBottom=false
//      走首行锚腿（tl 1699r2），把视口回拉到追加前的位置——新内容在下方不可见。
//   4. 链 B（手势在场→echo 击穿→intent 无位移降级）：写底的 scroll 观察被手势租约
//      在场时的 observeNativeScroll 放行（写前 scrollTop 与 writeTop 差 > 容差，
//      echo 识别被同一混合缓存击穿）；tl 1184 只把 atBottomValue 门在真实位移上，
//      intentValue/anchorValue 仍被无条件重推导——无位移观察把 tail 翻成 reader，
//      贴底跟随死亡，后续追加全部落在视口下方。
//
// 修法（两腿，均不动 tl 1255/1699/1740 语义）：
//   - 腿 1（根）：writer 写底改变 scrollTop 后作废帧内合并缓存（width 分支同款
//     理由——「同帧更早的读取是陈旧的」），链 A/B 的毒化读取全部变成实时读取；
//   - 腿 2（语义兜底）：observeNativeScroll 把 intent/anchor 重推导同样门在真实
//     位移上（tl 1184 原则补全）——非位移观察不得改写读者意图。
//
// 验收矩阵：
//   B1 贴底 + 流式追加帧（append→写底→同帧 scroll 观察）+ 静默期 + 宽度重排：
//      视口保持贴底（bottomDistance=0）、intent 留 tail——无回弹；
//   B2 贴底 + 追加帧写底 + 手势租约在场 + 同帧 scroll 观察：intent 不降级，
//      手势结束后继续追加仍跟随到底（scrollTop=新 floor）；
//   R  非贴底读者态（真实上滚）+ 追加 + 宽度重排：首行保持、不拽底
//      （tl 1699 语义保持——修法不得把读者拽回底部）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { useTranscriptKernel } from "../lib/useTranscriptKernel";
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

// ── 滚动容器几何 stub（tl 1740 同款）────────────────────────────────────
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
async function flushAll(): Promise<void> {
  await act(async () => {
    let guard = 0;
    while (clock.frames.size > 0 && guard < 32) {
      clock.flushFrames();
      guard += 1;
    }
  });
}
/** 生产语义：observe() 后 ResizeObserver 总先交付一次初始尺寸（分类=无变化）。 */
async function fireInitialObservation(): Promise<void> {
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  await flushAll();
}

/**
 * 流式追加帧（tl 1776 竞争窗口，生产顺序全部落在同一 transcriptFrame 内）：
 *   1. 帧边界推进（新帧 id，帧内缓存作废——起点干净）；
 *   2. 新内容 commit：scrollHeight 增长（工具执行/思考中卡片追加），scrollTop
 *      仍是旧底（写尚未发生）；
 *   3. settleGeometry 同步提交（measurement commit 的 beforePaint 路径）：
 *      先 snapshot（缓存住「写前 scrollTop + 追加后 scrollHeight」混合几何），
 *      后写底（scrollTop=新 floor）；
 *   4. 写底产生的 scroll 事件在同一帧内送达（无 bumpFrame——渲染步先于下一
 *      rAF tick，帧内合并缓存命中，混合几何被原样消费）；手势租约可先于送达
 *      在场（生产：贴底时滚轮跟读/轻点先到，scroll 事件后到）。
 * 不依赖任何 sleep——竞争窗口由帧计数与调用顺序构造，每步确定。
 */
async function streamingAppendFrame(newHeight: number, options?: { gestureBeforeScroll?: boolean }): Promise<void> {
  bumpFrame();
  geom.height = newHeight;
  await act(async () => { hookRef.current!.commitViewportGeometry(undefined, true); });
  if (options?.gestureBeforeScroll) await act(async () => { hookRef.current!.beginGesture(); });
  await act(async () => { hookRef.current!.onScroll(); });
}

/** 宽度重排一帧（侧栏过渡，tl 1740 同款）：布局变→RO 同步交付 widthChanged。 */
async function widthRewrapFrame(options: {
  width: number;
  blocks: Record<string, BlockGeom>;
  height: number;
}): Promise<void> {
  bumpFrame();
  geom.clientW = options.width;
  geom.blocks = options.blocks;
  geom.height = options.height;
  await act(async () => { FakeResizeObserver.fireAll(options.width, geom.client); });
  await flushAll();
}

process.stdout.write("\n[tl 1776] sticky-bottom bounce: stale same-frame facts must not pull the tail view up\n");

// ── B1: 追加帧毒化 preReflowFacts → 宽度补偿按旧首行回拉（间歇性回弹主链）──
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  await mount("t1776-b1");
  await flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "B1: the view starts pinned at the bottom (floor=3500)");
  eq(hookRef.current!.kernel.intent, "tail", "B1: the intent is tail");

  // 追加帧：工具执行卡片追加（4000→4200），写底落到 3700；写底 scroll 事件
  // 同帧送达消费帧内混合几何（3500,4200）——修复前 facts 记入 atBottom=false。
  await streamingAppendFrame(4200);
  eq(geom.top, 3700, "B1: the append frame's tail write lands on the new floor (3700)");

  // 静默期（工具执行中，无新 scroll 事件）：帧推进两拍，毒化的 facts 无人刷新。
  bumpFrame();
  bumpFrame();

  // 宽度重排（侧栏悬停 800→620，行重折行）：修复前按毒化 facts 走首行锚腿
  // （blk-b/2500 → 目标 3900，新 floor 4300——视口回拉 400px，新卡片不可见）；
  // 修复后 facts 为真（atBottom=true）→ tail 腿无条件回底。
  await widthRewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1400 }, "blk-b": { top: 1400, height: 3400 } },
    height: 4800,
  });
  eq(bottomDistance(), 0, `B1: the width reflow does NOT bounce the view off the bottom (bottomDistance=${bottomDistance()})`);
  eq(hookRef.current!.kernel.intent, "tail", "B1: the intent stays tail through the reflow");
  await unmount();
}

// ── B2: 手势在场 + 同帧混合几何击穿 echo → intent 无位移降级，跟随死亡 ─────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  await mount("t1776-b2");
  await flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "B2: the view starts pinned at the bottom");
  eq(hookRef.current!.kernel.intent, "tail", "B2: the intent is tail");

  // 追加帧写底到 3700，且手势租约先于写底 scroll 事件在场（生产模拟：贴底时
  // 滚轮向下保持跟读/轻点先到，scroll 事件后到）：修复前 echo 被帧内混合缓存
  // 击穿（写前 scrollTop 与 writeTop 差 200>4）、无位移观察把 intent 无条件
  // 重推导成 reader（tl 1184 只门了 atBottomValue）；修复后 echo 命中/位移门
  // 兜底——intent 留 tail。
  await streamingAppendFrame(4200, { gestureBeforeScroll: true });
  eq(geom.top, 3700, "B2: the append frame's tail write lands on the new floor (3700)");
  eq(hookRef.current!.kernel.intent, "tail", "B2: a displacement-less observation of our own write does not demote the intent");

  // 手势结束 + 继续追加：跟随必须活着（scrollTop 跟到新 floor 3800）。
  await act(async () => { hookRef.current!.endGesture(); });
  await flushAll();
  await act(async () => {
    bumpFrame();
    geom.height = 4300;
    hookRef.current!.scheduleTailSync();
  });
  await flushAll();
  eq(bottomDistance(), 0, `B2: streaming growth after the gesture still follows to the bottom (bottomDistance=${bottomDistance()})`);
  eq(geom.top, 3800, `B2: the viewport tracks the new floor (scrollTop=${geom.top})`);
  await unmount();
}

// ── R: 非贴底读者态保持——修法不得把读者拽回底部（tl 1699 语义护栏）────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  await mount("t1776-r");
  await flushAll();
  await fireInitialObservation();
  eq(bottomDistance(), 0, "R: the view starts pinned at the bottom");

  // 真实上滚（位移 3500→1000）切读者态：anchor=blk-b/0（首可见块）。
  await act(async () => { hookRef.current!.beginGesture(); });
  bumpFrame();
  geom.top = 1000;
  await act(async () => { hookRef.current!.onScroll(); });
  await act(async () => { hookRef.current!.endGesture(); });
  await flushAll();
  eq(hookRef.current!.kernel.intent, "reader", "R: a real upward displacement switches to reader");
  eq(geom.top, 1000, "R: the reader stays put");

  // 追加 + 宽度重排：读者首行保持（blk-b 顶回到视口顶），绝不被拽回底部。
  bumpFrame();
  geom.height = 4200;
  await act(async () => { hookRef.current!.commitViewportGeometry(undefined, true); });
  await widthRewrapFrame({
    width: 620,
    blocks: { "blk-a": { top: 0, height: 1400 }, "blk-b": { top: 1400, height: 2800 } },
    height: 4200,
  });
  eq(hookRef.current!.kernel.intent, "reader", "R: the reflow keeps the reader intent");
  eq(geom.top, 1400, `R: the reflow keeps the reader's first line (scrollTop=${geom.top})`);
  ok(bottomDistance() > 0, `R: the reader is NOT dragged to the bottom (bottomDistance=${bottomDistance()})`);
  await unmount();
}

process.stdout.write(`\n[tl 1776] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
