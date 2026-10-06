package control

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureSlog redirects the default slog logger into a buffer for the duration
// of the test and returns a function reading what was logged.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

func waitForAskEmitted(t *testing.T, sink *askProbeSink) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if asks, _ := sink.counts(); asks == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the ask never reached the sink")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// Task 567 read-only instrumentation: an uncontended ask must produce the
// three chain checkpoints — entry, prompt_lock acquisition, and the timed
// AskRequest emit — so a slow panel can be bisected from desktop.log alone.
func TestAskChainCheckpointsLoggedForUncontendedAsk(t *testing.T) {
	readLog := captureSlog(t)
	sink := &askProbeSink{}
	c := New(Options{Sink: sink, SessionDir: t.TempDir()})

	go func() { _, _ = c.Ask(t.Context(), askProbeQuestions()) }()
	waitForAskEmitted(t, sink)

	log := readLog()
	for _, stage := range []string{"stage=entry", "stage=prompt_lock", "stage=ask_emit"} {
		if !strings.Contains(log, stage) {
			t.Fatalf("ask chain log missing %s; got:\n%s", stage, log)
		}
	}
	if !strings.Contains(log, "durationMs=") {
		t.Fatalf("ask_emit checkpoint missing durationMs; got:\n%s", log)
	}
	if !strings.Contains(log, "waitMs=") {
		t.Fatalf("prompt_lock checkpoint missing waitMs; got:\n%s", log)
	}
}

// The lock checkpoint must carry the lock wait even when the ask actually
// queued behind an earlier prompt — that waitMs is the candidate-1 reading.
func TestAskChainLockCheckpointReportsQueueWait(t *testing.T) {
	shortenPromptQueueNotice(t)
	readLog := captureSlog(t)
	sink := &askProbeSink{}
	c := New(Options{Sink: sink, SessionDir: t.TempDir()})
	c.approval.promptMu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Ask(ctx, askProbeQuestions())
	}()
	deadline := time.After(2 * time.Second)
	for {
		if _, notices := sink.counts(); notices == 1 {
			break
		}
		select {
		case <-deadline:
			c.approval.promptMu.Unlock()
			t.Fatal("the queued ask never announced itself")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	c.approval.promptMu.Unlock()
	waitForAskEmitted(t, sink)

	// The ask is now parked waiting for an answer; cancelling its context is
	// how this test lets it return (done closes only after Ask unwinds).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not unblock after cancellation")
	}

	log := readLog()
	if !strings.Contains(log, "stage=prompt_lock") {
		t.Fatalf("queued ask missing prompt_lock checkpoint; got:\n%s", log)
	}
	if !strings.Contains(log, "stage=ask_emit") {
		t.Fatalf("queued ask missing ask_emit checkpoint; got:\n%s", log)
	}
}
