package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// oversizedCanonical builds a transcript over `pairs` tool-call/result units on
// top of a system head, closing with a small recent exchange. Each tool result
// is `lines` x "line\n" (~lines/4 tokens by the estimator).
func oversizedCanonical(pairs, lines int) []provider.Message {
	big := strings.Repeat("line\n", lines)
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "system"}}
	for i := range pairs {
		id := fmt.Sprintf("old-%d", i)
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: "{}"}}},
			provider.Message{Role: provider.RoleTool, ToolCallID: id, Name: "read_file", Content: big},
		)
	}
	msgs = append(msgs,
		provider.Message{Role: provider.RoleUser, Content: "recent question"},
		provider.Message{Role: provider.RoleAssistant, Content: "recent answer"},
	)
	return msgs
}

// assertPairingsIntact fails when a tool result in view has no matching
// assistant tool call before it, or an assistant tool call's results were cut.
func assertPairingsIntact(t *testing.T, view []provider.Message) {
	t.Helper()
	open := map[string]bool{}
	for _, m := range view {
		switch {
		case len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				open[tc.ID] = true
			}
		case m.Role == provider.RoleTool:
			if !open[m.ToolCallID] {
				t.Fatalf("orphan tool result %q in view: its assistant call was cut", m.ToolCallID)
			}
		}
	}
}

