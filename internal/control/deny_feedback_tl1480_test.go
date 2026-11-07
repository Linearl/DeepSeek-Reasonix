package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/permission"
	"reasonix/internal/sandbox"
)

// TestDenyFeedbackReachesToolResultTl1480 drives the real interactive gate:
// a deny answered through resolveApprovalLocked with card feedback must fold
// the feedback into the tool_result refusal the model sees.
func TestDenyFeedbackReachesToolResultTl1480(t *testing.T) {
	var c *Controller
	ids := make(chan string, 4)
	c = New(Options{
		Policy: permission.New("ask", nil, nil, nil),
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				ids <- e.Approval.ID
			}
		}),
	})
	defer c.Close()
	c.EnableInteractiveApproval()

	gate := c.newInteractiveGate()
	type checkResult struct {
		allow  bool
		reason string
	}
	results := make(chan checkResult, 1)
	go func() {
		allow, reason, err := gate.Check(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt"}`), false)
		if err != nil {
			t.Errorf("gate Check: %v", err)
			results <- checkResult{}
			return
		}
		results <- checkResult{allow: allow, reason: reason}
	}()

	id := <-ids
	if err := c.resolveApprovalLocked(id, false, sandbox.ApprovalScopeOnce, clampDenyFeedback(" 不要动 main 分支 ")); err != nil {
		t.Fatalf("resolveApprovalLocked: %v", err)
	}
	r := <-results
	if r.allow {
		t.Fatal("deny answered as allow")
	}
	if !strings.Contains(r.reason, "不要动 main 分支") {
		t.Fatalf("tool_result missing feedback: %q", r.reason)
	}
	if !strings.HasPrefix(r.reason, permission.UserDeclinedReason) {
		t.Fatalf("tool_result lost the declined sentence prefix: %q", r.reason)
	}
}

// TestDenyWithoutFeedbackKeepsLegacyToolResultTl1480 is the reverse pin: the
// empty-feedback deny (the legacy Approve path) must produce the exact legacy
// sentence — zero behavior change for 纯拒绝.
func TestDenyWithoutFeedbackKeepsLegacyToolResultTl1480(t *testing.T) {
	var c *Controller
	ids := make(chan string, 4)
	c = New(Options{
		Policy: permission.New("ask", nil, nil, nil),
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				ids <- e.Approval.ID
			}
		}),
	})
	defer c.Close()
	c.EnableInteractiveApproval()

	gate := c.newInteractiveGate()
	type checkResult struct {
		allow  bool
		reason string
	}
	results := make(chan checkResult, 1)
	go func() {
		allow, reason, err := gate.Check(context.Background(), "write_file", json.RawMessage(`{"path":"b.txt"}`), false)
		if err != nil {
			t.Errorf("gate Check: %v", err)
			results <- checkResult{}
			return
		}
		results <- checkResult{allow: allow, reason: reason}
	}()

	id := <-ids
	if err := c.approveChecked(id, false, false, false); err != nil {
		t.Fatalf("approveChecked: %v", err)
	}
	r := <-results
	if r.allow {
		t.Fatal("deny answered as allow")
	}
	if r.reason != permission.UserDeclinedReason {
		t.Fatalf("pure deny text drifted: %q", r.reason)
	}
}

// TestDenyFeedbackClampTl1480 covers the safety bound: rune clamp at
// maxDenyFeedback, whitespace trim, empty stays empty.
func TestDenyFeedbackClampTl1480(t *testing.T) {
	if got := clampDenyFeedback(""); got != "" {
		t.Fatalf("empty clamp = %q, want empty", got)
	}
	if got := clampDenyFeedback("  x  "); got != "x" {
		t.Fatalf("trim clamp = %q, want %q", got, "x")
	}
	clamped := clampDenyFeedback(strings.Repeat("字", maxDenyFeedback+500))
	if runes := []rune(clamped); len(runes) != maxDenyFeedback {
		t.Fatalf("clamped runes = %d, want %d", len(runes), maxDenyFeedback)
	}
	if withUserFeedback("base", "") != "base" {
		t.Fatalf("empty feedback must keep base byte-identical: %q", withUserFeedback("base", ""))
	}
	if got := withUserFeedback("base", "fb"); got != "base"+permission.UserFeedbackSuffix("fb") {
		t.Fatalf("feedback composition drifted: %q", got)
	}
}

// TestWriteAccessDenyFeedbackReachesReplyTl1480 pins the write-access deny
// arm: the card feedback rides approvalReply to the refusal composition site.
func TestWriteAccessDenyFeedbackReachesReplyTl1480(t *testing.T) {
	c := New(Options{
		Policy: permission.New("ask", nil, nil, nil),
		Sink:   event.Discard,
	})
	defer c.Close()
	c.EnableInteractiveApproval()

	id, reply := c.approval.registerWriteAccess("bash", "/tmp/demo", "needs write access", nil, nil)
	done := make(chan error, 1)
	go func() {
		done <- c.resolveApprovalLocked(id, false, sandbox.ApprovalScopeOnce, "这些目录不能放行")
	}()
	select {
	case r := <-reply:
		if r.allow {
			t.Fatal("write-access deny answered as allow")
		}
		if r.feedback != "这些目录不能放行" {
			t.Fatalf("write-access reply feedback = %q, want the card text", r.feedback)
		}
	case err := <-done:
		t.Fatalf("resolveApprovalLocked finished without a reply: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("resolveApprovalLocked: %v", err)
	}
}
