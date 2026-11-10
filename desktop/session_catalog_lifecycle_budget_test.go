package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"reasonix/internal/sessioncatalog"
)

// TestStartupReconcileWaveBudgetDefersEverythingPastTheBudget is the tl1526
// pin (acceptance ① Go-side equivalent): the wave always returns promptly even
// when every scan would run far past the budget, and every target that did not
// complete is handed to onDeferred exactly once so the async queue can resume
// it. The wall-clock assertion is the 3s-interactivity clause's backend
// anchor: the startup goroutine stops waiting on the scans at the budget.
func TestStartupReconcileWaveBudgetDefersEverythingPastTheBudget(t *testing.T) {
	targets := make([]sessioncatalog.DirectoryTarget, 6)
	for i := range targets {
		targets[i] = sessioncatalog.DirectoryTarget{Path: fmt.Sprintf("/fixture/dir-%d", i)}
	}
	var deferredMu sync.Mutex
	deferredPaths := map[sessioncatalog.DirectoryTarget]int{}
	deferred := 0
	budget := 50 * time.Millisecond
	started := time.Now()
	count := runStartupReconcileWave(context.Background(), budget, targets,
		func(ctx context.Context, target sessioncatalog.DirectoryTarget) error {
			// Simulate the 131s scan: every in-flight scan only ends when the
			// wave context is torn down.
			<-ctx.Done()
			return ctx.Err()
		},
		func(target sessioncatalog.DirectoryTarget) {
			deferredMu.Lock()
			defer deferredMu.Unlock()
			deferredPaths[target]++
			deferred++
		})
	elapsed := time.Since(started)
	if count != len(targets) {
		t.Fatalf("deferred count = %d, want %d", count, len(targets))
	}
	if deferred != len(targets) {
		t.Fatalf("onDeferred fired %d times, want %d", deferred, len(targets))
	}
	for target, hits := range deferredPaths {
		if hits != 1 {
			t.Fatalf("target %v deferred %d times, want exactly once", target, hits)
		}
	}
	// The wave itself must not cost more than the budget plus scheduling slack:
	// this is the assertion that keeps the load chain off the scan wall clock.
	if elapsed > budget+1500*time.Millisecond {
		t.Fatalf("wave ran %v, want within budget %v plus slack", elapsed, budget)
	}
}

// TestStartupReconcileWaveLogsDegradeMode pins acceptance ③: budget-out emits
// one WARN carrying the budget and the degrade mode so the event is visible in
// desktop.log.
func TestStartupReconcileWaveLogsDegradeMode(t *testing.T) {
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer func() { slog.SetDefault(previous) }()

	targets := []sessioncatalog.DirectoryTarget{{Path: "/fixture/one"}}
	count := runStartupReconcileWave(context.Background(), 20*time.Millisecond, targets,
		func(ctx context.Context, target sessioncatalog.DirectoryTarget) error {
			<-ctx.Done()
			return ctx.Err()
		},
		func(sessioncatalog.DirectoryTarget) {})
	if count != 1 {
		t.Fatalf("deferred count = %d, want 1", count)
	}
	logged := buf.String()
	if !bytes.Contains([]byte(logged), []byte("deferred_to_async_queue")) ||
		!bytes.Contains([]byte(logged), []byte("startup session catalog reconcile budget exhausted")) {
		t.Fatalf("budget-out WARN missing or wrong shape: %q", logged)
	}
}

// TestStartupReconcileWaveReportsFailedScansWithoutDeferring pins the boundary:
// an ordinary scan error neither counts as deferred nor re-queues the target
// (same semantics the pre-budget loop had), it only stays visible at debug.
func TestStartupReconcileWaveReportsFailedScansWithoutDeferring(t *testing.T) {
	targets := []sessioncatalog.DirectoryTarget{{Path: "/fixture/failing"}}
	count := runStartupReconcileWave(context.Background(), time.Second, targets,
		func(ctx context.Context, target sessioncatalog.DirectoryTarget) error {
			return context.DeadlineExceeded
		},
		func(sessioncatalog.DirectoryTarget) {
			t.Fatalf("an ordinary scan error must not be reported as deferred")
		})
	if count != 0 {
		t.Fatalf("deferred count = %d, want 0", count)
	}
}
