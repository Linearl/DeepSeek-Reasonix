package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// TestEventsRotationSettersWriteConfigAndPushAgent pins the host side of the
// task-333 chain: both setters persist the config and immediately forward the
// normalized values into the agent save path (no restart), while a bad mode is
// refused by the config layer instead of being stored.
func TestEventsRotationSettersWriteConfigAndPushAgent(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	t.Cleanup(func() { agent.SetEventsAutoRotation("manual", 4, 0) })
	app := &App{ctx: context.Background()}

	if err := app.SetEventsAutoRotation("auto"); err != nil {
		t.Fatalf("SetEventsAutoRotation(auto): %v", err)
	}
	if mode, _, _ := agent.EventsAutoRotationSnapshot(); mode != "auto" {
		t.Fatalf("agent snapshot mode = %q, want auto (push must be immediate)", mode)
	}
	body, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	if !strings.Contains(string(body), `events_auto_rotation = "auto"`) {
		t.Fatalf("config missing events_auto_rotation line:\n%s", body)
	}
	// The render table carries all three lines even at defaults (81/123).
	rendered := config.RenderTOML(config.Default())
	for _, want := range []string{"events_auto_rotation", "events_rotation_factor", "events_rotation_cap_mb"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderTOML(defaults) missing %q", want)
		}
	}

	if err := app.SetEventsRotation(8, 256); err != nil {
		t.Fatalf("SetEventsRotation(8, 256): %v", err)
	}
	_, factor, capMB := agent.EventsAutoRotationSnapshot()
	if factor != 8 || capMB != 256 {
		t.Fatalf("agent snapshot thresholds = (%v, %d), want (8, 256)", factor, capMB)
	}
	if err := app.SetEventsAutoRotation("bogus"); err == nil {
		t.Fatal("unknown mode must be refused by the setter")
	}
	if mode, _, _ := agent.EventsAutoRotationSnapshot(); mode != "auto" {
		t.Fatalf("agent snapshot after refused write = %q, want unchanged auto", mode)
	}
}

// newEventsTestSession writes a real schema-1 session whose event log the
// compact action will rewrite.
func newEventsTestSession(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := agent.NewSession("sys")
	msgs := make([]provider.Message, 0, 64)
	for i := 0; i < 64; i++ {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "m"})
	}
	s.AddBatch(msgs...)
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	return path
}

// TestCompactSessionEventsReportsExactFileSizes pins acceptance c5: the
// before/after numbers are byte-exact against the filesystem (±0).
func TestCompactSessionEventsReportsExactFileSizes(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := newEventsTestSession(t)
	eventsLog := store.SessionEventLog(path)
	beforeExpected := int64(-1)
	if info, err := os.Stat(eventsLog); err == nil {
		beforeExpected = info.Size()
	}
	if beforeExpected <= 0 {
		t.Fatalf("seed session must have an events log, stat err baseline=%d", beforeExpected)
	}
	app := &App{ctx: context.Background()}
	res, err := app.CompactSessionEvents(path)
	if err != nil {
		t.Fatalf("CompactSessionEvents: %v", err)
	}
	info, err := os.Stat(eventsLog)
	if err != nil {
		t.Fatalf("stat after compact: %v", err)
	}
	if res.Before != beforeExpected {
		t.Fatalf("before = %d, want exact fs size %d", res.Before, beforeExpected)
	}
	if res.After != info.Size() {
		t.Fatalf("after = %d, want exact fs size %d", res.After, info.Size())
	}
	if res.Freed != res.Before-res.After {
		t.Fatalf("freed = %d, want before-after = %d", res.Freed, res.Before-res.After)
	}
}

// TestCompactSessionEventsBusyReportsSkipped pins acceptance c5: a session
// whose lease is held elsewhere is reported as skipped with the readable
// reason — never silently dropped.
func TestCompactSessionEventsBusyReportsSkipped(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := newEventsTestSession(t)
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	defer lease.Release()

	app := &App{ctx: context.Background()}
	res, compactErr := app.CompactSessionEvents(path)
	if compactErr != nil {
		// The per-row result must reach Wails: a non-nil error would discard
		// (res, err) and hide the skipped state from the UI (review finding).
		t.Fatalf("busy must report through the result, not a rejected pair: %v", compactErr)
	}
	if !res.Skipped {
		t.Fatalf("busy result must be marked skipped: %+v", res)
	}
	if !strings.Contains(res.Error, "not idle") {
		t.Fatalf("lease reason must stay readable in res.Error, got %q", res.Error)
	}
}

