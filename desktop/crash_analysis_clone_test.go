package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Task 674: the one-click analysis clones the fork checkout when it is
// missing, so the flow stays one click end to end. These tests pin the pure
// pieces (clone argv, canonical directory) and the refusal paths that run
// before any gh process is spawned. The clone execution itself is a thin
// exec wrapper around ghCloneArgs — it is exercised by the lab mock-crash
// drill against the real repo, not by unit tests.

func TestGhCloneArgsIsShallowRepoClone(t *testing.T) {
	args := ghCloneArgs("Linearl/DeepSeek-Reasonix", filepath.Join("root", "reasonix"))
	want := []string{"repo", "clone", "Linearl/DeepSeek-Reasonix", filepath.Join("root", "reasonix"), "--", "--depth", "1"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("ghCloneArgs = %v, want %v", args, want)
	}
}

func TestCrashAnalysisSourceDirCandidateUnderStateHome(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	got := crashAnalysisSourceDirCandidate()
	want := filepath.Join("global-workspace", "github-repo", "reasonix")
	if !strings.HasSuffix(got, want) {
		t.Fatalf("candidate = %q, want it under %s", got, want)
	}
}

// The refusal must fire before any gh call: an existing directory that is not
// a reasonix checkout is never touched — deleting user data is not this
// function's job. The sentinel file proves the directory came out untouched.
func TestEnsureCrashAnalysisSourceRefusesNonCheckoutDir(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	dir := crashAnalysisSourceDirCandidate()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "user-data.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotDir, cloned, err := ensureCrashAnalysisSource()
	if err == nil {
		t.Fatalf("ensureCrashAnalysisSource = (%q, %v, nil), want a refusal for the non-checkout dir", gotDir, cloned)
	}
	if cloned {
		t.Fatalf("cloned = true, want false on the refusal path")
	}
	if !strings.Contains(err.Error(), "not a reasonix checkout") {
		t.Fatalf("error = %v, want the explicit non-checkout refusal", err)
	}
	body, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(body) != "keep me" {
		t.Fatalf("sentinel file disturbed: readErr=%v body=%q", readErr, body)
	}
}

// gh missing (fallback locations stubbed out, PATH cleared) must fail closed
// with the copy-path hint before any directory is created or any gh process
// runs.
func TestEnsureCrashAnalysisSourceFailsClosedWithoutGh(t *testing.T) {
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	origFallbacks := ghFallbackLocationDirs
	ghFallbackLocationDirs = func() []string { return nil }
	t.Cleanup(func() { ghFallbackLocationDirs = origFallbacks })
	t.Setenv("PATH", "")

	dir, cloned, err := ensureCrashAnalysisSource()
	if err == nil {
		t.Fatalf("ensureCrashAnalysisSource = (%q, %v, nil), want a gh-not-found failure", dir, cloned)
	}
	if cloned {
		t.Fatalf("cloned = true, want false when gh is missing")
	}
	if !strings.Contains(err.Error(), "gh CLI not found") {
		t.Fatalf("error = %v, want the gh-not-found hint", err)
	}
	if _, statErr := os.Stat(crashAnalysisSourceDirCandidate()); statErr == nil {
		t.Fatalf("clone target directory was created despite missing gh")
	}
}
