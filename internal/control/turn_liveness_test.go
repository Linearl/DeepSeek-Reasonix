package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// A running turn that produces no events for the stall threshold gets exactly
// one warning per silent stretch; any progress re-arms it.
func TestStalledTurnWarnsOncePerSilence(t *testing.T) {
	oldInterval, oldThreshold := midTurnSnapshotInterval.Load(), turnStallThreshold.Load()
	midTurnSnapshotInterval.Store(int64(10 * time.Millisecond))
	turnStallThreshold.Store(int64(80 * time.Millisecond))
	t.Cleanup(func() {
		midTurnSnapshotInterval.Store(oldInterval)
		turnStallThreshold.Store(oldThreshold)
	})

	notices := make(chan event.Event, 8)
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeTurnStalled {
			notices <- e
		}
	})})
	t.Cleanup(c.Close)

	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started

	select {
	case n := <-notices:
		if n.Level != event.LevelWarn || n.Text == "" {
			t.Fatalf("stall notice = %+v, want a warn-level explanation", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("silent running turn never produced a stall notice")
	}
	select {
	case <-notices:
		t.Fatal("stall notice repeated without any progress")
	case <-time.After(300 * time.Millisecond):
	}

	c.sink.Emit(event.Event{Kind: event.Text, Text: "still working"})
	select {
	case <-notices:
	case <-time.After(5 * time.Second):
		t.Fatal("renewed silence after progress did not warn again")
	}
	c.Cancel()
}

func TestIdleControllerNeverWarnsAboutStalls(t *testing.T) {
	notices := 0
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeTurnStalled {
			notices++
		}
	})})
	t.Cleanup(c.Close)
	c.liveness.reset(time.Now().Add(-time.Hour))
	c.warnIfTurnStalled(time.Now())
	if notices != 0 {
		t.Fatalf("idle controller emitted %d stall notices", notices)
	}
}

// 任务1147: while a compaction pass is in flight the same silence must NOT
// surface as the "press Stop" stall warning — the 2026-10-24 field report had
// a healthy chunked compaction read as a failure because of it. The notice
// arrives under the neutral compacting code (info level) instead. Driven
// directly against warnIfTurnStalled (same-package convention, cf.
// controller_clear_test.go): spawning a real turn would also spin the
// autosave ticker, whose mid-turn snapshot needs a live executor session.
func TestStallWhileCompactingUsesNeutralNotice(t *testing.T) {
	var mu sync.Mutex
	var codes []string
	record := func(e event.Event) {
		if e.Kind != event.Notice {
			return
		}
		mu.Lock()
		codes = append(codes, e.Code)
		mu.Unlock()
	}
	countOf := func(want string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, code := range codes {
			if code == want {
				n++
			}
		}
		return n
	}
	// warnIfTurnStalled only fires past the silence threshold; skip the wait
	// by stamping the last event two hours back.
	staleLiveness := func(c *Controller) { c.liveness.reset(time.Now().Add(-2 * time.Hour)) }

	// No compaction in flight (executor absent): the plain stall warning.
	c := New(Options{Sink: event.FuncSink(record)})
	t.Cleanup(c.Close)
	c.running = true
	staleLiveness(c)
	c.warnIfTurnStalled(time.Now())
	if got := countOf(event.NoticeCodeTurnStalled); got != 1 {
		t.Fatalf("plain silence: %d stall notices, want exactly one", got)
	}
	if got := countOf(event.NoticeCodeTurnStalledCompacting); got != 0 {
		t.Fatalf("plain silence: %d compacting notices, want none", got)
	}

	// Compaction pass begins: the same silence lands on the neutral code and
	// the press-Stop warning stays quiet.
	c.executor = &agent.Agent{}
	c.executor.CompactionLiveBeginForTest("auto")
	staleLiveness(c)
	c.warnIfTurnStalled(time.Now())
	if got := countOf(event.NoticeCodeTurnStalledCompacting); got != 1 {
		t.Fatalf("compacting silence: %d neutral notices, want exactly one", got)
	}
	if got := countOf(event.NoticeCodeTurnStalled); got != 1 {
		t.Fatalf("compacting silence still emitted the press-Stop warning (%d total)", got)
	}

	// Pass ends: the plain warning takes over again.
	c.executor.CompactionLiveStopForTest()
	staleLiveness(c)
	c.warnIfTurnStalled(time.Now())
	if got := countOf(event.NoticeCodeTurnStalled); got != 2 {
		t.Fatalf("post-compaction silence: %d stall notices, want the warning resumed", got)
	}
	if got := countOf(event.NoticeCodeTurnStalledCompacting); got != 1 {
		t.Fatalf("post-compaction silence: %d compacting notices, want none new", got)
	}
}
