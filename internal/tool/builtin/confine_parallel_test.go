package builtin

import (
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

func TestConfineParallelFullAccessOffRejectsOutside(t *testing.T) {
	config.ResetParallelFullAccessCacheForTest()
	config.ApplyParallelFullAccess(false)
	t.Setenv(config.EnvParallelFullAccess, "")

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere", "file.go")
	if err := confine([]string{root}, outside); err == nil {
		t.Fatal("off: write outside roots must be refused")
	}
}

func TestConfineParallelFullAccessOnAllowsManagedWorktree(t *testing.T) {
	config.ResetParallelFullAccessCacheForTest()
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	t.Setenv(config.EnvParallelFullAccess, "1")
	config.ResetParallelFullAccessCacheForTest()
	config.ApplyParallelFullAccess(true)
	t.Cleanup(func() {
		config.ApplyParallelFullAccess(false)
		config.ResetParallelFullAccessCacheForTest()
	})

	managed := config.DeliveryWorktreeDir()
	if managed == "" {
		t.Fatal("managed worktree dir empty")
	}
	inside := filepath.Join(managed, "repo", "id", "src", "a.go")
	if err := confine([]string{t.TempDir()}, inside); err != nil {
		t.Fatalf("on: managed worktree write must be allowed, got %v", err)
	}

	// Escape outside managed roots is still refused.
	outside := filepath.Join(t.TempDir(), "escape.go")
	if err := confine([]string{t.TempDir()}, outside); err == nil {
		t.Fatal("on: escape outside managed roots must still be refused")
	}
}
