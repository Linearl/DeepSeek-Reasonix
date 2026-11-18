package agent

import (
	"context"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// recallHarness builds an agent whose compaction state marks canonical[:covered]
// as folded, so recall reads against a frozen region without running a fold.
// Canonical layout: 0 system, 1 user request, 2 assistant+read-1 call,
// 3 read-1 result, 4 user live request, 5 assistant live answer.
func recallHarness(t *testing.T, covered int) (*Agent, []provider.Message) {
	t.Helper()
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "system stays"},
		{Role: provider.RoleUser, Content: "old request about the widget budget"},
		{Role: provider.RoleAssistant, Content: "planning the widget budget", ToolCalls: []provider.ToolCall{
			{ID: "read-1", Name: "read_file", Arguments: `{"path":"widgets.go"}`},
		}},
		{Role: provider.RoleTool, ToolCallID: "read-1", Name: "read_file", Content: "budget = 42"},
		{Role: provider.RoleUser, Content: "live request the fold kept verbatim"},
		{Role: provider.RoleAssistant, Content: "live answer"},
	}}
	a := New(&fakeProvider{reply: "summarized"}, tool.NewRegistry(), sess, Options{ArchiveDir: t.TempDir()}, event.Discard)
	canonical, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{
		SchemaVersion: compactionStateSchemaCurrent,
		Generation:    3,
		Projection: ContextProjection{
			Messages:          []provider.Message{formatSummaryMessage("digest of the first four")},
			CoveredCount:      covered,
			CoveredPrefixHash: coveredPrefixHash(canonical, covered),
			PinnedContextHash: pinnedContextCoverageHash(canonical, covered),
		},
	}
	a.sess.compactionMu.Unlock()
	return a, canonical
}

func TestRecallReadsFoldedSpanVerbatim(t *testing.T) {
	a, _ := recallHarness(t, 4)
	res, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1, 2}})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, want := range []string{
		"#1", "old request about the widget budget",
		"#2", "[assistant calls read_file] {\"path\":\"widgets.go\"}",
		"[tool read_file result]", "budget = 42",
	} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("recall text missing %q:\n%s", want, res.Text)
		}
	}
	if len(res.Recalled) != 2 || res.Recalled[0] != 1 || res.Recalled[1] != 2 {
		t.Fatalf("recalled = %v", res.Recalled)
	}
	if res.Tokens <= 0 || res.BudgetLeft >= a.recallBudget() {
		t.Fatalf("budget accounting did not move: tokens=%d left=%d budget=%d", res.Tokens, res.BudgetLeft, a.recallBudget())
	}
	a.sess.compactionMu.Lock()
	spent := a.sess.compactionState.Recall.SpentTokens
	gen := a.sess.compactionState.Recall.Generation
	a.sess.compactionMu.Unlock()
	if spent != res.Tokens || gen != 3 {
		t.Fatalf("ledger = gen %d spent %d, want gen 3 spent %d", gen, spent, res.Tokens)
	}
	// A second read charges the same generation's ledger.
	second, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1}})
	if err != nil {
		t.Fatalf("second recall: %v", err)
	}
	if second.BudgetLeft != res.BudgetLeft-second.Tokens {
		t.Fatalf("second left = %d, want %d", second.BudgetLeft, res.BudgetLeft-second.Tokens)
	}
}

func TestRecallRefusesNotFoldedPosition(t *testing.T) {
	a, _ := recallHarness(t, 4)
	_, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{4}})
	if err == nil || !strings.Contains(err.Error(), "not folded") || !strings.Contains(err.Error(), "still in your context") {
		t.Fatalf("want not-folded refusal, got %v", err)
	}
}

func TestRecallMissingPositionNamesTheAddress(t *testing.T) {
	a, _ := recallHarness(t, 4)
	res, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{99}})
	if err == nil || !strings.Contains(err.Error(), "#99") {
		t.Fatalf("want missing-address refusal, got %v / %+v", err, res)
	}
	if len(res.Missing) != 1 || res.Missing[0] != 99 {
		t.Fatalf("missing = %v", res.Missing)
	}
}

