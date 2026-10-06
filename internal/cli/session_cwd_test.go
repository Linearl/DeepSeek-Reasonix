package cli

import (
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// saveSessionWithProjectRoot writes a session transcript sidecar carrying the
// given project root, mimicking what the desktop persists on every topic
// binding (branch meta workspace_root).
func saveSessionWithProjectRoot(t *testing.T, dir, projectRoot string) string {
	t.Helper()
	sessionPath := filepath.Join(dir, "20261006_120000_cwdtest.jsonl")
	if err := agent.SaveBranchMeta(sessionPath, agent.BranchMeta{ID: agent.BranchID(sessionPath), WorkspaceRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

func sessionCwdFollowConfig(on bool) *config.Config {
	cfg := &config.Config{}
	cfg.Agent.ExperimentalSessionCwdFollow = on
	return cfg
}

// Adversarial input for task 545: the session's project directory differs from
// the process working directory. With the switch ON and no explicit --dir, the
// resume override must pin the PROJECT root, not the process cwd.
func TestResumeWorkspaceRootOverrideFollowsProjectNotProcessCwd(t *testing.T) {
	project := t.TempDir()
	processDir := t.TempDir() // deliberately a different directory than project

	t.Chdir(processDir) // simulate the process running outside the project

	sessionPath := saveSessionWithProjectRoot(t, t.TempDir(), project)
	got := resumeWorkspaceRootOverride("", sessionPath, sessionCwdFollowConfig(true))
	if got != filepath.Clean(project) {
		t.Fatalf("resume override = %q, want the session's project root %q (process cwd %q)", got, filepath.Clean(project), processDir)
	}
}

// Iron rule 2, closed state: with the switch off the override is empty, so
// boot keeps its process-cwd / git-root fallback byte-identical — even when a
// perfectly valid project root is persisted on the session.
func TestResumeWorkspaceRootOverrideSwitchOffKeepsZeroBehavior(t *testing.T) {
	project := t.TempDir()
	sessionPath := saveSessionWithProjectRoot(t, t.TempDir(), project)

	if got := resumeWorkspaceRootOverride("", sessionPath, sessionCwdFollowConfig(false)); got != "" {
		t.Fatalf("switch off: override = %q, want empty (pre-545 fallback)", got)
	}
	if got := resumeWorkspaceRootOverride("", sessionPath, nil); got != "" {
		t.Fatalf("nil config: override = %q, want empty", got)
	}
}

// An explicit --dir always wins, switch on or off: the user named the project.
func TestResumeWorkspaceRootOverrideExplicitDirWins(t *testing.T) {
	project := t.TempDir()
	explicit := t.TempDir()
	sessionPath := saveSessionWithProjectRoot(t, t.TempDir(), project)

	if got := resumeWorkspaceRootOverride(explicit, sessionPath, sessionCwdFollowConfig(true)); got != explicit {
		t.Fatalf("explicit --dir must win: override = %q, want %q", got, explicit)
	}
}

// Fallbacks stay empty so boot falls back exactly as before: no resume, no
// usable persisted root (missing meta / stale directory / global scope).
func TestResumeWorkspaceRootOverrideFallbacks(t *testing.T) {
	cfg := sessionCwdFollowConfig(true)

	if got := resumeWorkspaceRootOverride("", "", cfg); got != "" {
		t.Fatalf("no resume path: override = %q, want empty", got)
	}
	if got := resumeWorkspaceRootOverride("", filepath.Join(t.TempDir(), "missing.jsonl"), cfg); got != "" {
		t.Fatalf("missing session: override = %q, want empty", got)
	}
	stale := saveSessionWithProjectRoot(t, t.TempDir(), filepath.Join(t.TempDir(), "gone"))
	if got := resumeWorkspaceRootOverride("", stale, cfg); got != "" {
		t.Fatalf("stale project root: override = %q, want empty", got)
	}
	global := filepath.Join(t.TempDir(), "20261006_120001_global.jsonl")
	if err := agent.SaveBranchMeta(global, agent.BranchMeta{ID: agent.BranchID(global)}); err != nil {
		t.Fatal(err)
	}
	if got := resumeWorkspaceRootOverride("", global, cfg); got != "" {
		t.Fatalf("global-scope session: override = %q, want empty", got)
	}
}
