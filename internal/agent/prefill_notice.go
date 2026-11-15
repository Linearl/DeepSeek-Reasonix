package agent

import (
	"fmt"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// prefillNoticeAfter (任务929 S3): how long an attempt may run with ZERO
// provider chunks before the user is told what the silence is. The 2026-10-16
// incident spent 18.5 minutes this way — four attempts all killed during
// prefill with nothing on screen, which is indistinguishable from a hang. The
// threshold sits below the CLI's 90s expectation hint (task 370) so the
// transcript notice and the working-line hint roughly agree; a package var so
// tests can shrink it.
var prefillNoticeAfter = 60 * time.Second

// emitPrefillSlowNotice names the silence once per turn. The attribution is
// tiered (tl1608): at or above the compact trigger the context size makes
// "normal prefill phase" the honest reading, but below it the same silence
// may just as well be a slow upstream or a connection problem, so the notice
// stays neutral — the 60s watch itself (task 929 S3, anti-fake-hang) is
// unchanged. It is per TURN, not per attempt — each retry re-arms the watch,
// and repeating the same sentence for every replay only adds noise. Emission
// stays on the run loop (the streamWithFrozen select), never on a goroutine:
// Sink.Emit is documented as serial-call-only.
func (a *Agent) emitPrefillSlowNotice(sink event.Sink) {
	if a.turn.prefillNoticeShown {
		return
	}
	a.turn.prefillNoticeShown = true
	if sink == nil {
		sink = a.svc.sink
	}
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   event.NoticeCodePrefillSlow,
		Text:   a.prefillSlowNoticeText(),
		Detail: fmt.Sprintf("no provider output for %s (attempt-level first-token watch)", prefillNoticeAfter),
	})
}

// prefillSlowNoticeText picks the wording tier for the silent wait. The
// token count in the original notice was never the firing criterion — the
// criterion is pure first-token silence — so it only backs the strong
// attribution when it actually supports it: used >= compactTrigger(). Below
// that (or with no configured window, trigger <= 0) the neutral wording
// names both candidate causes. The seconds figure is interpolated from the
// (test-shrinkable) threshold so the text cannot drift from the watch.
func (a *Agent) prefillSlowNoticeText() string {
	used := a.ContextUsedTokens()
	if trigger := a.compactTrigger(); trigger > 0 && used >= trigger {
		return fmt.Sprintf(i18n.M.PrefillSlowNoticeFmt, used)
	}
	return fmt.Sprintf(i18n.M.PrefillSlowNoticeNeutralFmt, int(prefillNoticeAfter.Seconds()))
}
