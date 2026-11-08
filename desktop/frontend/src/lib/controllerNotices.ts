import { asArray } from "./array";
import { t, type DictKey } from "./i18n";
import type { HistoryMessage, WireFinalReadiness, WireDecisionReceipt } from "./types";
import type { Item } from "./useController";
import { readPauseItem } from "./readPause";

// Task 658: the concurrent-writer notice used to be plain text pointing at a
// manual route (history panel → select the conversation → View versions). The
// inline button opens the same version dialog directly, so the notice carries
// its own action instead of only describing one.
// tl-1166: the C fallback notice (reasoning_only_final) carries the retry
// action — one click drops a re-answer prompt into the composer (the
// manual_continue pattern; there is no regenerate API, the prompt IS the
// retry).
function inlineActionForCode(code?: string): "view_versions" | "retry_reasoning_only" | undefined {
  if (code === "session_concurrent_writer") return "view_versions";
  if (code === "reasoning_only_final") return "retry_reasoning_only";
  return undefined;
}

// 任务1079：六条高频失败提示的「怎么办」行——按 notice code 从 locale 词典
// （notice.suggestion.*，三语齐全）取文案。后端 Event.Suggestion 为空时兜底；
// 后端显式携带时（未来某个发射点硬指定）以后端为准。code 不在表内 → undefined，
// notice 渲染与改动前逐字一致（不渲染该行）。
const noticeSuggestionKeys: Record<string, DictKey> = {
  // 导航遮罩超时（任务1026 现场例①：「加载超时，重试无效」用户不知能做什么）。
  surface_timeout: "notice.suggestion.surfaceTimeout",
  // final_readiness 未通过（本轮输出可能不完整）。
  final_readiness: "notice.suggestion.finalReadiness",
  // recovery_paused（自动重试已暂停；关闭占用窗口后重试）。
  recovery_paused: "notice.suggestion.recoveryPaused",
  // provider_rejection（供应商侧拒绝：换措辞重发；二次被拒换模型）。
  provider_rejection: "notice.suggestion.providerRejection",
  // 修复会话面板「使用中」/ lease busy（任务1079 现场例②：124MB 会话按钮
  // 置灰无说明——这里是那句「为什么不能点、该怎么办」）。
  session_also_open: "notice.suggestion.leaseBusy",
  // oversized event log 保底跳过（设置→修复会话单独处理）。
  session_compact_fail_streak: "notice.suggestion.oversizedEventLog",
  // 任务1080：首字节等待 3~5s 轻提示——与 30s 档区分，只说「正在等待」。
  header_wait_light: "notice.suggestion.headerWaitLight",
  // 任务1081：续轮耗尽转人工时的「建议下一步」行（试过什么的摘要走 errText）。
  autopilot_resume_exhausted: "notice.suggestion.autopilotResumeExhausted",
};

// noticeSuggestion resolves the localized "怎么办" line for a code, or
// undefined when the code has no suggestion (renders nothing — identical to
// pre-1079 output).
export function noticeSuggestion(code?: string): string | undefined {
  if (!code) return undefined;
  const key = noticeSuggestionKeys[code];
  return key ? t(key) : undefined;
}

export function appendNoticeItem(items: Item[], seq: number, id: string, level: "info" | "warn", rawText: string, detail?: string, code?: string, decisionReceipt?: WireDecisionReceipt, createdAt?: number, backendSuggestion?: string): { items: Item[]; seq: number } {
  if (quietTranscriptNoticeKey(rawText, code)) return { items, seq };
  const trimmedDetail = detail?.trim();
  const text = localizedNoticeText(rawText, code, trimmedDetail);
  if (quietTranscriptNoticeKey(text, code)) return { items, seq };
  const inlineAction = inlineActionForCode(code);
  // 1079：「怎么办」行——后端显式携带优先，否则按 code 从 locale 词典取；
  // 两者皆空 → 不渲染该行（与改动前逐字一致）。
  const suggestion = backendSuggestion?.trim() || noticeSuggestion(code);
  return { items: [...items, { kind: "notice", id, level, text, ...(trimmedDetail ? { detail: trimmedDetail } : {}), ...(code ? { code } : {}), ...(suggestion ? { suggestion } : {}), ...(decisionReceipt ? { decisionReceipt } : {}), ...(inlineAction ? { action: inlineAction } : {}), ...(createdAt ? { createdAt } : {}) }], seq: seq + 1 };
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return String(err || "");
}

