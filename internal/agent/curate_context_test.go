package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// curateFixture seeds a session with two completed-turn tool results (their
// receipts recorded in the ledger) and an active turn the curate must not
// touch. Returns the agent, the two receipt ids, and the active-turn call id.
type curateFixture struct {
	a          *Agent
	sess       *Session
	receiptOne string
	receiptTwo string
	activeCall string
}

func newCurateFixture(t *testing.T) curateFixture {
	t.Helper()
	const activeCreatedAt = int64(99)
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "old request"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"a"}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: strings.Repeat("large tool output ", 120)},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-2", Name: "bash", Arguments: `{"command":"go test"}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-2", Name: "bash", Content: strings.Repeat("test log line ", 120)},
		{Role: provider.RoleUser, Content: "active request", CreatedAt: activeCreatedAt},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-active", Name: "bash", Arguments: `{}`}}},
	}}
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), sess, Options{}, event.Discard)
	a.activeTurnCreatedAt.Store(activeCreatedAt)
	rec1 := a.task.ledger.Record(evidence.Receipt{ToolCallID: "call-1", ToolName: "read_file", Success: true})
	rec2 := a.task.ledger.Record(evidence.Receipt{ToolCallID: "call-2", ToolName: "bash", Success: true})
	return curateFixture{a: a, sess: sess, receiptOne: rec1.ID, receiptTwo: rec2.ID, activeCall: "call-active"}
}

func TestCurateContextReplacesNamedResultsAndPreservesCanonical(t *testing.T) {
	fx := newCurateFixture(t)
	before := fx.sess.Snapshot()

	got, err := fx.a.CurateContext(context.Background(), tool.CurateRequest{Drop: []tool.CurateDrop{
		{ID: fx.receiptOne, Note: "file a listed 3 modules, all current"},
	}})
	if err != nil {
		t.Fatalf("CurateContext: %v", err)
	}
	if got.Status != "ok" || got.Replaced != 1 || got.ProjectionTokens >= got.SourceTokens {
		t.Fatalf("result = %+v", got)
	}
	if !reflect.DeepEqual(fx.sess.Snapshot(), before) {
		t.Fatal("curate changed the canonical transcript")
	}
	visible := fx.a.modelVisibleMessages()
	found := 0
	for _, msg := range visible {
		if msg.Role == provider.RoleTool && msg.ToolCallID == "call-1" {
			found++
			if msg.Content != "[curated] file a listed 3 modules, all current" || msg.Images != nil {
				t.Fatalf("curated result = %+v", msg)
			}
		}
		if msg.Role == provider.RoleTool && msg.ToolCallID == "call-2" && !strings.Contains(msg.Content, "test log line") {
			t.Fatalf("unnamed result was not preserved: %+v", msg)
		}
	}
	if found != 1 {
		t.Fatalf("named result missing from view (matches=%d): %+v", found, visible)
	}
	state := fx.a.sess.compactionState
	if state.Generation != 1 || state.Projection.ProjectionVersion != 1 || state.LastReceipt == nil {
		t.Fatalf("curate did not install a complete projection lineage: %+v", state)
	}
	if state.LastReceipt.Action != "curate" || state.LastReceipt.Status != "applied" || state.LastReceipt.Trigger != CompactionTriggerTool {
		t.Fatalf("receipt = %+v", state.LastReceipt)
	}
	if state.LastReceipt.SavedTokens <= 0 {
		t.Fatalf("receipt saved nothing: %+v", state.LastReceipt)
	}
	if fx.a.lastCurateAt.Load() == 0 {
		t.Fatal("successful curate did not stamp its rate-limit timestamp")
	}
	// The active turn must stay byte-for-byte.
	if last := visible[len(visible)-1]; last.ToolCalls[0].ID != "call-active" {
		t.Fatalf("active turn disturbed: %+v", last)
	}
}

func TestCurateContextRateLimitedIndependently(t *testing.T) {
	fx := newCurateFixture(t)
	first, err := fx.a.CurateContext(context.Background(), tool.CurateRequest{Drop: []tool.CurateDrop{
		{ID: fx.receiptOne, Note: "file a listed 3 modules"},
	}})
	if err != nil || first.Status != "ok" {
		t.Fatalf("first curate = %+v, err %v", first, err)
	}
	second, err := fx.a.CurateContext(context.Background(), tool.CurateRequest{Drop: []tool.CurateDrop{
		{ID: fx.receiptTwo, Note: "tests passed"},
	}})
	if err != nil {
		t.Fatalf("second curate: %v", err)
	}
	if second.Status != "rejected" || !strings.Contains(second.Reason, "prompt cache") {
		t.Fatalf("second curate = %+v, want a cache-guard rejection", second)
	}
	// Collapsing the window lets the next pass through: the limit is curate's
	// own, independent of the fold cooldown.
	defer func(prev time.Duration) { minCurateInterval = prev }(minCurateInterval)
	minCurateInterval = 0
	third, err := fx.a.CurateContext(context.Background(), tool.CurateRequest{Drop: []tool.CurateDrop{
		{ID: fx.receiptTwo, Note: "tests passed"},
	}})
	if err != nil || third.Status != "ok" {
		t.Fatalf("third curate = %+v, err %v", third, err)
	}
}

