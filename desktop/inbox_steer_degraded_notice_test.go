package main

// 任务1094缺陷B：本会话 composer 引导降级此前完全静默（inbox_app.go 只返回
// receipt）——用户只看到「必须点恢复执行才能发」，看不到任何「消息将在下一轮
// 发送」提示。本测试钉死两个方向：
//   ① 真实降级（queued_followup，非幂等）必须 emit steer_degraded notice，
//      与跨会话路径 session_collab.go:1465-1469 同语义；
//   ② 真实注入成功（steer_accepted）不得 emit 该 notice（避免噪音）。

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/tool"
)

// blockingSteerProvider blocks the first request so the test can admit a steer
// while the turn is genuinely running, then answers every later request with a
// plain final text. The started channel is closed exactly once (sync.Once) —
// a second Stream call must not re-close it (close of closed channel panic).
type blockingSteerProvider struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
}

func (p *blockingSteerProvider) Name() string { return "blocking-steer" }

func (p *blockingSteerProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	p.startedOnce.Do(func() { close(p.started) })
	go func() {
		defer close(ch)
		select {
		case <-p.release:
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "ready"}
			ch <- provider.Chunk{Type: provider.ChunkDone}
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// steerNoticeTestAppWithTurn builds an isolated App + tab with a real
// Controller whose runner blocks on the first provider request, so the test
// can admit a steer mid-turn. Returns the app, tab, recorded codes, the
// blocking provider, a release-once, and the sink's terminal channel.
func steerNoticeTestAppWithTurn(t *testing.T) (*App, *WorkspaceTab, *[]string, *blockingSteerProvider, *sync.Once, <-chan event.Event) {
	t.Helper()
	isolateDesktopUserDirs(t)
	// Windows: a running turn goroutine holds file handles on the session
	// dir, and t.TempDir's RemoveAll races them (unlinkat: directory not
	// empty). Use a manual temp dir removed by the test itself after Close.
	dir, err := os.MkdirTemp("", "steer-notice-test")
	if err != nil {
		t.Fatal(err)
	}
	prov := &blockingSteerProvider{started: make(chan struct{}), release: make(chan struct{})}
	sess := agent.NewSession("sys")
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	sink := &tabEventSink{tabID: "tab-steer-notice", ctx: context.Background()}
	terminal := make(chan event.Event, 1)
	sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			terminal <- e
		}
	}))
	ctrl := control.New(control.Options{
		Runner: exec, Executor: exec, Sink: sink,
		SessionDir:  dir,
		SessionPath: filepath.Join(dir, "s.jsonl"),
	})
	// Cleanup order matters: t.Cleanup runs LIFO, so the release-once closure
	// (registered later) runs BEFORE this ctrl.Close — a blocked provider
	// goroutine must be released before Close waits on the autosave waitgroup.
	t.Cleanup(func() {
		ctrl.Close()
		// Give the autosave goroutine a beat to release its file handles
		// before RemoveAll, then remove the manual temp dir (no t.TempDir
		// race on Windows).
		time.Sleep(200 * time.Millisecond)
		_ = os.RemoveAll(dir)
	})
	tab := &WorkspaceTab{ID: sink.tabID, Scope: "global", Ready: true, Ctrl: ctrl, sink: sink}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, activeTabID: tab.ID}
	sink.app = app

	codes := &[]string{}
	var codesMu sync.Mutex
	sink.runtimeEvents.emit = func(_ context.Context, _ string, payload ...any) {
		for _, p := range payload {
			switch wire := p.(type) {
			case wireEventTab:
				if wire.Code != "" {
					codesMu.Lock()
					*codes = append(*codes, wire.Code)
					codesMu.Unlock()
				}
			case correlatedWireEventTab:
				if wire.Code != "" {
					codesMu.Lock()
					*codes = append(*codes, wire.Code)
					codesMu.Unlock()
				}
			}
		}
	}
	sink.setContext(context.Background())

	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(prov.release) }) })
	return app, tab, codes, prov, &releaseOnce, terminal
}

