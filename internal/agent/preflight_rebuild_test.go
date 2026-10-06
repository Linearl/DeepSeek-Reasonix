package agent

import (
	"testing"
	"time"

	"reasonix/internal/provider"
)

// waitForProjection polls until the background rebuild installs a projection
// (or the deadline passes), reading the state under its lock.
func waitForProjection(t *testing.T, a *Agent) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.sess.compactionMu.Lock()
		installed := len(a.sess.compactionState.Projection.Messages) > 0
		pending := a.sess.rebuildPending.Load()
		a.sess.compactionMu.Unlock()
		if installed && !pending {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// 任务549 验收②: an invalidation that leaves an oversized session without a
// projection must rebuild it in the background immediately — not wait for the
// next sampling round, which in the meantime sends (and the panel shows) the
// full canonical transcript.
func TestKickProjectionRebuildRebuildsAfterInvalidation(t *testing.T) {
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, foldableSessionOverForce(6), 6000)
	if fold := a.compactTrigger(); fold <= 0 {
		t.Fatalf("compactTrigger = %d, fixture needs a window", fold)
	}
	a.InvalidateProjection()
	if !waitForProjection(t, a) {
		a.sess.compactionMu.Lock()
		defer a.sess.compactionMu.Unlock()
		t.Fatalf("projection not rebuilt after invalidation: msgs=%d pending=%v",
			len(a.sess.compactionState.Projection.Messages), a.sess.rebuildPending.Load())
	}
}

// 任务549: the rebuild kick must stay silent when it would be unforced work —
// no provider to summarize with, no window to trigger against, or the view
// below the compaction trigger (the canonical fallback is fine there).
func TestKickProjectionRebuildSkipsWhenUnforced(t *testing.T) {
	// No provider: nothing can summarize, so no rebuild may be armed.
	noProv := agentOverForceWindow(t, nil, foldableSessionOverForce(6), 6000)
	noProv.kickProjectionRebuild("test_no_provider")
	if noProv.sess.rebuildPending.Load() {
		t.Fatal("nil provider must not arm a rebuild")
	}

	// Below the trigger the full canonical view is what the next request sends
	// anyway; folding would be unforced work.
	small := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hi"},
	}}, 6000)
	small.InvalidateProjection()
	time.Sleep(50 * time.Millisecond)
	if small.sess.rebuildPending.Load() {
		t.Fatal("below-trigger session must not arm a rebuild")
	}
	small.sess.compactionMu.Lock()
	installed := len(small.sess.compactionState.Projection.Messages)
	small.sess.compactionMu.Unlock()
	if installed != 0 {
		t.Fatalf("below-trigger session folded anyway: %d projection messages", installed)
	}
}

// While a rebuild is armed, further kicks must be no-ops — a burst of history
// rewrites must not queue one summary per invalidation (the 549 storm shape).
func TestKickProjectionRebuildDeduplicatesWhileArmed(t *testing.T) {
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, foldableSessionOverForce(6), 6000)
	a.sess.rebuildPending.Store(true) // pretend a rebuild is in flight
	a.kickProjectionRebuild("test_dedupe")
	if !a.sess.rebuildPending.Load() {
		t.Fatal("kick while armed must be a no-op, but the flag got cleared")
	}
	a.sess.rebuildPending.Store(false)
}
