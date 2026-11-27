package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestTopicActivityReapLoopBodyClearsStaleAndBroadcasts is the tl1830
// integration test for the reaper wiring: one pass of the loop body (exactly
// what each tick of startTopicActivityReapLoop executes) over a mixed tab set
// must clear only the live spinner statuses past the TTL and broadcast the
// project-tree change so the frontend stops spinning. Terminal statuses
// (waiting_confirmation waits on the USER, error is a fact) are never
// TTL-reaped — that contract is shared with the unit test in
// project_tree_activity_ttl_test.go and asserted here end-to-end.
func TestTopicActivityReapLoopBodyClearsStaleAndBroadcasts(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()

	// asyncRuntimeEmitter.Emit is asynchronous (enqueue + background run
	// goroutine), so capture under a mutex and poll for the broadcast below.
	var mu sync.Mutex
	var broadcast []string
	app.runtimeEvents.emit = func(_ context.Context, name string, _ ...any) {
		mu.Lock()
		broadcast = append(broadcast, name)
		mu.Unlock()
	}

	now := time.Now()
	statuses := map[string]string{
		"tab_thinking":  topicStatusThinking,
		"tab_streaming": topicStatusStreaming,
		"tab_waiting":   topicStatusWaitingConfirmation,
		"tab_error":     topicStatusError,
	}
	for id, status := range statuses {
		tab := &WorkspaceTab{
			ID:             id,
			Scope:          "global",
			TopicID:        "topic_" + id,
			Ready:          true,
			ActivityStatus: status,
			disabledMCP:    map[string]ServerView{},
		}
		app.projectTreeRuntime.setActivityAt(id, now.Add(-topicActivityStatusTTL-time.Minute))
		app.tabs[id] = tab
	}

	// One tick of the wired loop, verbatim.
	app.reapStaleTopicActivityStatus(now)

	for _, id := range []string{"tab_thinking", "tab_streaming"} {
		if got := app.tabs[id].ActivityStatus; got != "" {
			t.Fatalf("%s activity status after reap = %q, want cleared (past TTL with no turn event)", id, got)
		}
	}
	for _, id := range []string{"tab_waiting", "tab_error"} {
		if got := app.tabs[id].ActivityStatus; got != statuses[id] {
			t.Fatalf("%s activity status after reap = %q, want %q (terminal status must survive the TTL watchdog)", id, got, statuses[id])
		}
	}

	// The frontend stop-spinning contract: the reap broadcast must carry both
	// the runtime snapshot event and the tagged legacy event. Wait out the
	// emitter's background queue instead of racing it.
	var sawRuntimeChanged, sawLegacyChanged bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, name := range broadcast {
			switch name {
			case "project-tree:runtime-changed":
				sawRuntimeChanged = true
			case "project-tree:changed":
				sawLegacyChanged = true
			}
		}
		mu.Unlock()
		if sawRuntimeChanged && sawLegacyChanged {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawRuntimeChanged || !sawLegacyChanged {
		t.Fatalf("broadcast after reap did not carry both required events within 2s (runtime-changed=%v legacy-changed=%v)", sawRuntimeChanged, sawLegacyChanged)
	}
}

// TestTopicActivityReapTickBoundsDetectionLatency guards the scheduling
// contract of the wiring: the tick must stay below the TTL so a stale spinner
// clears within [TTL, TTL+tick] instead of an unbounded multiple of the TTL.
func TestTopicActivityReapTickBoundsDetectionLatency(t *testing.T) {
	if topicActivityReapTick >= topicActivityStatusTTL {
		t.Fatalf("reap tick %s must stay below TTL %s so detection latency stays within [TTL, TTL+tick]", topicActivityReapTick, topicActivityStatusTTL)
	}
}
