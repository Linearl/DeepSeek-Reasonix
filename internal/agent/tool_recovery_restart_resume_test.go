package agent

import (
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 435: the restart resume chain (task 254 roster) settles a planned
// restart interruption's leftover effect records host-side, so the review
// panel 「中断的工具需要核实」 does not light up for a session that is being
// auto-resumed anyway. A genuine crash interruption never calls this and keeps
// the manual review — the two cases must stay distinguishable.

func restartResumeSession(records ...provider.ToolCallRecord) (*Session, *Agent) {
	var calls []provider.ToolCall
	for i := range records {
		calls = append(calls, provider.ToolCall{
			ID: records[i].Identity.CallID, Name: records[i].Identity.CanonicalTool,
			Arguments: string(records[i].Arguments), Recovery: &records[i],
		})
	}
	s := NewSession("")
	s.Messages = []provider.Message{{Role: provider.RoleAssistant, ID: "turn-1", ToolCalls: calls}}
	return s, New(nil, tool.NewRegistry(), s, Options{}, event.Discard)
}

// TestResolveInterruptedByRestartSettlesPendingEffects: every unresolved
// record — unknown-outcome write, failed-with-unknown-effect write, read-only
// straggler — is settled as not_started under the interrupted_by_restart
// resolution, and PendingToolRecovery (the panel/barrier face) empties. The
// record facts (tool, arguments, idempotency key) survive for after-the-fact
// reading.
func TestResolveInterruptedByRestartSettlesPendingEffects(t *testing.T) {
	write := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a1", CallID: "call-w", CanonicalTool: "write_file"}, State: provider.ToolRunUnknown, ReadOnly: false, Arguments: json.RawMessage(`{"path":"x"}`), IdempotencyKey: "k1"}
	failed := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a2", CallID: "call-f", CanonicalTool: "bash"}, State: provider.ToolRunFailed, ReadOnly: false, EffectSummary: "effect_unknown", Arguments: json.RawMessage(`{"command":"deploy"}`)}
	readonly := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a3", CallID: "call-r", CanonicalTool: "grep"}, State: provider.ToolRunUnknown, ReadOnly: true}
	s, a := restartResumeSession(write, failed, readonly)

	if got := a.ResolveInterruptedByRestart(); got != 3 {
		t.Fatalf("settled %d records, want 3", got)
	}
	if pending := a.PendingToolRecovery(); len(pending) != 0 {
		t.Fatalf("pending recovery must be empty after the settle: %+v", pending)
	}
	for id, want := range map[string]provider.ToolCallRecord{"call-w": write, "call-f": failed, "call-r": readonly} {
		got := s.toolRecoveryRecord(id)
		if got == nil {
			t.Fatalf("%s: record vanished", id)
		}
		if got.State != provider.ToolRunNotStarted {
			t.Fatalf("%s: state = %q, want not_started", id, got.State)
		}
		if got.Resolution != restartResumeResolution || got.ResolutionSource != "host" || got.ResolvedAt == 0 {
			t.Fatalf("%s: resolution = %q/%q/%d, want interrupted_by_restart/host/nonzero", id, got.Resolution, got.ResolutionSource, got.ResolvedAt)
		}
		if string(got.Arguments) != string(want.Arguments) {
			t.Fatalf("%s: record facts must survive: args %s -> %s", id, want.Arguments, got.Arguments)
		}
		if got.Identity.AttemptID != want.Identity.AttemptID {
			t.Fatalf("%s: attempt id must survive: %q -> %q", id, want.Identity.AttemptID, got.Identity.AttemptID)
		}
	}
	// Idempotent: a second settle finds nothing left to settle.
	if got := a.ResolveInterruptedByRestart(); got != 0 {
		t.Fatalf("second settle returned %d, want 0", got)
	}
}

// TestResolveInterruptedByRestartLeavesResolvedRecordsAlone: records already
// carrying a terminal state (completed, user-confirmed) are not touched — the
// settle only clears what is genuinely unresolved.
func TestResolveInterruptedByRestartLeavesResolvedRecordsAlone(t *testing.T) {
	completed := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a1", CallID: "call-c", CanonicalTool: "write_file"}, State: provider.ToolRunCompleted, FinishedAt: 5}
	confirmed := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "a2", CallID: "call-u", CanonicalTool: "bash"}, State: provider.ToolRunUserConfirmed, Resolution: "confirm", ResolutionSource: "user"}
	s, a := restartResumeSession(completed, confirmed)

	if got := a.ResolveInterruptedByRestart(); got != 0 {
		t.Fatalf("settled %d records, want 0 (nothing pending)", got)
	}
	if got := s.toolRecoveryRecord("call-c"); got == nil || got.State != provider.ToolRunCompleted {
		t.Fatalf("completed record must be untouched: %+v", got)
	}
	if got := s.toolRecoveryRecord("call-u"); got == nil || got.State != provider.ToolRunUserConfirmed || got.ResolutionSource != "user" {
		t.Fatalf("user-confirmed record must keep its provenance: %+v", got)
	}
}
