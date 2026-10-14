/**
 * 任务 861（fork #50）：readSnapshot 跨帧块坐标缓存的读取计数合约。
 *
 * #50 的切 tab 帧归因里 readSnapshot 全量几何读取占 (anonymous) 26x：
 * 每个动画帧都对全部已挂载块做 getBoundingClientRect。本文件用计数器
 * 断言三件事：
 *  1. 同帧重复读取仍由帧缓存合并（frameMerges，零新增读取）；
 *  2. 布局未变的跨帧读取走块坐标缓存——块级 rect 读取从 N 降到 2（首末哨兵）；
 *  3. 布局变了（签名/块集/哨兵/MutationObserver 任一闸门）必须回落全量扫描，
 *     不得把陈旧坐标喂给锚点修正。
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
const blocks = blockTops.map((_top, index) => {
  const block = document.createElement("div");
  block.dataset.transcriptBlockKey = `b${index}`;
  scroller.append(block);
  return block;
});
blocks.forEach((block, index) => {
  block.getBoundingClientRect = () => rect(blockTops[index] - scrollTop, BLOCK_HEIGHT);
});

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
assert.equal(transcriptGeometryReadStats.blockRectReads, 2, "a cache hit costs exactly the two sentinel reads");

// 同帧重复：帧缓存合并，零新增几何读取。
snapshot = current.snapshot()!;
assert.equal(snapshot.visibleBlocks.length, 4);
assert.equal(transcriptGeometryReadStats.frameMerges, 1, "same-frame repeat is merged");
assert.equal(transcriptGeometryReadStats.blockRectReads, 2, "frame merge adds no block reads");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "frame merge adds no full scan");

// 又一个未变帧：仍然只付哨兵成本。
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(snapshot.visibleBlocks.length, 4, "cached coordinates answer the unchanged layout");
assert.equal(transcriptGeometryReadStats.cacheHits, 2, "the unchanged frame hits the cross-frame cache");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "no full scan for the unchanged frame");
assert.equal(transcriptGeometryReadStats.blockRectReads, 4, "each cache hit costs exactly two sentinel reads");

// 滚动不改变块的 content 坐标：缓存仍命中，可见集按新视口重新过滤。
scrollTop = 250;
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(snapshot.scrollTop, 250);
assert.deepEqual(snapshot.visibleBlocks.map((block) => block.key), ["b2", "b3"], "viewport 250..750 covers b2..b3");
assert.equal(transcriptGeometryReadStats.cacheHits, 3, "a scroll keeps the cache (content coordinates are scroll-invariant)");
assert.equal(transcriptGeometryReadStats.fullScans, 0, "scroll filtering never forces a rescan");
assert.equal(transcriptGeometryReadStats.blockRectReads, 6, "the scroll frame costs two more sentinel reads");
scrollTop = 0;

// 哨兵闸门：末块位移（首块不动、签名不变、块集不变）必须回落全量。
blockTops = [0, 100, 200, 350];
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, 1, "a last-block shift fails the sentinel gate and forces a rescan");
assert.equal(transcriptGeometryReadStats.blockRectReads, 12, "sentinel-gate miss costs the 2 speculative sentinel reads plus one full scan");
assert.equal(snapshot.visibleBlocks[3].bottom, 450, "the rescan returns the corrected coordinates");
blockTops = [0, 100, 200, 300];
advanceFrame();
current.snapshot()!; // 复位后重建缓存（再次全量），供后续闸门从同一状态出发
const baselineScans = transcriptGeometryReadStats.fullScans;

// 签名闸门：滚动高度变化（内容增删、图片加载）直接回落全量。
scrollHeight = 1200;
advanceFrame();
current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, baselineScans + 1, "a scrollHeight change invalidates the cache by signature");

// 块集闸门：新增块（切 tab 挂载/前一页 prepend）按 keys 深比较失效。
const extra = document.createElement("div");
extra.dataset.transcriptBlockKey = "b4";
scroller.append(extra);
extra.getBoundingClientRect = () => rect(400 - scrollTop, BLOCK_HEIGHT);
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, baselineScans + 2, "a new block key invalidates the cache");
assert.equal(snapshot.visibleBlocks.length, 5, "the rescan includes the appended block");

// MutationObserver 闸门：签名/keys/哨兵都拦不住的中间块位移，靠异步脏标记补位。
// 先让块集恢复稳定（remove 触发一次 keys 闸门全量扫描），再做一次同步闸门
// 全绿的纯位移——此时唯一能发现它的只有异步投递的 MutationObserver。
extra.remove();
advanceFrame();
current.snapshot()!;
const scansBeforePureMove = transcriptGeometryReadStats.fullScans;
blockTops = [0, 150, 200, 300]; // 中间块下移 50px：首末块不动、签名不变、keys 不变
blocks[1].style.height = "50px"; // DOM attribute 变更 → MutationObserver 标脏（异步投递）
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, scansBeforePureMove, "synchronous gates cannot see a middle-block shift yet");
assert.equal(snapshot.visibleBlocks.length, 4, "this read still answers through the synchronous gates");
// 等 MutationObserver 的 microtask 投递，再读一次：必须回落全量。
await act(async () => { await Promise.resolve(); await Promise.resolve(); });
advanceFrame();
snapshot = current.snapshot()!;
assert.equal(transcriptGeometryReadStats.fullScans, scansBeforePureMove + 1, "the observer epoch forces a rescan after the mutation is delivered");
assert.equal(snapshot.visibleBlocks[1].top, 150, "the rescan returns the moved block's corrected top");

console.log("PASS transcript geometry read cache: frame merge, cross-frame cache, and every invalidation gate");
