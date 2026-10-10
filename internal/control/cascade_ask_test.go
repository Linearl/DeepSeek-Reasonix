package control

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/turnevent"
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

// ── task 732 (fork issue #40): the delegated wait must be visible in the turn
// ledger while it blocks. Before the stamp, a forwarded ask left the open
// turn's ledger silent for the whole bounded wait, and the doctor's
// responsiveness reader classified that silence as a hang.

// cascadeWaitLedgerFixture arms the switch, opens a real ledger, and holds a
// turn open via the gated runner (the shape Ask runs in: an in_progress turn
// with the model blocked).
func cascadeWaitLedgerFixture(t *testing.T) (*Controller, *turnEventGateRunner, chan event.Event, *turnevent.Ledger) {
	t.Helper()
	dir := t.TempDir()
	runner := &turnEventGateRunner{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan event.Event, 1)
	c := New(Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- e
			}
		}),
		SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl"),
		ApprovalTimeout: 120 * time.Millisecond,
	})
	t.Cleanup(c.Close)
	c.Submit("run")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	ledger := c.turnEventLedger()
	if ledger == nil {
		t.Fatal("controller did not open a turn ledger")
	}
	return c, runner, done, ledger
}

func TestCascadeWaitLedgerMarksWaitingUserDuringDelegateWait(t *testing.T) {
	enableCascadeApproval(t)
	c, runner, done, ledger := cascadeWaitLedgerFixture(t)

	stub := &cascadeStubAsker{block: true}
	c.onCascadeDelegate = func(string) (agent.Asker, string, bool) {
		return stub, "src-contact", true
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	askDone := make(chan struct{})
	go func() {
		defer close(askDone)
		_, _ = c.Ask(ctx, askProbeQuestions())
	}()

	// Mid-wait: the delegate is blocked, no local ask exists, and the ledger
	// must say waiting_user — exactly the state the doctor reads.
	deadline := time.Now().Add(5 * time.Second)
	for ledger.CurrentStatus() != event.TurnWaitingUser {
		if time.Now().After(deadline) {
			t.Fatalf("ledger status during cascade wait = %q, want waiting_user", ledger.CurrentStatus())
		}
		time.Sleep(5 * time.Millisecond)
	}
	records, err := c.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("TurnEventsAfter: %v", err)
	}
	// The wait stamp (turn_status) and its naming notice both carry
	// waiting_user; whichever landed last, the trail must hold the stamp
	// itself — that record is what the doctor's reader keys on.
	if records[len(records)-1].Status != event.TurnWaitingUser {
		t.Fatalf("last ledger record = %s/%s, want status waiting_user",
			records[len(records)-1].Kind, records[len(records)-1].Status)
	}
	stamped := false
	named := false
	for _, rec := range records {
		if rec.Kind == "turn_status" && rec.Status == event.TurnWaitingUser {
			stamped = true
		}
		if rec.Kind == "notice" && strings.Contains(rec.Event.Text, "cascade") && strings.Contains(rec.Event.Text, "src-contact") {
			named = true
		}
	}
	if !stamped {
		t.Fatal("turn_status(waiting_user) stamp missing — the doctor would have nothing to read")
	}
	if !named {
		t.Fatal("cascade wait notice missing — a waiting_user record without a local ask must stay explainable in the transcript")
	}

	cancel()
	select {
	case <-askDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ask did not return after the delegate wait was cancelled")
	}
	// The restore stamp lands before Ask returns to its caller.
	deadline = time.Now().Add(5 * time.Second)
	for ledger.CurrentStatus() != event.TurnInProgress {
		if time.Now().After(deadline) {
			t.Fatalf("ledger status after the delegate wait = %q, want the in_progress restore", ledger.CurrentStatus())
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(runner.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
}

// A delegate answer must hand back a turn whose ledger reads in_progress
// again — the wait window stays in the record trail, the stuck flag does not.
func TestCascadeWaitLedgerRestoresWhenDelegateAnswers(t *testing.T) {
	enableCascadeApproval(t)
	c, runner, done, ledger := cascadeWaitLedgerFixture(t)

	stub := &cascadeStubAsker{answers: []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}}}
	c.onCascadeDelegate = func(string) (agent.Asker, string, bool) {
		return stub, "src-contact", true
	}

	got, err := c.Ask(context.Background(), askProbeQuestions())
	if err != nil {
		t.Fatalf("delegated ask: %v", err)
	}
	if len(got) != 1 || len(got[0].Selected) != 1 || got[0].Selected[0] != "A" {
		t.Fatalf("answers = %+v, want the delegate's answer", got)
	}
	if ledger.CurrentStatus() != event.TurnInProgress {
		t.Fatalf("ledger status after the delegate answered = %q, want the in_progress restore", ledger.CurrentStatus())
	}
	records, err := c.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("TurnEventsAfter: %v", err)
	}
	flipped, restored := false, false
	for _, rec := range records {
		if rec.Kind != "turn_status" {
			continue
		}
		if rec.Status == event.TurnWaitingUser {
			flipped = true
		}
		if flipped && rec.Status == event.TurnInProgress {
			restored = true
		}
	}
	if !flipped || !restored {
		t.Fatalf("wait window trail incomplete: flipped=%v restored=%v, want both turn_status stamps", flipped, restored)
	}
	close(runner.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
}
