package main

import (
	"log/slog"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// tl1296 增强①：引导消息（durable inbox）发送面的 write authority stale
// 自愈。tl1342 覆盖了 UI 主发送面（Submit* 家族），tl1311 覆盖了 collab
// 泵与 idle-turn 桥，tl1201 覆盖了保存路径——唯独面板上的引导行（重试按钮
// RetryInboxItem / 恢复派发 SetInboxPaused(false)）在闸门拒绝后没有修复：
// admitGuardedTurn 拒发 turnDroppedWriteAuthority，TrySubmitInboxItem 把该
// 结果折成 DispositionRejectedBusy+nil error，dispatcher 按 idle 收场——条
// 目永远留在队列里，流上只有一条「no longer writable — close and reopen」，
// 应用崩溃后的恢复循环里用户点重试也只反复撞同一堵墙（tl1296 根因现场）。
//
// 修复面复用 1311/1342 的同一修复核心 repairTabWriteAuthority：lease 可重取
// （活 lease 重发一代，或空闲锁接管）才自动 re-mint；不可重取（活持有者在
// 别处）保持原行为——拒绝 notice 已上屏，条目留队，手动 reopen 语义零回退
// （tl1296 负钉）。

// noticeInboxAuthorityAutoRetried is the stream correction for a healed inbox
// retry: the refusal notice the refused dispatch emitted ("close and reopen")
// is stale advice the moment the re-drive succeeds — the same correction shape
// the tl1342 submit surface already uses. tl1296: the code maps to the
// localized crash-recovery copy (notice.writeAuthorityRecovered, three
// languages); other frontends keep this English line.
const noticeInboxAuthorityAutoRetried = "the session's write access had gone stale — it was restored automatically and your queued message is being delivered now"

// inboxDispatchRedrive is the neutral kick that re-runs maybeDispatchInbox on
// a healed authority. The probe surfaces (retry/resume) already did their own
// state mutation in the refused attempt (RetryItem → queued, resume →
// un-paused), so the recovery leg must NOT repeat them — a second RetryItem
// on a now-queued item would bounce ErrInvalidState, and a second resume
// could clear a user-held pause. NotifyInboxRuntimeReady is the exported,
// side-effect-free dispatch kick for exactly this shape.
type inboxDispatchRedrive interface {
	NotifyInboxRuntimeReady()
}

// healInboxDispatchAfterStaleAuthority applies the tl1296 inbox heal contract
// to a surface whose dispatch leg just ran:
//
//  1. probe the write-authority verdict the dispatch leg hit — the refusal
//     surfaces only as a transcript notice, never as the surface method's
//     error, so the probe is the only reliable classifier (the same
//     void-contract shape tl1342 probes after Submit*);
//  2. a healthy verdict returns false with zero side effects — the probe is
//     the only code path touched on the happy path;
//  3. stale/missing → heal through the shared repair core (lease 可重取判据
//     在 repairTabWriteAuthority 内：活 lease 重发一代，空闲锁才接管) and
//     re-drive the queue head once on the fresh authority;
//  4. a declined heal (live holder elsewhere, tab gone) returns false and
//     keeps the pre-tl1296 behavior: the item stays queued behind the refusal
//     notice and manual reopen remains the remedy.
//
// The report lets the caller decide whether to emit the stream correction:
// only surfaces that can verify delivery (an explicit item id) should speak.
func (a *App) healInboxDispatchAfterStaleAuthority(tabID string, ctrl control.SessionAPI) bool {
	if !submissionRefusedByStaleWriteAuthority(ctrl) {
		return false
	}
	if !a.healCollabTargetWriteAuthority(tabID) {
		slog.Warn("desktop: inbox dispatch refused at the write-authority gate and the heal declined",
			"tab", tabID, "reason", "inbox_write_authority_heal_declined")
		return false
	}
	if redrive, ok := ctrl.(inboxDispatchRedrive); ok {
		redrive.NotifyInboxRuntimeReady()
	}
	slog.Info("desktop: stale write authority healed, inbox queue re-driven on a fresh authority",
		"tab", tabID)
	return true
}

// inboxItemState reads one item's durable state from the tab's inbox snapshot;
// ok=false when the id is unknown (already consumed and pruned counts as
// delivered for the caller's purposes).
func inboxItemState(ctrl control.SessionAPI, id string) (sessioninbox.InboxState, bool) {
	for _, it := range ctrl.InboxSnapshot().Items {
		if it.ID == id {
			return it.State, true
		}
	}
	return "", false
}

// emitInboxAuthorityHealedNotice posts the post-heal correction on the tab's
// stream, mirroring the tl1342 "restored automatically" notice. Delivery is
// verified first: the retried item must have left the retryable states.
func (a *App) emitInboxAuthorityHealedNotice(tabID, id string, ctrl control.SessionAPI) {
	state, ok := inboxItemState(ctrl, id)
	if ok && (state == sessioninbox.StateQueued || state == sessioninbox.StateUncertain || state == sessioninbox.StateBlocked) {
		return // still stranded — the refusal notice stays the last word
	}
	if tab := a.tabByID(tabID); tab != nil && tab.sink != nil {
		tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Code: noticeCodeWriteAuthorityRecovered, Text: noticeInboxAuthorityAutoRetried})
	}
}
