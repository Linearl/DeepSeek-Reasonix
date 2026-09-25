package control

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/permission"
	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

// Task 157.A: config-backed write-directory mutations must land on the live
// runtime roots in the same session. Boot builds the WritableRootSet once
// (boot.go NewWritableRootSet); before this task, AddGlobalWriteDir /
// RemoveGlobalWriteDir and the project-scope remove only rewrote config, so
// the very next write still missed the root and prompted again.

// isolateUserConfig gives the test its own REASONIX_HOME config file, mirroring
// config's own tests (REASONIX_HOME + SaveTo — Save() would leak a relative
// reasonix.toml into the package directory).
func isolateUserConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// checkWriteWriteFile runs the real decision chain for a write_file declaration.
func checkWriteWriteFile(t *testing.T, c *Controller, dir string) agent.WriteAccessDecision {
	t.Helper()
	dec, err := c.CheckWriteAccess(context.Background(), agent.WriteAccessCheck{
		Tool:       "write_file",
		Expandable: true,
		Declaration: tool.WriteAccessDeclaration{
			Directories: []string{dir},
		},
	})
	if err != nil {
		t.Fatalf("CheckWriteAccess: %v", err)
	}
	return dec
}

// TestAddGlobalWriteDirTakesEffectImmediately pins the headline complaint:
// after adding a global write dir, a write to it (or a subdirectory) in the
// SAME controller/session must pass without a new authorization prompt.
func TestAddGlobalWriteDirTakesEffectImmediately(t *testing.T) {
	isolateUserConfig(t)
	ws := t.TempDir()
	target := canonicalWriteTestDir(t)
	set := sandbox.NewWritableRootSet([]string{ws})
	c := New(Options{WorkspaceRoot: ws, Policy: permission.New("allow", nil, nil, nil), WriteRoots: set})

	if dec := checkWriteWriteFile(t, c, target); dec.Allow {
		t.Fatal("precondition: an ungranted directory must not pass")
	}
	if err := c.AddGlobalWriteDir(target); err != nil {
		t.Fatalf("AddGlobalWriteDir: %v", err)
	}
	if dec := checkWriteWriteFile(t, c, target); !dec.Allow {
		t.Fatalf("same-session write right after add must pass, deny reason: %s", dec.Reason)
	}
	child := filepath.Join(target, "sub", "deeper")
	if dec := checkWriteWriteFile(t, c, child); !dec.Allow {
		t.Fatalf("subdirectory must pass too (allow_global covers children), deny reason: %s", dec.Reason)
	}
}

// TestRemoveGlobalWriteDirReblocksImmediately pins the second acceptance
// checkbox: removal restores the prompt in the same session instead of
// waiting for the next boot/LoadForRoot.
func TestRemoveGlobalWriteDirReblocksImmediately(t *testing.T) {
	isolateUserConfig(t)
	ws := t.TempDir()
	target := canonicalWriteTestDir(t)
	set := sandbox.NewWritableRootSet([]string{ws})
	c := New(Options{WorkspaceRoot: ws, Policy: permission.New("allow", nil, nil, nil), WriteRoots: set})

	if err := c.AddGlobalWriteDir(target); err != nil {
		t.Fatalf("AddGlobalWriteDir: %v", err)
	}
	if dec := checkWriteWriteFile(t, c, target); !dec.Allow {
		t.Fatalf("precondition: grant should pass, deny reason: %s", dec.Reason)
	}
	if err := c.RemoveGlobalWriteDir(target); err != nil {
		t.Fatalf("RemoveGlobalWriteDir: %v", err)
	}
	if dec := checkWriteWriteFile(t, c, target); dec.Allow {
		t.Fatal("after removal the same session must prompt again")
	}
	// The removal must not widen or narrow unrelated roots: the workspace
	// root itself stays writable.
	if dec := checkWriteWriteFile(t, c, filepath.Join(ws, "still", "mine.txt")); !dec.Allow {
		t.Fatalf("workspace root must stay writable, deny reason: %s", dec.Reason)
	}
}