// tl1393: the two Go-side running-turn refusal forms the guidance degrade
// chain may take over, pinned here so every surface classifies them the same
// way. Format strings live in exactly four Go sites (a drift breaks this
// predicate AND the desktop-side pin test together — extend both):
//   internal/control/controller.go ErrTurnRunning → "turn already running"
//   desktop/turn_runtime_api.go (validatePromptIdentity, AnswerPromptForTab)
//   desktop/inbox_app.go (EnqueueInboxSteerForTurn, SteerInboxItemForTurn)
//     → `turn %q is not the active turn for tab %q`
export function isTurnAlreadyRunningRejection(err: unknown): boolean {
  return /turn already running/i.test(errorMessage(err));
}

// tl1303/tl1393: the exact-turn fence bounced the turnId the frontend held
// (turn ended or rolled over between the snapshot and the fence). This is the
// "no injectable live turn" signal — the replacement turn must not be steered
// through the session-level channel; the text belongs in the durable
// follow-up queue (the turn-boundary pump dispatches it when the run exits).
export function isNotActiveTurnRejection(err: unknown): boolean {
  return /is not the active turn/i.test(errorMessage(err));
}

const noticeCodeKeys: Record<string, DictKey> = {
  final_readiness: "notice.finalReadiness",
  search_sources_not_provided: "sources.notProvided",
  empty_final: "notice.emptyFinal",
  executor_handoff: "notice.executorHandoff",
  tool_budget: "notice.toolBudget",
  prompt_queued: "notice.promptQueued",
  loop_guard: "notice.loopGuard",
  workspace_lease: "notice.workspaceLease",
  cancelled_turn_display: "notice.cancelledTurnDisplay",
  session_recovery_forked: "recovery.noticeSavedCopy",
  session_recovery_adopted: "recovery.noticeAdopted",
  session_recovery_adopted_covered: "recovery.noticeAdoptedCovered",
  session_recovery_depth_cap: "recovery.noticeKeptCurrent",
  session_shutdown_recovery_forked: "recovery.noticeSavedCopy",
  session_concurrent_writer: "recovery.noticeConcurrentWriter",
  // Task 203: an in-process second tab on the same conversation — the real
  // dual writer — gets its own phrasing instead of blaming another window.
  session_concurrent_dual_tab: "recovery.noticeConcurrentDualTab",
  // tl1446-2: diverged 终局（tl1409 引入的 code）——重试耗尽后发送暂停。
  // 此前无码映射，英文长句直出 warn 条；文案与 recovery.divergedToast 家族
  // 同源补齐 notice 半边（说清冲突/影响/下一步，历史不丢）。
  session_recovery_suspended: "recovery.noticeRecoverySuspended",
  session_head_switched: "recovery.noticeHeadSwitched",
  session_head_selected: "recovery.noticeHeadSelected",
  // 任务 1116 件 1：修复回填了检查点但 diet 没跑、投影仍超窗——面板告警。
  session_repair_still_over_window: "recovery.noticeRepairStillOverWindow",
  session_also_open: "recovery.noticeAlsoOpen",
  decision_receipt: "notice.decisionReceiptTitle",
  context_editing_fallback: "notice.contextEditingFallback",
  turn_stalled: "notice.turnStalled",
  // 任务1147：分块压缩运行期间的静默告警走中性文案——用户实测压缩全程被
  // 「卡住了请点击停止」诱导按失败处置；压缩中静默是等待单次摘要请求，
  // 不是卡死。
  turn_stalled_compacting: "notice.turnStalledCompacting",
  // Task 325: autopilot is yolo-only, so both gate outcomes get their own copy
  // instead of falling through to the backend's bilingual fallback string.
  autopilot_requires_yolo: "notice.autopilotRequiresYolo",
  autopilot_closed_off_yolo: "notice.autopilotClosedOffYolo",
  // 任务 465 两维矩阵：档位切换自动满足 yolo 前置的决策留痕（用户可见半）。 
  autopilot_assumed_yolo: "notice.autopilotAssumedYolo",
  // 任务 731 异常态自动续轮：一次续轮注入留痕 + 连续预算用尽的停止告警。
  autopilot_resume_fired: "notice.autopilotResumeFired",
  autopilot_resume_exhausted: "notice.autopilotResumeExhausted",
  // 任务 868 无人值守恢复门自动继续：恢复点的自动决策留痕（tl1357 起有
  // 人会话同样触发，见下方 restart_gate_auto_continue）。
  unattended_gate_auto_continue: "notice.unattendedGateAutoContinue",
  // tl1357 形态③：重启恢复门自动继续（有人/无人值守一体）——重启前的
  // 排队消息自动重放，不再等「继续执行」点击；用户暂停/dial=off/纯检视架/
  // 中断工具四道保留闸不触发本码。
  restart_gate_auto_continue: "notice.restartGateAutoContinue",
  // 任务999：单条 >4MiB 软提醒——已入队、无需行动，只作提示（不打断、不注入模型）。
  inbox_large_item: "notice.inboxLargeItem",
  // 任务1094缺陷B：本会话 composer 引导降级为排队 follow-up——语义与跨会话
  // 路径的 steer_degraded notice 相同（消息将在下一轮发送，不会丢失），仅是
  // same-session 适配文案（无 messageId / get_message_status）。
  steer_degraded: "notice.steerDegraded",
  // tl-1166: reasoning-only finals — B 的续写提醒（info）与 C 的保底合成正文
  // 告警（warn，带重试按钮）。
  reasoning_only_continuation: "notice.reasoningOnlyContinuation",
  reasoning_only_final: "notice.reasoningOnlyFinal",
};

