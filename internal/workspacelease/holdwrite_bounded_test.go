package workspacelease

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stubPathHoldWaitBudget(t *testing.T, d time.Duration) {
	t.Helper()
	prev := pathHoldWaitBudget
	pathHoldWaitBudget = d
	t.Cleanup(func() { pathHoldWaitBudget = prev })
}

// TestHoldWriteBoundedOnPathHoldsWithoutDeadline pins the 472 增补（474 切片 3）:
// HoldWrite with a deadline-less context returns at the in-process budget
// instead of waiting forever behind same-owner path holds — the set_target
// 55.4s unbounded-wait surface.
func TestHoldWriteBoundedOnPathHoldsWithoutDeadline(t *testing.T) {
	root := t.TempDir()
	owner, err := New(root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file.txt")
	pathRelease, err := owner.HoldWriteForPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	stubPathHoldWaitBudget(t, 150*time.Millisecond)
	started := time.Now()
	_, err = owner.HoldWrite(context.Background())
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HoldWrite error = %v, want deadline exceeded", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("HoldWrite waited %v, want bounded by the 150ms budget", elapsed)
	}

	pathRelease()
	wsRelease, err := owner.HoldWrite(context.Background())
	if err != nil {
		t.Fatalf("HoldWrite after path-hold release: %v", err)
	}
	wsRelease()
}

// TestHoldWriteReturnsWhenCallerCancels pins the stop SLA through the budget
// wrap: cancellation still ends the wait in under a second.
func TestHoldWriteReturnsWhenCallerCancels(t *testing.T) {
	root := t.TempDir()
	owner, err := New(root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file.txt")
	pathRelease, err := owner.HoldWriteForPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pathRelease)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(80*time.Millisecond, cancel)
	started := time.Now()
	_, err = owner.HoldWrite(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HoldWrite error = %v, want context.Canceled", err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("HoldWrite returned ≥1s after cancel (task 461 P1 停止 SLA)")
	}
}

// TestHoldWriteWaitLogged pins the 472 留痕: an in-process wait at or above
// the threshold emits a wait_ms log line (照 save-path lock waited 先例).
func TestHoldWriteWaitLogged(t *testing.T) {
	root := t.TempDir()
	owner, err := New(root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "file.txt")
	pathRelease, err := owner.HoldWriteForPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	stubPathHoldWaitBudget(t, 300*time.Millisecond) // ≥ pathHoldWaitLogThreshold(250ms)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		pathRelease()
	})

	if _, err := owner.HoldWrite(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HoldWrite error = %v, want deadline exceeded", err)
	}
	if s := buf.String(); !strings.Contains(s, "wait_ms") {
		t.Fatalf("in-process wait must log wait_ms, got %q", s)
	}
}
