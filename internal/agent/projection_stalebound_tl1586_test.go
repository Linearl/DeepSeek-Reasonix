package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1586 regression pins: the tl1203 stale-serving branch (an unusable
// projection BODY whose covered-prefix check failed — the content_mismatch
// episode) used to serve the fold+tail splice with no bound at all. When the
// fold covers a small prefix of a multi-window canonical, that splice is
// nearly the full transcript: the incident log carried view_tokens=1064450
// against a 1M window until the forced rebuild landed (or never did). These
// pins hold the fix's three faces: the over-window splice degrades bounded,
// the under-ceiling splice passes through byte-identical (tl1203 unchanged),
// and the content_mismatch episode still converges to a valid projection.

// staleSpliceFixture installs a valid-at-install-time projection covering the
// FIRST FIVE canonical messages of an oversized transcript, then edits one
// covered body message so the covered-prefix hash rejects the fold while the
// projection body stays in memory — the exact content_mismatch shape.
func staleSpliceFixture(t *testing.T, streamErr error) (*Agent, *Session, []provider.Message, CompactionState) {
	t.Helper()
	const window = 10_000
	msgs := oversizedCanonical(60, 200)
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{reply: "structured digest", streamErr: streamErr}, tool.NewRegistry(), sess, Options{
		ContextWindow: window,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	const covered = 5
	st := CompactionState{
		PromptCacheKey: a.currentPromptCacheKey(),
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "system"},
				formatSummaryMessage("earlier rounds digested"),
			},
			TranscriptVersion: uint64(len(msgs)),
			CoveredCount:      covered,
			CoveredPrefixHash: coveredPrefixHash(msgs, covered),
		},
	}
	// Real content drift behind the fold: the hash must reject the fold, the
	// body stays, and the episode classifies as content_mismatch (the key
	// above is still current — no model lineage change involved).
	sess.Messages[2].Content = "covered-turn-EDITED"
	a.sess.compactionMu.Lock()
	a.sess.compactionState = st
	a.sess.compactionMu.Unlock()
	if !a.staleProjectionServing() {
		t.Fatal("fixture must start in the stale-serving episode (body present, content invalid)")
	}
	return a, sess, msgs, st
}

// TestTl1586StaleSpliceBoundedOverWindow pins the fix: the stale splice over
// the hard ceiling must degrade to a bounded view that keeps the fold's
// digest, the truncation marker, and the recent verbatim turns — never the
// raw multi-window splice, and never more than the hard input ceiling.
func TestTl1586StaleSpliceBoundedOverWindow(t *testing.T) {
	// streamErr keeps the forced background rebuild from landing mid-test, so
	// the stale episode (and its bounded view) is deterministic.
	a, sess, canonical, st := staleSpliceFixture(t, errors.New("summary down (tl1586 pin)"))
	canonical, version := sess.snapshotMessagesVersion()

	rawSplice := modelVisibleFromProjection(st.Projection, canonical)
	if got := a.estimatedVisibleRequestTokens(rawSplice); got < a.hardInputCeiling() {
		t.Fatalf("fixture splice must outgrow the hard ceiling, got %d", got)
	}
	if !a.sess.staleRebuildArmed.CompareAndSwap(false, true) {
		t.Fatal("fixture pre-condition: stale rebuild flag must start unarmed")
	}
	a.sess.staleRebuildArmed.Store(false)

	view := a.modelVisibleMessages()
	if len(view) >= len(rawSplice) {
		t.Fatalf("bounded stale view must be shorter than the raw splice: %d vs %d", len(view), len(rawSplice))
	}
	if got := a.estimatedVisibleRequestTokens(view); got >= a.hardInputCeiling() {
		t.Fatalf("bounded stale view %d tokens must sit below the hard ceiling %d", got, a.hardInputCeiling())
	}
	digestKept, markerSeen := false, false
	for _, m := range view {
		if isCompactionSummary(m) {
			digestKept = true
		}
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, "truncated to fit the context window") {
			markerSeen = true
		}
	}
	if !digestKept {
		t.Fatal("the stale fold's digest must survive the degrade as a carrier")
	}
	if !markerSeen {
		t.Fatal("degraded stale view must carry the truncation marker")
	}
	if view[len(view)-2].Content != "recent question" || view[len(view)-1].Content != "recent answer" {
		t.Fatalf("the newest exchange must survive verbatim, got tail %+v", view[len(view)-2:])
	}
	assertPairingsIntact(t, view)
	// tl1203's kick still fired: the bounded view must not silence the rebuild.
	if !a.sess.staleRebuildArmed.Load() {
		t.Fatal("bounded stale view must still arm the forced stale rebuild")
	}
	// Same (version, projection version): the cached bounded view comes back.
	again := a.modelVisibleMessages()
	if len(again) != len(view) {
		t.Fatalf("cached stale view changed size: %d vs %d", len(again), len(view))
	}
	if a.staleProjectionServing() {
		if cached, ok := a.sess.staleSplice.load(version, st.Projection.ProjectionVersion); !ok || cached == nil {
			t.Fatal("the bounded stale view must be cached for its (version, projection version) pair")
		}
	}
}