func TestCurateContextValidationAndResolutionErrors(t *testing.T) {
	fx := newCurateFixture(t)
	before := fx.sess.Snapshot()

	tests := []struct {
		name string
		req  tool.CurateRequest
		want string
	}{
		{"empty drop", tool.CurateRequest{}, "at least one"},
		{"too many entries", tool.CurateRequest{Drop: make([]tool.CurateDrop, maxCurateDropEntries+1)}, "exceeds 32"},
		{"empty id", tool.CurateRequest{Drop: []tool.CurateDrop{{ID: "  ", Note: "x"}}}, "id must not be empty"},
		{"empty note", tool.CurateRequest{Drop: []tool.CurateDrop{{ID: "r_x", Note: " "}}}, "note must not be empty"},
		{"long note", tool.CurateRequest{Drop: []tool.CurateDrop{{ID: "r_x", Note: strings.Repeat("a", maxCurateNoteBytes+1)}}}, "240 bytes"},
		{"duplicate receipt", tool.CurateRequest{Drop: []tool.CurateDrop{
			{ID: fx.receiptOne, Note: "a"}, {ID: fx.receiptOne, Note: "b"},
		}}, "named twice"},
		{"unknown receipt", tool.CurateRequest{Drop: []tool.CurateDrop{{ID: "r_nope", Note: "x"}}}, "not one this session issued"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fx.a.CurateContext(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
	// An active-turn receipt resolves to a live result and must be refused.
	recActive := fx.a.task.ledger.Record(evidence.Receipt{ToolCallID: "call-active", ToolName: "bash", Success: true})
	_, err := fx.a.CurateContext(context.Background(), tool.CurateRequest{Drop: []tool.CurateDrop{
		{ID: recActive.ID, Note: "still working"},
	}})
	if err == nil || !strings.Contains(err.Error(), "completed-turn") {
		t.Fatalf("active-turn curate error = %v, want completed-turn refusal", err)
	}
	if !reflect.DeepEqual(fx.sess.Snapshot(), before) || len(fx.a.sess.compactionState.Projection.Messages) != 0 {
		t.Fatal("failed curate changed state")
	}
}

func TestCurateContextWouldNotShrinkRejected(t *testing.T) {
	const activeCreatedAt = int64(99)
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "old request"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "bash", Content: "ok"},
		{Role: provider.RoleUser, Content: "active", CreatedAt: activeCreatedAt},
	}}
	a := New(&fakeProvider{reply: "unused"}, tool.NewRegistry(), sess, Options{}, event.Discard)
	a.activeTurnCreatedAt.Store(activeCreatedAt)
	rec := a.task.ledger.Record(evidence.Receipt{ToolCallID: "call-1", ToolName: "bash", Success: true})

	got, err := a.CurateContext(context.Background(), tool.CurateRequest{Drop: []tool.CurateDrop{
		{ID: rec.ID, Note: "the command reported success with no further output"},
	}})
	if err != nil {
		t.Fatalf("CurateContext: %v", err)
	}
	if got.Status != "rejected" || !strings.Contains(got.Reason, "would not be smaller") {
		t.Fatalf("result = %+v, want a would-not-shrink rejection", got)
	}
	if len(a.sess.compactionState.Projection.Messages) != 0 {
		t.Fatal("rejected curate installed a projection")
	}
}

// TestPlanCurateProjectionResolution pins the pure resolution rules: the
// active-turn boundary excludes live results, and two receipts for one call
// cannot both claim it.
func TestPlanCurateProjectionResolution(t *testing.T) {
	visible := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "bash"}}},
		{Role: provider.RoleTool, ToolCallID: "c1", Content: "out"},
		{Role: provider.RoleUser, Content: "active"},
		{Role: provider.RoleTool, ToolCallID: "c-active", Content: "live"},
	}
	ledger := evidence.NewLedger()
	r1 := ledger.Record(evidence.Receipt{ToolCallID: "c1", ToolName: "bash", Success: true})
	r2 := ledger.Record(evidence.Receipt{ToolCallID: "c1", ToolName: "bash", Success: true, Command: "second"})
	rLive := ledger.Record(evidence.Receipt{ToolCallID: "c-active", ToolName: "bash", Success: true})

	if _, err := planCurateProjection(ledger, visible, 3, []tool.CurateDrop{{ID: rLive.ID, Note: "x"}}); err == nil || !strings.Contains(err.Error(), "completed-turn") {
		t.Fatalf("live result error = %v", err)
	}
	if _, err := planCurateProjection(ledger, visible, 5, []tool.CurateDrop{{ID: r1.ID, Note: "a"}, {ID: r2.ID, Note: "b"}}); err == nil || !strings.Contains(err.Error(), "same tool result") {
		t.Fatalf("same-result error = %v", err)
	}
	plan, err := planCurateProjection(ledger, visible, 3, []tool.CurateDrop{{ID: r1.ID, Note: "done"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if note, ok := plan.indexes[2]; !ok || note != "done" {
		t.Fatalf("plan.indexes = %+v", plan.indexes)
	}
}
