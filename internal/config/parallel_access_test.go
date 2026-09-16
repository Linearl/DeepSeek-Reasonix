package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParallelFullAccessDefaultsOff(t *testing.T) {
	ResetParallelFullAccessCacheForTest()
	t.Setenv(EnvParallelFullAccess, "")
	c := &Config{}
	if c.ParallelFullAccessEnabled() {
		t.Fatal("default config must keep the experiment off")
	}
	if ParallelFullAccessActive() {
		t.Fatal("process flag must start off")
	}
}

func TestParallelFullAccessConfigAndEnv(t *testing.T) {
	ResetParallelFullAccessCacheForTest()
	t.Setenv(EnvParallelFullAccess, "1")
	if !ParallelFullAccessActive() {
		t.Fatal("env=1 must enable the experiment")
	}

	ResetParallelFullAccessCacheForTest()
	t.Setenv(EnvParallelFullAccess, "")
	c := &Config{}
	c.Desktop.ExperimentalParallelFullAccess = true
	if !c.ParallelFullAccessEnabled() {
		t.Fatal("config flag must enable the experiment")
	}
	ApplyParallelFullAccess(true)
	if !ParallelFullAccessActive() {
		t.Fatal("ApplyParallelFullAccess(true) must arm the process switch")
	}
	ApplyParallelFullAccess(false)
}

func TestIsUnderParallelWorktreeRoot(t *testing.T) {
	ResetParallelFullAccessCacheForTest()
	managed := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", managed)
	t.Setenv(EnvParallelWorktreesRoot, "")
	ResetParallelFullAccessCacheForTest()

	root := DeliveryWorktreeDir()
	if root == "" {
		t.Fatal("DeliveryWorktreeDir empty under REASONIX_STATE_HOME")
	}
	inside := filepath.Join(root, "repo", "id", "proj", "file.go")
	if !IsUnderParallelWorktreeRoot(inside, "") {
		t.Fatalf("%s must be under managed worktree root %s", inside, root)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	if IsUnderParallelWorktreeRoot(outside, "") {
		t.Fatalf("%s must not be trusted", outside)
	}
}

func TestSiblingWorktreesRootDetected(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "reasonix")
	worktrees := filepath.Join(base, "worktrees")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktrees, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := ParallelWorktreeRoots(repo)
	found := false
	for _, r := range roots {
		if filepath.Clean(r) == filepath.Clean(worktrees) {
			found = true
		}
	}
	if !found {
		t.Fatalf("sibling worktrees root missing from %v", roots)
	}
	if !IsUnderParallelWorktreeRoot(filepath.Join(worktrees, "wt-a", "x.go"), repo) {
		t.Fatal("path under sibling worktrees must be trusted")
	}
}