// TestRunEventsRepairRounds pins acceptance c6 on the injected multi-round
// core: rounds advance only while progress is made, busy/failed sessions are
// listed without stopping the rest, and the round budget clamps.
func TestRunEventsRepairRounds(t *testing.T) {
	over := func(paths ...string) SessionEventsInventoryView {
		view := SessionEventsInventoryView{Mode: "auto", Factor: 4}
		for _, p := range paths {
			view.Entries = append(view.Entries, SessionEventsEntry{Path: p, Name: p, OverLimit: true})
			view.OverCount++
		}
		return view
	}
	t.Run("multi round until clean", func(t *testing.T) {
		scans := 0
		inventory := func() SessionEventsInventoryView {
			scans++
			switch scans {
			case 1:
				return over("a", "b")
			case 2:
				return over("b")
			default:
				return SessionEventsInventoryView{}
			}
		}
		compact := func(path string) (int64, int64, error) {
			if path == "b" && scans == 1 {
				return 0, 0, os.ErrDeadlineExceeded // busy-looking failure in round 1
			}
			return 1000, 100, nil
		}
		results := runEventsRepairRounds(3, inventory, compact)
		if len(results) != 3 {
			t.Fatalf("results = %d, want 3 (a+b round1, b round2): %+v", len(results), results)
		}
		if results[1].Error == "" {
			t.Fatalf("round-1 b failure must be recorded: %+v", results[1])
		}
		if scans != 3 {
			t.Fatalf("inventory scans = %d, want 3 (stop when clean)", scans)
		}
	})
	t.Run("all busy ends the loop early", func(t *testing.T) {
		scans := 0
		inventory := func() SessionEventsInventoryView {
			scans++
			return over("x")
		}
		compact := func(string) (int64, int64, error) { return 0, 0, os.ErrDeadlineExceeded }
		results := runEventsRepairRounds(3, inventory, compact)
		if len(results) != 1 || scans != 1 {
			t.Fatalf("no-progress run must stop after one round: results=%d scans=%d", len(results), scans)
		}
	})
	t.Run("round budget clamps to three", func(t *testing.T) {
		scans := 0
		inventory := func() SessionEventsInventoryView {
			scans++
			return over("always")
		}
		compact := func(string) (int64, int64, error) { return 1000, 500, nil }
		runEventsRepairRounds(99, inventory, compact)
		if scans != 3 {
			t.Fatalf("inventory scans = %d, want clamp at 3", scans)
		}
	})
}

// TestEventsRotationJudgmentDisplayMode pins the statistic card's judgment
// basis: auto reads the configured thresholds, off/manual fall back to the
// built-in factor so "over limit" keeps a stable meaning.
func TestEventsRotationJudgmentDisplayMode(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	factor, capMB := eventsRotationJudgment()
	if factor != config.EventsRotationFactorDefault || capMB != 0 {
		t.Fatalf("default judgment = (%v, %d), want (4, 0)", factor, capMB)
	}
	app := &App{ctx: context.Background()}
	if err := app.SetEventsAutoRotation("auto"); err != nil {
		t.Fatalf("set auto: %v", err)
	}
	if err := app.SetEventsRotation(6, 64); err != nil {
		t.Fatalf("set thresholds: %v", err)
	}
	factor, capMB = eventsRotationJudgment()
	if factor != 6 || capMB != 64 {
		t.Fatalf("auto judgment = (%v, %d), want (6, 64)", factor, capMB)
	}
	if err := app.SetEventsAutoRotation("off"); err != nil {
		t.Fatalf("set off: %v", err)
	}
	factor, capMB = eventsRotationJudgment()
	if factor != config.EventsRotationFactorDefault || capMB != 0 {
		t.Fatalf("off judgment = (%v, %d), want built-in (4, 0)", factor, capMB)
	}
}
