package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// Task 365 C6: when the 15s unattended grace fires for a child session, the
// cascade gets one fresh evaluation before the tier machinery refuses. The
// create-path grant (task 365 C5) or a mail grant may have landed while the
// request was waiting; a hit means the task source decides, a miss falls
// through to the original tiers unchanged, and askRiskNeedsHuman never
// cascades at all.

func stubDelegate(respond func(q []event.AskQuestion) ([]event.AskAnswer, error)) func(string) (agent.Asker, string, bool) {
	return func(string) (agent.Asker, string, bool) {
		return stubAskerFunc(respond), "sc_parent", true
	}
}

type stubAskerFunc func(q []event.AskQuestion) ([]event.AskAnswer, error)

func (f stubAskerFunc) Ask(_ context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	return f(questions)
}

func TestReviewUnattendedApprovalCascadesToSourceBeforeTierRefusal(t *testing.T) {
	enableCascadeApproval(t)
	sink := &noticeSink{}
	decided := false
	c := &Controller{
		sink:              sink,
		approvalTier:      ApprovalTierHuman,
		onCascadeDelegate: stubDelegate(func(q []event.AskQuestion) ([]event.AskAnswer, error) {
			decided = true
			if len(q) != 1 || q[0].Prompt == "" {
				t.Fatalf("the source must receive the rendered approval question, got %+v", q)
			}
			return []event.AskAnswer{{Selected: []string{"Allow"}}}, nil
		}),
	}
	reply, decided2 := c.reviewUnattendedApproval(context.Background(),
		"write_file", "src/config.go", "apply the agreed rename", json.RawMessage(`{"path":"src/config.go"}`))
	if !decided2 || !reply.allow {
		t.Fatalf("a fresh cascade hit must let the task source allow: decided=%v allow=%v", decided2, reply.allow)
	}
	if !decided {
		t.Fatal("the delegate stub was never asked")
	}
	notice, ok := sink.lastNotice()
	if !ok || !strings.Contains(notice.Text, "cascaded to task source sc_parent") {
		t.Fatalf("the source-side audit line must name the source, got %+v", notice)
	}
}

func TestReviewUnattendedApprovalCascadeMissFallsBackToTiers(t *testing.T) {
	enableCascadeApproval(t)
	sink := &noticeSink{}
	c := &Controller{
		sink:              sink,
		approvalTier:      ApprovalTierHuman,
		onCascadeDelegate: stubDelegate(func([]event.AskQuestion) ([]event.AskAnswer, error) { return nil, errors.New("source unavailable") }),
	}
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "src/config.go", "apply the agreed rename", json.RawMessage(`{"path":"src/config.go"}`))
	if !decided || reply.allow {
		t.Fatalf("a cascade miss must fall back to the original tier logic (human refuses): decided=%v allow=%v", decided, reply.allow)
	}
	// The miss line and the tier refusal must BOTH be audited, in order:
	// the miss explains the fallback, the tier line is the fail-closed end.
	missIdx, tierIdx := -1, -1
	sink.mu.Lock()
	for i, ev := range sink.events {
		switch {
		case strings.Contains(ev.Text, "cascade re-evaluation missed"):
			missIdx = i
		case strings.Contains(ev.Text, "approval tier requires a human"):
			tierIdx = i
		}
	}
	sink.mu.Unlock()
	if missIdx == -1 || tierIdx == -1 || missIdx > tierIdx {
		t.Fatalf("miss line must precede the tier refusal: missIdx=%d tierIdx=%d", missIdx, tierIdx)
	}
}

func TestReviewUnattendedApprovalHighRiskNeverCascades(t *testing.T) {
	sink := &noticeSink{}
	asked := false
	c := &Controller{
		sink:              sink,
		approvalTier:      ApprovalTierHuman,
		onCascadeDelegate: stubDelegate(func([]event.AskQuestion) ([]event.AskAnswer, error) { asked = true; return nil, nil }),
	}
	reply, decided := c.reviewUnattendedApproval(context.Background(),
		"bash", "force push", "force push to origin main", json.RawMessage(`{"command":"git push --force origin main"}`))
	if !decided || reply.allow {
		t.Fatalf("high-risk asks stay refused: decided=%v allow=%v", decided, reply.allow)
	}
	if asked {
		t.Fatal("askRiskNeedsHuman work must never reach the cascade, even on retry")
	}
	notice, ok := sink.lastNotice()
	if !ok || !strings.Contains(notice.Text, "refused") {
		t.Fatalf("the high-risk refusal must be the original audit line, got %+v", notice)
	}
}