// tl1459: Goal lifecycle notices. The backend emits one goal_* notice every
// time a running Goal leaves running (plus goal_auto_continue_halted when the
// Goal stays on but its auto-continue stopped). Dual track: the human half is
// localized here from the code and the cause in Detail (cause=<id>); the
// backend's technical line stays visible as the notice detail. useController
// also refreshes the tab's Goal state on any of these codes — that refresh is
// what keeps the Goal control from showing "on" for a Goal the backend already
// stopped (the 2026-11-06 early-stop desync).
const goalNoticeKeys: Record<string, DictKey> = {
  goal_complete: "notice.goalComplete",
  goal_blocked: "notice.goalBlocked",
  goal_paused: "notice.goalPaused",
  goal_stopped: "notice.goalStopped",
  goal_cleared: "notice.goalCleared",
  goal_auto_continue_halted: "notice.goalAutoContinueHalted",
};

const goalCauseKeys: Record<string, DictKey> = {
  report_blocked: "notice.goalCause.reportBlocked",
  evaluator_blocked: "notice.goalCause.evaluatorBlocked",
  budget_spend: "notice.goalCause.budgetSpend",
  evaluator_unavailable: "notice.goalCause.evaluatorUnavailable",
  recovery_paused: "notice.goalCause.recoveryPaused",
  ask_unanswered: "notice.goalCause.askUnanswered",
  manual: "notice.goalCause.manual",
  autopilot_time_limit: "notice.goalCause.autopilotTimeLimit",
  durability_failure: "notice.goalCause.durabilityFailure",
  stop_requested: "notice.goalCause.stopRequested",
  context_cancelled: "notice.goalCause.contextCancelled",
};

// isGoalLifecycleNotice reports whether a notice code announces a Goal state
// change the frontend must re-read.
export function isGoalLifecycleNotice(code?: string): boolean {
  return Boolean(code && Object.prototype.hasOwnProperty.call(goalNoticeKeys, code));
}

// goalCauseLabel localizes a backend cause id; an unknown id stays verbatim so
// a newer backend's cause is never hidden.
export function goalCauseLabel(cause?: string): string {
  const id = (cause ?? "").trim();
  if (!id) return "";
  const key = goalCauseKeys[id];
  return key ? t(key) : id;
}

const streamInterruptReasonCodeKeys: Record<string, DictKey> = {
  stream_interrupted_idle_timeout: "notice.streamInterruptReason.idleTimeout",
  stream_interrupted_premature_eof: "notice.streamInterruptReason.prematureEof",
  stream_interrupted_connection_reset: "notice.streamInterruptReason.connectionReset",
  // 任务929: the gateway itself reset the stream (HTTP/2 RST / SSE error
  // frame) — its own face instead of a flattened "premature eof".
  stream_interrupted_server_error: "notice.streamInterruptReason.serverError",
};

