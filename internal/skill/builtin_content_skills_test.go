package skill_test

import (
	"strings"
	"testing"

	"reasonix/internal/skill"
)

// Task 139 / 138: GitHub issue playbooks and the fork guide ship as builtins
// so a user installs nothing for them to appear in the catalog. Task 430
// replaced the reasonix-fork-guide builtin with the user-authored
// ll-fork-guide (fork development discipline quick reference).
func TestGitHubIssueSkillsRegistered(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	for _, name := range []string{"gh-issue-submit", "gh-issue-triage"} {
		sk, ok := store.Read(name)
		if !ok {
			t.Fatalf("%s must be a builtin", name)
		}
		if sk.Scope != skill.ScopeBuiltin {
			t.Fatalf("%s scope = %s", name, sk.Scope)
		}
		if sk.Description == "" {
			t.Fatalf("%s missing description", name)
		}
		if sk.RunAs != skill.RunInline {
			t.Fatalf("%s runAs = %s", name, sk.RunAs)
		}
	}
	idx := skill.IndexBlock(store.List())
	if !strings.Contains(idx, "gh-issue-submit") || !strings.Contains(idx, "gh-issue-triage") {
		t.Fatal("index missing GitHub issue skill lines")
	}
}

func TestLlForkGuideRegistered(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	sk, ok := store.Read("ll-fork-guide")
	if !ok {
		t.Fatal("ll-fork-guide must be a builtin")
	}
	if sk.Scope != skill.ScopeBuiltin {
		t.Fatalf("scope = %s", sk.Scope)
	}
	if !strings.Contains(sk.Body, "worktree") {
		t.Fatal("fork guide body missing worktree guidance")
	}
	if !strings.Contains(sk.Body, "FORK.md") {
		t.Fatal("ll-fork-guide body missing FORK.md pointer")
	}
	// The retired name must not come back as a builtin.
	if _, ok := store.Read("reasonix-fork-guide"); ok {
		t.Fatal("reasonix-fork-guide must be retired (replaced by ll-fork-guide)")
	}
	// Distinct from the self-diagnostics guide.
	guide, ok := store.Read("reasonix-guide")
	if !ok {
		t.Fatal("reasonix-guide still required")
	}
	if sk.Body == guide.Body {
		t.Fatal("fork guide must not duplicate reasonix-guide body")
	}
}

func TestLlUpdateRegistered(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	sk, ok := store.Read("ll-update")
	if !ok {
		t.Fatal("ll-update must be a builtin")
	}
	if sk.Scope != skill.ScopeBuiltin {
		t.Fatalf("scope = %s", sk.Scope)
	}
	if sk.Description == "" {
		t.Fatal("ll-update missing description")
	}
	if !strings.Contains(sk.Body, "restart_update") {
		t.Fatal("ll-update body missing restart_update guidance")
	}
	if !strings.Contains(sk.Body, "scripts/switch-version.sh") {
		t.Fatal("ll-update body missing switch-version.sh reference")
	}
	// Task 520 enhancement: the skill must also carry the plain-restart face —
	// the boot-settings "restart and continue" action, its switch difference
	// (restart needs only the autonomous-update experiment, the install is not
	// touched), and the taskkill anti-pattern it replaces (the 2026-10-06
	// incident that motivated the action). A missing section here would send
	// the agent back to taskkill + manual launcher start.
	if !strings.Contains(sk.Body, `"action": "restart"`) {
		t.Fatal("ll-update body missing the plain-restart action guidance (task 520)")
	}
	if !strings.Contains(sk.Body, "taskkill") {
		t.Fatal("ll-update body missing the taskkill anti-pattern warning (task 520)")
	}
	if !strings.Contains(sk.Body, "不触碰安装") {
		t.Fatal("ll-update body must state restart touches no install (the switch difference)")
	}
}
