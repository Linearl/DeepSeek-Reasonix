#!/usr/bin/env node
// tl1650 阶段 1：注入通道归一化——user 形态消息写入转录/事件流的合法入口断言。
//
// Why: 同一条「往会话转录/事件流注入 user 形态消息」的语义，本仓有七条实现
// 通道（正常回合提交 / steer / durable inbox / 后端直提通报 / 心跳 / host 引
// 导 / 取消恢复重写），每条各自有落盘格式、事件发射面、渲染订阅面与历史提
// 示词判据。三例实证（tl1602 面板判据漏 steer、tl1643 注入消息渲染断链、
// tl1648 压缩窗口内注入无排队）都是「某条通道少接了一面」的同一问题族。
// 阶段 2 要把这些通道收口到单入口；阶段 1 先钉住现状：本脚本固化「合法入
// 口清单」，任何新增旁路写入点（新文件构造 user 消息写入转录 / 新文件发射
// user_input 事件 / reducer 分叉）立即红。
//
// 三层断言：
//   R1 ENTRIES     锚清单——每个合法入口的 file+patterns 必须存在（防腐化：
//                  重构挪走锚即红，强制同步更新本清单）。
//   R2 SCANS       组合扫描——写入方法调用行（conversation.Add / AddBatch /
//                  AppendTurnContextAndUser）+ 窗口内 user 形态标志（RoleUser /
//                  HostGeneratedUserMessage / persistedUserTurn）组合命中的文
//                  件必须登记在白名单；未登记 = 新增旁路。
//   R3 前端 reducer——case "user_input" / "steer" 只允许在 useController.ts。
//
// 漏报边界（诚实声明）：既有白名单文件里新增第二个写入点抓不到（文件粒度
// 白名单的固有折衷）；R1 锚清单保证已知点不消失，阶段 2 收口时文件清单会
// 收敛变窄。枚举方法与完整对照表见
// global-workspace/docs/reasonix/report/tl1650-注入通道拦点枚举-20261116.md。
//
// Usage: node scripts/check-injection-entrypoints.mjs   (from the repo root)

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, sep } from "node:path";

const ROOT = process.cwd();

// ── 扫描范围 ────────────────────────────────────────────────────────────
// Go 主仓两棵树；desktop 是独立 Go module 但注入面就在 desktop/*.go，
// 一并扫。benchmarks/cmd/prod_test 等独立入口不进转录通道，排除。
const GO_ROOTS = ["internal", "desktop"];
const GO_SKIP_DIRS = new Set([
  "frontend", "dist", "node_modules", "tmp", "dev", "docs", "hooks",
]);
const TS_ROOT = "desktop/frontend/src";

function isTestGoFile(rel) {
  return /_test\.go$/.test(rel) || /tl1280_test_helpers\.go$/.test(rel);
}

function walk(dir, skipDirs, out) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const name of entries) {
    const full = join(dir, name);
    let st;
    try {
      st = statSync(full);
    } catch {
      continue;
    }
    if (st.isDirectory()) {
      if (!skipDirs.has(name)) walk(full, skipDirs, out);
    } else if (/\.(go|ts|tsx)$/.test(name)) {
      out.push(full);
    }
  }
  return out;
}

function relPath(full) {
  return full.slice(ROOT.length + 1).replaceAll(sep, "/");
}

function goFiles() {
  const out = [];
  for (const root of GO_ROOTS) walk(join(ROOT, root), GO_SKIP_DIRS, out);
  return out.map(relPath).filter((r) => !isTestGoFile(r) && r.endsWith(".go"));
}

function tsFiles() {
  const out = [];
  walk(join(ROOT, TS_ROOT), new Set(["node_modules", "__tests__"]), out);
  return out.map(relPath).filter((r) => /\.(ts|tsx)$/.test(r));
}

function readRepo(rel) {
  return readFileSync(join(ROOT, rel), "utf8");
}

