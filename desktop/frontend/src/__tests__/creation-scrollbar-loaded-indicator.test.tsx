// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/creation-scrollbar-loaded-indicator.test.tsx
//
// 任务 1096：creation 模式右侧滚动条的「已加载区间」指示。
//
// 修法：rail 上叠一层「已加载」区段，比例的唯一权威来源是 controller 的
// 历史页状态（historyStartTurn / historyTotalTurns / hasOlderHistory /
// loadingOlderHistory，均由 useController reducer 在加载更多成功后更新）。
// Transcript 把 (totalTurns − startTurn + 1) / totalTurns 传给
// useCreationTranscriptScrollbar，由纯函数 mapCreationLoadedGeometry 映射成
// rail 像素高度 + 状态枚举（none / partial / loading / exhausted）。
//
// 本测试走真实 Transcript（jsdom harness），断言 DOM 上区段的**具体像素
// 高度**与**状态类名**，覆盖任务书全部验收场景：
//   1. 初始进入（第一页）⇒ 比例 = 已加载/总数；
//   2. 加载更多成功（startTurn 前移）⇒ 深色段变长（数值变大）；
//   3. 全部加载完（hasOlder=false）⇒ exhausted 态，与 partial 可区分；
//   4. 加载在途（loading）⇒ 高度不变、虚线态（与 partial/exhausted 可区分）；
//   5. 加载失败 ⇒ 不回退、不假装成功（回到加载前的 partial 高度与类名）；
//   6. 切换会话 ⇒ 按新会话值重置（不残留上一会话进度）。
import { act } from "react";
import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string): void {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

function turns(count: number): Item[] {
  return Array.from({ length: count }, (_, index) => [
    { kind: "user", id: `user-${index}`, text: `question ${index}`, historyTurn: index + 1 } as Item,
    { kind: "assistant", id: `answer-${index}`, text: `answer ${index}`, reasoning: "", streaming: false } as Item,
  ]).flat();
}

/** 高度 800 的 rail 上，区段像素值读数（与 mapCreationLoadedGeometry 同口径）。 */
const expectedHeight = (startTurn: number, totalTurns: number) =>
  Math.round(((totalTurns - startTurn + 1) / totalTurns) * 800);

function readLoaded(container: HTMLElement) {
  const segment = container.querySelector<HTMLElement>(".transcript__scrollbar-loaded");
  if (!segment) return null;
  return {
    height: Number.parseFloat(segment.style.height || "0"),
    phase: ["partial", "loading", "exhausted", "none"].find((name) => segment.classList.contains(`transcript__scrollbar-loaded--${name}`)) ?? "?",
  };
}

console.log("\ncreation scrollbar loaded indicator");
const harness = await createTranscriptHarness({ deterministic: true, viewportHeight: 800, rowHeight: 24 });
try {
  const loadCalls: Array<"viewport-user" | undefined> = [];
  const onLoadOlderHistory = async () => { loadCalls.push("viewport-user"); return true; };

  // 1) 初始进入：第 31-40 轮已加载（共 40 轮）⇒ 25% × 800px = 200px。
  await harness.render(turns(40), {
    creationMode: true,
    geometrySessionKey: "loaded-A",
    hasOlderHistory: true,
    historyStartTurn: 31,
    historyTotalTurns: 40,
    onLoadOlderHistory,
  });
  await harness.settle();
  const initial = readLoaded(harness.container);
  eq(initial?.height, expectedHeight(31, 40), "initial entry renders the loaded segment at 已加载/总数");
  eq(initial?.height, 200, "initial entry reads 200px (25% of an 800px rail)");
  eq(initial?.phase, "partial", "an initial partial load reports the partial phase");

  // 2) 加载更多成功：30 轮更早的历史被 prepend ⇒ startTurn 31 → 1。
  //    断言具体数值变化：200px → 800px（分段变长 = 已加载区间扩大）。
  await harness.render(turns(40), {
    creationMode: true,
    geometrySessionKey: "loaded-A",
    hasOlderHistory: true,
    historyStartTurn: 1,
    historyTotalTurns: 40,
    onLoadOlderHistory,
  });
  await harness.settle();
  const afterLoadMore = readLoaded(harness.container);
  eq(afterLoadMore?.height, expectedHeight(1, 40), "load-more advances the segment with the new startTurn");
  eq(afterLoadMore?.height, 800, "one more page fills the 800px rail (was 200px)");
  eq(afterLoadMore?.phase, "partial", "still-partial history keeps the partial phase");

  // 4) 加载在途：高度维持上一次读数（200px），phase 翻转为 loading —— 不推进、
  //    不回退，与 partial / exhausted 可区分（CSS 虚线态）。
  await harness.render(turns(40), {
    creationMode: true,
    geometrySessionKey: "loaded-B",
    hasOlderHistory: true,
    historyStartTurn: 31,
    historyTotalTurns: 40,
    loadingOlderHistory: true,
    onLoadOlderHistory,
  });
  await harness.settle();
  const loading = readLoaded(harness.container);
  eq(loading?.height, 200, "an in-flight page keeps the loaded height (no fake progress)");
  eq(loading?.phase, "loading", "the in-flight page reports the loading phase (≠ partial / exhausted)");

  // 5) 加载失败：reducer 的 history_older_error 不改 startTurn/totalTurns ⇒
  //    指示条不回退（仍 200px）、不假装成功（phase 回到 partial，非 exhausted）。
  await harness.render(turns(40), {
    creationMode: true,
    geometrySessionKey: "loaded-B",
    hasOlderHistory: true,
    historyStartTurn: 31,
    historyTotalTurns: 40,
    olderHistoryError: "会话内容已变化，请重试",
    onLoadOlderHistory,
  });
  await harness.settle();
  const failedLoad = readLoaded(harness.container);
  eq(failedLoad?.height, 200, "a failed load keeps the loaded height (no rollback)");
  eq(failedLoad?.phase, "partial", "a failed load reports partial again (not exhausted, not loading)");

  // 3) 全部加载完：hasOlder=false ⇒ 整条 rail 变深 + 收口标记（exhausted 态，
  //    与还能加载的 partial 态可区分）。
  await harness.render(turns(40), {
    creationMode: true,
    geometrySessionKey: "loaded-C",
    hasOlderHistory: false,
    historyStartTurn: 1,
    historyTotalTurns: 40,
    onLoadOlderHistory,
  });
  await harness.settle();
  const exhausted = readLoaded(harness.container);
  eq(exhausted?.height, 800, "an exhausted history fills the whole rail");
  eq(exhausted?.phase, "exhausted", "the top-of-history state is distinguishable from partial");

  // 6) 切换会话：B 会话只加载了 31-40/共 48 轮 ⇒ round(10/48×800)=167px ——
  //    断言按新会话值重置（≠ A 会话的 200/800px 任一读数，不残留进度）。
  await harness.render(turns(40), {
    creationMode: true,
    geometrySessionKey: "loaded-D",
    hasOlderHistory: true,
    historyStartTurn: 39,
    historyTotalTurns: 48,
    onLoadOlderHistory,
  });
  await harness.settle();
  const sessionSwitch = readLoaded(harness.container);
  eq(sessionSwitch?.height, expectedHeight(39, 48), "switching sessions resets the segment to the new session's progress");
  eq(sessionSwitch?.height, 167, "the new session reads 167px (10/48 of an 800px rail)");
  eq(sessionSwitch?.phase, "partial", "the new session reports its own partial phase");

  // 从未翻页（无 totalTurns）⇒ 不画区段（none 语义）。
  await harness.render(turns(6), {
    creationMode: true,
    geometrySessionKey: "loaded-E",
    onLoadOlderHistory,
  });
  await harness.settle();
  eq(readLoaded(harness.container), null, "a surface without history totals renders no loaded segment");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