// Budget refusal is whole, never truncated: the ledger must not move.
func TestRecallBudgetRefusalIsWhole(t *testing.T) {
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "system stays"},
		{Role: provider.RoleUser, Content: strings.Repeat("huge folded tool output ", 1200)},
		{Role: provider.RoleUser, Content: "live request"},
	}}
	a := New(&fakeProvider{reply: "summarized"}, tool.NewRegistry(), sess, Options{ArchiveDir: t.TempDir()}, event.Discard)
	canonical, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{
		SchemaVersion: compactionStateSchemaCurrent,
		Generation:    1,
		Projection: ContextProjection{
			Messages:          []provider.Message{formatSummaryMessage("digest")},
			CoveredCount:      2,
			CoveredPrefixHash: coveredPrefixHash(canonical, 2),
			PinnedContextHash: pinnedContextCoverageHash(canonical, 2),
		},
	}
	a.sess.compactionMu.Unlock()
	res, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1}})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want budget refusal, got %v / %+v", err, res)
	}
	if res.Text != "" {
		t.Fatalf("budget refusal returned partial content: %q", res.Text)
	}
	a.sess.compactionMu.Lock()
	spent := a.sess.compactionState.Recall.SpentTokens
	a.sess.compactionMu.Unlock()
	if spent != 0 {
		t.Fatalf("refusal spent %d tokens", spent)
	}
}

// A new compaction generation grants a fresh ledger.
func TestRecallLedgerResetsPerGeneration(t *testing.T) {
	a, _ := recallHarness(t, 4)
	if _, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1}}); err != nil {
		t.Fatalf("recall: %v", err)
	}
	a.sess.compactionMu.Lock()
	a.sess.compactionState.Generation = 9
	a.sess.compactionMu.Unlock()
	second, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1}})
	if err != nil {
		t.Fatalf("recall after regen: %v", err)
	}
	if second.BudgetLeft != a.recallBudget()-second.Tokens {
		t.Fatalf("generation did not reset: left=%d budget=%d spent=%d", second.BudgetLeft, a.recallBudget(), second.Tokens)
	}
}

func TestRecallNothingFoldedRefuses(t *testing.T) {
	a, _ := recallHarness(t, 0)
	_, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1}})
	if err == nil || !strings.Contains(err.Error(), "nothing has been folded") {
		t.Fatalf("want nothing-folded refusal, got %v", err)
	}
}

// Search returns canonical addresses a read can use directly.
func TestRecallSearchReturnsReadableAddresses(t *testing.T) {
	a, _ := recallHarness(t, 4)
	res, err := a.RecallContext(context.Background(), tool.RecallRequest{Query: "widget budget"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("no hits:\n%s", res.Text)
	}
	found := false
	for _, h := range res.Hits {
		if h.Position == 1 && h.Kind == recallKindUser && strings.Contains(h.Snippet, "widget") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want #1 user_text hit, got %+v", res.Hits)
	}
	read, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{res.Hits[0].Position}})
	if err != nil {
		t.Fatalf("read after search: %v", err)
	}
	if !strings.Contains(read.Text, "widget budget") {
		t.Fatalf("read back lost the searched content:\n%s", read.Text)
	}
}

func TestRecallRejectsBothPositionsAndQuery(t *testing.T) {
	a, _ := recallHarness(t, 4)
	_, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{1}, Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("want both-forms refusal, got %v", err)
	}
}

// pointerE2ESession mirrors the compress_tool_test shape: enough verbatim bulk
// that foldEconomics engages, an anchor user turn, and a live tail. The
// package's countingProvider records every summarizer request, so the pointer
// lane's zero-request claim is measurable as len(prov.got).
func pointerE2ESession() *Session {
	large := strings.Repeat("old tool output ", 160)
	return &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "system stays"},
		{Role: provider.RoleUser, Content: "old request alpha with the widget budget"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("analysis ", 160)},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read_file", Arguments: `{"path":"a"}`}}},
		{Role: provider.RoleTool, ToolCallID: "read-1", Name: "read_file", Content: large},
		{Role: provider.RoleUser, Content: "unique boundary request"},
		{Role: provider.RoleAssistant, Content: "tail stays byte-for-byte"},
	}}
}

