package main

// tl1692 tests: the automatic AI naming chain must be zero-action when
// agent.title_model is unset (the shipped default — behaviour identical to the
// heuristic-only chain), must never re-fire for the same session, must commit
// through the auto-title registry channel (source=auto, never manual), and
// must always bow to a manual rename.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"reasonix/internal/agent"

	"reasonix/internal/control"
	"reasonix/internal/provider"
)

func resetAutoAITitleTestState(t *testing.T) {
	t.Helper()
	autoAITitleMu.Lock()
	autoAITitleSeen = map[string]struct{}{}
	autoAITitleMu.Unlock()
	t.Cleanup(func() {
		autoAITitleMu.Lock()
		autoAITitleSeen = map[string]struct{}{}
		autoAITitleMu.Unlock()
	})
}

func p18SaveDesktopSession(t *testing.T, userTurns int) string {
	t.Helper()
	s := agent.NewSession("You are Reasonix, a coding agent.")
	for i := 0; i < userTurns; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "please refactor the parser"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	}
	path := filepath.Join(t.TempDir(), "auto-ai-title.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMaybeKickAutoAITitleZeroActionWhenUnset: agent.title_model unset (the
// shipped default) closes the whole chain before anything runs — no generate
// call, no done-marker, behaviour byte-identical to the heuristic-only chain.
func TestMaybeKickAutoAITitleZeroActionWhenUnset(t *testing.T) {
	resetAutoAITitleTestState(t)
	var calls atomic.Int64
	savedConfigured, savedGenerate := titleModelConfigured, autoAITitleGenerate
	titleModelConfigured = func() bool { return false }
	autoAITitleGenerate = func(context.Context, *control.Controller, string) (string, error) {
		calls.Add(1)
		return "should never be called", nil
	}
	t.Cleanup(func() { titleModelConfigured, autoAITitleGenerate = savedConfigured, savedGenerate })

	app := &App{}
	tab := &WorkspaceTab{}
	path := p18SaveDesktopSession(t, 2)
	app.maybeKickAutoAITopicTitle(tab, "topic-unset", t.TempDir(), path)

	if calls.Load() != 0 {
		t.Fatalf("generate ran %d times with title_model unset; want 0", calls.Load())
	}
	if autoAITitleAlreadyDone(path) {
		t.Fatal("the done-marker must not be recorded when the chain is off")
	}
}

// TestMaybeKickAutoAITitleZeroActionWithoutController: configured but the tab
// carries no controller (closed/background session) — the chain stays inert
// and must not consume the one-shot marker.
func TestMaybeKickAutoAITitleZeroActionWithoutController(t *testing.T) {
	resetAutoAITitleTestState(t)
	var calls atomic.Int64
	savedConfigured, savedGenerate := titleModelConfigured, autoAITitleGenerate
	titleModelConfigured = func() bool { return true }
	autoAITitleGenerate = func(context.Context, *control.Controller, string) (string, error) {
		calls.Add(1)
		return "", nil
	}
	t.Cleanup(func() { titleModelConfigured, autoAITitleGenerate = savedConfigured, savedGenerate })

	app := &App{}
	tab := &WorkspaceTab{}
	path := p18SaveDesktopSession(t, 2)
	app.maybeKickAutoAITopicTitle(tab, "topic-noctrl", t.TempDir(), path)

	if calls.Load() != 0 {
		t.Fatalf("generate ran %d times without a controller; want 0", calls.Load())
	}
	if autoAITitleAlreadyDone(path) {
		t.Fatal("a no-controller session must not burn the one-shot marker")
	}
}

// TestMaybeKickAutoAITitleOncePerSession: a configured session fires exactly
// one generate call — a failed or repeated autosave pass must not retry-storm
// the title model.
func TestMaybeKickAutoAITitleOncePerSession(t *testing.T) {
	resetAutoAITitleTestState(t)
	var calls atomic.Int64
	savedConfigured, savedGenerate := titleModelConfigured, autoAITitleGenerate
	titleModelConfigured = func() bool { return true }
	autoAITitleGenerate = func(context.Context, *control.Controller, string) (string, error) {
		calls.Add(1)
		return "", fmt.Errorf("provider down")
	}
	t.Cleanup(func() { titleModelConfigured, autoAITitleGenerate = savedConfigured, savedGenerate })

	app := &App{}
	workspaceRoot := t.TempDir()
	tab := &WorkspaceTab{Ctrl: control.New(control.Options{})}
	path := p18SaveDesktopSession(t, 2)
	// A fresh topic state so the source gate (auto) passes.
	if err := createTopicState(workspaceRoot, "topic-once", defaultTopicTitle, topicTitleSourceAuto, 1); err != nil {
		t.Fatal(err)
	}
	app.maybeKickAutoAITopicTitle(tab, "topic-once", workspaceRoot, path)
	app.maybeKickAutoAITopicTitle(tab, "topic-once", workspaceRoot, path)

	if calls.Load() != 1 {
		t.Fatalf("generate ran %d times across two passes; want exactly 1", calls.Load())
	}
}

// TestCommitAutoAITopicTitleAutoSourceAndManualGuard: the commit lands through
// the auto-title registry (source stays auto) and a manual rename wins — a
// manually renamed session is never overwritten by the parallel AI title.
func TestCommitAutoAITopicTitleAutoSourceAndManualGuard(t *testing.T) {
	resetAutoAITitleTestState(t)
	app := &App{}
	workspaceRoot := t.TempDir()

	t.Run("commits as auto source", func(t *testing.T) {
		if err := createTopicState(workspaceRoot, "topic-ai", defaultTopicTitle, topicTitleSourceAuto, 1); err != nil {
			t.Fatal(err)
		}
		path := p18SaveDesktopSession(t, 2)
		app.commitAutoAITopicTitle("topic-ai", workspaceRoot, path, "Parser refactor", "please refactor the parser", 1)
		if got := loadTopicTitle(workspaceRoot, "topic-ai"); got != "Parser refactor" {
			t.Fatalf("title = %q, want the AI-generated name", got)
		}
		if source := loadTopicTitleSource(workspaceRoot, "topic-ai"); source != topicTitleSourceAuto {
			t.Fatalf("source = %q, want auto (an AI name is an auto name, never manual)", source)
		}
	})

	t.Run("manual rename wins", func(t *testing.T) {
		if err := createTopicState(workspaceRoot, "topic-manual", defaultTopicTitle, topicTitleSourceAuto, 1); err != nil {
			t.Fatal(err)
		}
		path := p18SaveDesktopSession(t, 2)
		if err := setTopicTitleWithSource(workspaceRoot, "topic-manual", "My manual name", topicTitleSourceManual); err != nil {
			t.Fatal(err)
		}
		app.commitAutoAITopicTitle("topic-manual", workspaceRoot, path, "AI name", "please refactor the parser", 1)
		if got := loadTopicTitle(workspaceRoot, "topic-manual"); got != "My manual name" {
			t.Fatalf("title = %q, want the manual name to survive", got)
		}
	})
}
