/**
 * 任务 861（fork #50）+ tl1416（issue #82）：readSnapshot 跨帧块坐标缓存的读取计数合约。
 *
 * #50 的切 tab 帧归因里 readSnapshot 全量几何读取占 (anonymous) 26x：每个动画帧都对
 * 全部已挂载块做 getBoundingClientRect。tl1416 的切 tab 冻结（16.6s 单任务）里，#50 的
 * 全局 epoch 让每次子树 mutation 都作废整个缓存——切 tab 的 commit 风暴（每 commit 写
 * DOM、每 commit 的 MutationObserver 投递落在读之间）把每次读都打回 O(N) 全量扫描。
 *
 * tl1416 把 epoch 换成按 key 分片失效：mutation 只标脏所在块的 key，读只重测脏/新 key，
 * 其余沿用缓存；不可归因的整体位移由两处干净参照兜底——首块 + 自尾向前的最后一个干净
 * 缓存 key（真实流式布局里任何后缀位移必移动它）。本文件用计数器断言：
 *  1. 同帧重复读取仍由帧缓存合并（零新增读取）；
 *  2. 布局未变的跨帧读取走块坐标缓存——块级 rect 读取 = 2（首/尾参照）；
 *  3. 【tl1416 验收】切 tab 风暴：每 commit 变更一块、观察者投递落在读之间，全程
 *     fullScans 增量为 0（O(N) 路径不触发），每读块级读取上界 = 脏数 + 2 参照；
 *  4. 新增/前置块按分片重测进缓存，不再强制全量；
 *  5. 【#50 不变量保留】参照闸失败的不可归因位移（尾/首参照错位）必须回落全量，
 *     不得把陈旧坐标喂给锚点修正；签名闸门（scrollHeight 变化）同样保留。
 *
 * 必须先装可控 rAF 再动态 import 被测模块：transcriptFrame 计数器由模块
 * 顶层的 rAF 自循环推进，静态 import 会让它拿不到推进通道。
 */
import assert from "node:assert/strict";
import React, { act, useLayoutEffect } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";

const rafCallbacks: FrameRequestCallback[] = [];
(globalThis as unknown as { requestAnimationFrame: (cb: FrameRequestCallback) => number }).requestAnimationFrame = (cb) => {
  rafCallbacks.push(cb);
  return rafCallbacks.length;
};
const advanceFrame = () => {
  const callbacks = rafCallbacks.splice(0);
  for (const callback of callbacks) callback(performance.now());
};

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, { window: dom.window, document: dom.window.document,
  HTMLElement: dom.window.HTMLElement, Element: dom.window.Element,
  MutationObserver: dom.window.MutationObserver, IS_REACT_ACT_ENVIRONMENT: true });

const { TranscriptKernelClockContext, useTranscriptKernel, transcriptGeometryReadStats, resetTranscriptGeometryReadStats } =
  await import("../lib/useTranscriptKernel");
const { TranscriptTestClock } = await import("./transcript-test-clock");

const clock = new TranscriptTestClock();
const scroller = document.createElement("div");
scroller.dataset.transcriptBlockCount = "4";
const EXPECTED_VISIBLE_BASELINE = 4;
// tl1416: blocks are flow-coupled in the mock — moving/growing one block moves
// every block after it, exactly what the tail reference gate relies on. The
// rect closures read blockTops[index] at call time, so reassigning the array
// entries models the resulting flow shift.
let blockTops = [0, 100, 200, 300];
const BLOCK_HEIGHT = 100;
let scrollTop = 0;
let scrollHeight = 1000;
Object.defineProperties(scroller, {
  scrollTop: { get: () => scrollTop, set: (value: number) => { scrollTop = value; } },
  scrollHeight: { get: () => scrollHeight }, clientHeight: { get: () => 500 }, clientWidth: { get: () => 800 },
});
const rect = (top: number, height: number) => ({ top, bottom: top + height, left: 0, right: 800,
  width: 800, height, x: 0, y: top, toJSON: () => ({}) });