// The zero-summary lane folds with no provider request, installs a pointer
// message the existing summary-tag contract recognizes, records the
// content-hash pointer in the receipt, and the transcript is untouched. The
// index addresses read back verbatim through recall. Driven through the
// automatic compaction entry — the compress TOOL path keeps its digest on
// purpose (the model's focus instruction is digest material).
func TestZeroSummaryPointerLaneEndToEnd(t *testing.T) {
	prov := &countingProvider{reply: "this summarizer must never run"}
	sess := pointerE2ESession()
	a := New(prov, tool.NewRegistry(), sess, Options{ArchiveDir: t.TempDir()}, event.Discard)
	on := true
	a.zeroSummaryOverride = &on
	before := sess.Snapshot()

	if err := a.compact(context.Background(), CompactionTriggerManual, "", true); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if reqs := len(prov.got); reqs != 0 {
		t.Fatalf("pointer lane made %d provider requests", reqs)
	}
	state := a.sess.compactionState
	if state.LastReceipt == nil || !strings.HasPrefix(state.LastReceipt.FoldPointer, "fold-") {
		t.Fatalf("receipt = %+v", state.LastReceipt)
	}
	if state.LastReceipt.Action != "pointer" {
		t.Fatalf("receipt action = %q", state.LastReceipt.Action)
	}
	if !reflect.DeepEqual(sess.Snapshot(), before) {
		t.Fatal("pointer fold changed the canonical transcript")
	}
	// The pointer message rides the summary-tag wrapper, so the existing
	// digest contract (isCompactionSummary, projection install) still holds.
	visible := a.modelVisibleMessages()
	var pointer string
	for _, m := range visible {
		if isCompactionSummary(m) {
			pointer = m.Content
		}
	}
	if pointer == "" {
		t.Fatalf("no summary-tagged pointer message installed: %+v", joinContents(visible))
	}
	if !strings.Contains(pointer, "Folded work index") || !strings.Contains(pointer, "recall(positions=[n])") {
		t.Fatalf("pointer document malformed:\n%s", pointer)
	}
	if strings.Contains(pointer, strings.Repeat("old tool output ", 160)) {
		t.Fatal("pointer inlined the folded content")
	}
	// Every index line is a live address: read it back verbatim.
	addrRe := regexp.MustCompile(`#(\d+) `)
	matches := addrRe.FindStringSubmatch(pointer)
	if matches == nil {
		t.Fatalf("pointer carries no addresses:\n%s", pointer)
	}
	addr, err := strconv.Atoi(matches[1])
	if err != nil {
		t.Fatalf("parse address %q: %v", matches[1], err)
	}
	read, err := a.RecallContext(context.Background(), tool.RecallRequest{Positions: []int{addr}})
	if err != nil {
		t.Fatalf("read index address #%d: %v", addr, err)
	}
	if !strings.Contains(read.Text, "widget budget") && !strings.Contains(read.Text, "read_file") {
		t.Fatalf("read back missing indexed content:\n%s", read.Text)
	}
}

// With the lane off, the summary path is byte-identical to the baseline: the
// digest runs through the provider, the receipt names no pointer, and the
// transcript is untouched.
func TestSummaryLaneUnchangedWhenPointerLaneOff(t *testing.T) {
	prov := &countingProvider{reply: "old work summarized"}
	sess := pointerE2ESession()
	a := New(prov, tool.NewRegistry(), sess, Options{ArchiveDir: t.TempDir()}, event.Discard)
	off := false
	a.zeroSummaryOverride = &off
	before := sess.Snapshot()

	out, err := a.CompressContext(context.Background(), tool.CompressRequest{
		Direction: "before", Anchor: "unique boundary request", Focus: "keep the tail",
	})
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if out.Status != "ok" || out.Mode != CompactionModeSummarized {
		t.Fatalf("result = %+v", out)
	}
	if reqs := len(prov.got); reqs != 1 {
		t.Fatalf("summary lane made %d provider requests, want 1", reqs)
	}
	state := a.sess.compactionState
	if state.LastReceipt == nil || state.LastReceipt.FoldPointer != "" || state.LastReceipt.Action != "summary" {
		t.Fatalf("receipt = %+v", state.LastReceipt)
	}
	if !reflect.DeepEqual(sess.Snapshot(), before) {
		t.Fatal("summary fold changed the canonical transcript")
	}
	visible := a.modelVisibleMessages()
	if !hasCompactionSummary(visible) || !strings.Contains(joinContents(visible), "unique boundary request") {
		t.Fatalf("projection lost the retained tail: %+v", joinContents(visible))
	}
}