export function localizedNoticeText(text: string, code?: string, detail?: string): string {
  if (code === "unapplied_steer") {
    const separator = text.indexOf("\n");
    const guidance = separator >= 0 ? text.slice(separator + 1) : text;
    return t("notice.unappliedSteer", { guidance });
  }
  // 任务 1010: the head-behind and materialization-lag notices carry their
  // readings as key=value pairs in the event Detail; without a Detail the
  // backend's English text falls through unchanged.
  if (code === "session_selected_head_behind" || code === "session_materialization_lag") {
    const params = detailParams(detail);
    if (Object.keys(params).length > 0) {
      const key: DictKey = code === "session_selected_head_behind" ? "recovery.noticeSelectedHeadBehind" : "recovery.noticeMaterializationLag";
      return t(key, params);
    }
  }
  // 任务 1011: the self-heal notices carry their readings the same way.
  if (code === "session_selfhealed") {
    const params = detailParams(detail);
    if (Object.keys(params).length > 0) {
      return t("recovery.noticeSelfHealed", params);
    }
  }
  if (code === "session_compact_fail_streak") {
    const params = detailParams(detail);
    if (Object.keys(params).length > 0) {
      return t("recovery.noticeCompactFailStreak", params);
    }
  }
  const streamReasonKey = code ? streamInterruptReasonCodeKeys[code] : undefined;
  if (streamReasonKey) {
    return t("notice.streamInterruptReason", { reason: t(streamReasonKey) });
  }
  const goalKey = code ? goalNoticeKeys[code] : undefined;
  if (goalKey) {
    return t(goalKey, { cause: goalCauseLabel(detailParams(detail).cause) });
  }
  const key = code ? noticeCodeKeys[code] : undefined;
  return key ? t(key) : localizedBackendNoticeText(text);
}

// detailParams parses the key=value reading list the backend puts in a
// notice's Detail field (任务 1010). Each key is also registered in camelCase
// so both placeholder styles interpolate: the 1010 copy reads
// {selectedMessages} while the backend Detail says selected_messages — the
// un-bridged mismatch left the raw {placeholder} on screen (the pinned
// heads-notices test caught it during 1011).
function detailParams(detail?: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of (detail ?? "").matchAll(/([a-zA-Z_]+)=(\S+)/g)) {
    const key = m[1];
    out[key] = m[2];
    const camel = key.replace(/_([a-z])/g, (_, c: string) => c.toUpperCase());
    if (camel !== key) out[camel] = m[2];
  }
  return out;
}

const deliveryRequirementKeys: Record<string, DictKey> = {
  project_check: "notice.deliveryRequirementProjectCheck",
  todo: "notice.deliveryRequirementTodo",
  criteria: "notice.deliveryRequirementCriteria",
  verification: "notice.deliveryRequirementVerification",
  review: "notice.deliveryRequirementReview",
  signoff: "notice.deliveryRequirementSignoff",
  action: "notice.deliveryRequirementAction",
  mutation: "notice.deliveryRequirementMutation",
  task: "notice.deliveryRequirementTask",
  capability: "notice.deliveryRequirementCapability",
};

export function readinessMissingIds(readiness: WireFinalReadiness | undefined): string[] {
  return asArray(readiness?.missing).map((id) => String(id));
}

export function deliveryReadinessDetail(readiness: WireFinalReadiness | undefined, fallback = ""): string {
  const labels = asArray(readiness?.missing)
    .map((id) => deliveryRequirementKeys[id])
    .filter((key): key is DictKey => Boolean(key))
    .map((key) => t(key));
  return labels.length === 0
    ? fallback
    : t("notice.deliveryIncompleteMissing", { items: labels.join(t("notice.deliveryRequirementSeparator")) });
}

function localizedSessionAction(action: string): string {
  switch (action.trim()) {
    case "changing model": return t("status.actionChangingModel");
    case "changing effort": return t("status.actionChangingEffort");
    case "rebuilding settings": return t("status.actionRebuildingSettings");
    case "switching sessions": return t("status.actionSwitchingSessions");
    case "switching tabs": return t("status.actionSwitchingTabs");
    case "autosave": return t("status.actionAutosave");
    default: return action.trim() || t("status.actionCurrentSession");
  }
}

