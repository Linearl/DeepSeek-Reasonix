// Run: tsx src/__tests__/turn-result-notice-collapse.test.tsx
// 任务524 回归钉：「本轮结果」面板不再常驻挤压对话视野——
// 默认折叠为一行摘要（关键结论可见）、点击展开/收起、
// 终态动作按钮（审阅/撤销代码改动/查看差异/查看检查详情）随折叠收起、
// 新一轮结果（key 变化）重置回折叠。

import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";
import type { TurnChanges, WireCompletionSummary } from "../lib/types";

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

console.log("\n任务524：本轮结果面板默认折叠，不常驻挤压视野");

const diff: TurnChanges = {
  id: "0:1", turn: 0, coverage: "partial",
  files: [
    { path: "src/a.ts", kind: "modify", added: 60, removed: 0 },
    { path: "src/b.ts", kind: "create", added: 12, removed: 0 },
  ],
  added: 72, removed: 0, reasons: ["部分文件未纳入统计"],
};
const summaryFor = (turnId: string): WireCompletionSummary => ({
  preset: "balanced", verdict: "partial", mutations: 2,
  checks_passed: 1, checks_failed: 0, checks_suppressed: 0,
  review: "passed", turnId, checkpointTurn: 0,
  receipt: { verdict: "partial", diff },
});
const completionNotice = (id: string, summary: WireCompletionSummary): Item => ({
  kind: "notice", id, level: "info", variant: "completion",
  title: "Turn result", text: "turn result body", action: "open_changes",
  completionSummary: summary,
});
const items = (summary: WireCompletionSummary): Item[] => [
  { kind: "user", id: "u1", text: "do it" },
  { kind: "assistant", id: "a1", text: "Done.", reasoning: "", streaming: false },
  completionNotice("q1", summary),
];

const harness = await createTranscriptHarness();
try {
  const first = summaryFor("t1");
  await harness.render(items(first), {
    running: false,
    onOpenChanges: () => {},
    onOpenVerification: () => {},
    onRewind: () => {},
  });

  const toggle = () => harness.container.querySelector<HTMLButtonElement>(".notice-line__summary-toggle");

  // ① 默认折叠：一行摘要 + 关键结论可见。
  ok(Boolean(toggle()), "折叠态渲染一行摘要开关");
  ok(toggle()?.getAttribute("aria-expanded") === "false", "默认折叠（aria-expanded=false）");
  ok(/2 files counted/.test(toggle()?.textContent ?? "") && /\+72/.test(toggle()?.textContent ?? "") && /−0/.test(toggle()?.textContent ?? ""),
    "折叠态关键结论可见（文件数 + 增删行数）");
  ok(/Partial statistics/.test(toggle()?.textContent ?? ""), "折叠态保留部分统计标记");
  ok(/check/i.test(toggle()?.textContent ?? ""), "折叠态保留检查结论");

  // ② 折叠态零占位：完整回执与终态动作按钮全部不在 DOM。
  ok(!harness.container.querySelector(".turn-edit-list"), "折叠态不渲染文件列表");
  ok(!harness.container.querySelector(".turn-result-summary"), "折叠态不渲染完整回执块");
  const actionLabels = ["Review", "Undo code changes", "View changes", "View check details"];
  ok(!Array.from(harness.container.querySelectorAll("button")).some((node) => actionLabels.some((label) => node.textContent?.includes(label))),
    "折叠态终态动作按钮（审阅/撤销/查看差异/查看检查详情）全部收起");

  // ③ 点击展开：完整列表与动作按钮出现。
  toggle()?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await harness.flush();
  ok(toggle()?.getAttribute("aria-expanded") === "true", "点击后展开（aria-expanded=true）");
  ok(Boolean(harness.container.querySelector(".turn-edit-list")), "展开后渲染文件列表");
  ok(Array.from(harness.container.querySelectorAll("button")).some((node) => node.textContent?.includes("View changes")), "展开后出现查看差异");
  ok(Array.from(harness.container.querySelectorAll("button")).some((node) => node.textContent?.includes("View check details")), "展开后出现查看检查详情");
  ok(Array.from(harness.container.querySelectorAll("button")).some((node) => node.textContent?.includes("Undo code changes")), "展开后出现撤销代码改动");
  ok(Array.from(harness.container.querySelectorAll("button")).some((node) => node.textContent?.includes("Review")), "展开后出现审阅");

  // ④ 再点收起：动作按钮随之收起，摘要行仍在。
  toggle()?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await harness.flush();
  ok(toggle()?.getAttribute("aria-expanded") === "false", "再次点击收起");
  ok(!harness.container.querySelector(".turn-edit-list"), "收起后文件列表消失");
  ok(!Array.from(harness.container.querySelectorAll("button")).some((node) => actionLabels.some((label) => node.textContent?.includes(label))),
    "收起后终态动作按钮不再渲染（避免误触）");
  ok(/2 files counted/.test(toggle()?.textContent ?? ""), "收起后一行摘要仍可见");

  // ⑤ 新一轮结果（key 变化）重置回折叠：用户下一次输入后面板回到一行。
  await harness.render(items(summaryFor("t2")), {
    running: false,
    onOpenChanges: () => {},
    onOpenVerification: () => {},
    onRewind: () => {},
  });
  ok(toggle()?.getAttribute("aria-expanded") === "false", "新一轮结果默认重新折叠（不延续上一轮展开态）");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