// TestRemoveAuthorizedWriteDirProjectReblocksImmediately covers the
// project-scope remove: it used to defer to "the next LoadForRoot" (the old
// comment), so a removed project entry kept passing until restart.
func TestRemoveAuthorizedWriteDirProjectReblocksImmediately(t *testing.T) {
	ws := t.TempDir()
	target := canonicalWriteTestDir(t)
	set := sandbox.NewWritableRootSet([]string{ws})
	c := New(Options{
		WorkspaceRoot: ws,
		Policy:        permission.New("allow", nil, nil, nil),
		WriteRoots:    set,
		OnPersistWriteAccess: func(dirs []string, _ string) error {
			return nil
		},
	})

	if err := c.AddAuthorizedWriteDir(sandbox.ApprovalScopeProject, target); err != nil {
		t.Fatalf("AddAuthorizedWriteDir(project): %v", err)
	}
	if dec := checkWriteWriteFile(t, c, target); !dec.Allow {
		t.Fatalf("project add is hot (pre-existing), deny reason: %s", dec.Reason)
	}
	if err := c.RemoveAuthorizedWriteDir(sandbox.ApprovalScopeProject, target); err != nil {
		t.Fatalf("RemoveAuthorizedWriteDir(project): %v", err)
	}
	if dec := checkWriteWriteFile(t, c, target); dec.Allow {
		t.Fatal("project removal must re-block in the same session")
	}
}

// TestAutopilotAuthorizedWriteTargetNeverPrompts pins task 157.B tier a: once
// the directory is authorized, an autopilot run never reaches a write-access
// prompt for it — the pass happens in CheckWriteAccess (missing is empty), not
// inside the unattended approval flow. The tier must also not widen: an
// ungranted sibling directory still prompts.
func TestAutopilotAuthorizedWriteTargetNeverPrompts(t *testing.T) {
	isolateUserConfig(t)
	ws := t.TempDir()
	target := canonicalWriteTestDir(t)
	set := sandbox.NewWritableRootSet([]string{ws})
	c := New(Options{
		WorkspaceRoot:          ws,
		Policy:                 permission.New("allow", nil, nil, nil),
		WriteRoots:             set,
		Autopilot:              true,
		AutopilotApprovalGrace: time.Millisecond,
	})

	ungranted := canonicalWriteTestDir(t)
	if dec := checkWriteWriteFile(t, c, ungranted); dec.Allow {
		t.Fatal("precondition: ungranted directory must prompt even under autopilot")
	}
	if err := c.AddGlobalWriteDir(target); err != nil {
		t.Fatalf("AddGlobalWriteDir: %v", err)
	}
	if dec := checkWriteWriteFile(t, c, target); !dec.Allow {
		t.Fatalf("autopilot write to an authorized dir must pass without a prompt, deny reason: %s", dec.Reason)
	}
	// Tier a is not a blanket pass: a different ungranted directory still
	// produces the prompt the unattended flow would have to decide.
	stillMissing := canonicalWriteTestDir(t)
	if dec := checkWriteWriteFile(t, c, stillMissing); dec.Allow {
		t.Fatal("tier a must not widen: ungranted dirs still prompt")
	}
}

// TestUnattendedWriteApprovalDangerousStillRefused pins the acceptance line
// "不可逆操作仍拒": even with the most permissive decision tier (parent), the
// absolute keyword screen refuses a destructive write before any tier logic.
func TestUnattendedWriteApprovalDangerousStillRefused(t *testing.T) {
	c := New(Options{Policy: permission.Policy{Mode: permission.Allow}, ApprovalTier: ApprovalTierParent})
	dec, decided := c.reviewUnattendedApproval(context.Background(),
		"write_file", "cache/build", "delete stale build artifacts", nil)
	if !decided {
		t.Fatal("unattended write approval must decide")
	}
	if dec.allow {
		t.Fatal("keyword screen must refuse a destructive write on every tier")
	}
}