function backendNoticeKey(msg: string): DictKey | "" {
  switch (msg) {
    case "Task status needs one more check; asking the assistant to finish or explain what is blocking it.": return "notice.finalReadiness";
    case "No visible answer was produced; asking the assistant to respond again.": return "notice.emptyFinal";
    case "The assistant answered before taking action; asking it to use the required tools.": return "notice.executorHandoff";
    case "Tool round limit reached; asking the assistant to summarize progress.": return "notice.toolBudget";
    case "The assistant is stuck retrying a blocked action; asking it to change approach.": return "notice.loopGuard";
    case "Context is getting large; preserving cache until cleanup is needed.": return "notice.contextLarge";
    case "Context cleanup skipped for now.": return "notice.contextCleanupSkipped";
    case "Automatic context cleanup paused because the context window is too small.": return "notice.contextCleanupPaused";
    case "Context was compacted without a generated summary.": return "notice.compactionNoSummary";
    case "Goal is not ready to complete yet; continuing the remaining work.": return "notice.goalNotReady";
    case "Goal still has unfinished task state; continuing the remaining work.": return "notice.goalUnfinished";
    case "Job artifact migration failed.": return "notice.jobArtifactMigrationFailed";
    case "Background job teardown timed out.": return "notice.jobTeardownTimeout";
    case "Some plan-mode tool settings were ignored.": return "notice.planModeToolSettingsIgnored";
    case "Some plan-mode command settings were ignored.": return "notice.planModeCommandSettingsIgnored";
    case "Config migration did not complete.": return "notice.configMigrationIncomplete";
    case "Selected model is missing its API key.": return "notice.modelMissingApiKey";
    case "An MCP server failed to start.": return "notice.mcpServerFailed";
    case "Some MCP servers failed to start; run /mcp for details.": return "notice.mcpServersFailed";
    case "Guardian was disabled because its model was not found.": return "notice.guardianModelMissing";
    case "Guardian was disabled because it could not start.": return "notice.guardianStartFailed";
    default: return "";
  }
}

export function localizedBackendNoticeText(text: string): string {
  const msg = text.trim();
  // tl1317 P3: the backend replaces Go-internal error texts with this filler
  // before a notice is emitted; localize it instead of interpolating English
  // filler into the autosave template.
  const internalFiller = "internal serialization error (details in logs)";
  const autosave = /^Session autosave failed: (.+)$/s.exec(msg);
  if (autosave) {
    if (autosave[1] === internalFiller) return t("notice.snapshotInternalError");
    return t("status.sessionAutosaveFailed", { err: autosave[1] });
  }
  const saveBefore = /^Session save failed before (.+?): (.+)$/s.exec(msg);
  if (saveBefore) {
    if (saveBefore[2] === internalFiller) return t("status.sessionSaveFailedBefore", { action: localizedSessionAction(saveBefore[1]), err: t("notice.snapshotInternalError") });
    return t("status.sessionSaveFailedBefore", { action: localizedSessionAction(saveBefore[1]), err: saveBefore[2] });
  }
  const modelFallback = /^model (.+) is no longer available; switched to (.+)$/s.exec(msg);
  if (modelFallback) return t("status.modelFallbackSwitched", { model: modelFallback[1], fallback: modelFallback[2] });
  const backgroundJob = /^background (.+) failed: needs attention$/s.exec(msg);
  if (backgroundJob) return t("notice.backgroundJobFailed", { kind: backgroundJob[1] });
  const canonical = backendNoticeKey(msg);
  if (canonical) return t(canonical);
  if (/^session changed on disk; unsaved local transcript was saved as a conflict copy$/i.test(msg) || /^session changed on disk; unsaved local transcript was saved as recovery branch\b/i.test(msg)) return t("recovery.noticeSavedCopy");
  if (/^repeated save conflicts were detected; saved the current conflict copy in place$/i.test(msg) || /^repeated save conflicts were detected; saved the current conflict copy in an isolated recovery branch$/i.test(msg) || /^session conflicts kept recurring; kept the transcript on the current recovery branch$/i.test(msg)) return t("recovery.noticeKeptCurrent");
  if (/^session changed on disk; adopted the newer transcript \(local changes already covered\)$/i.test(msg)) return t("recovery.noticeAdoptedCovered");
  if (/^session changed on disk; adopted the newer transcript$/i.test(msg)) return t("recovery.noticeAdopted");
  return msg;
}