// waitForSteerNoticeCode polls the recorded codes until want shows up. The
// runtime emitter drains its queue on a goroutine, so the notice lands
// asynchronously relative to the setter call that produced it.
func waitForSteerNoticeCode(t *testing.T, codes *[]string, want string) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, code := range *codes {
			if code == want {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// countSteerNoticeCode counts occurrences of a code in the recorded slice.
func countSteerNoticeCode(codes *[]string, want string) int {
	n := 0
	for _, code := range *codes {
		if code == want {
			n++
		}
	}
	return n
}

// TestEnqueueSteerEmitsDegradedNoticeOnQueuedFollowup: a same-session composer
// steer that degrades to queued_followup (non-idempotent, inbox paused) must
// emit a steer_degraded notice — the same semantic as the cross-session path.
func TestEnqueueSteerEmitsDegradedNoticeOnQueuedFollowup(t *testing.T) {
	app, tab, codes, prov, _, _ := steerNoticeTestAppWithTurn(t)

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}

	// Pause the inbox so TryEnqueueAndSteerForTurn's ErrPaused branch produces
	// queued_followup (Paused=true) — the deterministic degradation face.
	if err := tab.Ctrl.SetInboxPaused(true); err != nil {
		t.Fatalf("SetInboxPaused: %v", err)
	}
	rec, err := app.EnqueueInboxSteerForTurn(tab.ID, start.TurnID, "late steer", "late steer", "idem-degraded-1")
	if err != nil {
		t.Fatalf("EnqueueInboxSteerForTurn: %v", err)
	}
	if rec.Disposition != string(sessioninbox.DispositionQueuedFollowup) {
		t.Fatalf("disposition = %q, want queued_followup (degraded)", rec.Disposition)
	}
	if !rec.Paused {
		t.Fatal("receipt must carry Paused=true for a paused-inbox steer")
	}
	if !waitForSteerNoticeCode(t, codes, "steer_degraded") {
		t.Fatalf("steer_degraded notice not emitted for a degraded composer steer, got %v", *codes)
	}
	// Exactly one notice — not duplicated by any other emit path.
	if got := countSteerNoticeCode(codes, "steer_degraded"); got != 1 {
		t.Fatalf("steer_degraded notice count = %d, want exactly 1", got)
	}
}

// TestEnqueueSteerForTurnEmitsNoNoticeWhenAccepted: a genuine mid-turn steer
// (steer_accepted) must NOT emit a steer_degraded notice — the notice is
// degradation-only, not a generic "steer happened" trace.
func TestEnqueueSteerForTurnEmitsNoNoticeWhenAccepted(t *testing.T) {
	app, tab, codes, prov, releaseOnce, terminal := steerNoticeTestAppWithTurn(t)

	start, err := app.StartTurnForTab(tab.ID, "hold this turn", "submission-1")
	if err != nil {
		t.Fatalf("StartTurnForTab: %v", err)
	}
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn runner did not start")
	}

	rec, err := app.EnqueueInboxSteerForTurn(tab.ID, start.TurnID, "mid-turn steer", "mid-turn steer", "idem-accepted-1")
	if err != nil {
		t.Fatalf("EnqueueInboxSteerForTurn: %v", err)
	}
	if rec.Disposition != string(sessioninbox.DispositionSteerAccepted) {
		t.Fatalf("disposition = %q, want steer_accepted (genuine injection)", rec.Disposition)
	}
	// Give the async emitter a beat to flush, then assert no degradation notice.
	time.Sleep(100 * time.Millisecond)
	if got := countSteerNoticeCode(codes, "steer_degraded"); got != 0 {
		t.Fatalf("steer_degraded notice emitted for an accepted steer, count=%d", got)
	}

	// Release the blocked stream so the turn reaches TurnDone cleanly.
	releaseOnce.Do(func() { close(prov.release) })
	select {
	case <-terminal:
	case <-time.After(10 * time.Second):
		t.Fatal("turn did not reach terminal state")
	}
}
