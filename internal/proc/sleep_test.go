package proc

import (
	"context"
	"testing"
	"time"
)

// tl1620 acceptance pins: the shared SleepCtx must complete, honor
// cancellation immediately, and short-circuit non-positive durations.

func TestSleepCtxCompletes(t *testing.T) {
	if !SleepCtx(context.Background(), time.Millisecond) {
		t.Fatal("SleepCtx should return true when the full delay elapses")
	}
}

func TestSleepCtxCancelledReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if SleepCtx(ctx, 10*time.Second) {
		t.Fatal("SleepCtx should return false when ctx is cancelled mid-wait")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("SleepCtx ignored cancellation: waited %v", elapsed)
	}
}

func TestSleepCtxAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if SleepCtx(ctx, time.Second) {
		t.Fatal("SleepCtx should return false immediately when ctx is already cancelled")
	}
}

func TestSleepCtxNonPositiveDurationReturnsImmediately(t *testing.T) {
	start := time.Now()
	if !SleepCtx(context.Background(), 0) {
		t.Fatal("SleepCtx should return true for a zero delay")
	}
	if !SleepCtx(context.Background(), -time.Second) {
		t.Fatal("SleepCtx should return true for a negative delay")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("SleepCtx waited %v for a non-positive delay", elapsed)
	}
}
