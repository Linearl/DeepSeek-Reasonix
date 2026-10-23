package agent

import (
	"fmt"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// 任务927 knobs for the response-header wait watch. Package vars so tests can
// shrink them (the recovery suite's established pattern).
var (
	// headerWaitNoticeAfter: how long the request may wait for response
	// headers before the first user-visible notice. Below 929 S3's prefill
	// threshold on purpose: this phase is "request sent, gateway silent" —
	// naming it earlier is what turns the incident's blank five minutes into
	// an explained wait.
	headerWaitNoticeAfter = 30 * time.Second
	// headerWaitNoticeRepeat: while the header wait continues, re-announce
	// with the elapsed seconds so a multi-minute stall reads as progress
	// instead of a frozen counter.
	headerWaitNoticeRepeat = 60 * time.Second
	// 任务1080 轻提示档：首字节等待的 3~5s 提示（建议 4s）。此前只有 30s 一档，
	// 前 30 秒界面完全静止、体感「没反应」。轻提示只发一次（与 prefill 的
	// once-per-turn 同款去噪），文案与 30s 档区分：只说「正在等待模型响应」，
	// 不带「响应较慢」的告警语义。包变量便于测试缩时。
	headerWaitLightNoticeAfter = 4 * time.Second
)

// 任务1080: one light "waiting for the model" line at the 3~5s mark. The
// threshold deliberately sits far below 927's 30s watch so the two notices are
// two TIERS of the same phase, not duplicates: this one names an ordinary
// wait (no complaint tone), the 30s one names a slow one. Once per turn, like
// 929 S3's prefill notice — the run-loop state machine below (rather than a
// per-call flag) keeps it at exactly one emission.
func (a *Agent) emitHeaderWaitLightNotice(sink event.Sink, elapsed time.Duration) {
	if sink == nil {
		sink = a.svc.sink
	}
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   event.NoticeCodeHeaderWaitLight,
		Text:   i18n.M.HeaderWaitLightNotice,
		Detail: fmt.Sprintf("awaiting response headers (light tier), elapsed %s", elapsed.Round(time.Second)),
	})
}

// emitHeaderWaitSlowNotice names the response-header wait (task 927 phase 1):
// the request is out but the gateway has not sent response headers yet. This
// is a DIFFERENT phase from 929 S3's prefill_slow (phase 2, body silence after
// headers arrive) — the two watches are structurally exclusive, so the two
// notices can never claim the same stretch of silence. Repeats are intended
// (every headerWaitNoticeRepeat) with the elapsed wait in the copy, so a
// five-minute gateway stall shows movement rather than one line going stale.
// Emission stays on the run loop (the streamWithFrozen select), never on a
// goroutine: Sink.Emit is documented as serial-call-only.
func (a *Agent) emitHeaderWaitSlowNotice(sink event.Sink, elapsed time.Duration) {
	if sink == nil {
		sink = a.svc.sink
	}
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   event.NoticeCodeHeaderWaitSlow,
		Text:   fmt.Sprintf(i18n.M.HeaderWaitSlowNoticeFmt, a.ContextUsedTokens(), int(elapsed.Seconds())),
		Detail: fmt.Sprintf("awaiting response headers, elapsed %s", elapsed.Round(time.Second)),
	})
}
