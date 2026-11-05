package control

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// newAdmissionConflictController builds a controller whose session file and
// writer-bound authority are ready for diverged-ladder probing. The caller
// latches its own admission error.
func newAdmissionConflictController(t *testing.T, path string) *Controller {
	t.Helper()
	writer, err := agent.AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	exec := agent.New(&streamlessProvider{}, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	if err := writer.Bind(exec.Session(), agent.NextSessionWriteGeneration()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Release)
	sink, _, _ := collectSink()
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		Sink:        sink,
		SessionDir:  filepath.Dir(path),
		SessionPath: path,
	})
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })
	return c
}

// latchDivergedAdmission arms the tl1409 subject directly: a diverged conflict
// latched into the admission error, the way a failed tool checkpoint leaves it.
func latchDivergedAdmission(c *Controller, conflict *agent.SessionSnapshotConflictError) {
	c.turnEvents.mu.Lock()
	c.turnEvents.err = conflict
	c.turnEvents.mu.Unlock()
}

func admissionError(t *testing.T, c *Controller) error {
	t.Helper()
	body := func(context.Context) error { return nil }
	fn := c.prepareTurnAdmission(body)
	return fn(context.Background())
}

func withFastDivergedLadder(t *testing.T) {
	t.Helper()
	oldLimit, oldBase, oldCap := turnAdmissionConflictRetryLimit, turnAdmissionConflictBackoffBase, turnAdmissionConflictBackoffCap
	turnAdmissionConflictRetryLimit = 3
	turnAdmissionConflictBackoffBase = time.Millisecond
	turnAdmissionConflictBackoffCap = 5 * time.Millisecond
	t.Cleanup(func() {
		turnAdmissionConflictRetryLimit = oldLimit
		turnAdmissionConflictBackoffBase = oldBase
		turnAdmissionConflictBackoffCap = oldCap
	})
}

func divergedConflict(path string, base, disk int64, diskMsgs, snapMsgs int) *agent.SessionSnapshotConflictError {
	return &agent.SessionSnapshotConflictError{
		Path:             path,
		Kind:             agent.SessionSnapshotConflictDiverged,
		ExistingMessages: diskMsgs,
		SnapshotMessages: snapMsgs,
		BaseRevision:     base,
		DiskRevision:     disk,
	}
}

