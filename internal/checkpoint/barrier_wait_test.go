package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func stubEnterWriteBudget(t *testing.T, d time.Duration) {
	t.Helper()
	prev := defaultEnterWriteBudget
	defaultEnterWriteBudget = d
	t.Cleanup(func() { defaultEnterWriteBudget = prev })
}

// TestEnterWriteBoundedWhenExclusiveHeld pins the 472 增补（474 切片 3）: a
// writer queuing behind the rewind exclusive section returns a deadline error
// at the budget instead of hanging forever, and enters normally once exclusive
// releases.
func TestEnterWriteBoundedWhenExclusiveHeld(t *testing.T) {
	b := NewMutationBarrier()
	if !b.TryEnterExclusive() {
		t.Fatal("take exclusive")
	}
	stubEnterWriteBudget(t, 50*time.Millisecond)
	started := time.Now()
	err := b.EnterWrite()
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnterWrite error = %v, want deadline exceeded", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("EnterWrite returned after %v, want bounded by the 50ms budget", elapsed)
	}
	b.ExitExclusive()
	if err := b.EnterWrite(); err != nil {
		t.Fatalf("EnterWrite after exclusive release: %v", err)
	}
	b.ExitWrite()
}

// TestEnterWriteContextReturnsWhenCallerCancels pins the 461-P1 stop SLA
// through the new ctx-aware entry: cancellation during a blocked wait ends it
// in under a second.
func TestEnterWriteContextReturnsWhenCallerCancels(t *testing.T) {
	b := NewMutationBarrier()
	if !b.TryEnterExclusive() {
		t.Fatal("take exclusive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	started := time.Now()
	err := b.EnterWriteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnterWriteContext error = %v, want context.Canceled", err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("EnterWriteContext returned ≥1s after cancel (task 461 P1 停止 SLA)")
	}
	b.ExitExclusive()
}

// TestEnterWriteUnblockedByExitExclusive pins the broadcast replacement for
// the old cond: a writer queued behind the exclusive section wakes when
// exclusive releases. (Writers never block each other — that is pinned by
// TestEnterWriteAllowsConcurrentWriters.)
func TestEnterWriteUnblockedByExitExclusive(t *testing.T) {
	b := NewMutationBarrier()
	if !b.TryEnterExclusive() {
		t.Fatal("take exclusive")
	}
	acquired := make(chan error, 1)
	go func() { acquired <- b.EnterWrite() }()
	select {
	case err := <-acquired:
		t.Fatalf("writer entered while exclusive held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	b.ExitExclusive()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued writer not unblocked by ExitExclusive")
	}
	b.ExitWrite()
}

// TestEnterWriteAllowsConcurrentWriters pins the unchanged writer semantics:
// writers do not block each other, only the exclusive section does.
func TestEnterWriteAllowsConcurrentWriters(t *testing.T) {
	b := NewMutationBarrier()
	for range 3 {
		if err := b.EnterWrite(); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		b.ExitWrite()
	}
}

// TestEnterWriteWaitLogged pins the 472 留痕: a wait at or above the threshold
// emits a wait_ms log line; uncontended enters stay silent.
func TestEnterWriteWaitLogged(t *testing.T) {
	b := NewMutationBarrier()
	// 无争用进入必须静默。
	var quiet bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&quiet, nil)))
	if err := b.EnterWrite(); err != nil {
		t.Fatal(err)
	}
	b.ExitWrite()
	if s := quiet.String(); strings.Contains(s, "wait_ms") {
		t.Fatalf("uncontended enter must stay silent, got %q", s)
	}

	if !b.TryEnterExclusive() {
		t.Fatal("take exclusive")
	}
	stubEnterWriteBudget(t, 300*time.Millisecond) // ≥ barrierWaitLogThreshold(250ms)
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		b.ExitExclusive()
	})
	if err := b.EnterWrite(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EnterWrite error = %v, want deadline exceeded", err)
	}
	if s := buf.String(); !strings.Contains(s, "wait_ms") {
		t.Fatalf("waited enter must log wait_ms, got %q", s)
	}
}
