package serve

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// saveServeSessionWithRoot writes a session sidecar carrying projectRoot.
func saveServeSessionWithRoot(t *testing.T, dir, projectRoot string) string {
	t.Helper()
	sessionPath := filepath.Join(dir, "20261006_120100_servecwd.jsonl")
	if err := agent.SaveBranchMeta(sessionPath, agent.BranchMeta{ID: agent.BranchID(sessionPath), WorkspaceRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

func withSessionCwdFollow(on bool) func() {
	prev := sessionCwdFollowConfigForTest
	cfg := &config.Config{}
	cfg.Agent.ExperimentalSessionCwdFollow = on
	sessionCwdFollowConfigForTest = func() (*config.Config, error) { return cfg, nil }
	return func() { sessionCwdFollowConfigForTest = prev }
}

// The busy /resume replacement must pin the replacement build to the target
// session's own project root when the switch is on — not inherit the
// foreground's root (which ultimately traces back to the serve spawn cwd).
func TestBusyDetachPinsTargetSessionProjectRoot(t *testing.T) {
	restore := withSessionCwdFollow(true)
	defer restore()

	project := t.TempDir()
	targetPath := saveServeSessionWithRoot(t, t.TempDir(), project)

	foregroundRoot := t.TempDir() // ≠ project: the spawn-cwd stand-in
	curPath := filepath.Join(t.TempDir(), "foreground.jsonl")
	bc := NewBroadcaster()
	cur := control.New(control.Options{Sink: bc, SessionPath: curPath, SessionDir: filepath.Dir(curPath), WorkspaceRoot: foregroundRoot})
	server := New(cur, bc, config.ServeConfig{})
	tag := NewSessionTagSink(server.bc)
	tag.SetPath(cur.SessionPath())
	server.RegisterSessionTag(cur, tag)

	var gotRoot string
	server.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		gotRoot = opts.WorkspaceRoot
		return control.New(control.Options{Sink: opts.Sink, SessionDir: opts.SessionDir, WorkspaceRoot: opts.WorkspaceRoot}), nil
	}

	err := server.busyDetach(context.Background(), cur, targetPath, func(next *control.Controller) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if gotRoot != filepath.Clean(project) {
		t.Fatalf("replacement root = %q, want the target session's project root %q (foreground root %q)", gotRoot, filepath.Clean(project), foregroundRoot)
	}
}

// Iron rule 2, closed state: with the switch off, the busy replacement keeps
// the inherited foreground root — byte-identical pre-545 behavior.
func TestBusyDetachSwitchOffInheritsForegroundRoot(t *testing.T) {
	restore := withSessionCwdFollow(false)
	defer restore()

	project := t.TempDir()
	targetPath := saveServeSessionWithRoot(t, t.TempDir(), project)

	foregroundRoot := t.TempDir()
	curPath := filepath.Join(t.TempDir(), "foreground.jsonl")
	bc := NewBroadcaster()
	cur := control.New(control.Options{Sink: bc, SessionPath: curPath, SessionDir: filepath.Dir(curPath), WorkspaceRoot: foregroundRoot})
	server := New(cur, bc, config.ServeConfig{})
	tag := NewSessionTagSink(server.bc)
	tag.SetPath(cur.SessionPath())
	server.RegisterSessionTag(cur, tag)

	var gotRoot string
	server.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		gotRoot = opts.WorkspaceRoot
		return control.New(control.Options{Sink: opts.Sink, SessionDir: opts.SessionDir, WorkspaceRoot: opts.WorkspaceRoot}), nil
	}

	err := server.busyDetach(context.Background(), cur, targetPath, func(next *control.Controller) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if gotRoot != foregroundRoot {
		t.Fatalf("switch off: replacement root = %q, want the inherited foreground root %q", gotRoot, foregroundRoot)
	}
}

// A target without a usable persisted root (fresh/foreign/global session)
// keeps the inherited root even with the switch on.
func TestBusyDetachSwitchOnWithoutSessionRootInherits(t *testing.T) {
	restore := withSessionCwdFollow(true)
	defer restore()

	targetPath := filepath.Join(t.TempDir(), "20261006_120200_noroot.jsonl")
	foregroundRoot := t.TempDir()
	curPath := filepath.Join(t.TempDir(), "foreground.jsonl")
	bc := NewBroadcaster()
	cur := control.New(control.Options{Sink: bc, SessionPath: curPath, SessionDir: filepath.Dir(curPath), WorkspaceRoot: foregroundRoot})
	server := New(cur, bc, config.ServeConfig{})
	tag := NewSessionTagSink(server.bc)
	tag.SetPath(cur.SessionPath())
	server.RegisterSessionTag(cur, tag)

	var gotRoot string
	server.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		gotRoot = opts.WorkspaceRoot
		return control.New(control.Options{Sink: opts.Sink, SessionDir: opts.SessionDir, WorkspaceRoot: opts.WorkspaceRoot}), nil
	}

	err := server.busyDetach(context.Background(), cur, targetPath, func(next *control.Controller) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if gotRoot != foregroundRoot {
		t.Fatalf("no session root: replacement root = %q, want inherited %q", gotRoot, foregroundRoot)
	}
}

// buildTagged's plain path (model switch, session rotation) must not gain a
// root override by accident: empty override keeps the inherit semantics.
func TestBuildTaggedWithoutOverrideKeepsInheritedRoot(t *testing.T) {
	foregroundRoot := t.TempDir()
	curPath := filepath.Join(t.TempDir(), "foreground.jsonl")
	bc := NewBroadcaster()
	cur := control.New(control.Options{Sink: bc, SessionPath: curPath, SessionDir: filepath.Dir(curPath), WorkspaceRoot: foregroundRoot})
	server := New(cur, bc, config.ServeConfig{})

	var gotRoot string
	server.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		gotRoot = opts.WorkspaceRoot
		return control.New(control.Options{Sink: opts.Sink, SessionDir: opts.SessionDir, WorkspaceRoot: opts.WorkspaceRoot}), nil
	}
	built, _, err := server.buildTagged(context.Background(), "provider/model", false)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	if gotRoot != foregroundRoot {
		t.Fatalf("plain buildTagged root = %q, want inherited %q", gotRoot, foregroundRoot)
	}
}
