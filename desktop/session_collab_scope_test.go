package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 158.C: creating a session for ANOTHER project is allowed, but only for a
// project this desktop already knows. An unregistered root would create a stray
// project nobody opened — the same failure mode as the ghost "global-workspace"
// project (task 156.B).
func TestResolveCollabTargetScopeRouting(t *testing.T) {
	registered := []string{`C:\work\alpha`, `C:\work\beta`}

	t.Run("no project keeps the caller scope", func(t *testing.T) {
		scope, root, err := resolveCollabTargetScope("", "", registered)
		if err != nil || scope != "global" || root != "" {
			t.Fatalf("a host-level caller must land in Global: %q %q %v", scope, root, err)
		}
		scope, root, err = resolveCollabTargetScope(`C:\work\alpha`, "", registered)
		if err != nil || scope != "project" || root != `C:\work\alpha` {
			t.Fatalf("a project caller must land in its own project: %q %q %v", scope, root, err)
		}
	})

	t.Run("registered project is honoured", func(t *testing.T) {
		scope, root, err := resolveCollabTargetScope(`C:\work\alpha`, `C:\work\beta`, registered)
		if err != nil {
			t.Fatal(err)
		}
		if scope != "project" || root != `C:\work\beta` {
			t.Fatalf("requested project must win over the caller's: %q %q", scope, root)
		}
	})

	t.Run("unregistered project is refused with the options", func(t *testing.T) {
		scope, root, err := resolveCollabTargetScope("", `C:\work\gamma`, registered)
		if err == nil {
			t.Fatalf("an unknown project must be refused, got %q %q", scope, root)
		}
		msg := err.Error()
		if !strings.Contains(msg, "not a registered project") {
			t.Fatalf("error must say why: %s", msg)
		}
		// The suggested next step has to exist: every registered root is listed.
		for _, want := range registered {
			if !strings.Contains(msg, want) {
				t.Fatalf("error must list the available projects, missing %q: %s", want, msg)
			}
		}
		if scope != "" || root != "" {
			t.Fatalf("a refused request must not carry a target: %q %q", scope, root)
		}
	})

	t.Run("the global workspace is not a project", func(t *testing.T) {
		// Empty or not, the global root can never be adopted as a project root.
		_, _, err := resolveCollabTargetScope("", globalWorkspaceRoot(), registered)
		if err == nil {
			t.Fatal("the global workspace must not be accepted as a project")
		}
	})

	t.Run("no projects at all is explained", func(t *testing.T) {
		_, _, err := resolveCollabTargetScope("", `C:\work\gamma`, nil)
		if err == nil || !strings.Contains(err.Error(), "no projects yet") {
			t.Fatalf("an empty project list needs its own message, got %v", err)
		}
	})
}

// Task 158.D: the dry-run impact must name the session the way the contact
// directory names it, and must not report "nothing here" for a transcript that
// already holds work. A stale or empty title is what made the reported dry run
// useless.
func TestDeleteCollabSessionDryRunReportsTitleAndContent(t *testing.T) {
	dir := t.TempDir()
	withWork := filepath.Join(dir, "with-work.jsonl")
	if err := os.WriteFile(withWork, []byte(`{"type":"append","message":{"role":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptySession := filepath.Join(dir, "not-started.jsonl")
	if err := os.WriteFile(emptySession, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	impact, result, err := app.deleteCollabSession("sc_target", withWork, true)
	if err != nil {
		t.Fatal(err)
	}
	if impact.Title != "with-work" {
		t.Fatalf("dry run must report the directory title, got %q", impact.Title)
	}
	if !impact.HasTurn {
		t.Fatal("a transcript with turns must not be reported as empty")
	}
	if impact.TurnInFlight {
		t.Fatal("no controller is running: turnInFlight must stay false")
	}
	if impact.OpenTab || result.Trashed {
		t.Fatalf("a dry run must not open a tab or trash anything: %+v %+v", impact, result)
	}
	// The file must still be where it was.
	if _, err := os.Stat(withWork); err != nil {
		t.Fatalf("dry run touched the session: %v", err)
	}

	// A session that never ran a turn is reported as empty, so the two cases
	// stay distinguishable.
	impact, _, err = app.deleteCollabSession("sc_new", emptySession, true)
	if err != nil {
		t.Fatal(err)
	}
	if impact.HasTurn {
		t.Fatal("a freshly created session has no turns")
	}
	if impact.Title != "not-started" {
		t.Fatalf("title must fall back to the file stem, got %q", impact.Title)
	}
}