func TestTailWindowViewKeepsRecentUnitsAndPairings(t *testing.T) {
	const window = 10_000
	msgs := oversizedCanonical(60, 200) // ~6000+ tokens of tool results
	a := New(&fakeProvider{}, tool.NewRegistry(), &Session{Messages: msgs}, Options{
		ContextWindow: window,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	target := a.tailViewTarget()
	if target <= 0 || target >= a.hardInputCeiling() {
		t.Fatalf("tail target %d must sit strictly below the hard ceiling %d", target, a.hardInputCeiling())
	}
	view, dropped := a.tailWindowView(msgs, target)
	if dropped <= 0 {
		t.Fatalf("expected a degrade to drop units, got dropped=%d", dropped)
	}
	if len(view) >= len(msgs) {
		t.Fatalf("degraded view must be shorter than canonical: %d vs %d", len(view), len(msgs))
	}
	if view[0].Role != provider.RoleSystem {
		t.Fatalf("pinned system head must survive, got first role %q", view[0].Role)
	}
	markerSeen, recentKept := false, false
	for _, m := range view {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, "truncated to fit the context window") {
			markerSeen = true
		}
		if m.Content == "recent question" || m.Content == "recent answer" {
			recentKept = true
		}
	}
	if !markerSeen {
		t.Fatal("degraded view must carry the truncation marker")
	}
	if !recentKept {
		t.Fatal("the newest exchange must survive the degrade")
	}
	assertPairingsIntact(t, view)
}

func TestBoundedTailViewDegradesOversizeCanonical(t *testing.T) {
	const window = 10_000
	msgs := oversizedCanonical(60, 200)
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{}, tool.NewRegistry(), sess, Options{
		ContextWindow: window,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	_, version := sess.snapshotMessagesVersion()
	hard := a.hardInputCeiling()
	if a.estimatedVisibleRequestTokens(msgs) < hard {
		t.Fatal("fixture must outgrow the hard ceiling")
	}
	view := a.boundedTailView(msgs, version)
	if len(view) == len(msgs) {
		t.Fatal("oversize canonical must degrade to the tail view")
	}
	if got := a.estimatedVisibleRequestTokens(view); got >= hard {
		t.Fatalf("degraded view %d tokens must sit below the hard ceiling %d", got, hard)
	}
	if !a.sess.tailView.active() {
		t.Fatal("degrade must arm the tail-view flag")
	}
	// Same version: cached view comes back unchanged.
	again := a.boundedTailView(msgs, version)
	if len(again) != len(view) {
		t.Fatalf("cached view changed size: %d vs %d", len(again), len(view))
	}
	// Append moves the version: re-evaluation still bounds the view.
	grown := append(append([]provider.Message(nil), msgs...), provider.Message{Role: provider.RoleUser, Content: strings.Repeat("more\n", 500)})
	sess.Rewrite(grown, "append")
	v2 := sess.TranscriptVersion()
	view2 := a.boundedTailView(grown, v2)
	if got := a.estimatedVisibleRequestTokens(view2); got >= hard {
		t.Fatalf("re-evaluated view %d tokens must stay below the hard ceiling %d", got, hard)
	}
}

func TestBoundedTailViewPassthroughUnderCeiling(t *testing.T) {
	const window = 100_000
	msgs := oversizedCanonical(6, 20) // tiny against a 100k window
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{}, tool.NewRegistry(), sess, Options{
		ContextWindow: window,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	_, version := sess.snapshotMessagesVersion()
	view := a.boundedTailView(msgs, version)
	if len(view) != len(msgs) {
		t.Fatalf("canonical below the ceiling must pass through: %d vs %d", len(view), len(msgs))
	}
	if a.sess.tailView.active() {
		t.Fatal("passthrough must not arm the tail-view flag")
	}
}

func TestModelVisibleMessagesClearsTailViewWhenProjectionValid(t *testing.T) {
	const window = 10_000
	msgs := oversizedCanonical(60, 200)
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{}, tool.NewRegistry(), sess, Options{
		ContextWindow: window,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	canonical, version := sess.snapshotMessagesVersion()
	if view := a.boundedTailView(canonical, version); len(view) == len(canonical) {
		t.Fatal("fixture must degrade first")
	}
	// Install a valid projection over the covered prefix: the view must come
	// back from the projection branch and the tail flag must clear.
	st := CompactionState{
		TranscriptVersion: version,
		PromptCacheKey:    a.currentPromptCacheKey(),
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "system"}, {Role: provider.RoleUser, Content: "digest of earlier work"}},
			TranscriptVersion: version,
			CoveredCount:      len(canonical) - 2,
			CoveredPrefixHash: coveredPrefixHash(canonical, len(canonical)-2),
		},
	}
	a.sess.compactionMu.Lock()
	a.sess.compactionState = st
	a.sess.compactionMu.Unlock()
	view := a.modelVisibleMessages()
	if a.sess.tailView.active() {
		t.Fatal("a valid projection must clear the tail-view flag")
	}
	if len(view) == 0 || view[1].Content != "digest of earlier work" {
		t.Fatalf("view must come from the projection branch, got len=%d", len(view))
	}
}

// The live regression shape: projection invalid, canonical 2.17x the window.
// Prepare must leave with a projection installed (not stuck behind the
// below-fold early return the bounded view would otherwise hit).
func TestPrepareInstallsFoldWhileTailViewActive(t *testing.T) {
	const window = 10_000
	msgs := oversizedCanonical(60, 200)
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{reply: "structured digest"}, tool.NewRegistry(), sess, Options{
		ContextWindow: window,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	canonical, version := sess.snapshotMessagesVersion()
	if view := a.boundedTailView(canonical, version); len(view) == len(canonical) {
		t.Fatal("fixture must degrade to the tail view first")
	}
	prepared, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	if len(st.Projection.Messages) == 0 {
		t.Fatal("prepare must install a projection while the tail view is active")
	}
	if st.LastReceipt == nil || st.LastReceipt.Status != "applied" {
		t.Fatalf("expected an applied receipt, got %+v", st.LastReceipt)
	}
	if prepared.InputTokens >= a.hardInputCeiling() {
		t.Fatalf("prepared view %d must sit below the hard ceiling", prepared.InputTokens)
	}
	// The projection is authoritative now: the tail flag cleared.
	if !projectionValid(st, canonical) {
		t.Fatal("expected the installed projection to validate")
	}
	_ = a.modelVisibleMessages()
	if a.sess.tailView.active() {
		t.Fatal("tail-view flag must clear once the projection serves the view")
	}
}
