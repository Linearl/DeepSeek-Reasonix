package control

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/workspacelease"
)

// startBlockingHandoffJob registers a job scoped to sessionPath that blocks
// until released (or its context is cancelled by a manager close).
func startBlockingHandoffJob(t *testing.T, jm *jobs.Manager, sessionPath string) (release func(), finished <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	releaseCh := make(chan struct{})
	jm.StartForSession(agent.BranchID(sessionPath), "bash", "blocking", func(jobCtx context.Context, _ io.Writer) (string, error) {
		defer close(done)
		select {
		case <-releaseCh:
			return "ok", nil
		case <-jobCtx.Done():
			return "", jobCtx.Err()
		}
	})
	var once sync.Once
	return func() { once.Do(func() { close(releaseCh) }) }, done
}

// handoffTestController wires the shared pair plus a cleanup probe; the
// caller closes jm.
func handoffTestController(t *testing.T, jm *jobs.Manager, lease *workspacelease.Owner, borrowed bool, cleaned *atomic.Bool) *Controller {
	t.Helper()
	dir := t.TempDir()
	return New(Options{
		SessionDir:     dir,
		SessionPath:    filepath.Join(dir, "handoff.jsonl"),
		Label:          "old",
		Sink:           event.Discard,
		Jobs:           jm,
		JobsHandoff:    borrowed,
		WorkspaceLease: lease,
		Cleanup:        func() { cleaned.Store(true) },
	})
}

// TestCloseHandingOffJobsKeepsManagerAndDefersCleanup (task 890): the
// outgoing close of a hot swap leaves the shared manager and its running
// background job alive, and defers the build-scoped cleanup chain until that
// job exits.
func TestCloseHandingOffJobsKeepsManagerAndDefersCleanup(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	lease, err := workspacelease.New(t.TempDir(), filepath.Join(t.TempDir(), "locks"), nil)
	if err != nil {
		t.Fatalf("workspace lease: %v", err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	release, finished := startBlockingHandoffJob(t, jm, path)

	var cleaned atomic.Bool
	c := handoffTestController(t, jm, lease, false, &cleaned)
	if c.JobsManager() != jm {
		t.Fatalf("JobsManager() = %p, want handed-in manager %p", c.JobsManager(), jm)
	}
	if c.WorkspaceLeaseOwner() != lease {
		t.Fatal("WorkspaceLeaseOwner() did not return the handed-in lease")
	}

	c.CloseHandingOffJobs()

	select {
	case <-finished:
		t.Fatal("handoff close killed the running background job")
	default:
	}
	if !jm.HasUnfinishedForSession(agent.BranchID(path)) {
		t.Fatal("handoff close left the manager reporting no unfinished jobs")
	}
	if cleaned.Load() {
		t.Fatal("build-scoped cleanup ran while the handed-off job was still running")
	}

	release()
	waitFor(t, "deferred cleanup after the handed-off job exited", cleaned.Load)
	<-finished
}

// TestBorrowedControllerCloseLeavesSharedManagerRunning (task 890): a failed
// swap closes the replacement without touching the borrowed manager the
// outgoing controller still serves — while the replacement's own cleanup
// still runs.
func TestBorrowedControllerCloseLeavesSharedManagerRunning(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	lease, err := workspacelease.New(t.TempDir(), filepath.Join(t.TempDir(), "locks"), nil)
	if err != nil {
		t.Fatalf("workspace lease: %v", err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	release, finished := startBlockingHandoffJob(t, jm, path)
	defer release()

	var cleaned atomic.Bool
	c := handoffTestController(t, jm, lease, true, &cleaned)
	c.Close()

	select {
	case <-finished:
		t.Fatal("borrowed close killed the shared background job")
	default:
	}
	if !jm.HasUnfinishedForSession(agent.BranchID(path)) {
		t.Fatal("borrowed close stopped the shared manager")
	}
	if !cleaned.Load() {
		t.Fatal("borrowed close skipped the replacement's own cleanup chain")
	}
}

// TestAdoptJobsMakesCloseOwnTheManager (task 890): after the swap commits
// (AdoptJobs), the replacement's ordinary Close owns the manager and stops
// its jobs — ownership is not leaked.
func TestAdoptJobsMakesCloseOwnTheManager(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	lease, err := workspacelease.New(t.TempDir(), filepath.Join(t.TempDir(), "locks"), nil)
	if err != nil {
		t.Fatalf("workspace lease: %v", err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	_, finished := startBlockingHandoffJob(t, jm, path)

	var cleaned atomic.Bool
	c := handoffTestController(t, jm, lease, true, &cleaned)
	c.AdoptJobs()
	c.Close()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("close after AdoptJobs did not stop the adopted job")
	}
	if jm.HasUnfinishedForSession(agent.BranchID(path)) {
		t.Fatal("manager still reports unfinished jobs after the owner closed")
	}
	if !cleaned.Load() {
		t.Fatal("owner close did not run the cleanup chain")
	}
}
