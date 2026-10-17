package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/tool"
)

func deltaTestSections(memory string) sessioncontext.Sections {
	return sessioncontext.Sections{
		Environment:       "go/linux",
		Workspace:         "ws",
		SkillsCatalog:     "skills",
		CapabilityCatalog: "catalog",
		BackgroundMemory:  memory,
	}
}

// Task 969 acceptance ③: identical content reuses the same block — a second
// turn with the same sections appends nothing, so the provider prefix (which
// ends at the cached session-context block) stays untouched.
func TestDeltaAppendSkipsUnchangedSections(t *testing.T) {
	sess := NewSession("system")
	a := New(nil, tool.NewRegistry(), sess, Options{}, event.Discard)
	full := sessioncontext.Build(deltaTestSections("memory v1"))

	ctx := WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: full})
	if !a.AppendTurnContext(ctx) {
		t.Fatal("first snapshot must append")
	}
	ctx = WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: full})
	if a.AppendTurnContext(ctx) {
		t.Fatal("unchanged snapshot appended a duplicate block")
	}
	if got := countSessionContexts(sess.Snapshot()); got != 1 {
		t.Fatalf("context count = %d, want 1", got)
	}
}

// Task 969 acceptance ④ (分节 digest): a memory-only change appends a delta
// that carries only the memory section — the standing catalog/environment
// bytes are not re-sent.
func TestDeltaAppendMemoryChangeResendsOnlyMemory(t *testing.T) {
	sess := NewSession("system")
	a := New(nil, tool.NewRegistry(), sess, Options{}, event.Discard)
	full := sessioncontext.Build(deltaTestSections("memory v1"))
	if !a.AppendTurnContext(WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: full})) {
		t.Fatal("first snapshot must append")
	}

	changed := sessioncontext.Build(deltaTestSections("memory v2"))
	if !a.AppendTurnContext(WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: changed})) {
		t.Fatal("memory change must append")
	}
	messages := sess.Snapshot()
	if got := countSessionContexts(messages); got != 2 {
		t.Fatalf("context count = %d, want 2", got)
	}
	delta := messages[len(messages)-1]
	if !sessioncontext.IsContent(delta.Content) {
		t.Fatalf("last message is not a snapshot: %+v", delta)
	}
	if !strings.Contains(delta.Content, "memory v2") {
		t.Fatalf("delta missing new memory:\n%s", delta.Content)
	}
	for _, absent := range []string{"catalog", "skills", "go/linux", "## Environment", "## Capability catalog"} {
		if strings.Contains(delta.Content, absent) {
			t.Fatalf("delta re-sent unchanged section %q:\n%s", absent, delta.Content)
		}
	}
	// Third turn: merged view already has memory v2 → nothing to append.
	if a.AppendTurnContext(WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: changed})) {
		t.Fatal("post-delta unchanged turn appended a duplicate block")
	}
}

// Merged-history evaluation: after [full, delta(memory)], a full snapshot whose
// only difference is the catalog compares against the merged view and appends a
// catalog-only delta — the merge must not treat the newest (memory-only)
// envelope as the whole state.
func TestDeltaAppendMergesAcrossFullThenDelta(t *testing.T) {
	sess := NewSession("system")
	a := New(nil, tool.NewRegistry(), sess, Options{}, event.Discard)
	full := sessioncontext.Build(deltaTestSections("memory v1"))
	if !a.AppendTurnContext(WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: full})) {
		t.Fatal("first snapshot must append")
	}
	memoryOnly := sessioncontext.BuildDelta(deltaTestSections("memory v2"), deltaTestSections("memory v1"))
	if memoryOnly.Content == "" {
		t.Fatal("expected a memory delta fixture")
	}
	if !a.AppendTurnContext(WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: sessioncontext.Build(deltaTestSections("memory v2"))})) {
		t.Fatal("memory change must append")
	}

	catalogChanged := deltaTestSections("memory v2")
	catalogChanged.CapabilityCatalog = "catalog v2"
	if !a.AppendTurnContext(WithTurnContextBundle(context.Background(), TurnContextBundle{Executor: sessioncontext.Build(catalogChanged)})) {
		t.Fatal("catalog change must append")
	}
	messages := sess.Snapshot()
	last := messages[len(messages)-1]
	if strings.Contains(last.Content, "memory v2") {
		t.Fatalf("catalog delta resent unchanged memory:\n%s", last.Content)
	}
	if !strings.Contains(last.Content, "catalog v2") {
		t.Fatalf("catalog delta missing new catalog:\n%s", last.Content)
	}
}

// Compaction carriers: with [full, delta] history, dropping only the newest
// envelope would orphan the delta's sections — the carrier set must include
// both messages (the full snapshot resolves what the delta omits).
func TestSessionContextCarriersKeepDeltaAncestry(t *testing.T) {
	full := sessioncontext.Build(deltaTestSections("memory v1"))
	fullMsg := HostGeneratedUserMessage(full.Content)
	delta := sessioncontext.BuildDelta(deltaTestSections("memory v2"), deltaTestSections("memory v1"))
	if delta.Content == "" {
		t.Fatal("expected a delta fixture")
	}
	deltaMsg := HostGeneratedUserMessage(delta.Content)
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "stable"},
		fullMsg,
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "answer"},
		deltaMsg,
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "second"},
	}
	carriers := sessionContextCarrierSet(messages)
	if len(carriers) != 2 || !carriers[1] || !carriers[4] {
		t.Fatalf("carrier set = %v, want full(1) + delta(4)", carriers)
	}

	// A lone full snapshot is its own complete carrier set.
	onlyFull := []provider.Message{fullMsg}
	set := sessionContextCarrierSet(onlyFull)
	if len(set) != 1 || !set[0] {
		t.Fatalf("single full carrier set = %v", set)
	}

	// Merged evaluation over the delta chain resolves every section.
	merged, ok := mergedTurnContextSections(messages)
	if !ok {
		t.Fatal("merged view not found")
	}
	if merged.BackgroundMemory != "memory v2" || merged.CapabilityCatalog != "catalog" ||
		merged.Environment != "go/linux" || merged.SkillsCatalog != "skills" {
		t.Fatalf("merged sections = %+v", merged)
	}
}
