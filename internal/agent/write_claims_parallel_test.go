package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

func TestNormalizeWritePathsParallelFullAccess(t *testing.T) {
	config.ResetParallelFullAccessCacheForTest()
	config.ApplyParallelFullAccess(false)
	t.Setenv(config.EnvParallelFullAccess, "")

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "wt", "file.go")
	if _, err := NormalizeWritePaths(root, []string{outside}); err == nil {
		t.Fatal("off: write_paths outside workspace must be rejected")
	}

	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	t.Setenv(config.EnvParallelFullAccess, "1")
	config.ResetParallelFullAccessCacheForTest()
	config.ApplyParallelFullAccess(true)
	t.Cleanup(func() {
		config.ApplyParallelFullAccess(false)
		config.ResetParallelFullAccessCacheForTest()
	})

	managed := filepath.Join(config.DeliveryWorktreeDir(), "repo", "id", "src")
	claim, err := NormalizeWritePaths(root, []string{managed})
	if err != nil {
		t.Fatalf("on: write_paths under managed worktree must be accepted: %v", err)
	}
	if claim.Empty() || len(claim.Paths) != 1 {
		t.Fatalf("claim = %+v, want one path", claim)
	}

	escape := filepath.Join(t.TempDir(), "escape")
	if _, err := NormalizeWritePaths(root, []string{escape}); err == nil {
		t.Fatal("on: write_paths outside managed roots must still be rejected")
	}
}
