package boot

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/workspacelease"
)

// handoffBuildConfig isolates the config home and writes the minimal provider
// config every task-890 build test shares.
func handoffBuildConfig(t *testing.T) {
	t.Helper()
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`)
}

// TestBuildHandoffRequiresBothManagerAndLease (task 890): the manager's
// job-start observer retains the lease it was created with, so handing over
// half a pair would rebind one side of that relationship — build rejects it
// instead of silently splitting the pair.
func TestBuildHandoffRequiresBothManagerAndLease(t *testing.T) {
	handoffBuildConfig(t)
	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	lease, err := workspacelease.New(robustTempDir(t), filepath.Join(robustTempDir(t), "locks"), nil)
	if err != nil {
		t.Fatalf("workspace lease: %v", err)
	}

	_, err = Build(context.Background(), Options{Jobs: jm, Sink: event.Discard})
	if err == nil || !strings.Contains(err.Error(), "handoff requires") {
		t.Fatalf("Build with Jobs but no lease: err = %v, want pair-required error", err)
	}
	_, err = Build(context.Background(), Options{WorkspaceLease: lease, Sink: event.Discard})
	if err == nil || !strings.Contains(err.Error(), "handoff requires") {
		t.Fatalf("Build with lease but no Jobs: err = %v, want pair-required error", err)
	}
}

// TestBuildReusesHandedOffJobsAndLease (task 890): a hot-swap build borrows
// the handed-in pair — manager and lease arrive by identity, not copy — and
// the borrowed controller's ordinary Close (the failed-swap shape) leaves the
// shared manager and its running job untouched.
func TestBuildReusesHandedOffJobsAndLease(t *testing.T) {
	handoffBuildConfig(t)
	jm := jobs.NewManager(event.Discard)
	defer jm.Close()
	lease, err := workspacelease.New(robustTempDir(t), filepath.Join(robustTempDir(t), "locks"), nil)
	if err != nil {
		t.Fatalf("workspace lease: %v", err)
	}

	finished := make(chan struct{})
	release := make(chan struct{})
	jm.StartForSession("boot-handoff", "bash", "block", func(ctx context.Context, _ io.Writer) (string, error) {
		defer close(finished)
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})

	ctrl, err := Build(context.Background(), Options{
		Jobs:           jm,
		WorkspaceLease: lease,
		Sink:           event.Discard,
	})
	if err != nil {
		t.Fatalf("Build with handoff pair: %v", err)
	}
	if ctrl.JobsManager() != jm {
		t.Fatalf("ctrl.JobsManager() = %p, want handed-in manager %p", ctrl.JobsManager(), jm)
	}
	if ctrl.WorkspaceLeaseOwner() != lease {
		t.Fatal("ctrl.WorkspaceLeaseOwner() did not return the handed-in lease")
	}

	// Failed-swap shape: the borrowed controller closes without adopting.
	ctrl.Close()
	select {
	case <-finished:
		t.Fatal("borrowed controller Close killed the handed-in job")
	default:
	}
	if !jm.HasUnfinishedForSession("boot-handoff") {
		t.Fatal("borrowed controller Close stopped the handed-in manager")
	}

	close(release)
	<-finished
}
