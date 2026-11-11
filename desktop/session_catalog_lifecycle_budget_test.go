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

// TestStartupCatalogDegradedEventOnlyOnDeferred pins the tl1543 event source:
// budget-out (deferred > 0) emits exactly one "app:startup-degraded" runtime
// event carrying the deferred count and the WARN's mode tag, while a
// fully-settled wave (deferred == 0) emits nothing — the negative half of the
// frontend acceptance: a normal startup must not flip any degrade UX.
// asyncRuntimeEmitter drains on a goroutine, so the capture is a channel.
func TestStartupCatalogDegradedEventOnlyOnDeferred(t *testing.T) {
	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	events := make(chan runtimeEventEnvelope, 8)
	app.runtimeEvents.emit = func(ctx context.Context, name string, payload ...any) {
		events <- runtimeEventEnvelope{ctx: ctx, name: name, payload: append([]any(nil), payload...)}
	}
	receive := func() runtimeEventEnvelope {
		select {
		case ev := <-events:
			return ev
		case <-time.After(2 * time.Second):
			t.Fatalf("runtime event did not arrive within 2s")
			return runtimeEventEnvelope{}
		}
	}

	app.emitStartupCatalogDegraded(0)
	select {
	case ev := <-events:
		t.Fatalf("deferred=0 emitted %q, want silence (normal startup must stay byte-identical)", ev.name)
	case <-time.After(50 * time.Millisecond):
	}

	app.emitStartupCatalogDegraded(2)
	ev := receive()
	if ev.name != startupCatalogDegradedEvent {
		t.Fatalf("deferred=2 event = %q, want %q", ev.name, startupCatalogDegradedEvent)
	}
	if len(ev.payload) != 1 {
		t.Fatalf("payload arity = %d, want 1", len(ev.payload))
	}
	m, ok := ev.payload[0].(map[string]any)
	if !ok {
		t.Fatalf("payload = %#v, want map[string]any", ev.payload[0])
	}
	if m["deferred"] != 2 || m["mode"] != "deferred_to_async_queue" {
		t.Fatalf("payload = %v, want deferred=2 mode=deferred_to_async_queue", m)
	}
}

// TestRetryStartupCatalogScanRidesExistingRefreshPrimitive pins the tl1543
// retry door: one call re-kicks the catalog refresh primitive the watch loop
// already uses (per-directory reconcile queue + metadata sync) and fires the
// one tree event the sidebar honors. No new reconcile mechanism.
func TestRetryStartupCatalogScanRidesExistingRefreshPrimitive(t *testing.T) {
	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	refreshes := 0
	app.projectTreeCatalogRefreshHook = func() { refreshes++ }
	treeSignals := 0
	app.projectTreeChangedHook = func() { treeSignals++ }

	app.RetryStartupCatalogScan()
	if refreshes != 1 {
		t.Fatalf("catalog refresh primitive fired %d times, want exactly 1", refreshes)
	}
	if treeSignals != 1 {
		t.Fatalf("sidebar tree signal fired %d times, want exactly 1", treeSignals)
	}
}
