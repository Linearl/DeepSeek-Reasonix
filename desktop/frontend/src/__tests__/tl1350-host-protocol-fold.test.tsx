// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1350-host-protocol-fold.test.tsx
// tl1350 回归钉：host 协议消息（session-context/压缩提醒/reasoning-language/
// nudge）不再全宽铺开——默认折叠为一行紧凑条、点击展开全文、再点收起；
// 连续注入多条时每条占一行；真实用户 steer 不受影响仍走 steer 卡。

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

const nudge = "[Feedback inbox nudge task-172 T1]\nThe user's task has just been completed. If this session surfaced anything worth reporting — a bug, a rough edge, or a suggestion about Reasonix itself — end your reply with ONE short sentence inviting the user to record it in the feedback inbox.";
const sessionContext = '<session-context version="1">\n## Environment\n- OS: windows/amd64\n## Skills catalog\n- some-skill — does things\n</session-context>';
const compactionBrief = "Compact the preceding conversation prefix into a durable resume briefing.\nWrite under these exact headings, omitting a heading only if it has no content:";
const reasoningLang = "<reasoning-language>\nVisible reasoning/thinking text preference: use English when the provider exposes reasoning text.\n</reasoning-language>";

console.log("\ntl1350：host 协议消息自动折叠为紧凑条");

const harness = await createTranscriptHarness();
try {
  // ① 单条 feedback nudge：默认一行折叠条（aria-expanded=false），全文不在 DOM。
  const single: Item[] = [
    { kind: "user", id: "u1", text: "帮我跑一下测试" },
    { kind: "notice", id: "h1", level: "info", text: `↪ ${nudge}` },
    { kind: "assistant", id: "a1", text: "测试跑完了。", reasoning: "", streaming: false },
  ];
  await harness.render(single);
  let bar = () => harness.container.querySelector<HTMLButtonElement>(".host-protocol-bar");
  ok(Boolean(bar()), "nudge 渲染折叠条");
  ok(bar()?.getAttribute("aria-expanded") === "false", "默认收起（aria-expanded=false）");
  ok((bar()?.textContent ?? "").includes("host context") || (bar()?.textContent ?? "").includes("host 上下文"), "折叠条带 host context 标签");
  ok((bar()?.textContent ?? "").includes("[Feedback inbox nudge task-172 T1]"), "折叠态可见首行摘要");
  ok(!harness.container.querySelector(".host-protocol-bar__body"), "折叠态全文不在 DOM（≤一行占用）");
  ok(!harness.container.textContent?.includes("ONE short sentence inviting the user"), "折叠态长段正文不渲染");

  // ② 点击展开：全文出现；再点收起。
  bar()?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await harness.flush();
  ok(bar()?.getAttribute("aria-expanded") === "true", "点击后展开");
  const body = harness.container.querySelector<HTMLElement>(".host-protocol-bar__body");
  ok(Boolean(body) && (body?.textContent ?? "").includes("ONE short sentence inviting the user"), "展开后全文可见");
  bar()?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await harness.flush();
  ok(bar()?.getAttribute("aria-expanded") === "false", "再点收起");
  ok(!harness.container.querySelector(".host-protocol-bar__body"), "收起后全文消失");

  // ③ 连续注入多条（session-context + 压缩提醒 + reasoning-language + nudge）：
  //    info 级三条走 work-process fold——回合结束后 fold 默认收起，正文零渲染
  //    （占用 0 行，优于验收线）；↪ 前缀的 nudge 走 steer 路径，渲染折叠条
  //    （占用 1 行）。验收「≤一行/条」两路都成立。
  const burst: Item[] = [
    { kind: "user", id: "u1", text: "继续" },
    { kind: "notice", id: "h1", level: "info", text: sessionContext },
    { kind: "notice", id: "h2", level: "info", text: compactionBrief },
    { kind: "notice", id: "h3", level: "info", text: reasoningLang },
    { kind: "notice", id: "h4", level: "info", text: `↪ ${nudge}` },
    { kind: "assistant", id: "a1", text: "好的。", reasoning: "", streaming: false },
  ];
  await harness.render(burst);
  ok(harness.container.querySelectorAll(".host-protocol-bar").length === 1, "steer 路径 nudge 渲染折叠条");
  ok(harness.container.querySelectorAll(".host-protocol-bar__body").length === 0, "折叠态全文零渲染");
  ok(!harness.container.textContent?.includes("durable resume briefing.\nWrite under"), "压缩 briefing 正文不铺开（fold 收起）");
  ok(!harness.container.querySelector(".steer-line"), "host 协议消息不再走 steer 全宽卡");
  ok(!harness.container.textContent?.includes("## Skills catalog"), "session-context 信封正文不铺开（fold 收起）");

  // ④ 打开 work-process fold：fold 内三条 info host notice 各渲染折叠条（一行/条）。
  const foldHead = harness.container.querySelector<HTMLElement>(".reasoning__head");
  ok(Boolean(foldHead), "work-process fold 头部在 DOM");
  foldHead?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await harness.flush();
  const bars = harness.container.querySelectorAll(".host-protocol-bar");
  ok(bars.length === 4, `fold 打开后四类各渲染一条折叠条（实际 ${bars.length}）`);
  ok(harness.container.querySelectorAll(".host-protocol-bar__body").length === 0, "fold 打开后默认仍是折叠条形态（不自动铺全文）");

  // ④b 展开其中一条（reasoning-language）只影响该条。
  (bars[2] as HTMLButtonElement).dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await harness.flush();
  ok(harness.container.querySelectorAll(".host-protocol-bar__body").length === 1, "单条展开互不影响");
  ok((harness.container.querySelector(".host-protocol-bar__body")?.textContent ?? "").includes("Visible reasoning"), "展开的是被点的条");

  // ⑤ 真实用户 steer 不受影响：仍走 steer 卡（非折叠条）。
  const userSteer: Item[] = [
    { kind: "user", id: "u1", text: "跑测试" },
    { kind: "assistant", id: "a1", text: "跑着。", reasoning: "", streaming: false },
    { kind: "notice", id: "s1", level: "info", text: "↪ 先只跑 store 包，别动前端", createdAt: Date.now() },
  ];
  await harness.render(userSteer);
  ok(!harness.container.querySelector(".host-protocol-bar"), "用户 steer 不命中折叠条");
  ok(Boolean(harness.container.querySelector(".steer-line")), "用户 steer 仍渲染 steer 卡");

  // ⑥ 普通系统 notice 不受影响：仍走 NoticeCard。
  const sysNotice: Item[] = [
    { kind: "user", id: "u1", text: "继续" },
    { kind: "notice", id: "n1", level: "warn", text: "Tool round limit reached; asking the assistant to summarize progress." },
  ];
  await harness.render(sysNotice);
  ok(!harness.container.querySelector(".host-protocol-bar"), "系统 notice 不命中折叠条");
  ok(Boolean(harness.container.querySelector(".notice-line:not(.notice-line--host-protocol)")), "系统 notice 仍渲染 notice 卡");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
