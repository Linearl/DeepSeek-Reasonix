// Run: tsx src/__tests__/tl1243-sidebar-resize-bottom-anchor.test.tsx
//
// tl 1243 — 侧栏展开/收起（自动隐藏↔展开）时右侧滚动条跳动。
//
// 用户实锤（双截图）：hover 展开/收起左右侧栏后视口内容跳动。期望=以页面
// 底部为锚：布局宽度变化不改变内容滚动位置（tail 停底、reader 阅读位不动）。
//
// 根因（本件修复面）：宽度变化的重排从未走到结构性补偿。唯一 resize 反应链
// （任务 267 R3：scroller ResizeObserver → scheduleTailSync）只服务 tail 且
// 只补底距；reader 意图零补偿=视口下的内容被换掉（持久跳动），tail 也只拿
// 到一次守卫性补贴。footerHeight 路径早有同型机制（beginStructural
// ("composer-resize") + commitViewportGeometry），宽度路径没接上。
//
// 修复（本件）：scroller RO 交付按宽度/高度分类（classifyTranscriptGeometry
// Change，transcriptGeometryObserver.ts）；宽度变化走 footer 同款结构性补偿
// （现有机制，无新事务种类）；高度-only 保持 267 R3 原语义（reader 永不被拽）。
//
// 验收矩阵（对应下方场景）：
//   A  tail 多轮 展开↔收起：每轮重排后视口底部距离先受伤（≠0）→ 补偿后=0
//      （展开前后视口底部一致；写经单点 writer，pinTail/Infinity）。
//   B  reader 多轮：块锚重放，写 owner=composer-resize、offset=块新 top+
//      offsetPx（视口下内容不变），意图不降级、永不写 Infinity（不拽底）。
//   C  高度-only 回归（267 R3）：tail 拿 scheduleTailSync 补贴、
//      composer-resize 不开；reader 零写入。
//   D  漂移门回归（task 753）：reader 块锚重排漂移超门 → 拒绝恢复、重锚
//      可见块、零写入（新触发器不得绕过旧门）。
//   E  首帧观察/同尺寸/高度-only 交付分类为无变化 → 不开结构性事务。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { TranscriptKernel, TranscriptViewportSnapshot } from "../lib/transcriptKernel";
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

// ── DOM 全局（在动态 import hook 之前就位：hook 模块顶层抓 requestAnimationFrame
//    做帧号自增器；本 harness 用手控帧号保证 readSnapshot 跨轮新鲜）───────
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
/** 前移一帧：让下一次 readSnapshot 绕过同帧合并缓存（模拟真实浏览器帧推进）。 */
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

// ── 写探针 sink（tl 1187 同款；验收读数来源）────────────────────────────
const writes: TranscriptScrollWriteRecord[] = [];
(window as unknown as { __REASONIX_TRANSCRIPT_SCROLL_WRITE__?: (write: TranscriptScrollWriteRecord) => void })
  .__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => writes.push(write);
const kernelEvents: Array<{ outcome: string; owner?: string; intent: string }> = [];
(window as unknown as { __REASONIX_TRANSCRIPT_SCROLL_DIAGNOSTIC__?: (type: string, fields: Record<string, unknown>) => void })
  .__REASONIX_TRANSCRIPT_SCROLL_DIAGNOSTIC__ = (type, fields) => {
    if (type === "kernel") kernelEvents.push({ outcome: String(fields.outcome), owner: fields.owner as string | undefined, intent: String(fields.intent) });
  };

// ── 滚动容器几何 stub（挂在 React 创建的真实 div 上，tl 1187 同款）────────
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
let handle: { kernel: TranscriptKernel } | null = null;
let clock: TranscriptTestClock = new TranscriptTestClock();

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
let root: Root | null = null;

// hook 模块必须在 rAF 全局就位后加载（模块顶层的帧号自增器要抓到它）。
const { TranscriptKernelClockContext, useTranscriptKernel } = await import("../lib/useTranscriptKernel");

function Harness({ sessionKey, geometryRevision }: { sessionKey: string; geometryRevision: string }) {
  const hook = useTranscriptKernel({ sessionKey, geometryRevision });
  return <div
    ref={(node) => {
      if (node) ensureScrollerStubs(node);
      hook.setScroller(node);
      if (node) handle = { kernel: hook.kernel };
    }}
  />;
}

