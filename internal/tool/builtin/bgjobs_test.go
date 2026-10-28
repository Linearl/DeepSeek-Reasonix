package builtin

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/jobs"
	"reasonix/internal/planmode"
)

// End-to-end through the actual tools: a background bash job runs under a manager
// injected on the context, the wait tool collects its output, and bash_output
// reads it — the same path the agent drives.
func TestBackgroundBashWaitAndOutput(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	ctx := jobs.WithManager(context.Background(), m)

	start, err := bash{}.Execute(ctx, []byte(`{"command":"printf hello; sleep 0.3","run_in_background":true}`))
	if err != nil {
		t.Fatalf("bash background: %v", err)
	}
	if !strings.Contains(start, "Started background job") {
		t.Fatalf("unexpected start message: %q", start)
	}

	// The job is registered and running synchronously before Execute returns.
	running := m.Running()
	if len(running) != 1 {
		t.Fatalf("want 1 running job, got %d", len(running))
	}
	id := running[0].ID

	// wait blocks until it finishes, then returns its output.
	wout, err := waitJob{}.Execute(ctx, []byte(`{"job_ids":["`+id+`"]}`))
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(wout, "done") || !strings.Contains(wout, "hello") {
		t.Errorf("wait output = %q, want it to report done with hello", wout)
	}

	// bash_output reads the buffered output (wait doesn't consume the read cursor).
	bo, err := bashOutput{}.Execute(ctx, []byte(`{"job_id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("bash_output: %v", err)
	}
	if !strings.Contains(bo, "hello") {
		t.Errorf("bash_output = %q, want hello", bo)
	}
}

// 任务1033: a background bash job must carry the FULL command as Detail (the
// capsule hover card's wrap source) while Label stays the 48-rune preview the
// status bar renders. Before Detail existed the preview was all the frontend
// ever received, so hover could never show the complete command.
func TestBackgroundBashCarriesFullCommandDetail(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	ctx := jobs.WithManager(context.Background(), m)

	command := strings.Repeat("npm run build --silent && node scripts/check-bundle-budget.mjs && ", 8) + "echo done"
	if len([]rune(command)) <= 48 {
		t.Fatalf("fixture shorter than the preview cap: %d", len([]rune(command)))
	}
	payload, err := json.Marshal(map[string]any{"command": command, "run_in_background": true})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	start, err := bash{}.Execute(ctx, payload)
	if err != nil {
		t.Fatalf("bash background: %v", err)
	}
	if !strings.Contains(start, "Started background job") {
		t.Fatalf("unexpected start message: %q", start)
	}
	running := m.Running()
	if len(running) != 1 {
		t.Fatalf("want 1 running job, got %d", len(running))
	}
	view := running[0]
	if view.Detail != command {
		t.Fatalf("View.Detail = %q, want the full %d-char command", view.Detail, len(command))
	}
	if view.Label == command || !strings.HasSuffix(view.Label, "…") {
		t.Fatalf("View.Label = %q, want the 48-rune preview with the ellipsis suffix", view.Label)
	}
	if _, err := (waitJob{}).Execute(ctx, []byte(`{"job_ids":["`+view.ID+`"]}`)); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestWaitMergesBackgroundEvidenceExactlyOnce(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	ledger := evidence.NewLedger()
	ctx := jobs.WithManager(context.Background(), m)
	ctx = jobs.WithSession(ctx, "session")
	ctx = evidence.WithLedger(ctx, ledger)

	j := m.StartForSession("session", "task", "writer", func(jobCtx context.Context, _ io.Writer) (string, error) {
		jobs.PublishEvidence(jobCtx, evidence.ChildEvidenceSummary{Receipts: []evidence.Receipt{{
			ToolName: "write_file",
			Success:  true,
			Mutation: true,
			Write:    true,
			Paths:    []string{"changed.go"},
		}}})
		return "done", nil
	})

	args := []byte(`{"job_ids":["` + j.ID + `"]}`)
	if _, err := (waitJob{}).Execute(ctx, args); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !ledger.Summary().HasMutation() {
		t.Fatal("wait did not merge the task job's mutation evidence")
	}
	firstLen := ledger.Len()
	if _, err := (waitJob{}).Execute(ctx, args); err != nil {
		t.Fatalf("second wait: %v", err)
	}
	if got := ledger.Len(); got != firstLen {
		t.Fatalf("second wait duplicated evidence: len %d -> %d", firstLen, got)
	}
}

func TestWaitWithoutLedgerDoesNotConsumeBackgroundEvidence(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	baseCtx := jobs.WithSession(jobs.WithManager(context.Background(), m), "session")
	j := m.StartForSession("session", "task", "writer", func(jobCtx context.Context, _ io.Writer) (string, error) {
		jobs.PublishEvidence(jobCtx, evidence.ChildEvidenceSummary{Receipts: []evidence.Receipt{{
			ToolName: "write_file", Success: true, Mutation: true, Write: true, Paths: []string{"changed.go"},
		}}})
		return "done", nil
	})
	args := []byte(`{"job_ids":["` + j.ID + `"]}`)
	if _, err := (waitJob{}).Execute(baseCtx, args); err != nil {
		t.Fatalf("wait without ledger: %v", err)
	}

	ledger := evidence.NewLedger()
	if _, err := (waitJob{}).Execute(evidence.WithLedger(baseCtx, ledger), args); err != nil {
		t.Fatalf("wait with ledger: %v", err)
	}
	if !ledger.Summary().HasMutation() {
		t.Fatal("wait without a ledger consumed evidence before a collecting turn could merge it")
	}
}

func TestWaitInPlanModeDefersBackgroundEvidence(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	baseCtx := jobs.WithSession(jobs.WithManager(context.Background(), m), "session")
	j := m.StartForSession("session", "task", "writer", func(jobCtx context.Context, _ io.Writer) (string, error) {
		jobs.PublishEvidence(jobCtx, evidence.ChildEvidenceSummary{Receipts: []evidence.Receipt{{
			ToolName: "write_file", Success: true, Mutation: true, Write: true, Paths: []string{"changed.go"},
		}}})
		return "done", nil
	})
	args := []byte(`{"job_ids":["` + j.ID + `"]}`)

	// A planning turn may wait on jobs, but merging mutation receipts there
	// would arm delivery sign-off demands the read-only turn cannot satisfy.
	planLedger := evidence.NewLedger()
	planCtx := planmode.WithActive(evidence.WithLedger(baseCtx, planLedger), true)
	if _, err := (waitJob{}).Execute(planCtx, args); err != nil {
		t.Fatalf("wait in plan mode: %v", err)
	}
	if planLedger.Summary().HasMutation() {
		t.Fatal("plan-mode wait merged mutation evidence into the planning turn")
	}

	// The evidence stays on the job for the first normal turn to collect.
	ledger := evidence.NewLedger()
	normalCtx := planmode.WithActive(evidence.WithLedger(baseCtx, ledger), false)
	if _, err := (waitJob{}).Execute(normalCtx, args); err != nil {
		t.Fatalf("wait after plan mode: %v", err)
	}
	if !ledger.Summary().HasMutation() {
		t.Fatal("plan-mode wait consumed the background evidence instead of deferring it")
	}
}

// kill_shell terminates a long-running background job.
func TestBackgroundKill(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	ctx := jobs.WithManager(context.Background(), m)

	if _, err := (bash{}).Execute(ctx, []byte(`{"command":"sleep 120","run_in_background":true}`)); err != nil {
		t.Fatalf("bash background: %v", err)
	}
	id := m.Running()[0].ID

	kout, err := killShell{}.Execute(ctx, []byte(`{"job_id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("kill_shell: %v", err)
	}
	if !strings.Contains(kout, "Killed") {
		t.Errorf("kill_shell = %q, want it to report Killed", kout)
	}
	// 120s natural duration keeps the job far from finishing on its own, so the
	// reap window is the only thing this measures: a loaded machine's slow
	// process-tree teardown (up to ~bashWaitDelay) still fits, while a genuinely
	// broken kill trips the 40s timeout. Pairing the sleep with the timeout (as
	// 10/10 did) raced natural completion against the reap.
	res := m.Wait(ctx, []string{id}, 40)
	if len(res) != 1 || res[0].Status != jobs.Killed {
		t.Fatalf("want killed, got %+v", res)
	}
}

// kill_shell flips a job's status to Killed synchronously, well before its
// cancelled run goroutine actually unwinds, flushes PublishEvidence, and closes
// done. A bash_output poll that lands in that window must not note an empty
// lease: the ledger's lease is idempotent per turn, so noting it early would
// dedupe away every later retry in this same turn while the job's real
// mutation evidence is still forthcoming — and a turn that goes on to deliver
// would then commit (permanently drain) evidence nobody ever merged or
// reviewed. This deterministically drives that exact window with channels
// instead of a timing race.
func TestKilledJobBashOutputDoesNotNoteLeaseBeforeEvidenceIsReady(t *testing.T) {
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	ledger := evidence.NewLedger()
	ctx := jobs.WithManager(context.Background(), m)
	ctx = jobs.WithSession(ctx, "session")
	ctx = evidence.WithLedger(ctx, ledger)

	cancelSeen := make(chan struct{})
	release := make(chan struct{})
	j := m.StartForSession("session", "task", "writer", func(jobCtx context.Context, _ io.Writer) (string, error) {
		<-jobCtx.Done()
		close(cancelSeen)
		// Simulate a job that keeps unwinding (e.g. a subprocess still tearing
		// down) after cancellation is requested but before it actually returns.
		<-release
		jobs.PublishEvidence(jobCtx, evidence.ChildEvidenceSummary{Receipts: []evidence.Receipt{{
			ToolName: "write_file", Success: true, Mutation: true, Write: true, Paths: []string{"changed.go"},
		}}})
		return "", context.Canceled
	})

	if _, err := (killShell{}).Execute(ctx, []byte(`{"job_id":"`+j.ID+`"}`)); err != nil {
		t.Fatalf("kill_shell: %v", err)
	}
	<-cancelSeen // the goroutine observed the cancellation but has not returned

	// bash_output lands in the unwinding window: status already reports Killed,
	// but the job's done channel is not closed yet and no evidence exists.
	bo, err := bashOutput{}.Execute(ctx, []byte(`{"job_id":"`+j.ID+`"}`))
	if err != nil {
		t.Fatalf("bash_output during unwind: %v", err)
	}
	if !strings.Contains(bo, "killed") {
		t.Fatalf("bash_output during unwind = %q, want killed status", bo)
	}
	if ledger.Summary().HasMutation() {
		t.Fatal("bash_output merged mutation evidence before the job was ready")
	}
	if leases := ledger.BackgroundLeases(); len(leases) != 0 {
		t.Fatalf("bash_output noted a lease before the job was ready: %+v", leases)
	}

	close(release)
	if res := m.WaitForSession(context.Background(), "session", []string{j.ID}, 5); len(res) != 1 || res[0].Status != jobs.Killed {
		t.Fatalf("post-unwind wait = %+v, want one killed result", res)
	}

	// A later retry — the model calling bash_output again, or the next turn's
	// automatic re-lease — must still find the evidence, not a dead dedupe entry.
	if _, err := (bashOutput{}).Execute(ctx, []byte(`{"job_id":"`+j.ID+`"}`)); err != nil {
		t.Fatalf("bash_output after unwind: %v", err)
	}
	if !ledger.Summary().HasMutation() {
		t.Fatal("bash_output did not collect the killed job's evidence once it became ready")
	}
	if leases := ledger.BackgroundLeases(); len(leases) != 1 {
		t.Fatalf("leases = %+v, want exactly one lease recorded", leases)
	}
}

// 1030: after a controller session rebind (resume / recovery branch / fork) the
// agent's tool calls carry the NEW branch id while a background job started
// before the rebind keeps its start-time owner. The three collection tools must
// agree on that job: wait collects it, bash_output reads it, kill_shell stops
// it — none of them may report it missing while it runs. The job is registered
// directly through the manager (channel-gated) so the test triggers the
//失联形态 deterministically, with no real subprocess.
func TestBackgroundToolsTrackJobAcrossSessionRebind(t *testing.T) {
	root := t.TempDir()
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	// The controller binds its transcript path before the job starts, so the
	// job's owner is the branch id of session-a.
	m.SetActiveSessionPath("session-a", filepath.Join(root, "a.jsonl"))

	release := make(chan struct{})
	defer close(release)
	j := m.StartForSession("session-a", "bash", "wsl idf.py build", func(_ context.Context, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, "compiling\n")
		<-release
		return "", nil
	})

	// The transcript path rotates; the branch id stamped on tool calls changes.
	m.SetActiveSessionPath("session-b", filepath.Join(root, "b.jsonl"))
	ctx := jobs.WithSession(jobs.WithManager(context.Background(), m), "session-b")

	// wait with no ids must not report "No background jobs to wait for."
	wout, err := (waitJob{}).Execute(ctx, []byte(`{"timeout_seconds":1}`))
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if strings.Contains(wout, "No background jobs") {
		t.Fatalf("wait lost the running job after a session rebind: %q", wout)
	}
	if !strings.Contains(wout, j.ID) || !strings.Contains(wout, "running") {
		t.Fatalf("wait = %q, want running %s", wout, j.ID)
	}

	// bash_output reads it under the new session scope.
	bo, err := (bashOutput{}).Execute(ctx, []byte(`{"job_id":"`+j.ID+`"}`))
	if err != nil {
		t.Fatalf("bash_output: %v", err)
	}
	if !strings.Contains(bo, "running") || !strings.Contains(bo, "compiling") {
		t.Fatalf("bash_output = %q, want running with output", bo)
	}

	// kill_shell stops it under the new session scope.
	kout, err := (killShell{}).Execute(ctx, []byte(`{"job_id":"`+j.ID+`"}`))
	if err != nil {
		t.Fatalf("kill_shell: %v", err)
	}
	if !strings.Contains(kout, "Killed") {
		t.Fatalf("kill_shell = %q, want it to report Killed", kout)
	}
}

// Without a manager on the context the background tools degrade to a clear error
// rather than panicking.
func TestBackgroundToolsNoManager(t *testing.T) {
	ctx := context.Background()
	if _, err := (bashOutput{}).Execute(ctx, []byte(`{"job_id":"bash-1"}`)); err == nil {
		t.Error("bash_output without a manager should error")
	}
	if _, err := (bash{}).Execute(ctx, []byte(`{"command":"echo hi","run_in_background":true}`)); err == nil {
		t.Error("background bash without a manager should error")
	}
}
