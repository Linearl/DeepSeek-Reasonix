package main

import (
	"log/slog"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

// tl1342: the desktop UI send surface (submitToTabResult and the WithID
// wrappers) hands input to the controller's void Submit* contracts. When the
// controller's write authority is stale, the control-layer admission gates
// (submitCommandOrTurn / admitGuardedTurn — the tl1311 family) refuse the
// input BEFORE admission and drop it with a warn notice only: the frontend
// sees "Send failed", the banner catches the message, and the user must press
// resend by hand (20261101 20:24 现场: 宿主 restart 后连发 4 条全部 Send
// failed, 全靠兜底横幅人工重发). This file is the UI-send sibling of the
// tl1201 save-path heal and the tl1311 pump heal: detect the refusal right
// after the submit call, repair the tab's write authority through the shared
// repair core, and retry the submission exactly once on a fresh admission.

// staleWriteAuthoritySubmitRetryCooldown is the settle beat between the heal
// and the single retry. The repair itself is synchronous (fresh generation
// from the held lease, or takeover + rebind), so the cooldown only spaces the
// retry away from a concurrent rebinder (startup rebuild, tab switch) landing
// in the same instant. var 以便测试收窄。
var staleWriteAuthoritySubmitRetryCooldown = 300 * time.Millisecond

// noticeWriteAuthorityAutoRetried tells the user why their message needed a
// beat before running — the refusal notice the dropped first attempt already
// emitted ("no longer writable — close and reopen") is stale advice the moment
// the retry succeeds, so it must not be the last word on the stream.
const noticeWriteAuthorityAutoRetried = "the session's write access had gone stale — it was restored automatically and your message is running now"

// submissionRefusedByStaleWriteAuthority reports whether the controller that
// just swallowed a submission did so at its write-authority gate. The gates
// run before turn admission, so a stale/missing verdict immediately after the
// submit call proves the input never became a turn. Controllers without the
// probe (test fakes) report false — their behavior is unchanged.
func submissionRefusedByStaleWriteAuthority(ctrl control.SessionAPI) bool {
	if ctrl == nil {
		return false
	}
	probe, ok := ctrl.(interface{ WriteAuthorityReady() error })
	if !ok {
		return false
	}
	return isSessionWriteAuthorityError(probe.WriteAuthorityReady())
}

// retryTabTurnAfterStaleWriteAuthority is the tl1342 single cooldown retry on
// the desktop submission surfaces. Call it where admission.finish runs, with:
//
//   - admission/ctrl: the first attempt's admission token and admitting
//     controller;
//   - started: admission.finish's return — a started turn owns the gate and is
//     never retried (a stale probe next to a running turn is mid-turn
//     staleness, not a dropped input; healing under a live turn could disturb
//     it);
//   - reclaim/submissionID: the exact beginTabTurn arguments the surface used,
//     so the retry re-runs the full admission (turnStartMu, runtime admission,
//     sink tryBeginTurn — the first finish's started=false already released the
//     sink reservation via cancelTurnStart, which is what makes the retry's
//     tryBeginTurn land);
//   - resubmit: the surface's own submit body against a fresh admission.
//
// Retry contract: exactly one attempt, only after a successful heal. A refused
// heal (live holder elsewhere, tab gone) keeps the pre-tl1342 behavior — the
// dropped submission stays a warn notice the banner recovery already covers.
func (a *App) retryTabTurnAfterStaleWriteAuthority(
	admission *tabTurnAdmission,
	ctrl control.SessionAPI,
	started bool,
	reclaim bool,
	submissionID []string,
	resubmit func(*tabTurnAdmission, control.SessionAPI),
) {
	if admission == nil || admission.released || started || !submissionRefusedByStaleWriteAuthority(ctrl) {
		return
	}
	tab := admission.tab
	if tab == nil {
		return
	}
	tabID := tab.ID
	if !a.healCollabTargetWriteAuthority(tabID) {
		slog.Warn("desktop: submit refused at the write-authority gate and the heal declined",
			"tab", tabID, "reason", "write_authority_heal_declined")
		return
	}
	if staleWriteAuthoritySubmitRetryCooldown > 0 {
		time.Sleep(staleWriteAuthoritySubmitRetryCooldown)
	}
	retryAdmission, retryCtrl, err := a.beginTabTurn(tabID, reclaim, submissionID...)
	if err != nil {
		// Healed but the gate is genuinely busy again (a heartbeat or takeover
		// turn slipped in during the cooldown). The heal itself succeeded; the
		// next real submit flows normally, and this input stays on the banner
		// recovery path like any other refused submit.
		slog.Warn("desktop: write authority healed but the retry admission failed",
			"tab", tabID, "err", err)
		return
	}
	defer retryAdmission.abort()
	resubmit(retryAdmission, retryCtrl)
	retryStarted := retryAdmission.finish(retryCtrl)
	if retryStarted {
		slog.Info("desktop: stale write authority healed, submission retried on a fresh admission",
			"tab", tabID)
		if retryTab := retryAdmission.tab; retryTab != nil && retryTab.sink != nil {
			retryTab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
				Text: noticeWriteAuthorityAutoRetried})
		}
	}
}
