package event

import "reasonix/internal/nilutil"

// Notice codes are stable machine-readable identifiers for known notices.
// Frontends localize a notice's main copy by Code and fall back to matching
// the English Text (or showing it raw) when Code is empty or unknown, so
// wording edits in Go no longer silently break localization. Values are
// wire-stable: never rename or reuse one once shipped.
const (
	NoticeCodeFinalReadiness        = "final_readiness"
	NoticeCodeEmptyFinal            = "empty_final"
	NoticeCodeProviderRejection     = "provider_rejection"
	NoticeCodeFallbackModelSwitched = "fallback_model_switched"
	NoticeCodeReadinessAdvisory     = "readiness_advisory"
	NoticeCodeExecutorHandoff       = "executor_handoff"
	// NoticeCodeStalledIntent (任务 1069)：模型「宣布下一步就停」触发的续轮
	// 提醒——此前复用 executor_handoff，日志/前端无法单独检索本类事件，现独立
	// 成码（executor-handoff 自身的 run_loop 279/300/682 三处不变）。
	NoticeCodeStalledIntent                    = "stalled_intent"
	NoticeCodeToolBudget                       = "tool_budget"
	NoticeCodePromptQueued                     = "prompt_queued"
	NoticeCodeLoopGuard                        = "loop_guard"
	NoticeCodeProgressGuard                    = "progress_guard"
	NoticeCodeOperationNeedsUser               = "operation_needs_user"
	NoticeCodeEvidenceNudge                    = "evidence_nudge"
	NoticeCodeReasoningGovernor                = "reasoning_governor"
	NoticeCodeWorkspaceLease                   = "workspace_lease"
	NoticeCodeBackgroundJobFinished            = "background_job_finished"
	NoticeCodeCancelledTurn                    = "cancelled_turn_display"
	NoticeCodeProviderRequestFailed            = "provider_request_failed"
	NoticeCodeStreamInterruptedIdleTimeout     = "stream_interrupted_idle_timeout"
	NoticeCodeStreamInterruptedPrematureEOF    = "stream_interrupted_premature_eof"
	NoticeCodeStreamInterruptedConnectionReset = "stream_interrupted_connection_reset"
	// NoticeCodeStreamInterruptedServerError (任务929): the provider gateway
	// itself reset the stream (HTTP/2 RST_STREAM / an SSE error frame) — the
	// distinct face that used to flatten into premature_eof.
	NoticeCodeStreamInterruptedServerError = "stream_interrupted_server_error"
	// NoticeCodeProviderDeterministicReject (任务929 S2): the breaker proved
	// the gateway is rejecting THIS request deterministically (same reason,
	// near-identical durations, N times) — the loop stopped early instead of
	// replaying another full prefill.
	NoticeCodeProviderDeterministicReject = "provider_deterministic_reject"
	// NoticeCodePrefillSlow (任务929 S3): a first-token watch fired — no
	// provider chunk for prefillNoticeAfter while a very large context is
	// being prefetched. Says "still working" instead of leaving the transcript
	// indistinguishable from a hang.
	NoticeCodePrefillSlow                   = "prefill_slow"
	NoticeCodeUnappliedSteer                = "unapplied_steer"
	NoticeCodeMCPToolsList                  = "mcp_tools_list"
	NoticeCodeSessionRecoveryForked         = "session_recovery_forked"
	NoticeCodeSessionRecoveryAdopted        = "session_recovery_adopted"
	NoticeCodeSessionRecoveryAdoptedCovered = "session_recovery_adopted_covered"
	NoticeCodeSessionRecoveryDepthCap       = "session_recovery_depth_cap"
	NoticeCodeSessionShutdownRecoveryForked = "session_shutdown_recovery_forked"
	NoticeCodeSessionConcurrentWriter       = "session_concurrent_writer"
	// NoticeCodeSessionConcurrentDualTab (task 203): the competing writer was
	// an in-process second tab on the same conversation — the real dual-writer
	// case, phrased as such instead of blaming another window or process.
	NoticeCodeSessionConcurrentDualTab = "session_concurrent_dual_tab"
	NoticeCodeSessionHeadSwitched      = "session_head_switched"
	NoticeCodeSessionHeadSelected      = "session_head_selected"
	// 任务 1116 件 1: a repair refilled the checkpoint (hydrate=done) but the
	// diet never ran and the projection is still over the model window — the
	// 20261024 新9 shape (312KB → 33.7MB, source 298%) produced silently. The
	// notice tells the user the repair is not finished and names the one-step
	// remedy (run repair again with Diet checked).
	NoticeCodeSessionRepairStillOverWindow = "session_repair_still_over_window"
	// 任务 1010 需求 4: the open landed on a head that another live head
	// strictly dominates (fewer messages AND older activity). The notice
	// carries both chains' readings; switching stays a manual versions action.
	NoticeCodeSessionSelectedHeadBehind = "session_selected_head_behind"
	// 任务 1010 需求 3: the events log holds a strictly newer, longer chain
	// than the materialized .jsonl checkpoint. Detection only — the notice
	// points at the manual fix-conversation entry (任务 1011); nothing in the
	// detection path ever writes a session file.
	NoticeCodeSessionMaterializationLag = "session_materialization_lag"
	// 任务 1011: the self-heal ran on this conversation and made it usable
	// again — head re-selected to the longer, newer chain and/or the
	// projection dieted (images stripped, old messages folded). Detail
	// carries action=key=value readings; every moved-aside version stays in
	// the versions panel, so the heal is reversible.
	NoticeCodeSessionSelfHealed = "session_selfhealed"
	// 任务 1011: consecutive compaction passes keep failing at runtime. The
	// notice only reports the streak; the local projection diet runs on the
	// open-session heal or a manual repair, never mid-turn.
	NoticeCodeSessionCompactFailStreak                          = "session_compact_fail_streak"
	NoticeCodeSessionAlsoOpen                                   = "session_also_open"
	NoticeCodeCompletionUncertain                               = "completion_uncertain"
	NoticeCodeIncompleteReadDetected                            = "incomplete_read_detected"
	NoticeCodeReadContinuationRequired                          = "continuation_required"
	NoticeCodeReadCompleted                                     = "read_completed"
	NoticeCodeReadOversizeRejected                              = "oversize_rejected"
	NoticeCodeReadStrategyRequired                              = "read_strategy_required"
	NoticeCodeReadStrategyProgress                              = "read_strategy_progress"
	NoticeCodeReadStrategyResolved                              = "read_strategy_resolved"
	NoticeCodeReadLocalSafetyPaged                              = "read_local_safety_paged"
	NoticeCodeDecisionReceipt, NoticeCodeContextEditingFallback = "decision_receipt", "context_editing_fallback"
	NoticeCodeSessionTakenOver                                  = "session_taken_over"
	NoticeCodeSessionReclaimRequested                           = "session_reclaim_requested"
	NoticeCodeSessionReclaimed                                  = "session_reclaimed"
	NoticeCodeReasoningReplayRepair                             = "reasoning_replay_repair"
	NoticeCodeTurnStalled                                       = "turn_stalled"
	// NoticeCodeCollabReplyNudge (task 530): a turn closed with unanswered
	// require_reply cross-session mail and the host injected one visible
	// reply reminder into the conversation.
	NoticeCodeCollabReplyNudge = "collab_reply_nudge"
	// 任务731 异常态自动续轮（579 家族第三形态）：一次续轮注入的用户可见
	// 留痕，与连续续轮预算用尽后的停止告警。
	NoticeCodeAutopilotResumeFired     = "autopilot_resume_fired"
	NoticeCodeAutopilotResumeExhausted = "autopilot_resume_exhausted"
	// NoticeCodeDelegationReminder (task 850, 816 P1-②): the efficiency tier
	// burned many main-chain steps with zero sub-agent dispatches and the host
	// injected one transient mid-turn delegation reminder.
	NoticeCodeDelegationReminder = "delegation_reminder"
	// NoticeCodeUnattendedGateAutoContinue (task 868): an unattended tab's
	// restart recovery gate was cleared automatically at the restore point so
	// the queued work keeps flowing without the three-choice banner. The
	// user-visible trace of the automatic decision — attended tabs keep the
	// gate and never emit this.
	NoticeCodeUnattendedGateAutoContinue = "unattended_gate_auto_continue"
	// NoticeCodeCollabDeliverySilent (任务894): a cross-session delivery
	// outcome that needs no sender action (injected / normal queued_followup /
	// steer degraded to a queued follow-up) was demoted from an injected host
	// message to this UI-layer notice — the model never reads it; the durable
	// channel stays the 570 delivery receipt (get_message_status by messageId).
	NoticeCodeCollabDeliverySilent = "collab_delivery_silent"
	// NoticeCodeInboxLargeItem (任务999): a queued instruction's encoded body
	// crossed the advisory 4 MiB threshold. Informational only — the item was
	// admitted (the hard ceiling is DefaultMaxItemBytes, 16 MiB since 999) and
	// nothing needs action, so it follows the 任务894 no-action tier: a UI
	// notice, never an injected model message.
	NoticeCodeInboxLargeItem = "inbox_large_item"
	// NoticeCodeSteerDegraded (任务1094缺陷B): a same-session composer steer
	// (EnqueueInboxSteerForTurn / SteerInboxItemForTurn) was rejected by the
	// mid-turn admission gate and degraded to a queued follow-up — the exact
	// semantic the cross-session pump already surfaces as a "steer_degraded"
	// notice (session_collab.go). 任务894 no-action tier: UI notice only, the
	// durable queue keeps the item (it runs as the next turn's input), and the
	// model never sees the notice.
	NoticeCodeSteerDegraded = "steer_degraded"
)

