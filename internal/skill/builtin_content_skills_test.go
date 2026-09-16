package skill_test

import (
	"strings"
	"testing"

	"reasonix/internal/skill"
)

// Task 139 / 138: GitHub issue playbooks and the fork usage guide ship as
// builtins so a user installs nothing for them to appear in the catalog.
func TestGitHubIssueSkillsRegistered(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	for _, name := range []string{"gh-issue-submit", "github-issue-triage"} {
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
	if !strings.Contains(idx, "gh-issue-submit") || !strings.Contains(idx, "github-issue-triage") {
		t.Fatal("index missing GitHub issue skill lines")
	}
}

func TestReasonixForkGuideRegistered(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	sk, ok := store.Read("reasonix-fork-guide")
	if !ok {
		t.Fatal("reasonix-fork-guide must be a builtin")
	}
	if sk.Scope != skill.ScopeBuiltin {
		t.Fatalf("scope = %s", sk.Scope)
	}
	if !strings.Contains(sk.Body, "worktree") {
		t.Fatal("fork guide body missing worktree guidance")
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
