package control

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// Task 367 C2: the parent-side cascade decision must be auditable from the
// log alone — one grep-able line per Allow/Deny, naming the source contact
// and the tool. Captured through a slog handler around the default logger.

// 367-local Ask stub (named apart from the task-365 harness so both can land
// on the same controller package without a rename collision).
type cascadeAuditAsker func(q []event.AskQuestion) ([]event.AskAnswer, error)

func (f cascadeAuditAsker) Ask(_ context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	return f(questions)
}

func captureCascadeSlog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func cascadeDecisionController(selection string) *Controller {
	sink := &noticeSink{}
	return &Controller{
		sink:         sink,
		approvalTier: ApprovalTierHuman,
		onCascadeDelegate: func(string) (agent.Asker, string, bool) {
			return cascadeAuditAsker(func([]event.AskQuestion) ([]event.AskAnswer, error) {
				return []event.AskAnswer{{QuestionID: "q", Selected: []string{selection}}}, nil
			}), "sc_parent_src", true
		},
	}
}

func TestCascadeParentDecisionApprovedIsLogged(t *testing.T) {
	enableCascadeApproval(t)
	buf := captureCascadeSlog(t)
	c := cascadeDecisionController("Allow")
	// The interactive decision path is where the parent-side cascade lives
	// (requestApprovalDecisionWithOptions, task 225 at :6221) — the
	// unattended review in the 365 branch is a different function on a
	// different baseline.
	reply, err := c.requestApprovalDecisionWithOptions(context.Background(),
		"write_file", "src/config.go", json.RawMessage(`{"path":"src/config.go"}`), "apply the agreed rename", approvalDecisionOptions{})
	if err != nil || !reply.allow {
		t.Fatalf("parent Allow must allow: err=%v allow=%v", err, reply.allow)
	}
	out := buf.String()
	if !strings.Contains(out, "cascade approved by parent sc_parent_src") {
		t.Fatalf("the parent-side approval line must be grep-able, got: %s", out)
	}
	if !strings.Contains(out, "tool=write_file") {
		t.Fatalf("the decision line must carry the tool, got: %s", out)
	}
}

func TestCascadeParentDecisionDeniedIsLogged(t *testing.T) {
	enableCascadeApproval(t)
	buf := captureCascadeSlog(t)
	c := cascadeDecisionController("Deny")
	reply, err := c.requestApprovalDecisionWithOptions(context.Background(),
		"write_file", "src/config.go", json.RawMessage(`{"path":"src/config.go"}`), "apply the agreed rename", approvalDecisionOptions{})
	if err != nil || reply.allow {
		t.Fatalf("parent Deny must refuse: err=%v allow=%v", err, reply.allow)
	}
	out := buf.String()
	if !strings.Contains(out, "cascade denied by parent sc_parent_src") {
		t.Fatalf("the parent-side denial line must be grep-able, got: %s", out)
	}
	if !strings.Contains(out, "tool=write_file") {
		t.Fatalf("the denial line must carry the tool, got: %s", out)
	}
}