// 任务1079: high-frequency failure notices now carry an actionable next step
// ("怎么办") instead of only naming what happened. The wire field is
// Event.Suggestion; the copy itself lives in the FRONTEND locale dictionaries
// keyed by code (notice.suggestion.* in zh/en/zh-TW, resolved by
// noticeSuggestions.ts) so it follows the three-locale contract and language
// switches apply. Go keeps only the stable codes below plus the Suggestion
// pass-through field — any notice without a suggestion renders exactly as
// before (empty field, no line).
const (
	// NoticeCodeSurfaceTimeout (任务1026 导航遮罩超时)：加载遮罩超时已释放，
	// 用户不知道还能做什么。此前只有前端文案键（navigation.surfaceTimeout），
	// 没有 notice 码；1079 给它一个稳定码以便统一挂「怎么办」。
	NoticeCodeSurfaceTimeout = "surface_timeout"
	// NoticeCodeHeaderWaitLight (任务1080)：首字节等待的 3~5s 轻提示档，
	// 与 30s 的 header_wait_slow（927）区分：轻提示只说「正在等待响应」，
	// 不带「响应较慢」的告警语义。
	NoticeCodeHeaderWaitLight = "header_wait_light"
)

// EmitEvent is the notice 统一 Emit 入口（任务1079-b 保留）：仅做非 nil 保护，
// 行为与直接 s.Emit 等价。历史上这里曾按 code 回填双语 Suggestion 常量——
// 已改为前端按 code 从 locale 词典取（见下方口径说明）。
func EmitEvent(s Sink, e Event) {
	if nilutil.IsNil(s) {
		return
	}
	s.Emit(e)
}