scroller.getBoundingClientRect = () => rect(0, 500);
const blockRects: HTMLElement[] = [];
function mountBlock(key: string, index: number): HTMLElement {
  const block = document.createElement("div");
  block.dataset.transcriptBlockKey = key;
  block.getBoundingClientRect = () => rect(blockTops[index] - scrollTop, BLOCK_HEIGHT);
  scroller.append(block);
  blockRects.push(block);
  return block;
}
for (let index = 0; index < 4; index += 1) mountBlock(`b${index}`, index);

let current!: ReturnType<typeof useTranscriptKernel>;
function Probe() {
  current = useTranscriptKernel({ sessionKey: "source", geometryRevision: 1 });
  const attach = current.setScroller;
  useLayoutEffect(() => { attach(scroller); return () => attach(null); }, [attach]);
  return null;
}
const root = createRoot(document.getElementById("root")!);
await act(async () => root.render(React.createElement(TranscriptKernelClockContext.Provider,
  { value: clock }, React.createElement(Probe))));
// 挂载期 render/layout effect 已做过全量扫描并建立块坐标缓存；清零计数后
// 开始稳态合约断言——挂载之后的稳态读取就是本任务要压降的对象。
resetTranscriptGeometryReadStats();
advanceFrame();
let snapshot = current.snapshot()!;
assert.equal(snapshot.visibleBlocks.length, EXPECTED_VISIBLE_BASELINE, "all four blocks are inside the 500px viewport");
assert.equal(transcriptGeometryReadStats.cacheHits, 1, "the first post-mount cross-frame read reuses the mount-time cache");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "an unchanged layout never rescans");
assert.equal(transcriptGeometryReadStats.blockRectReads, 2, "an unchanged cross-frame read costs the two clean references (head + tail)");

// 同帧重复：帧缓存合并，零新增几何读取。
snapshot = current.snapshot()!;
assert.equal(snapshot.visibleBlocks.length, 4);
assert.equal(transcriptGeometryReadStats.frameMerges, 1, "same-frame repeat is merged");
assert.equal(transcriptGeometryReadStats.blockRectReads, 2, "frame merge adds no block reads");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "frame merge adds no full scan");

// 又一个未变帧：仍然只付参照成本。
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(snapshot.visibleBlocks.length, 4, "cached coordinates answer the unchanged layout");
assert.equal(transcriptGeometryReadStats.cacheHits, 2, "the unchanged frame hits the cross-frame cache");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "no full scan for the unchanged frame");
assert.equal(transcriptGeometryReadStats.blockRectReads, 4, "each cache hit costs exactly two reference reads");

// 滚动不改变块的 content 坐标：缓存仍命中，可见集按新视口重新过滤。
scrollTop = 250;
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(snapshot.scrollTop, 250);
assert.deepEqual(snapshot.visibleBlocks.map((block) => block.key), ["b2", "b3"], "viewport 250..750 covers b2..b3");
assert.equal(transcriptGeometryReadStats.cacheHits, 3, "a scroll keeps the cache (content coordinates are scroll-invariant)");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "scroll filtering never forces a rescan");
assert.equal(transcriptGeometryReadStats.blockRectReads, 6, "the scroll frame costs two more reference reads");
scrollTop = 0;

// 尾参照闸门（#50 不变量）：末块位移（首块不动、签名不变、无脏 key）必须回落全量。
blockTops = [0, 100, 200, 350];
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, 1, "a clean tail-block shift fails the tail reference and forces a rescan");
assert.equal(snapshot.visibleBlocks[3].bottom, 450, "the rescan returns the corrected coordinates");

// 复位（位移还原同样是干净参照错位 → 再次全量重建），供后续闸门从同一状态出发。
blockTops = [0, 100, 200, 300];
advanceFrame();
current.snapshot()!;
const baselineScans = transcriptGeometryReadStats.fullScans;

// 签名闸门（#50 不变量保留）：滚动高度变化（内容增删、图片加载）直接回落全量。
scrollHeight = 1200;
advanceFrame();
current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, baselineScans + 1, "a scrollHeight change invalidates the cache by signature");

