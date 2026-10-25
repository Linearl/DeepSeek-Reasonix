// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1160-concise-live-restore.test.tsx
//
// tl 1160 — 中断恢复/重连后 concise 动态状态行消失的复现钉 + 修复验收。
//
// 现场构象（与用户观测逐项对齐）：
//   时长行在（「工作中 27 分 2 秒·2 段思考」）而 1141 动态行消失，跨模型
//   跨会话复现。定读结论：流式 delta 只进 live 缓冲不进 items（useController
//   "streamDeltaOnly" 契约），恢复/重连把 live 指针重新挂到当前 assistant
//   （id 命中）时缓冲为空——hasLiveAssistant 仅凭 id 命中就让调用方无条件
//   取 live.reasoning（=""），把 item 上已落盘的快照 reasoning 顶掉 → 思考腿
//   恒空；若无 running 工具（或仅 subagent 卡，被 displayItems 过滤）→ 腿A
//   同空 → showLiveStatus=false。
//
// 断言面：
//   R1 复现：恢复构象（快照有思考 + live 空缓冲 id 命中 + 无 running 工具）
//      → 状态行必须出现且取快照文本（修复前 FAIL）；
//   R2 不回归：live 缓冲有内容时仍优先 live（1141 B2 语义不变）；
//   R3 时长/动态同源一致性：同构象下 hasRunningWork=true 的 fold 必然产出
//      状态行（思考腿或动作腿至少一腿非空）。

import { JSDOM } from "jsdom";
import type { Item } from "../lib/useController";
import { createTranscriptHarness } from "./transcript-dom-harness";

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

function textOf(selector: string): string {
  return harness.container.querySelector<HTMLElement>(selector)?.textContent ?? "";
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

console.log("\ntl 1160: 中断恢复/重连构象（concise 动态行消失）");

const harness = await createTranscriptHarness({
  storage: { "reasonix-session-experience": "concise" },
});
const sessionExperience = await harness.loadModule<{
  hydrateSessionExperience: (v: unknown) => void;
}>("/src/lib/sessionExperience.ts");
sessionExperience.hydrateSessionExperience("concise");

// 恢复构象：回合仍在跑（running=true），折叠关闭，fold 内两段思考（标签
// 「2 段思考」的来源），第二段是恢复时已带落盘 reasoning 的流中 assistant，
// live 指针已重新挂上（id 命中）但缓冲为空（首 delta 未到/被回滚清空），
// 无 running 工具（推理/作答段或仅 subagent 卡被 displayItems 过滤）。
const restoredItems: Item[] = [
  { kind: "user", id: "u1", text: "继续排查" },
  { kind: "assistant", id: "a1", text: "", reasoning: "第一段思考（已完成）", streaming: false, reasoningComplete: true },
  { kind: "assistant", id: "a2", text: "", reasoning: "恢复后落盘的第二段思考快照", streaming: true },
];
const restoredLive = { id: "a2", text: "", reasoning: "", reasoningComplete: false };
const liveStatus = () => harness.container.querySelector("[data-live-status]");

try {
  // R1 复现：空 live 缓冲不得顶掉快照 → 状态行出现
  await harness.render(restoredItems, { running: true, geometrySessionKey: "tl1160-restore", live: restoredLive });
  ok(liveStatus() !== null, "R1: 恢复构象（live 空缓冲 id 命中）状态行出现");
  ok(textOf("button.turn-collapse__live-summary").includes("恢复后落盘的第二段思考快照"),
    "R1: 思考腿取快照文本（空 live 缓冲回退 latestReasoningText）");
  await sleep(700);
  await harness.flush();
  ok(textOf("button.turn-collapse__live-summary").includes("恢复后落盘的第二段思考快照"),
    "R1: 节流窗口后仍稳定显示快照文本");

  // R2 不回归：live 缓冲有内容时仍优先 live（1141 B2 语义）
  await harness.render(restoredItems, {
    running: true,
    geometrySessionKey: "tl1160-restore",
    live: { id: "a2", text: "", reasoning: "live 流式新思考尾行", reasoningComplete: false },
  });
  await sleep(700);
  await harness.flush();
  ok(textOf("button.turn-collapse__live-summary").includes("live 流式新思考尾行"),
    "R2: live 缓冲非空时仍优先 live 文本");

  // R3 时长/动态同源一致性：live 指针挂在 displayItems 之外的 assistant
  // （新轮次尚未落 item）时，思考腿回退快照而不是静默。
  await harness.render(restoredItems, {
    running: true,
    geometrySessionKey: "tl1160-restore",
    live: { id: "a3-new-attach", text: "", reasoning: "", reasoningComplete: false },
  });
  await sleep(700);
  await harness.flush();
  ok(liveStatus() !== null, "R3: live 指针指向未落 item 的新 assistant 时状态行仍出现");
  ok(textOf("button.turn-collapse__live-summary").includes("恢复后落盘的第二段思考快照"),
    "R3: 指针未命中时取尾部快照");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
