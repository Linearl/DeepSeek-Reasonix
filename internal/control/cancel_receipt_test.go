package control

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
)

// tl1304 ③ 后端诚实化验收：cancel==nil 的四处静默 no-op（cancelTurnLocked 主路径 +
// armStopEscalation/openStopGrace/fireStopForce 三处升级护体早退）必须
// ① 给出可区分的 coded 回执（idle=从未有过轮活动 / finished=轮已结束、context
// 已在收尾时拆除）；② 刷新 runtime 镜像（stale StopLevel 投影被纠正）；
// ③ 不伪造 TurnCancelling 事件——对不存在的轮发假轮况是另一种不诚实。

// cancelReceiptSink records turn-status events so the no-op receipt paths can
// be asserted silent on fakes and loud (in logs) on truth.
type cancelReceiptSink struct {
	mu      sync.Mutex
	events  []event.Event
	cancels int
}

func (s *cancelReceiptSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	if e.Kind == event.TurnStatusChanged && e.Status == event.TurnCancelling {
		s.cancels++
	}
}

func (s *cancelReceiptSink) cancellingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancels
}

func cancelReceiptController(t *testing.T, sink event.Sink) *Controller {
	t.Helper()
	c := New(Options{Sink: sink, SessionDir: t.TempDir(), SessionPath: filepath.Join(t.TempDir(), "session.jsonl")})
	t.Cleanup(c.Close)
	return c
}

// A quick real turn (body returns immediately) so the controller transitions
// to the post-turn shape: cancel context torn down, ledger holding activity.
func runOneQuickTurn(t *testing.T, c *Controller) {
	t.Helper()
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		return nil
	})
	<-started
	deadline := time.Now().Add(2 * time.Second)
	for c.RuntimeStatus().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.RuntimeStatus().Running {
		t.Fatal("quick turn did not finish in time")
	}
}

// ① fresh controller (never admitted a turn): the stop finds no context and
// no ledger activity — the receipt must say idle, and no fake cancelling
// event may leave the sink.
func TestCancelNoTurnReceiptIdle(t *testing.T) {
	sink := &cancelReceiptSink{}
	c := cancelReceiptController(t, sink)

	turnID, cancelled, outcome := c.cancelTurnLocked()
	if cancelled || turnID != "" {
		t.Fatalf("idle cancel = (%q, %v), want (\"\", false)", turnID, cancelled)
	}
	if outcome != cancelTurnIdle {
		t.Fatalf("idle receipt outcome = %v, want idle", outcome)
	}
	if got := sink.cancellingCount(); got != 0 {
		t.Fatalf("idle cancel emitted %d fake TurnCancelling events, want 0", got)
	}
	if st := c.RuntimeStatus(); st.Running || st.CancelRequested {
		t.Fatalf("idle cancel must not mutate the gate, got %+v", st)
	}
}

// ② the wedge-adjacent shape from the 诊断报告: the turn ran and finished, the
// context was torn down at finish, RuntimeStatus may still echo busy — the
// stop must say finished (not live, not idle) and emit no fake cancelling.
func TestCancelNoTurnReceiptFinished(t *testing.T) {
	sink := &cancelReceiptSink{}
	c := cancelReceiptController(t, sink)
	runOneQuickTurn(t, c)

	turnID, cancelled, outcome := c.cancelTurnLocked()
	if cancelled || turnID != "" {
		t.Fatalf("finished cancel = (%q, %v), want (\"\", false)", turnID, cancelled)
	}
	if outcome != cancelTurnFinished {
		t.Fatalf("finished receipt outcome = %v, want finished", outcome)
	}
	if got := sink.cancellingCount(); got != 0 {
		t.Fatalf("finished cancel emitted %d fake TurnCancelling events, want 0", got)
	}
}

// ③ end-to-end through the public CancelStop entry: pressing stop against a
// finished turn behaves like the ordinary no-op cancel (level stays None —
// the escalation state died with the turn) and stays silent on fake events.
func TestCancelStopOnFinishedTurnStaysLevelNoneAndSilent(t *testing.T) {
	sink := &cancelReceiptSink{}
	c := cancelReceiptController(t, sink)
	runOneQuickTurn(t, c)

	c.CancelStop()
	if st := c.RuntimeStatus(); st.StopLevel != StopLevelNone {
		t.Fatalf("stop on finished turn left stop level %d, want None", st.StopLevel)
	}
	if got := sink.cancellingCount(); got != 0 {
		t.Fatalf("no-op stop emitted %d fake TurnCancelling events, want 0", got)
	}
}

// ④ the mirror-refresh half of the receipt: fireStopForce's no-turn leg is
// the one no-turn site that MUTATES state (drops a stale armed level) and it
// used to return without a refresh — the one window that could leave a wrong
// StopLevel mirrored forever. Reproduce the stale mirror, fire, and require
// the snapshot to correct itself.
func TestFireStopForceNoTurnCorrectsStaleStopLevelMirror(t *testing.T) {
	sink := &cancelReceiptSink{}
	c := cancelReceiptController(t, sink)
	runOneQuickTurn(t, c)

	// Stale armed mirror: a level in the real state AND mirrored out, with
	// the turn already gone (the race window the guard defends).
	c.mu.Lock()
	c.stopLevel = StopLevelNormal
	c.mu.Unlock()
	c.refreshRuntimeState(event.Event{})
	if snap := c.RuntimeStateSnapshot(); snap.StopLevel != StopLevelNormal {
		t.Fatalf("fixture: snapshot stop level = %d, want armed stale %d", snap.StopLevel, StopLevelNormal)
	}

	c.fireStopForce()
	if st := c.RuntimeStatus(); st.StopLevel != StopLevelNone || st.StopDeadlineUnix != 0 {
		t.Fatalf("fireStopForce without a live turn left %+v, want reset level/deadline", st)
	}
	if snap := c.RuntimeStateSnapshot(); snap.StopLevel != StopLevelNone {
		t.Fatalf("stale mirrored stop level = %d, want %d after the no-turn refresh", snap.StopLevel, StopLevelNone)
	}
	if got := sink.cancellingCount(); got != 0 {
		t.Fatalf("no-turn force emitted %d fake TurnCancelling events, want 0", got)
	}
}

// ⑤ the two remaining guard sites (armStopEscalation/openStopGrace early
// returns) must keep the escalation state clean on a controller with no live
// turn — no arming, no grace, no panic — which is their observable contract
// beyond the receipt log.
func TestEscalationGuardsNoTurnStayClean(t *testing.T) {
	sink := &cancelReceiptSink{}
	c := cancelReceiptController(t, sink)
	runOneQuickTurn(t, c)

	c.armStopEscalation()
	c.openStopGrace()
	if st := c.RuntimeStatus(); st.StopLevel != StopLevelNone {
		t.Fatalf("no-turn guard calls left stop level %d, want None", st.StopLevel)
	}
	if got := sink.cancellingCount(); got != 0 {
		t.Fatalf("no-turn guard calls emitted %d fake TurnCancelling events, want 0", got)
	}
}