// tl1416 分片重测：新增块不再强制全量。同步读（观察者投递前）只补测新 key，
// 其余沿用缓存；投递后的下一读也只重测脏 key + 两处参照。
const extra = mountBlock("b4", 4);
blockTops = [0, 100, 200, 300, 400];
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, baselineScans + 1, "a new block key is absorbed surgically, no full scan");
assert.equal(transcriptGeometryReadStats.cacheHits, 4, "the append read still answers through the cache");
assert.equal(snapshot.visibleBlocks.length, 5, "the surgical re-measure includes the appended block");
await act(async () => { await Promise.resolve(); await Promise.resolve(); });
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, baselineScans + 1, "the delivered mutation batch costs no full scan either");
assert.equal(snapshot.visibleBlocks.length, 5, "the cache carries all five blocks");

// 【tl1416 验收主断言】切 tab 风暴：每 commit 变更一块的 DOM、每次观察者投递
// 落在读之间。#50 的全局 epoch 在这里每次读都打回 O(N) 全量（N 次风暴 = N 次
 // fullScan × N 块读取）；分片失效必须全程 fullScans 零增量，且每读块级读取
// 上界 = 1 脏 key + 2 参照。
const stormScansBefore = transcriptGeometryReadStats.fullScans;
const stormReadsBefore = transcriptGeometryReadStats.blockRectReads;
const STORM_ROUNDS = 4;
for (let round = 0; round < STORM_ROUNDS; round += 1) {
  blockRects[round].style.height = "50px"; // commit 写 DOM → MutationObserver 标脏（异步投递）
  await act(async () => { await Promise.resolve(); await Promise.resolve(); });
  advanceFrame();
  const readsBefore = transcriptGeometryReadStats.blockRectReads;
  snapshot = current.snapshot()!;
  assert.equal(transcriptGeometryReadStats.fullScans, stormScansBefore, `storm round ${round}: the O(N) full-scan path never triggers`);
  assert.equal(snapshot.visibleBlocks.length, 5, `storm round ${round}: the surgical re-measure keeps every block visible`);
  const readsDelta = transcriptGeometryReadStats.blockRectReads - readsBefore;
  assert.ok(readsDelta <= 3, `storm round ${round}: one dirty key + two references (got ${readsDelta})`);
}
assert.equal(
  transcriptGeometryReadStats.blockRectReads - stormReadsBefore <= 3 * STORM_ROUNDS, true,
  "the whole storm costs at most (dirty + 2 references) per read, never O(N) per commit",
);

// 【#50 不变量保留】不可归因的流式位移：b2 高度变化把其后干净块（b3、b4）整体
// 下推——分片标脏看不到这种跨块副作用，尾参照（最后一个干净 key b4）必须 trip，
// 回落全量，陈旧中段坐标不得进锚点修正。
blockRects[2].style.height = "120px";
blockTops = [0, 100, 200, 350, 450];
await act(async () => { await Promise.resolve(); await Promise.resolve(); });
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, stormScansBefore + 1, "an unattributable suffix shift fails the tail reference and forces a rescan");
assert.equal(snapshot.visibleBlocks[3].top, 350, "the rescan returns the flow-shifted coordinates");

// 还原（b2 高度复原 + b3/b4 随动回去）同样是参照错位 → 全量重建，状态自愈。
blockRects[2].style.height = "";
blockTops = [0, 100, 200, 300, 400];
await act(async () => { await Promise.resolve(); await Promise.resolve(); });
advanceFrame();
current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, stormScansBefore + 2, "the restored layout rebuilds the cache through the same gate");

// 首参照闸门（#50 不变量）：首块位移（整列随动）在主循环即 trip，回落全量。
blockTops = [50, 150, 250, 350, 450];
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, stormScansBefore + 3, "a clean head-block shift fails the head reference and forces a rescan");
assert.equal(snapshot.visibleBlocks[0].top, 50, "the rescan returns the shifted head coordinate");

console.log("PASS transcript geometry read cache: frame merge, reference cost, per-key storm absorption, and every #50 invalidation gate");