function recoveryNoticeDedupeKey(text: string, code?: string): string {
  switch (code) {
    case "session_recovery_forked":
    case "session_shutdown_recovery_forked": return "recovery:saved-copy";
    case "session_recovery_depth_cap": return "recovery:kept-current";
    case "session_recovery_adopted_covered": return "recovery:adopted-covered";
    case "session_recovery_adopted": return "recovery:adopted";
  }
  const msg = text.trim();
  if (/^session changed on disk; unsaved local transcript was saved as a conflict copy$/i.test(msg) || /^session changed on disk; unsaved local transcript was saved as recovery branch\b/i.test(msg) || msg === t("recovery.noticeSavedCopy")) return "recovery:saved-copy";
  if (/^repeated save conflicts were detected; saved the current conflict copy in place$/i.test(msg) || /^repeated save conflicts were detected; saved the current conflict copy in an isolated recovery branch$/i.test(msg) || /^session conflicts kept recurring; kept the transcript on the current recovery branch$/i.test(msg) || msg === t("recovery.noticeKeptCurrent")) return "recovery:kept-current";
  if (/^session changed on disk; adopted the newer transcript \(local changes already covered\)$/i.test(msg) || msg === t("recovery.noticeAdoptedCovered")) return "recovery:adopted-covered";
  if (/^session changed on disk; adopted the newer transcript$/i.test(msg) || msg === t("recovery.noticeAdopted")) return "recovery:adopted";
  return "";
}

export function quietTranscriptNoticeKey(text: string, code?: string): string {
  const recovery = recoveryNoticeDedupeKey(text, code);
  if (recovery) return recovery;
  const msg = text.trim();
  if (/^guardian enabled · model=.+$/i.test(msg)) return "startup:guardian-enabled";
  if (/^\d+ MCP server\(s\) failed to start: .+ \u2014 run \/mcp for details$/i.test(msg)) return "startup:mcp-failures";
  const directMCPFailure = /^mcp\s+([A-Za-z0-9._-]+):\s+.+$/i.exec(msg);
  if (directMCPFailure && !["add", "auth", "config", "connect", "import", "mode", "remove"].includes(directMCPFailure[1].toLowerCase())) return "startup:mcp-failure";
  if (/^plugin ".+" has been slow \d+ startups in a row \(last \d+ms, budget \d+ms\); demoting to background startup this session$/i.test(msg)) return "startup:plugin-demote";
  if (/^.+ applied: session refreshed after the lease was released$/i.test(msg)) return "settings:deferred-refresh-applied";
  return "";
}
// Windowed history and live read pauses share presentation without growing
// the transcript record store's unrelated paging and cache responsibilities.
export function historyNoticeItems(m: HistoryMessage, id: string): Item[] {
  if (m.code === "incomplete_read") return [readPauseItem(m.readPause, id)];
  if (m.content.trim() === "" && !m.decisionReceipt) return [];
  if (quietTranscriptNoticeKey(m.content, m.code)) return [];
  const text = localizedNoticeText(m.content, m.code, m.detail?.trim());
  if (quietTranscriptNoticeKey(text, m.code)) return [];
  const detail = m.detail?.trim();
  const inlineAction = inlineActionForCode(m.code);
  // 任务998：notice 行补时间——后端 steer 行不带 createdAt（historySteerRows
  // 零值），steer/未应用引导/未带时间的历史行统一打重建时刻，让三档契约
  // 有锚点；带 createdAt 的历史行保留原始时间（跨天/跨年才显示日期）。
  return [{ kind: "notice", id, level: m.level === "warn" ? "warn" : "info", text,
    ...(detail ? { detail } : {}), ...(m.decisionReceipt ? { decisionReceipt: m.decisionReceipt } : {}),
    ...(inlineAction ? { action: inlineAction } : {}), createdAt: m.createdAt ?? Date.now() }];
}
