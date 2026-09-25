package control

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
)

// Task 225 round-2: the cascade delegate must answer per the source's own
// policy, give up boundedly when the source is unattended, and stop before
// the chain can cycle. The switch is experimental, so each test arms the
// isolated REASONIX_HOME config first (the same pattern config's own tests
// use).
func enableCascadeApproval(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load isolated config: %v", err)
	}
	if err := cfg.SetExperimentalCascadeApproval(true); err != nil {
		t.Fatalf("arm cascade approval: %v", err)
	}
	// SaveTo the isolated home: Save() with no SourcePath falls back to the
	// RELATIVE "reasonix.toml" — i.e. the package working directory — which
	// leaked a config file into internal/control the first time this ran.
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save isolated config: %v", err)
	}
	if !config.CascadeApprovalLive() {
		t.Fatal("cascade approval switch did not take effect in the isolated home")
	}
}

type cascadeStubAsker struct {
	calls   atomic.Int32
	block   bool
	answers []event.AskAnswer
}

func (s *cascadeStubAsker) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	s.calls.Add(1)
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.answers, nil
}

func cascadeAskFixture(t *testing.T, stub *cascadeStubAsker) *Controller {
	t.Helper()
	c := New(Options{Sink: &askProbeSink{}, SessionDir: t.TempDir(), ApprovalTimeout: 120 * time.Millisecond})
	if stub != nil {
		c.onCascadeDelegate = func(string) (agent.Asker, string, bool) {
			return stub, "src-contact", true
		}
	}
	return c
}

// The source's Ask answers directly — the delegate returns its answers to the
// child untouched (its own autopilot/user semantics decided inside).
func TestCascadeDelegateAnswersForTheChild(t *testing.T) {
	enableCascadeApproval(t)
	stub := &cascadeStubAsker{answers: []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}}}
	c := cascadeAskFixture(t, stub)

	got, err := c.Ask(context.Background(), askProbeQuestions())
	if err != nil {
		t.Fatalf("delegated ask: %v", err)
	}
	if len(got) != 1 || len(got[0].Selected) != 1 || got[0].Selected[0] != "A" {
		t.Fatalf("answers = %+v, want the delegate's answer", got)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("delegate calls = %d, want 1", stub.calls.Load())
	}
}

// An unattended (or wedged) source must not freeze the child: the delegate
// wait is bounded by the parent ctx deadline, the failure falls back to the
// local prompt, and the local prompt is itself bounded by ApprovalTimeout —
// the run ends with an error instead of hanging forever (the 264 stall).
func TestCascadeDelegateTimeoutFallsBackBounded(t *testing.T) {
	enableCascadeApproval(t)
	stub := &cascadeStubAsker{block: true}
	c := cascadeAskFixture(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Ask(ctx, askProbeQuestions())
	if err == nil {
		t.Fatal("expected the bounded run to end with an error, got answers")
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("delegate calls = %d, want the bounded attempt to reach it", stub.calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("ask took %v; the combined delegate+local bounds must keep it short", elapsed)
	}
}

// A chain that grants itself would otherwise stack Ask frames forever: once
// the stamped depth reaches the bound the prompt stays local and the delegate
// is never consulted.
func TestCascadeHopBoundKeepsPromptLocal(t *testing.T) {
	enableCascadeApproval(t)
	stub := &cascadeStubAsker{}
	c := cascadeAskFixture(t, stub)

	ctx := context.Background()
	for i := 0; i < agent.MaxCascadeHops(); i++ {
		ctx = agent.WithCascadeHop(ctx)
	}
	if !agent.CascadeHopExhausted(ctx) {
		t.Fatalf("hop depth %d must be exhausted at the bound %d", agent.MaxCascadeHops(), agent.MaxCascadeHops())
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := c.Ask(ctx, askProbeQuestions())
		if !errors.Is(err, context.DeadlineExceeded) && err == nil {
			t.Errorf("bounded local wait must end with an error, got %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hop-exhausted prompt must not block past the local bound")
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("delegate calls = %d, want 0 once the hop bound is reached", stub.calls.Load())
	}
}

// The sensitive-write approval path cascades the same way the Ask path does
// (the pre-225 gap the task's open question asked about: ask and permission
// prompts are NOT the same route). A source's "Allow" grants the request; a
// refusal or an unreachable source keeps the safe direction.
func TestCascadeApprovalAllowsFromSourceAnswer(t *testing.T) {
	enableCascadeApproval(t)
	stub := &cascadeStubAsker{answers: []event.AskAnswer{{QuestionID: "approval-write_file", Selected: []string{"Allow"}}}}
	c := cascadeAskFixture(t, stub)

	allow, _, err := c.requestApproval(context.Background(), "write_file", "/tmp/x", nil)
	if err != nil {
		t.Fatalf("approval: %v", err)
	}
	if !allow {
		t.Fatal("source Allow must grant the request")
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("delegate calls = %d, want 1", stub.calls.Load())
	}
}

// A denying source answer refuses without consulting the local UI, and an
// unreachable source falls back to the bounded local prompt — never a silent
// allow (225: a non-autopilot source shows its user; an offline source keeps
// the pre-225 local wait, which ApprovalTimeout bounds in this fixture).
func TestCascadeApprovalDenyAndFallback(t *testing.T) {
	enableCascadeApproval(t)

	deny := &cascadeStubAsker{answers: []event.AskAnswer{{QuestionID: "approval-push", Selected: []string{"Deny"}}}}
	cDeny := cascadeAskFixture(t, deny)
	allow, _, err := cDeny.requestApproval(context.Background(), "push", "origin main", nil)
	if err != nil {
		t.Fatalf("denied approval: %v", err)
	}
	if allow {
		t.Fatal("source Deny must refuse")
	}
	if deny.calls.Load() != 1 {
		t.Fatalf("deny delegate calls = %d, want 1", deny.calls.Load())
	}

	dead := &cascadeStubAsker{block: true}
	cDead := cascadeAskFixture(t, dead)
	// A wedged source is bounded by the parent deadline, not by the 10-minute
	// default: the child turn itself carries the deadline here, mirroring a
	// real child run whose ctx is cancelled or times out.
	parentCtx, cancelParent := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancelParent()
	start := time.Now()
	_, _, err = cDead.requestApproval(parentCtx, "write_file", "/tmp/y", nil)
	if err == nil {
		t.Fatal("unreachable source must end bounded with the local-prompt timeout, not silently allow")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("fallback took %v; combined bounds must keep it short", elapsed)
	}
}
