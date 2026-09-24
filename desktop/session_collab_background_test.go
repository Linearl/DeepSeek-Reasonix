package main

import (
	"strings"
	"testing"

	"reasonix/internal/config"
)

// TestCollabOpenDetachedFromConfigTwoModes pins task 264's mode gate (the
// two-mode test): default/off = byte-for-byte baseline (stand-up opens and
// activates a tab), on = detached stand-up (no tab in the bar). Delivery
// semantics live outside this flag — it only picks where the runtime lands.
func TestCollabOpenDetachedFromConfigTwoModes(t *testing.T) {
	if CollabOpenDetachedFromConfig(nil) {
		t.Fatal("nil config must read as baseline (tab stand-up)")
	}
	off := &config.Config{}
	if CollabOpenDetachedFromConfig(off) {
		t.Fatal("default (off) must be the byte-for-byte baseline")
	}
	on := &config.Config{}
	on.Agent.SessionCollabBackground = true
	if !CollabOpenDetachedFromConfig(on) {
		t.Fatal("session_collab_background=true must select the detached stand-up")
	}
}

// TestSetSessionCollabBackgroundRoundTrips pins the panel path: the setter
// writes the same key the drain reads, so a panel flip applies live.
func TestSetSessionCollabBackgroundRoundTrips(t *testing.T) {
	c := &config.Config{}
	if err := c.SetSessionCollabBackground(true); err != nil {
		t.Fatal(err)
	}
	if !CollabOpenDetachedFromConfig(c) {
		t.Fatal("setter must flip the mode the drain reads")
	}
	out := config.RenderTOMLForScope(c, config.RenderScopeUser)
	if !strings.Contains(out, "session_collab_background = true") {
		t.Fatalf("rendered config is missing session_collab_background:\n%s", out)
	}
	if err := c.SetSessionCollabBackground(false); err != nil {
		t.Fatal(err)
	}
	if CollabOpenDetachedFromConfig(c) {
		t.Fatal("setter must turn the mode back off")
	}
}

// TestParkTabAsDetachedRefusesGracefully pins the failure contract: a tab
// that cannot be parked (unknown id, no runtime) stays visible in a.tabs —
// parking may fail toward baseline, never toward an unreachable runtime.
func TestParkTabAsDetachedRefusesGracefully(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	if err := app.parkTabAsDetached("missing"); err == nil {
		t.Fatal("parking a missing tab must fail")
	}

	// A tab without a runtime cannot be parked and must NOT be removed.
	app.tabs["no-ctrl"] = &WorkspaceTab{ID: "no-ctrl"}
	if err := app.parkTabAsDetached("no-ctrl"); err == nil {
		t.Fatal("parking a runtime-less tab must fail")
	}
	if _, ok := app.tabs["no-ctrl"]; !ok {
		t.Fatal("a failed park must leave the tab visible (baseline fallback)")
	}
}

// TestOpenTopicSessionDetachedFallsBackToVisible pins the composite: if the
// session cannot open at all the caller sees the error; parking failure (see
// above) degrades to a visible inactive tab, logged — the message always has
// a home.
func TestOpenTopicSessionDetachedFallsBackToVisible(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{tabs: map[string]*WorkspaceTab{}}
	// An invalid scope/root fails validation before any tab exists — the error
	// must surface so drain() logs and retries next pass (message not acked).
	_, err := app.OpenTopicSessionDetached("project", "", "topic-x", "/nope/nowhere.jsonl")
	if err == nil || !strings.Contains(err.Error(), "workspaceRoot") {
		t.Fatalf("expected scope validation error, got %v", err)
	}
	if len(app.tabs) != 0 {
		t.Fatalf("failed open must not leave a tab behind: %d", len(app.tabs))
	}
}
