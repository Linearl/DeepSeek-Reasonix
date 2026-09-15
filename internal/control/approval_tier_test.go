package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/guardian"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestNormalizeApprovalTier(t *testing.T) {
	cases := map[string]string{
		"":           ApprovalTierGuardian,
		"guardian":   ApprovalTierGuardian,
		"GUARDIAN":   ApprovalTierGuardian,
		"parent":     ApprovalTierParent,
		" Parent ":   ApprovalTierParent,
		"human":      ApprovalTierHuman,
		"nonsense":   ApprovalTierGuardian,
		"self-approve": ApprovalTierGuardian,
	}
	for raw, want := range cases {
		if got := NormalizeApprovalTier(raw); got != want {
			t.Errorf("NormalizeApprovalTier(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestReviewUnattendedApprovalParentTierApprovesReversible(t *testing.T) {
	sink := &noticeSink{}
	c := &Controller{sink: sink, approvalTier: ApprovalTierParent}
	// Reversible: a workspace-local write with no destructive markers.
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "src/config.go", "apply the agreed rename", json.RawMessage(`{"path":"src/config.go"}`))
	if !decided || !reply.allow {
		t.Fatalf("parent tier must approve reversible work: decided=%v allow=%v", decided, reply.allow)
	}
	notice, ok := sink.lastNotice()
	if !ok || !strings.Contains(notice.Text, "parent session") {
		t.Fatalf("parent decision must be audited, got %+v", notice)
	}
}

func TestReviewUnattendedApprovalParentTierStillRefusesHighRisk(t *testing.T) {
	sink := &noticeSink{}
	c := &Controller{sink: sink, approvalTier: ApprovalTierParent}
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"bash", "force push", "force push to origin main", json.RawMessage(`{"command":"git push --force origin main"}`))
	if !decided || reply.allow {
		t.Fatalf("parent tier must never self-approve destructive/outward work: decided=%v allow=%v", decided, reply.allow)
	}
	notice, ok := sink.lastNotice()
	if !ok || !strings.Contains(notice.Text, "refused") {
		t.Fatalf("high-risk refusal must be audited, got %+v", notice)
	}
}

func TestReviewUnattendedApprovalHumanTierRefusesReversible(t *testing.T) {
	sink := &noticeSink{}
	c := &Controller{sink: sink, approvalTier: ApprovalTierHuman}
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "notes.md", "update notes", json.RawMessage(`{"path":"notes.md"}`))
	if !decided || reply.allow {
		t.Fatalf("human tier must refuse unattended reversible work: decided=%v allow=%v", decided, reply.allow)
	}
}

func TestReviewUnattendedApprovalGuardianTierUsesReviewer(t *testing.T) {
	sink := &noticeSink{}
	// No reviewer configured: guardian tier fails closed.
	c := &Controller{sink: sink, approvalTier: ApprovalTierGuardian}
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "notes.md", "update notes", json.RawMessage(`{"path":"notes.md"}`))
	if !decided || reply.allow {
		t.Fatalf("guardian tier without a reviewer must refuse: decided=%v allow=%v", decided, reply.allow)
	}
}

// Scripted guardian proves the guardian tier still routes reversible work to
// the independent reviewer rather than inventing a parent-side yes.
func TestReviewUnattendedApprovalGuardianTierConsultsReviewer(t *testing.T) {
	sink := &noticeSink{}
	guardianProv := &recordingProvider{
		name:    "guardian",
		streams: [][]provider.Chunk{textTurn(`{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"safe local write"}`)},
	}
	reviewer := guardian.NewSession(guardianProv, tool.NewRegistry(), guardian.PolicyPrompt(), "guardian-test", 0, nil, event.Discard)
	c := &Controller{sink: sink, approvalTier: ApprovalTierGuardian, guardianSess: reviewer}
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "notes.md", "update notes", json.RawMessage(`{"path":"notes.md"}`))
	if !decided || !reply.allow {
		t.Fatalf("guardian allow should proceed: decided=%v allow=%v", decided, reply.allow)
	}
	notice, ok := sink.lastNotice()
	if !ok || !strings.Contains(notice.Text, "reviewer") {
		t.Fatalf("guardian decision must name the reviewer, got %+v", notice)
	}
	if len(guardianProv.requests) != 1 {
		t.Fatalf("guardian reviews = %d, want 1", len(guardianProv.requests))
	}
}
