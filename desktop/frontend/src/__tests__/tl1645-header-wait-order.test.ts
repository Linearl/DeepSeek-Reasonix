// Run: tsx src/__tests__/tl1645-header-wait-order.test.ts
//
// tl1645 验收：回合进行中转录 live 块序 = 等待提示 → 思考输出 → 正文输出。
//   根因：header_wait_light（任务1080 4s 轻提示）作为普通 notice push 到
//   items 尾部（appendNoticeItem），落在当前流式 assistant（思考占位/思考
//   流）之后——顺序反了（用户 20261116 15:33 截图：思考中… 在上、等待提示
//   被压到下面）。另：1080 后端正文与 1079 suggestion 文案逐字相同，正文+
//   怎么办行两行连排同句（「出现两次」）；后者在 controllerNotices 摘除映射
//   （notice-suggestion-1079.test.ts 钉）。
//   修复：header_wait_light 插入到当前回合 assistant 块之前
//   （headerWaitNoticeInsertIndex）；无 assistant 时回落尾部追加（=旧行为，
//   普通回合零回退）。「思考开始流式后等待提示消失/降级」既有设计=不消失，
//   保持不动。

import { initialState, reducer, headerWaitNoticeInsertIndex, type Item, type State } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function submit(s: State, text: string, seq: number) {
  return reducer(s, { type: "user", text, seq, submissionId: `s${seq}` });
}

console.log("\ntl1645：等待提示块序（等待 → 思考 → 正文）");

{
  // 场景 A（任务书现场）：轮开始 → 占位 assistant（turn_started 预建）→
  // 4s 等待提示到达 —— 提示插到 assistant 之前。
  let s = submit(initialState, "帮我改这个文件", 0);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
  const assistantAt = s.items.findIndex((it) => it.kind === "assistant");
  ok(assistantAt >= 0, "前置：turn_started 预建占位 assistant");

  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "header_wait_light", text: "正在等待模型响应…（通常几十秒内会开始输出；若长时间无响应可按 Esc 取消）", detail: "awaiting response headers (light tier), elapsed 4s" } });
  const waitAt = s.items.findIndex((it) => it.kind === "notice" && it.code === "header_wait_light");
  const assistantAfter = s.items.findIndex((it) => it.kind === "assistant");
  ok(waitAt >= 0, "等待提示落行");
  ok(waitAt >= 0 && waitAt < assistantAfter, "tl1645：等待提示在 assistant（思考/正文）块之前（顺序=等待→思考→正文）");
  const waitItem = s.items[waitAt];
  ok(waitItem.kind === "notice" && !("suggestion" in waitItem && waitItem.suggestion), "tl1645：等待提示不再携带重复 suggestion 行（正文单点承载文案）");
}

{
  // 场景 B（思考流式中）：思考 delta 已流 → 等待提示到达 → 仍在思考块前。
  let s = submit(initialState, "想想再答", 0);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
  s = reducer(s, { type: "event", e: { kind: "reasoning", turnId: "t1", text: "先分析一下…" } });
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "header_wait_light", text: "正在等待模型响应…", detail: "awaiting response headers (light tier), elapsed 4s" } });
  const waitAt = s.items.findIndex((it) => it.kind === "notice" && it.code === "header_wait_light");
  const assistantAt = s.items.findIndex((it) => it.kind === "assistant");
  ok(waitAt >= 0 && assistantAt >= 0 && waitAt < assistantAt, "tl1645：思考流式中到达的等待提示同样插到思考块之前");
}

{
  // 场景 C（零回退）：无 assistant 条目时回落尾部追加（与旧行为一致）。
  const at = headerWaitNoticeInsertIndex([
    { kind: "user", id: "u1", text: "问" },
    { kind: "tool", id: "t1", name: "exec_command", args: "{}", readOnly: false, status: "done" },
  ]);
  eq(at, -1, "tl1645：无 assistant 时插入点=-1（回落尾部追加，普通回合零回退）");

  let s = submit(initialState, "问", 0);
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "header_wait_light", text: "正在等待模型响应…", detail: "awaiting" } });
  const waitAt = s.items.findIndex((it) => it.kind === "notice" && it.code === "header_wait_light");
  ok(waitAt >= 0 && waitAt === s.items.length - 1, "tl1645：无 assistant 场景等待提示仍落尾部（既有顺序不变）");
}

{
  // 场景 D（纯函数边界）：多段 assistant 取最后一段（工具间隙第二段思考前）。
  const items: Item[] = [
    { kind: "assistant", id: "a1", text: "第一段", reasoning: "", streaming: false },
    { kind: "tool", id: "t1", name: "exec_command", args: "{}", readOnly: false, status: "done" },
    { kind: "assistant", id: "a2", text: "", reasoning: "", streaming: true },
  ];
  eq(headerWaitNoticeInsertIndex(items), 2, "tl1645：多段 assistant 时插到最后一段（当前回合输出块）之前");
}

console.log(`\ntl1645 header-wait order: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
