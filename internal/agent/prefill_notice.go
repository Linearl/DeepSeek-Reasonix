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

// emitPrefillSlowNotice names the silence once per turn: the model is chewing
// through a very large context, this is the normal prefill phase, waiting is
// correct. It is per TURN, not per attempt — each retry re-arms the watch, and
// repeating the same sentence for every replay only adds noise. Emission
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
		Text:   fmt.Sprintf(i18n.M.PrefillSlowNoticeFmt, a.ContextUsedTokens()),
		Detail: fmt.Sprintf("no provider output for %s (attempt-level first-token watch)", prefillNoticeAfter),
	})
}
