// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1350-host-protocol-notice-classify.test.ts
// tl1350 分类器纯逻辑钉：四类 host 协议消息（session-context / 压缩提醒 /
// reasoning-language 约束 / nudge）全命中；真实用户 steer / 普通系统 notice
// 不误伤。

import { isHostProtocolNoticeText, hostProtocolNoticeBody } from "../lib/hostProtocolNotice";

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

console.log("\ntl1350：host 协议消息分类器");

// ① session-context 信封（原始泄漏形态，含属性）。
ok(isHostProtocolNoticeText('<session-context version="1">\n## Environment\n- OS: windows\n</session-context>'), "session-context 信封命中");
// ② 压缩提醒（compaction briefing lead，IsCompactionBriefingInstruction 前端镜像）。
ok(isHostProtocolNoticeText("Compact the preceding conversation prefix into a durable resume briefing.\nWrite under these exact headings…"), "压缩 briefing 首句命中");
// ③ reasoning-language 约束块。
ok(isHostProtocolNoticeText("<reasoning-language>\n必须使用简体中文书写全部可见思考/推理文本\n</reasoning-language>"), "reasoning-language 块命中");
// ④ nudge：feedback（任务 172）+ 跨会话回信（任务 530）。
ok(isHostProtocolNoticeText("[Feedback inbox nudge task-172 T1]\nThe user's task has just been completed. If this session surfaced anything worth reporting…"), "feedback nudge 命中");
ok(isHostProtocolNoticeText("[跨会话回信提醒]\n以下信件已读未回…"), "跨会话回信提醒命中");
// ↪ 前缀（hostGuidanceRows/live steer 统一加）不影响命中。
ok(isHostProtocolNoticeText("↪ [Feedback inbox nudge task-172 T1]\nThe user's task has just been completed…"), "↪ 前缀剥除后命中");
// 其余同族 host 引导（英文长段）。
ok(isHostProtocolNoticeText("Host note before the plan: no read-only investigation has been delegated this turn…"), "plan research gate 命中");
ok(isHostProtocolNoticeText("Continue this task. The host could not settle the turn's verification evidence before the last answer…"), "readiness advisory retry 命中");
ok(isHostProtocolNoticeText("Your tool-call round limit (steps) has been reached. Do not call any more tools…"), "finalization nudge（轮上限）命中");
ok(isHostProtocolNoticeText("This task has reached its time budget. Do not call any more tools…"), "finalization nudge（预算）命中");
ok(isHostProtocolNoticeText("Host progress reset: the current todo list produced no new completion…"), "todo 进度重定向命中");
// 其余 transient 块标签泄漏（context-state / current-time 等）。
ok(isHostProtocolNoticeText("<context-state>window occupancy 12k/1000k (12%)</context-state>"), "context-state 块命中");
ok(isHostProtocolNoticeText("<current-time>2026-11-03T10:00:00+08:00</current-time>"), "current-time 块命中");

// 不误伤：真实用户 steer、普通系统 notice、决策回执。
ok(!isHostProtocolNoticeText("↪ 帮我把这个函数改成异步的，另外看下报错日志"), "用户 steer（中文指令）不命中");
ok(!isHostProtocolNoticeText("↪ fix the failing test in pkg/store then rerun the suite"), "用户 steer（英文指令）不命中");
ok(!isHostProtocolNoticeText("Tool round limit reached; asking the assistant to summarize progress."), "系统 notice（tool_budget 本地化短句）不命中");
ok(!isHostProtocolNoticeText("The assistant answered before taking action; asking it to use the required tools."), "系统 notice（executor_handoff 短句）不命中");
ok(!isHostProtocolNoticeText(""), "空文本不命中");
ok(!isHostProtocolNoticeText("继续，把上一步的输出贴出来"), "普通续轮指令不命中");
// 前缀剥除函数本体。
ok(hostProtocolNoticeBody("↪ 内容") === "内容", "hostProtocolNoticeBody 剥 ↪ 前缀");
ok(hostProtocolNoticeBody("无前缀") === "无前缀", "hostProtocolNoticeBody 无前缀原样返回");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
