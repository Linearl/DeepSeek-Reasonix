// Run: tsx src/__tests__/creation-transcript-scrollbar.test.ts

import {
  mapFrozenScrollbarDrag,
  mapCreationLoadedGeometry,
  rebaseFrozenScrollbarDrag,
  readCreationScrollbarGeometry,
} from "../lib/useCreationTranscriptScrollbar";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\ncreation transcript scrollbar");

const initial = readCreationScrollbarGeometry(800, 4_000);
if (!initial) throw new Error("expected scrollable geometry");
eq(initial.overflow, 3_200, "geometry records the pointerdown overflow");
eq(initial.thumbHeight, 160, "geometry derives the thumb from the pointerdown layout");
eq(initial.maxThumbTop, 640, "geometry records the pointerdown thumb track");

const drag = {
  startY: 100,
  startThumbTop: 320,
  overflow: initial.overflow,
  maxThumbTop: initial.maxThumbTop,
};
const moved = mapFrozenScrollbarDrag(drag, 200);
eq(moved.thumbTop, 420, "drag follows pointer pixels on the frozen track");
eq(moved.scrollTop, 2_100, "drag maps through the frozen pointerdown overflow");

// Content can grow while virtual rows mount. Rebase the active drag at the
// current physical position so the next pointer move cannot write through a
// stale extent while preserving the thumb's visible position.
const grown = readCreationScrollbarGeometry(800, 8_000);
if (!grown) throw new Error("expected grown scrollable geometry");
eq(grown.overflow, 7_200, "content growth would otherwise change the live mapping");
const rebased = rebaseFrozenScrollbarDrag(drag, grown, 2_100, 200);
eq(rebased.startY, 200, "geometry rebase pins the drag to the latest pointer sample");
eq(rebased.startThumbTop, 210, "geometry rebase preserves the current physical scroll ratio");
eq(mapFrozenScrollbarDrag(rebased, 300).scrollTop, 3_100, "subsequent drag movement uses the current extent");

eq(mapFrozenScrollbarDrag(drag, -1_000).thumbTop, 0, "drag clamps at the top");
eq(mapFrozenScrollbarDrag(drag, 2_000).thumbTop, 640, "drag clamps at the bottom");

// 任务 1096：已加载区段几何映射（比例 → 像素 + 状态枚举）。断言具体数值，
// 不用 bool。语义：
//   - partial：还能往更早加载 ⇒ 高度 = ratio × rail；
//   - loading：一次更早页在途 ⇒ 高度维持 ratio 映射，phase 翻转（虚线态）；
//   - exhausted：已到头 ⇒ 整条 rail 变深 + 收口标记（与 partial 可区分）；
//   - none：无历史语义（无 totalTurns / 从未翻页）⇒ 不画区段。
const loadedRatio = 0.25;
const partial = mapCreationLoadedGeometry(loadedRatio, false, false, 800);
eq(partial.loadedHeight, 200, "loaded segment = ratio × rail height");
eq(partial.phase, "partial", "a partial load reports the partial phase");

const moreLoaded = mapCreationLoadedGeometry(0.6, false, false, 800);
eq(moreLoaded.loadedHeight, 480, "more loaded history grows the segment");
eq(moreLoaded.phase, "partial", "a bigger loaded ratio stays partial");

const allLoaded = mapCreationLoadedGeometry(1, false, false, 800);
eq(allLoaded.loadedHeight, 800, "fully loaded ratio fills the rail");

const exhausted = mapCreationLoadedGeometry(0.25, true, false, 800);
eq(exhausted.loadedHeight, 800, "exhausted overrides ratio to fill the rail");
eq(exhausted.phase, "exhausted", "exhausted reports its own phase (≠ partial)");

const inFlightAtTop = mapCreationLoadedGeometry(undefined, true, true, 800);
eq(inFlightAtTop.loadedHeight, 800, "an exhausted surface keeps the filled rail while a page is in flight");
eq(inFlightAtTop.phase, "loading", "the in-flight phase is distinguishable from exhausted");

const loadingPartial = mapCreationLoadedGeometry(0.25, false, true, 800);
eq(loadingPartial.loadedHeight, 200, "loading keeps the loaded height (no fake progress)");
eq(loadingPartial.phase, "loading", "the loading phase is distinguishable from partial");

const zeroRatio = mapCreationLoadedGeometry(0, false, false, 800);
eq(zeroRatio.loadedHeight, 0, "zero ratio renders no loaded segment");
eq(zeroRatio.phase, "partial", "a zero ratio is still a known partial state");

const unknownRatio = mapCreationLoadedGeometry(undefined, false, false, 800);
eq(unknownRatio.loadedHeight, 0, "undefined ratio renders no segment");
eq(unknownRatio.phase, "none", "no ratio at all reports the none phase");

const negativeRatio = mapCreationLoadedGeometry(-0.5, false, false, 800);
eq(negativeRatio.loadedHeight, 0, "negative ratio clamps to 0");

const overRatio = mapCreationLoadedGeometry(1.5, false, false, 800);
eq(overRatio.loadedHeight, 800, "ratio > 1 clamps to the rail height");

const noViewport = mapCreationLoadedGeometry(0.5, false, false, 0);
eq(noViewport.loadedHeight, 0, "zero-height rail renders no segment");
eq(noViewport.phase, "none", "no viewport reports the none phase");

// 任务 1096 口径：(totalTurns − startTurn + 1) / totalTurns —— 与后端
// desktop/app_history.go `historyPageFromMessages` 的 1-based 轮次一致。
const ratioOf = (startTurn: number, totalTurns: number) =>
  totalTurns > 0 && startTurn > 0 ? (totalTurns - startTurn + 1) / totalTurns : undefined;
eq(ratioOf(31, 40), 0.25, "the first page (turns 31-40 of 40) reads 25% loaded");
const afterLoadMore = mapCreationLoadedGeometry(ratioOf(11, 40), false, false, 800);
eq(afterLoadMore.loadedHeight, 600, "loading one more page (turns 11-40) grows the segment to 600px");
const afterExhausted = mapCreationLoadedGeometry(ratioOf(1, 40), true, false, 800);
eq(afterExhausted.loadedHeight, 800, "the last page (turn 1+) fills the rail");
eq(afterExhausted.phase, "exhausted", "the last page reports the exhausted phase");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