// TestDivergedAdmissionTerminalAfterRetryLimit is acceptance ①: the retry
// ladder is bounded. Each admission inside the backoff window refuses without
// advancing the counter; each due attempt burns exactly one retry; after the
// limit the admission suspends terminally and later attempts stop retrying
// entirely (the counter freezes — no permanent background work, no fixed-
// cadence loop). The heal never succeeds here because the disk writer is
// ALIVE: it advanced the ledger again after the conflict was raised, so the
// takeover's disk-still-stale verification refuses every attempt — the exact
// shape that used to retry diverged saves forever.
func TestDivergedAdmissionTerminalAfterRetryLimit(t *testing.T) {
	withFastDivergedLadder(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "term.jsonl")

	// A real diverged history on disk, then the second writer advances it
	// again — the conflict fingerprint the admission will hold is stale from
	// that moment on, and every takeover attempt must be refused.
	live := agent.NewSession("sys")
	live.AddBatch(provider.Message{Role: "user", Content: "m1"}, provider.Message{Role: "assistant", Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	other.Add(provider.Message{Role: "user", Content: "other-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer save: %v", err)
	}
	live.AddBatch(provider.Message{Role: "user", Content: "live3"}, provider.Message{Role: "assistant", Content: "live4"})
	saveErr := live.SaveSnapshot(path)
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(saveErr, &conflict) || conflict == nil || conflict.Kind != agent.SessionSnapshotConflictDiverged {
		t.Fatalf("live save err = %v, want diverged conflict", saveErr)
	}
	// The disk writer is not dead: it writes again, past the fingerprint.
	other.Add(provider.Message{Role: "user", Content: "other-still-alive"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer follow-up save: %v", err)
	}

	c := newAdmissionConflictController(t, path)
	latchDivergedAdmission(c, conflict)

	countRetries := func() int {
		c.turnEvents.mu.RLock()
		defer c.turnEvents.mu.RUnlock()
		return c.turnEvents.conflictRetries
	}

	// First attempt: immediately due, burns retry 1.
	if err := admissionError(t, c); err == nil || !strings.Contains(err.Error(), "persist turn admission") {
		t.Fatalf("first admission err = %v, want persist turn admission failure", err)
	}
	if got := countRetries(); got != 1 {
		t.Fatalf("retries after first attempt = %d, want 1", got)
	}

	// Within the (tiny) backoff window the counter must not advance, no matter
	// how many times a submission source retries.
	for range 5 {
		if err := admissionError(t, c); err == nil {
			t.Fatal("admission inside the backoff window succeeded, want refusal")
		}
	}
	if got := countRetries(); got != 1 {
		t.Fatalf("retries advanced inside the backoff window: %d, want 1", got)
	}

	// Due attempts each burn exactly one retry until the limit suspends.
	deadline := time.Now().Add(2 * time.Second)
	for countRetries() < turnAdmissionConflictRetryLimit {
		if time.Now().After(deadline) {
			t.Fatalf("retry ladder never converged; retries=%d suspended=%v", countRetries(), c.divergedAdmissionSuspended())
		}
		_ = admissionError(t, c)
	}
	if !c.divergedAdmissionSuspended() {
		t.Fatal("retry budget spent but admission is not suspended")
	}

	// Terminal state: repeated submissions keep failing closed while the
	// ladder stays frozen — the field's fixed-cadence infinite loop is gone.
	for range 5 {
		err := admissionError(t, c)
		if err == nil || !strings.Contains(err.Error(), "reopen this session") {
			t.Fatalf("suspended admission err = %v, want reopen hint", err)
		}
	}
	if got := countRetries(); got != turnAdmissionConflictRetryLimit {
		t.Fatalf("retries moved after suspension: %d, want frozen at %d", got, turnAdmissionConflictRetryLimit)
	}
}

// TestDivergedAdmissionBackoffEscalates pins the exponential shape: the wait
// before attempt N grows with N and is capped.
func TestDivergedAdmissionBackoffEscalates(t *testing.T) {
	oldBase, oldCap := turnAdmissionConflictBackoffBase, turnAdmissionConflictBackoffCap
	turnAdmissionConflictBackoffBase = time.Second
	turnAdmissionConflictBackoffCap = 30 * time.Second
	t.Cleanup(func() {
		turnAdmissionConflictBackoffBase = oldBase
		turnAdmissionConflictBackoffCap = oldCap
	})
	if got := turnAdmissionConflictBackoffFor(1); got != time.Second {
		t.Fatalf("backoff(1) = %v, want 1s", got)
	}
	if got := turnAdmissionConflictBackoffFor(2); got != 2*time.Second {
		t.Fatalf("backoff(2) = %v, want 2s", got)
	}
	if got := turnAdmissionConflictBackoffFor(3); got != 4*time.Second {
		t.Fatalf("backoff(3) = %v, want 4s", got)
	}
	if got := turnAdmissionConflictBackoffFor(10); got != 30*time.Second {
		t.Fatalf("backoff(10) = %v, want the 30s cap", got)
	}
}

// TestDivergedAdmissionSelfHealClearsLatch is acceptance ②: a real on-disk
// diverged shape (dead second writer, live transcript ahead) latched into the
// admission error is healed by takeover on the first admission attempt — the
// latch clears, the takeover lands on disk, and admission proceeds normally.
func TestDivergedAdmissionSelfHealClearsLatch(t *testing.T) {
	withFastDivergedLadder(t)
	path := filepath.Join(t.TempDir(), "heal.jsonl")
	live := agent.NewSession("sys")
	live.AddBatch(provider.Message{Role: "user", Content: "m1"}, provider.Message{Role: "assistant", Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Second writer advances the ledger and dies.
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded.Add(provider.Message{Role: "user", Content: "dead-tail"})
	if err := loaded.SaveSnapshot(path); err != nil {
		t.Fatalf("dead writer save: %v", err)
	}

	// The live side keeps growing and hits the diverged refusal.
	live.AddBatch(provider.Message{Role: "user", Content: "live3"}, provider.Message{Role: "assistant", Content: "live4"})
	saveErr := live.SaveSnapshot(path)
	var conflict *agent.SessionSnapshotConflictError
	if !errors.As(saveErr, &conflict) || conflict == nil || conflict.Kind != agent.SessionSnapshotConflictDiverged {
		t.Fatalf("live save err = %v, want diverged conflict", saveErr)
	}

	c := newAdmissionConflictController(t, path)
	// The controller's executor session IS the live session (same shape as the
	// field: the runtime holds the growing transcript).
	c.executor.SetSession(live)
	latchDivergedAdmission(c, conflict)

	bodyRan := false
	body := func(context.Context) error { bodyRan = true; return nil }
	fn := c.prepareTurnAdmission(body)
	if fn == nil {
		t.Fatal("admission blocked after successful self-heal, want the body admitted")
	}
	if err := fn(context.Background()); err != nil {
		t.Fatalf("admitted body err = %v, want nil", err)
	}
	if !bodyRan {
		t.Fatal("admitted body did not run")
	}
	if err := c.turnEventLedgerError(); err != nil {
		t.Fatalf("admission latch still set after self-heal: %v", err)
	}
	if c.divergedAdmissionSuspended() {
		t.Fatal("self-healed admission must not suspend")
	}

	// No second-writer residue: the disk holds the live transcript at a
	// revision past the dead writer's.
	reloaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("reload after heal: %v", err)
	}
	if got := reloaded.Snapshot(); len(got) != 5 || got[4].Content != "live4" {
		t.Fatalf("reload = %d messages (last %q), want the live transcript", len(got), reloaded.Snapshot()[max(len(got)-1, 0)].Content)
	}
	revision, _, err := agent.SessionContentIdentity(path)
	if err != nil || !revision.RevisionKnown || revision.Revision != conflict.DiskRevision+1 {
		t.Fatalf("post-heal revision = (%v, known=%v), want %d", revision.Revision, revision.RevisionKnown, conflict.DiskRevision+1)
	}
}

// TestDivergedAdmissionRebindResetsTerminal pins the recovery exit: reopening
// the session (rebind) clears the suspension so the user's explicit action
// re-arms admission.
func TestDivergedAdmissionRebindResetsTerminal(t *testing.T) {
	withFastDivergedLadder(t)
	path := filepath.Join(t.TempDir(), "rebind.jsonl")
	c := newAdmissionConflictController(t, path)
	conflict := divergedConflict(path, 1, 2, 4, 6)
	latchDivergedAdmission(c, conflict)

	for c.divergedAdmissionSuspended() {
		_ = admissionError(t, c)
	}

	c.rebindTurnEvents(path)
	if c.divergedAdmissionSuspended() {
		t.Fatal("rebind did not clear the terminal suspension")
	}
	c.turnEvents.mu.RLock()
	retries := c.turnEvents.conflictRetries
	c.turnEvents.mu.RUnlock()
	if retries != 0 {
		t.Fatalf("rebind left retries=%d, want 0", retries)
	}
}

// TestTransientAdmissionKeepsTl1292 guards the boundary of the change: a
// non-conflict latched admission error must keep the tl1292 cooldown behavior
// untouched — no diverged retries, no suspension. The first admission attempt
// finds the cooldown satisfied (fresh controller, zero lastAdmissionFailure)
// and clears the latch — the tl1292 first-chance retry is exactly its contract.
// A transient error re-latched INSIDE the cooldown window must then be refused
// by the plain path until the window passes, never routed through the diverged
// ladder.
func TestTransientAdmissionKeepsTl1292(t *testing.T) {
	withFastDivergedLadder(t)
	path := filepath.Join(t.TempDir(), "transient.jsonl")
	c := newAdmissionConflictController(t, path)

	// First attempt: the zero cooldown is satisfied, tl1292 clears and
	// admission proceeds — the transient shape never touches the diverged
	// ladder.
	c.turnEvents.mu.Lock()
	c.turnEvents.err = errors.New("simulated transient write failure")
	c.turnEvents.mu.Unlock()
	if err := admissionError(t, c); err != nil {
		t.Fatalf("first transient admission err = %v, want tl1292 clear-and-proceed", err)
	}

	// Re-latch inside the cooldown window: refusal via the plain path.
	c.turnEvents.mu.Lock()
	c.turnEvents.err = errors.New("simulated transient write failure")
	c.turnEvents.mu.Unlock()
	if err := admissionError(t, c); err == nil || strings.Contains(err.Error(), "reopen this session") {
		t.Fatalf("transient admission inside cooldown err = %v, want plain persist turn admission failure", err)
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	if c.turnEvents.conflictRetries != 0 || c.turnEvents.conflictSuspended {
		t.Fatal("transient error leaked into the diverged ladder")
	}
}