// TestTl1586StaleSplicePassesThroughUnderCeiling pins the unchanged half: a
// stale splice that fits under the hard ceiling passes through byte-identical
// (the tl1203 shape the hot-switch pins rely on) and caches "no degradation
// needed" instead of a bounded view.
func TestTl1586StaleSplicePassesThroughUnderCeiling(t *testing.T) {
	fx := newHotSwitchFixture(t)
	a := fx.agent

	// Real content drift behind the fold: the stale episode starts.
	a.sess.conversation.Messages[1].Content = "task-one-EDITED"
	wire := a.modelVisibleMessages()
	expected := modelVisibleFromProjection(fx.agent.sess.compactionState.Projection, fx.canonical)
	if len(wire) != len(expected) {
		t.Fatalf("under-ceiling stale splice must pass through unchanged: %d vs %d", len(wire), len(expected))
	}
	for i := range expected {
		if wire[i].Content != expected[i].Content {
			t.Fatalf("splice message %d changed: %q vs %q", i, wire[i].Content, expected[i].Content)
		}
	}
	_, version := a.sess.conversation.snapshotMessagesVersion()
	if cached, ok := a.sess.staleSplice.load(version, a.sess.compactionState.Projection.ProjectionVersion); !ok || cached != nil {
		t.Fatalf("under-ceiling splice must cache the passthrough answer, got known=%v view=%d msgs", ok, len(cached))
	}
}

// TestTl1586ContentMismatchRebuildsValidProjection pins acceptance ③'s
// rebuild half: the bounded content_mismatch episode still converges — the
// forced rebuild installs a valid projection and the view returns to the
// projected (small) shape with the stale-serving machinery disarmed.
func TestTl1586ContentMismatchRebuildsValidProjection(t *testing.T) {
	a, sess, canonical, _ := staleSpliceFixture(t, nil) // summary succeeds: the rebuild can land
	canonical, _ = sess.snapshotMessagesVersion()
	if view := a.modelVisibleMessages(); a.estimatedVisibleRequestTokens(view) >= a.hardInputCeiling() {
		t.Fatal("fixture must start bounded (degraded stale view)")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		a.sess.compactionMu.Lock()
		valid := projectionValid(a.sess.compactionState, canonical)
		a.sess.compactionMu.Unlock()
		if valid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("content_mismatch episode did not rebuild a valid projection in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The projection is authoritative again: the view comes from the valid
	// branch, far below the window, and the stale episode is over.
	view := a.modelVisibleMessages()
	if a.staleProjectionServing() {
		t.Fatal("a valid projection must end the stale-serving episode")
	}
	if got := a.estimatedVisibleRequestTokens(view); got >= a.compactTrigger() {
		t.Fatalf("rebuild view %d must sit below the fold trigger %d", got, a.compactTrigger())
	}
	if a.sess.tailView.active() {
		t.Fatal("tail-view flag must be clear once the projection serves the view")
	}
}