async function mount(sessionKey: string): Promise<void> {
  handle = null;
  // 每场景全新容器 + 全新 root：React 19 对复用容器重建 root 会警告并行为不稳。
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
  handle = null;
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
/** 一次重排交付 + 补偿落定（帧号先推进，保证 settle 读到新鲜几何）。 */
async function fireRewrap(width: number, height: number): Promise<void> {
  bumpFrame();
  await act(async () => { FakeResizeObserver.fireAll(width, height); });
  flushAll();
  flushAll();
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

process.stdout.write("\n[tl 1243] sidebar expand/collapse keeps the viewport bottom anchored\n");

// ── A: tail 多轮展开↔收起，视口底部一致（任务验收原文）──────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1243-a");
  flushAll();
  eq(geom.top, bottomOf(), "A: mount lands at the bottom");
  await fireInitialObservation();

  for (let round = 1; round <= 3; round += 1) {
    // 展开：栏宽 800→620，重排变高（窄栏下每轮多 wrap 一截）。
    writes.length = 0;
    geom.clientW = 620;
    geom.blocks = { "blk-a": { top: 0, height: 1000 + round * 120 }, "blk-b": { top: 1000 + round * 120, height: 3000 + round * 480 } };
    geom.height = 4000 + round * 600;
    const injuredDistance = geom.height - geom.client - geom.top;
    ok(injuredDistance > 4, `A round ${round}: the rewrap opened a bottom gap before compensation (${injuredDistance}px)`);
    await fireRewrap(620, geom.client);
    eq(bottomDistance(), 0, `A round ${round}: expand keeps the viewport bottom anchored (视口底部一致)`);
    ok(
      writes.some((write) => write.kind === "pinTail"),
      `A round ${round}: the expand re-pin rode the single writer (pinTail)`,
    );

    // 收起：栏宽 620→800，重排变矮；浏览器先把 scrollTop 截回新底。
    writes.length = 0;
    geom.clientW = 800;
    geom.blocks = ORIGINAL_BLOCKS;
    geom.height = 4000;
    geom.top = Math.min(geom.top, bottomOf());
    await fireRewrap(800, geom.client);
    eq(bottomDistance(), 0, `A round ${round}: collapse keeps the viewport bottom anchored`);
    ok(
      writes.some((write) => write.kind === "pinTail"),
      `A round ${round}: the collapse re-pin rode the single writer`,
    );
  }
  eq(handle!.kernel.intent, "tail", "A: tail intent survives every expand/collapse round");
  await unmount();
}

// ── B: reader 多轮——块锚重放，布局宽度变化不改变内容滚动位置 ────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1243-b");
  flushAll();
  await fireInitialObservation();

  geom.top = 1200; // 用户真实滚动到位（kernel 只观察位移，不代写 scrollTop）
  scrollUpTo(handle!.kernel, 1200);
  eq(handle!.kernel.intent, "reader", "B: scrolled up parks as reader");
  eq(handle!.kernel.anchor.kind, "block", "B: reader anchor is a block");
  eq((handle!.kernel.anchor as { blockKey: string }).blockKey, "blk-b", "B: anchored to blk-b");
  eq((handle!.kernel.anchor as { offsetPx: number }).offsetPx, 200, "B: anchor offset 1200-1000=200");

  // 展开：blk-a 重排变高 1000→1240，blk-b 整体下移 → 视口下内容换人（伤）。
  writes.length = 0;
  geom.clientW = 620;
  geom.blocks = { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3160 } };
  geom.height = 4400;
  await fireRewrap(620, geom.client);
  const expandWrite = writes.find((write) => write.owner === "composer-resize");
  ok(expandWrite != null, "B round 1: the width rewrap ran the structural composer-resize compensation");
  eq(expandWrite?.requestedOffset, 1440, "B round 1: write = new block top 1240 + offset 200 (same content under the viewport)");
  eq(geom.top, 1440, "B round 1: scroll position compensated to the rewrapped reading spot");
  eq(handle!.kernel.intent, "reader", "B round 1: intent stays reader");
  ok(!writes.some((write) => write.requestedOffset === Number.POSITIVE_INFINITY), "B round 1: never yanked toward the bottom (no Infinity write)");

  // 收起：回到原布局，blk-b 回到 top 1000 → 补偿写 1200。
  writes.length = 0;
  kernelEvents.length = 0;
  geom.clientW = 800;
  geom.blocks = ORIGINAL_BLOCKS;
  geom.height = 4000;
  geom.top = Math.min(geom.top, bottomOf());
  await fireRewrap(800, geom.client);
  if (writes.find((write) => write.owner === "composer-resize") == null) {
    process.stdout.write(`  DEBUG B collapse: kernelEvents=${JSON.stringify(kernelEvents)}\n`);
    process.stdout.write(`  DEBUG B collapse: anchor=${JSON.stringify(handle!.kernel.anchor)} intent=${handle!.kernel.intent} top=${geom.top}\n`);
  }
  const collapseWrite = writes.find((write) => write.owner === "composer-resize");
  ok(collapseWrite != null, "B round 2: collapse compensation ran");
  eq(collapseWrite?.requestedOffset, 1200, "B round 2: write = restored block top 1000 + offset 200");
  eq(geom.top, 1200, "B round 2: reading position restored across the round trip");
  eq(handle!.kernel.intent, "reader", "B round 2: intent stays reader (round trip stable)");
  await unmount();
}