// 任务1079（1079-b，2026-10-23 改口径）：不再在 EmitEvent 里按 code 回填
// Suggestion。旧方案把「怎么办」硬编码成 Go 侧中英双语常量，与本仓库「文案
// 一律走 locale 三语（zh/en/zh-TW）」的规范相悖——同一句话会绕过前端 i18n
// 词典，语言切换不生效，且双语混排在任一语言下都读不干净。
//
// 现口径：前端（desktop/frontend/src/lib/noticeSuggestions.ts）按 code 从
// locale 词典（notice.suggestion.* 键）取「怎么办」，Go 侧只保留 Event.
// Suggestion 字段与 wire 透传（发射方需要硬指定时仍可手填）。EmitEvent 仅
// 作为 notice 统一入口保留（非 nil 保护），行为与直接 s.Emit 等价。
// NoticeCodeSuggestionTexts 同步移除——任何按 code 查文案的逻辑收敛到前端
// 一处，避免两份文案漂移。

// 任务553: wake-yield code lives in its own block so the wire-stable table
// above keeps its alignment history intact.
const (
	// NoticeCodeBackgroundJobWakeYielded: a completion wakeup yielded to
	// in-flight or queued user work; the queued summaries ride the next real
	// turn or the automatic re-kick once the gate reopens.
	NoticeCodeBackgroundJobWakeYielded = "background_job_wake_yielded"
)

// 任务408 异步决策点回访: pending-card lifecycle notices. Enqueue marks the
// in-session "决策点已挂起待你决定" flag; the count itself rides the runtime
// state snapshot (PendingCards).
const (
	NoticeCodePendingCardEnqueued = "pending_card_enqueued"
)

// 任务912 P1-d: a rejected credential is the one provider failure the user —
// not the agent — has to fix, so it gets its own code for a front-end banner
// instead of a log line nobody reads.
const (
	NoticeCodeCredentialExpired = "credential_expired"
)

// 任务912 P1-c: compaction failed N times in a row, so the turn's context was
// clipped to a sliding window instead of going out full. A warn notice, not a
// log line: the model just lost history and the user should be able to see why.
const (
	NoticeCodeSlidingWindowCap = "sliding_window_cap"
)

// 任务927: the response-header wait is an EARLIER silence phase than 929 S3's
// prefill watch — the request is out but the gateway has not sent response
// headers at all (the 2026-10-16 anthropic-endpoint incident sat 300s here
// with zero events, then burned the same wait up to four times). The two
// watch phases are structurally exclusive: the prefill watch only arms once
// headers arrive, so header_wait_slow and prefill_slow can never both name
// the same stretch of silence.
const (
	// NoticeCodeHeaderWaitSlow: still awaiting response headers past the
	// threshold; repeats carry the elapsed wait in Detail so a multi-minute
	// wait reads as progress, not a frozen counter.
	NoticeCodeHeaderWaitSlow = "header_wait_slow"
	// NoticeCodeHeaderTimeoutRetry: one response-header timeout consumed a
	// retry; before the next full replay, tell the user this loop re-pays the
	// whole prefill each round and what to do instead.
	NoticeCodeHeaderTimeoutRetry = "header_timeout_retry"
	// NoticeCodeHeaderTimeoutGiveup: consecutive response-header timeouts on
	// the same request are deterministic (the timeout is OUR client-side
	// timer, so durations are near-identical by construction) — stop the
	// loop early instead of burning every remaining attempt.
	NoticeCodeHeaderTimeoutGiveup = "header_timeout_giveup"
)
