// Run: LANG=en node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/task857-collapse-all-new-thinking.test.tsx
//      (css-stub + LANG=en is the required harness route — a bare `tsx` first
//       run stalls >120s on the stylesheet import; LANG guards zh copy false-reds.)
//
// 任务 857 回归钉（端到端，用户 1146 澄清的现象原样复现）：
//   点「收起全部工作过程」确实收起 ✓；继续工作一旦出现新思考（thinking 流式），
//   过程又自动展开 ✗ ——收起状态对新渲染内容不生效。
//
// 归因：collapse-all 只对当时已存在的 segment 写 userOverridden 条目；新 segment
// （新回合的新 thinking）在 FoldMap 无条目，reconcile 按 defaultFoldOpen 给默认值
// ——standard 下运行中即展开。修复：会话级收起意图（task 857 collapse-intent），
// 新块按意图收起，直到用户主动展开（expand-all / 点开块头 / 切显示档）。
//
// 钉面（standard 档，transcript 侧，事件即 Composer 按钮派发的原事件）：
//   P0 前置：standard + running → fresh fold 展开（defaultFoldOpen 语义未变）；
//   P1 collapse-all 全收起；
//   P2 新回合新 thinking 流式到达 → 不展开（修复本体；修复前此断言红）;
//   P3 再经历 running:false→true（tab 切回/重挂载的 fresh-run）→ 不展开；
//   P4 expand-all 撤回意图 → 展开；之后新块恢复默认展开（原语义不漂移）。

import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntask 857: collapse-all survives new thinking streams");

const harness = await createTranscriptHarness({
  storage: { "reasonix-session-experience": "standard" },
});
const { container } = harness;
const { act } = await import("react");

try {
  const turnOne: Item[] = [
    { kind: "user", id: "u1", text: "first ask" },
    { kind: "assistant", id: "a1", text: "working on it", reasoning: "first thought", streaming: true, workDurationMs: 500 },
  ];
  // P0 前置锚：standard 体验下运行中的 fresh fold 默认展开。
  await harness.render(turnOne, { running: true });
  await harness.flush();
  ok(Boolean(container.querySelector(".turn-collapse--open")), "P0: standard live-expands a fresh running fold (precondition)");

  // P1: collapse-all（Composer 按钮派发的同一事件）全收起。
  await act(async () => {
    window.dispatchEvent(new CustomEvent("reasonix:collapse-all-folds"));
  });
  await harness.flush();
  ok(!container.querySelector(".turn-collapse--open"), "P1: collapse-all closes every work process");

  // P2（回归本体）：新回合的新 thinking 流式到达——修复前 defaultFoldOpen
  // 把新 segment 展开顶回去；修复后按会话级收起意图保持收起。
  const withNewThinking: Item[] = [
    ...turnOne.map((item) => (item.kind === "assistant" ? { ...item, streaming: false } : item)),
    { kind: "user", id: "u2", text: "second ask" },
    { kind: "assistant", id: "a2", text: "", reasoning: "new thinking streaming", streaming: true, workDurationMs: 100 },
  ];
  await harness.render(withNewThinking, { running: true });
  await harness.settle();
  ok(!container.querySelector(".turn-collapse--open"), "P2: a new thinking turn arriving after collapse-all stays collapsed");

  // P3: fresh-run（running false→true，等价 tab 切回后回合仍在跑）不清除收起。
  await harness.render(withNewThinking, { running: false });
  await harness.flush();
  await harness.render(withNewThinking, { running: true });
  await harness.settle();
  ok(!container.querySelector(".turn-collapse--open"), "P3: a fresh running reconcile under the intent keeps everything collapsed");

  // P4: 用户主动展开（expand-all 事件）撤回意图；之后的新块恢复默认展开。
  await act(async () => {
    window.dispatchEvent(new CustomEvent("reasonix:expand-all-folds"));
  });
  await harness.flush();
  ok(Boolean(container.querySelector(".turn-collapse--open")), "P4: expand-all opens the folds again");
  const turnThree: Item[] = [
    ...withNewThinking.map((item) => (item.kind === "assistant" && item.id === "a2" ? { ...item, streaming: false } : item)),
    { kind: "user", id: "u3", text: "third ask" },
    { kind: "assistant", id: "a3", text: "", reasoning: "third thinking", streaming: true, workDurationMs: 100 },
  ];
  await harness.render(turnThree, { running: true });
  await harness.settle();
  ok(Boolean(container.querySelector(".turn-collapse--open")), "P4: after the intent is lifted a new thinking turn live-expands (legacy default)");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