// ── C: 高度-only 回归（267 R3 语义不变）─────────────────────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1243-c");
  flushAll();
  await fireInitialObservation();

  // tail + 容器自身变高（footer 漂移/终端开合族）：补贴写底，不开结构事务。
  writes.length = 0;
  geom.client = 560;
  bumpFrame();
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  flushAll();
  ok(!writes.some((write) => write.owner === "composer-resize"), "C: height-only opens no structural transaction");
  eq(bottomDistance(), 0, "C: tail still re-pins via the guarded subsidy (267 R3)");

  // reader + 容器再变高：零写入（reader 永不被高度变化拽动）。
  geom.top = 900; // 用户真实滚动到位（kernel 只观察位移，不代写 scrollTop）
  scrollUpTo(handle!.kernel, 900);
  writes.length = 0;
  geom.client = 640;
  bumpFrame();
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  flushAll();
  eq(writes.length, 0, "C: height-only never writes under a reader (267 R3)");
  eq(geom.top, 900, "C: reader position untouched by a height-only change");
  await unmount();
}

// ── D: 漂移门回归（task 753 经由新触发器仍然成立）────────────────────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1243-d");
  flushAll();
  await fireInitialObservation();
  geom.top = 1200; // 用户真实滚动到位（kernel 只观察位移，不代写 scrollTop）
  scrollUpTo(handle!.kernel, 1200);

  // 先做一次小幅重排，建立 alignedAnchor（门只挡同锚的后续对齐）。
  geom.clientW = 620;
  geom.blocks = { "blk-a": { top: 0, height: 1240 }, "blk-b": { top: 1240, height: 3160 } };
  geom.height = 4400;
  await fireRewrap(620, geom.client);
  eq(geom.top, 1440, "D round 1: small rewrap compensates (gate lets a legit correction through)");

  // 再来一次超门重排：继续变窄，blk-b top 1240→3000（重排位移远超 max(320, 500)）。
  writes.length = 0;
  geom.clientW = 460;
  geom.blocks = { "blk-a": { top: 0, height: 3000 }, "blk-b": { top: 3000, height: 2000 } };
  geom.height = 5000;
  await fireRewrap(460, geom.client);
  ok(!writes.some((write) => write.owner === "composer-resize"), "D round 2: a drifted anchor restore is refused, no write");
  eq(geom.top, 1440, "D round 2: viewport stays where the reader is (no teleport)");
  const anchor = handle!.kernel.anchor as { kind: string; blockKey?: string; offsetPx?: number };
  ok(anchor.kind === "block" && anchor.blockKey === "blk-a" && anchor.offsetPx === 1440,
    "D round 2: anchor re-based to the block the reader is actually looking at (753 semantics)");
  await unmount();
}

// ── E: 首帧观察/同尺寸/高度-only 交付分类为无变化（不开结构事务）────────
{
  resetGeom();
  clock = new TranscriptTestClock();
  FakeResizeObserver.reset();
  writes.length = 0;
  await mount("t1243-e");
  flushAll();
  await fireInitialObservation();
  ok(!writes.some((write) => write.owner === "composer-resize"), "E: the initial observation classifies as unchanged");
  writes.length = 0;
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client); });
  flushAll();
  await act(async () => { FakeResizeObserver.fireAll(geom.clientW, geom.client + 60); });
  flushAll();
  ok(!writes.some((write) => write.owner === "composer-resize"), "E: same-size and height-only deliveries never open composer-resize");
  eq(handle!.kernel.intent, "tail", "E: tail follow alive throughout");
  await unmount();
}

process.stdout.write(`\n[tl 1243] passed=${passed} failed=${failed}\n`);
if (failed > 0) process.exit(1);