// ── R1：合法入口锚清单 ─────────────────────────────────────────────────
// 每行：通道、仓库相对文件、该文件必须全部出现的锚（函数定义/调用形态）。
// 锚随实现换代要同步更新——更新即代表人工复核过入口面，这正是门禁的价值。
const ENTRIES = [
  // ── 通道 A：正常回合提交（composer / bot bridge / ACP prompt / slash-skill）──
  {
    channel: "A 正常回合提交 desktop 面",
    file: "desktop/turn_io_app.go",
    patterns: [
      "func (a *App) submitToTabResult(",
      "SubmitDisplayWithResult(display, input)",
      "ctrl.SubmitUserTurn(input, input)",
    ],
  },
  {
    channel: "A 正常回合提交 control 面",
    file: "internal/control/turn_orchestrator.go",
    patterns: [
      "func (o *turnOrchestrator) runOrchestratedTurn(",
      "persistedUserTurn(input, firstNonEmpty(raw, task), images, nil, time.Now().UnixMilli())",
    ],
  },
  {
    channel: "A 正常回合提交 agent 落盘（唯一 user 输入落盘点）",
    file: "internal/agent/run_loop.go",
    patterns: [
      "Role: provider.RoleUser, Origin: inputMessageOrigin(ctx), Content: input, RawContent: rawContent",
      "appendPinnedRevisionAndUser(ctx, pinned, userMessage)",
    ],
  },
  {
    channel: "A 规划空转回合的原始输入落盘（persistExecutorNoOp）",
    file: "internal/agent/coordinator.go",
    patterns: [
      "func (c *Coordinator) persistExecutorNoOp(",
      "c.executor.AppendTurnContextAndUser(ctx, provider.Message{",
    ],
  },
  {
    channel: "A turn context + user 批量落盘方法",
    file: "internal/agent/session_context.go",
    patterns: [
      "func (a *Agent) AppendTurnContextAndUser(ctx context.Context, user provider.Message) bool",
    ],
  },
  {
    channel: "A/G persistedUserTurn 工厂（Origin=user 固定）",
    file: "internal/control/turn_message_origin.go",
    patterns: [
      "func persistedUserTurn(content, raw string, images, audio []string, createdAt int64) provider.Message {",
    ],
  },

  // ── 通道 B：steer（mid-turn 引导）──
  {
    channel: "B steer desktop 入口",
    file: "desktop/turn_io_app.go",
    patterns: [
      "func (a *App) SteerForTab(",
      "steerer.TrySteer(text)",
    ],
  },
  {
    channel: "B steer controller 入口（tl1357 受理即落盘）",
    file: "internal/control/controller.go",
    patterns: [
      "func (c *Controller) TrySteer(text string) bool {",
      "Intent:  sessioninbox.IntentSteer,",
      "func (c *Controller) Steer(text string) {",
    ],
  },
  {
    channel: "B steer agent 队列与未消费归档",
    file: "internal/agent/agent.go",
    patterns: [
      "func (a *Agent) SteerItem(",
      "func (a *Agent) consumeSteer(",
      "func (a *Agent) flushSteerQueue(",
      "func (a *Agent) recordUnappliedSteerArchive(",
    ],
  },
  {
    channel: "B steer 注入落盘 + Steer 事件发射（工具轮间隙）",
    file: "internal/agent/run_loop.go",
    patterns: [
      "midTurnSteerMessage(payload.Text)",
      "Kind: event.Steer",
    ],
  },
  {
    channel: "B steer durable item 手动领取",
    file: "internal/control/inbox_steer.go",
    patterns: [
      "func (c *Controller) TrySteerInboxItem(",
      "func (c *Controller) TrySteerInboxItemForTurn(",
    ],
  },
  {
    channel: "B steer desktop 面板领取",
    file: "desktop/inbox_app.go",
    patterns: [
      "func (a *App) SteerInboxItem(",
      "func (a *App) SteerInboxItemForTurn(",
    ],
  },

  // ── 通道 C：durable inbox（跨会话 / bot / CLI / serve / 引导注入）──
  {
    channel: "C durable 入队 + steer 尝试（TryEnqueueAndSteer）",
    file: "internal/control/inbox.go",
    patterns: [
      "func (c *Controller) TryEnqueueAndSteer(req InboxRequest)",
    ],
  },
  {
    channel: "C 引导注入单入口（任务229 G3 InjectGuidance）",
    file: "internal/control/guidance.go",
    patterns: [
      "func InjectGuidance(inbox Inbox, req GuidanceRequest)",
      "func (c *Controller) InjectGuidance(req GuidanceRequest)",
    ],
  },
  {
    channel: "C inbox 派发 + UserInput 事件发射（任务580 wire 唯一 user 通道）",
    file: "internal/control/inbox_run.go",
    patterns: [
      "func (c *Controller) emitInboxUserInput(",
      "Kind: event.UserInput, Text: display, ItemID: itemID, At: atMs",
      "func (c *Controller) submitPreparedInboxTurn(",
    ],
  },
  {
    channel: "C ACP sessionSteer 外部引导",
    file: "internal/acp/service.go",
    patterns: [
      "func (s *service) sessionSteer(",
      "Intent:  sessioninbox.IntentSteer,",
    ],
  },
  { channel: "C ACP 引导入队", file: "internal/acp/inbox.go", patterns: ["api.TryEnqueueAndSteer(req)"] },
  { channel: "C IM bot 入队", file: "internal/bot/inbox_queue.go", patterns: ["ctrl.TryEnqueueAndSteer(req)"] },
  { channel: "C CLI 入队", file: "internal/cli/inbox_queue.go", patterns: ["m.ctrl.TryEnqueueAndSteer(control.InboxRequest{"] },
  { channel: "C serve HTTP 入队", file: "internal/serve/inbox.go", patterns: ["api.TryEnqueueAndSteer(req)"] },

  // ── 通道 D：后端直提通报（tl1643 / task734 wire 通报）──
  {
    channel: "D 后端直提通报发射器",
    file: "desktop/turn_io_app.go",
    patterns: [
      "func (a *App) emitBackendSubmittedUserInput(",
      "tab.sink.Emit(event.Event{Kind: event.UserInput, Text: display})",
    ],
  },
  { channel: "D 自主更新重启恢复（resumeRosterSession ×3 通报）", file: "desktop/autonomous_update_resume.go", patterns: ["func (a *App) resumeRosterSession(", "a.emitBackendSubmittedUserInput(tabID, prompt)"] },
  { channel: "D 一键崩溃分析", file: "desktop/crash_analysis.go", patterns: ["a.emitBackendSubmittedUserInput(tabID, instruction)"] },
  { channel: "D autopilot 恢复", file: "desktop/tabs.go", patterns: ["a.emitBackendSubmittedUserInput(id, autopilotResumePrompt)"] },

  // ── 通道 E：心跳定时提交 ──
  {
    channel: "E 心跳回合提交（task1062 标记变体）",
    file: "desktop/turn_io_app.go",
    patterns: [
      "func (a *App) submitHeartbeatTurnToTabWithSink(",
      "ctrl.SubmitHeartbeatTurn(input, input)",
    ],
  },
  { channel: "E 心跳引擎调用", file: "desktop/heartbeat.go", patterns: ["e.app.submitHeartbeatTurnToTabWithSink(tabMeta.ID, prompt, botForwarder)"] },
  { channel: "E control 心跳回合入口", file: "internal/control/heartbeat_turn.go", patterns: ["func (c *Controller) SubmitHeartbeatTurn(input, display string) {"] },

  // ── 通道 F：host 引导（Origin=host 的 user 形态，仅落盘+历史重放渲染）──
  {
    channel: "F host 引导工厂",
    file: "internal/agent/message_origin.go",
    patterns: [
      "func HostGeneratedUserMessage(content string) provider.Message {",
    ],
  },
  {
    channel: "F host 历史重放判据（渲染/提示词构造共用）",
    file: "internal/agent/preview.go",
    patterns: [
      "func IsHostGeneratedUserMessage(msg provider.Message) bool {",
    ],
  },
  { channel: "F 回信提醒（任务530）", file: "internal/agent/collab_reply_nudge.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(collabReplyNudgeMessage(due))))"] },
  { channel: "F 压缩指令", file: "internal/agent/compact.go", patterns: ["HostGeneratedUserMessage(compactionInstructionWithFocus(instructions))"] },
  { channel: "F 压缩截断标记（fold/slim/tailview/truncate 投影）", file: "internal/agent/compact_fold_input.go", patterns: ["HostGeneratedUserMessage(fmt.Sprintf(truncatedHistoryMarker, dropped))"] },
  { channel: "F 反馈邀请（任务172 T1）", file: "internal/agent/feedback_nudge.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(guidance)))"] },
  { channel: "F 终局 nudge", file: "internal/agent/finalization.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(cause.nudge(state, canSubmitPlan))))"] },
  { channel: "F goal 边界 checkpoint", file: "internal/agent/goal_run_boundary.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(checkpoint)))"] },
  { channel: "F 读继续指令", file: "internal/agent/incomplete_read_runtime.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(instruction)))"] },
  { channel: "F reasoning-only 续答（tl-1166）", file: "internal/agent/reasoning_only_final.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(reasoningOnlyContinuationMessage())))"] },
  { channel: "F todo 续跑", file: "internal/agent/standard_todo_continuation.go", patterns: ["conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(standardTodoContinuationMessage())))"] },
  { channel: "F turn context snapshot 构造", file: "internal/agent/session_context.go", patterns: ["return HostGeneratedUserMessage(snapshot.Content), true"] },
  { channel: "F pinned context revision 落盘（编码记录）", file: "internal/agent/pinned_context_revision.go", patterns: ["message := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: string(encoded)}"] },
  { channel: "F 压缩摘要投影包装", file: "internal/agent/projection.go", patterns: ["func formatSummaryMessage(summary string) provider.Message {"] },

  // ── 通道 G：取消恢复转录重写 ──
  {
    channel: "G 取消恢复重写（fallback user turn + steer/host 保活 tl1602）",
    file: "internal/control/controller.go",
    patterns: [
      "func (c *Controller) stripCancelledVisibleTurnMessagesAfterWithFallbackAt(",
      "c.replaceSessionAfterCancel(next)",
    ],
  },

  // ── 渲染订阅面（前端 reducer）──
  {
    channel: "前端 reducer user_input/steer 订阅（任务580/tl1152/tl1611）",
    file: "desktop/frontend/src/lib/useController.ts",
    patterns: [
      'case "user_input":',
      'case "steer":',
    ],
  },
  {
    channel: "前端 host 恢复引导判据（不渲染为用户 steer）",
    file: "desktop/frontend/src/lib/hostRecoverySteer.ts",
    patterns: ["export function isHostRecoveryGuidance(text: string): boolean {"],
  },

  // ── 历史提示词构造判据（tl1602-r2 面板收录判据）──
  {
    channel: "历史面板收录判据（promptHistoryIncludesUserInput，tl1602-r2）",
    file: "desktop/prompt_history_app.go",
    patterns: [
      "func promptHistoryIncludesUserInput(msg provider.Message) bool {",
    ],
  },
];

