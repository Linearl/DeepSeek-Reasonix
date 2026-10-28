// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1212-concise-live-recurrence.test.tsx
//
// tl 1212 — 简洁模式流式思考失效复发：三分辨探针（新会话 / 恢复重连 / 收起后）。
//
// 任务书嫌疑机理：
//   ① 1160 同族复发——live 指针命中但 reasoning 空串顶掉快照（另一触发路径）；
//   ② 857 会话级收起意图门控 reconcile 残留；
//   ③ experience 三层状态链脱钩（localStorage 镜像 / 内存 hydrated / 启动快照）。
//
// 本文件先把三条机理各自构象在当前代码上跑一遍：红 = 复现钉死该路径，
// 绿 = 该路径排除。R2 状态快照：每景打印取数输入（items 思考腿/live 旗标）
// 与 DOM 判读，作为留证。

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

function snapshot(tag: string, items: Item[], live: { id?: string; reasoning?: string; text?: string } | undefined, extra: Record<string, unknown> = {}) {
  const assistants = items
    .map((it, index) => ({ it, index }))
    .filter(({ it }) => it.kind === "assistant")
    .map(({ it, index }) => `${index}:${(it as { id: string }).id}/r=${Math.min((it as { reasoning: string }).reasoning.length, 24)}`);
  const header = harness.container.querySelector("button.reasoning__head");
  process.stdout.write(`  [snap ${tag}] items=${items.length} assistants=[${assistants.join(", ")}] live=${live ? `${live.id}/r=${(live.reasoning ?? "").length}/t=${(live.text ?? "").length}` : "none"} ${JSON.stringify(extra)}\n`);
  process.stdout.write(`  [dom  ${tag}] header=${header ? "yes" : "no"} headerText=${(header?.textContent ?? "").slice(0, 40)} liveStatus=${liveStatus() ? "yes" : "no"} liveStatusText=${(liveStatus()?.textContent ?? "").slice(0, 60)}\n`);
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

console.log("\ntl 1212: 三分辨探针（concise 动态行复发）");

const harness = await createTranscriptHarness({
  storage: { "reasonix-session-experience": "concise" },
});
const sessionExperience = await harness.loadModule<{
  hydrateSessionExperience: (v: unknown) => void;
  getSessionExperience: () => string;
}>("/src/lib/sessionExperience.ts");
const foldOverrides = await harness.loadModule<{
  setTranscriptCollapseIntent: (sessionKey: string, intent: boolean) => void;
}>("/src/lib/transcriptFoldOverrides.ts");

sessionExperience.hydrateSessionExperience("concise");

const liveStatus = () => harness.container.querySelector("[data-live-status]");

const user = (): Item => ({ kind: "user", id: "u1", text: "继续排查" });

try {
  // ── S1 新会话（控制组，预期绿）：流中 assistant 带落盘快照 + running 工具 ──
  {
    const items: Item[] = [
      user(),
      { kind: "assistant", id: "a1", text: "", reasoning: "新会话第一段思考快照", streaming: true },
      { kind: "tool", id: "t1", name: "grep", args: '{"pattern":"p"}', readOnly: true, status: "running" },
    ];
    const live = { id: "a1", text: "", reasoning: "live 新思考尾行", reasoningComplete: false };
    await harness.render(items, { running: true, geometrySessionKey: "tl1212-s1", live });
    await sleep(700);
    await harness.flush();
    snapshot("S1 新会话", items, live, { dom: liveStatus() !== null });
    ok(liveStatus() !== null, "S1 新会话：状态行出现（控制组）");
  }

  // ── S2 恢复重连·快照在 + 指针空挂（1160 已修面，控制组，预期绿）──
  {
    const items: Item[] = [
      user(),
      { kind: "assistant", id: "a1", text: "", reasoning: "第一段已完成思考", streaming: false, reasoningComplete: true },
      { kind: "assistant", id: "a2", text: "", reasoning: "恢复后落盘的第二段思考快照", streaming: true },
    ];
    const live = { id: "a2", text: "", reasoning: "", reasoningComplete: false };
    await harness.render(items, { running: true, geometrySessionKey: "tl1212-s2", live });
    await sleep(700);
    await harness.flush();
    snapshot("S2 恢复+快照", items, live, { dom: liveStatus() !== null });
    ok(liveStatus() !== null, "S2 恢复重连（有快照+空指针挂）：状态行出现（1160 已修面）");
  }

  // ── S3 恢复重连·快照不在 + 指针空挂（嫌疑①「另一触发路径」，预期红）──
  // 中断发生在第一段思考尚未落盘（streamDeltaOnly：delta 只进 live 缓冲），
  // 恢复后指针重新挂上但缓冲被清空——displayItems 无任何思考腿可回退。
  {
    const items: Item[] = [
      user(),
      { kind: "assistant", id: "a1", text: "", reasoning: "", streaming: true },
    ];
    const live = { id: "a1", text: "", reasoning: "", reasoningComplete: false };
    await harness.render(items, { running: true, geometrySessionKey: "tl1212-s3", live });
    await sleep(700);
    await harness.flush();
    snapshot("S3 恢复无快照", items, live, { dom: liveStatus() !== null });
    ok(liveStatus() !== null, "S3 恢复重连（无快照+空指针挂）：状态行出现");
  }

  // ── S4 S3 + running 工具（动作腿应单腿撑起，预期绿=动作行在思考行无源）──
  {
    const items: Item[] = [
      user(),
      { kind: "assistant", id: "a1", text: "", reasoning: "", streaming: true },
      { kind: "tool", id: "t1", name: "grep", args: '{"pattern":"p"}', readOnly: true, status: "running" },
    ];
    const live = { id: "a1", text: "", reasoning: "", reasoningComplete: false };
    await harness.render(items, { running: true, geometrySessionKey: "tl1212-s4", live });
    await sleep(700);
    await harness.flush();
    snapshot("S4 无快照+工具", items, live, { dom: liveStatus() !== null });
    ok(liveStatus() !== null, "S4 无快照+running 工具：动作腿撑起状态行");
  }

  // ── S5 收起后（嫌疑②，预期绿）：857 收起意图激活 + 新流式思考带内容 ──
  {
    foldOverrides.setTranscriptCollapseIntent("tl1212-s5", true);
    const items: Item[] = [
      user(),
      { kind: "assistant", id: "a1", text: "", reasoning: "收起后新流式思考快照", streaming: true },
      { kind: "tool", id: "t1", name: "grep", args: '{"pattern":"p"}', readOnly: true, status: "running" },
    ];
    const live = { id: "a1", text: "", reasoning: "收起后 live 思考", reasoningComplete: false };
    await harness.render(items, { running: true, geometrySessionKey: "tl1212-s5", live });
    await sleep(700);
    await harness.flush();
    snapshot("S5 收起后", items, live, { dom: liveStatus() !== null });
    ok(liveStatus() !== null, "S5 收起全部后新流式：状态行出现（857 残留排除）");
    foldOverrides.setTranscriptCollapseIntent("tl1212-s5", false);
  }

  // ── S6 指针未命中 + 无快照（S3 变体：恢复后指针挂到未落 item 的新 id）──
  {
    const items: Item[] = [
      user(),
      { kind: "assistant", id: "a1", text: "", reasoning: "", streaming: true },
    ];
    const live = { id: "a2-new-attach", text: "", reasoning: "", reasoningComplete: false };
    await harness.render(items, { running: true, geometrySessionKey: "tl1212-s6", live });
    await sleep(700);
    await harness.flush();
    snapshot("S6 指针未命中无快照", items, live, { dom: liveStatus() !== null });
    ok(liveStatus() !== null, "S6 指针未命中+无快照：状态行出现");
  }

  // ── S7 三层链一致性（嫌疑③，预期绿）：快照缺字段回落镜像 concise 生效 ──
  {
    const moduleState = {
      effective: sessionExperience.getSessionExperience(),
      mirror: globalThis.localStorage.getItem("reasonix-session-experience"),
    };
    snapshot("S7 三层链", [], undefined, moduleState);
    ok(moduleState.effective === "concise" && moduleState.mirror === "concise",
      "S7 三层链：内存生效与镜像一致 concise（753 守卫在位）");
  }
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
