package event

// Notice codes are stable machine-readable identifiers for known notices.
// Frontends localize a notice's main copy by Code and fall back to matching
// the English Text (or showing it raw) when Code is empty or unknown, so
// wording edits in Go no longer silently break localization. Values are
// wire-stable: never rename or reuse one once shipped.
const (
	NoticeCodeFinalReadiness                   = "final_readiness"
	NoticeCodeEmptyFinal                       = "empty_final"
	NoticeCodeProviderRejection                = "provider_rejection"
	NoticeCodeFallbackModelSwitched            = "fallback_model_switched"
	NoticeCodeReadinessAdvisory                = "readiness_advisory"
	NoticeCodeExecutorHandoff                  = "executor_handoff"
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
	NoticeCodeSessionConcurrentDualTab                          = "session_concurrent_dual_tab"
	NoticeCodeSessionHeadSwitched                               = "session_head_switched"
	NoticeCodeSessionHeadSelected                               = "session_head_selected"
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
)

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