// ── R2：组合扫描白名单 ─────────────────────────────────────────────────
// 「写入方法调用 + 窗口内 user 形态标志」组合命中的文件必须全部在此登记。
// 文件粒度折衷：同文件新增第二个写入点抓不到（见文件头漏报边界）。
const WRITER_METHOD_RE = /\.Add\(provider\.Message\{|\.AddBatch\(|AppendTurnContextAndUser\(/;
// window flag：写入调用行起的 8 行窗口内出现任一即为 user 形态注入组合。
// lookbehind 排除 IsHostGeneratedUserMessage 判据调用。
const USER_FLAG_RE = /provider\.RoleUser|Role: "user"|(?<!Is)HostGeneratedUserMessage\(|(?<!\w)persistedUserTurn\(/;
const USER_INJECTION_FILES = new Set([
  "internal/agent/run_loop.go", // 正常输入 + steer 注入 + host 引导群（最大合法聚集地）
  "internal/agent/coordinator.go", // persistExecutorNoOp 原始输入落盘
  "internal/agent/pinned_context_revision.go", // pinned revision 批量落盘
  "internal/agent/session_context.go", // AppendTurnContextAndUser / snapshot 构造
  "internal/agent/agent.go", // unapplied steer 归档（RoleTool 承载 steer 文本，窗口同构）
  "internal/agent/reasoning_replay.go", // 推理重放恢复记录
  "internal/control/turn_orchestrator.go", // slash-skill 宿主回合 + 取消恢复 fallback 构造
  "internal/control/controller.go", // 取消恢复重写（replaceSessionAfterCancel）
  "internal/control/mcp_apps.go", // MCP app 结果回灌（RoleTool LocalOnly）
  "internal/agent/final_readiness_recovery.go", // final readiness checkpoint（RoleTool LocalOnly）
  "internal/agent/read_pause.go", // 读暂停 LocalOnly 标记
  "internal/agent/tool_recovery_actions.go", // 工具恢复 LocalOnly 对
  "internal/session/v4import_migrate.go", // v4 会话导入迁移
]);

// ── R3：HostGeneratedUserMessage 调用文件白名单（写侧+投影，排除判据）──
const HOST_FACTORY_FILES = new Set([
  "internal/agent/message_origin.go", // 定义
  "internal/agent/collab_reply_nudge.go",
  "internal/agent/compact.go",
  "internal/agent/compact_fold_input.go",
  "internal/agent/compact_slim.go",
  "internal/agent/feedback_nudge.go",
  "internal/agent/finalization.go",
  "internal/agent/goal_run_boundary.go",
  "internal/agent/incomplete_read_runtime.go",
  "internal/agent/reasoning_only_final.go",
  "internal/agent/run_loop.go",
  "internal/agent/session_context.go",
  "internal/agent/standard_todo_continuation.go",
  "internal/agent/tailview.go",
  "internal/agent/truncate.go",
]);

// ── R4：event.UserInput 事件面白名单 ──────────────────────────────────
const USERINPUT_EVENT_FILES = new Set([
  "internal/control/inbox_run.go", // durable inbox 派发（任务580，ItemID+At）
  "desktop/turn_io_app.go", // 后端直提通报（task734/tl1643）
  "internal/eventwire/wire.go", // wire kind 名映射定义（非发射）
]);

// ── R5：persistedUserTurn 调用面白名单 ─────────────────────────────────
const PERSISTED_TURN_FILES = new Set([
  "internal/control/turn_message_origin.go", // 定义
  "internal/control/turn_orchestrator.go",
]);

// ── R6：字符串 Role: "user" 白名单（非转录通道：辅助探测 / provider wire / 读侧）──
const STRING_ROLE_FILES = new Set([
  "desktop/provider_settings_probe.go", // provider 连通性探测（辅助请求）
  "internal/provider/openai/openai.go", // provider wire 格式化
  "desktop/app_history_tools.go", // HistoryMessage 展示类型读侧转换
]);

// ── R7：前端 reducer user 形态 case 白名单 ─────────────────────────────
const REDUCER_CASE_FILES = new Set([
  "desktop/frontend/src/lib/useController.ts",
]);
const REDUCER_CASE_RE = /case "(user_input|steer)"/;

// ── 执行 ───────────────────────────────────────────────────────────────
const failures = [];
let checkedAnchors = 0;

for (const entry of ENTRIES) {
  let content;
  try {
    content = readRepo(entry.file);
  } catch {
    failures.push(`${entry.channel} :: 文件不存在 ${entry.file}`);
    continue;
  }
  for (const pattern of entry.patterns) {
    checkedAnchors++;
    if (!content.includes(pattern)) {
      failures.push(`${entry.channel} :: 锚丢失 ${entry.file} :: ${JSON.stringify(pattern)}`);
    }
  }
}

const goFilesAll = goFiles();
const tsFilesAll = tsFiles();
let scannedFiles = goFilesAll.length;
let scannedTsFiles = tsFilesAll.length;

for (const rel of tsFilesAll) {
  const content = readRepo(rel);
  if (REDUCER_CASE_RE.test(content) && !REDUCER_CASE_FILES.has(rel)) {
    failures.push(`reducer 分叉 :: ${rel} 含 case "user_input"/"steer" 但未登记（user 形态渲染订阅必须在 useController.ts）`);
  }
}

for (const rel of goFilesAll) {
  const content = readRepo(rel);

  if (content.includes("event.UserInput") && !USERINPUT_EVENT_FILES.has(rel)) {
    failures.push(`事件面旁路 :: ${rel} 引用 event.UserInput 但未登记（合法面仅 inbox_run/turn_io_app/wire 定义）`);
  }
  if (/(?<!\w)persistedUserTurn\(/.test(content) && !PERSISTED_TURN_FILES.has(rel)) {
    failures.push(`旁路构造 :: ${rel} 调用 persistedUserTurn 但未登记`);
  }
  if (/Role: "user"/.test(content) && !STRING_ROLE_FILES.has(rel)) {
    failures.push(`字符串 role 旁路 :: ${rel} 出现 Role: "user"（应走 provider.RoleUser 常量，且需登记）`);
  }
  if (/(?<!Is)HostGeneratedUserMessage\(/.test(content) && !HOST_FACTORY_FILES.has(rel)) {
    failures.push(`host 引导旁路 :: ${rel} 调用 HostGeneratedUserMessage 但未登记（新 host 注入点？）`);
  }

  // 组合检测：写入方法调用行 + 8 行窗口内 user 形态标志。
  const lines = content.split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (!WRITER_METHOD_RE.test(lines[i])) continue;
    const windowEnd = Math.min(i + 8, lines.length);
    const windowText = lines.slice(i, windowEnd).join("\n");
    if (USER_FLAG_RE.test(windowText) && !USER_INJECTION_FILES.has(rel)) {
      failures.push(
        `转录写入旁路 :: ${rel}:${i + 1} 写入调用 + user 形态标志组合命中但未登记` +
        `（行内容: ${lines[i].trim().slice(0, 120)}）`
      );
      break; // 每文件报一次即可
    }
  }
}

// ── 汇总输出 ───────────────────────────────────────────────────────────
if (failures.length > 0) {
  console.error(`check-injection-entrypoints: ${failures.length} 处违规\n`);
  for (const f of failures) console.error(`  ✗ ${f}`);
  console.error(
    `\n新增注入写入点必须走合法入口（tl1650 单入口收口前的白名单制）。` +
    `\n确属新合法通道：更新 scripts/check-injection-entrypoints.mjs 的白名单并在` +
    `\ntl1650 拦点枚举文档登记通道分类（落盘/事件/渲染/提示词判据四面齐全才可登记）。`
  );
  process.exit(1);
}

console.log(
  `check-injection-entrypoints: OK — ${checkedAnchors} 个入口锚全命中，` +
  `${scannedFiles} 个 Go 文件 + ${scannedTsFiles} 个前端文件扫描无旁路（R2 组合/R3 工厂/R4 事件面/R5 工厂调用/R6 字符串 role/R7 reducer）。`
);
