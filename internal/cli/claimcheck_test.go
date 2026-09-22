package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunClaimCheckVerdictsAndExitCodes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "routes", "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "routes", "dev", "dev.workflow.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A "missing" claim that is actually present must be REFUTED (exit 1).
	if code := runClaimCheck([]string{"--claim", "missing", "--pattern", "dev.workflow.yaml", "--base", root}); code != 1 {
		t.Fatalf("missing-but-present exit=%d want 1", code)
	}
	// A "missing" claim on a clean root is CONFIRMED (exit 0).
	if code := runClaimCheck([]string{"--claim", "missing", "--pattern", "ghost.xyz", "--base", root}); code != 0 {
		t.Fatalf("missing-and-absent exit=%d want 0", code)
	}
	// Bad usage exits 2, never a verdict.
	if code := runClaimCheck([]string{"--claim", "bogus", "--path", root}); code != 2 {
		t.Fatalf("bad usage exit=%d want 2", code)
	}
}

func TestRunClaimCheckPathMode(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runClaimCheck([]string{"--claim", "exists", "--path", target}); code != 0 {
		t.Fatalf("exists-on-present exit=%d want 0", code)
	}
	if code := runClaimCheck([]string{"--claim", "exists", "--path", filepath.Join(root, "nope.txt")}); code != 1 {
		t.Fatalf("exists-on-absent exit=%d want 1", code)
	}
}
