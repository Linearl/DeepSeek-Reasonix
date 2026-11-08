package main

import (
	"context"
	"sync/atomic"
	"testing"

	"reasonix/internal/config"
)

// TestParseDetachedIdleMinutes pins the O4 gate: off unless a positive minute
// value is set explicitly (fork rule 2 — default process keeps today's
// never-release behaviour).
func TestParseDetachedIdleMinutes(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"garbage", 0},
		{"30", 30},
		{"1", 1},
	}
	for _, c := range cases {
		if got := parseDetachedIdleMinutes(c.raw); got != c.want {
			t.Fatalf("parse(%q)=%d, want %d", c.raw, got, c.want)
		}
	}
}

// TestDetachedIdleReleaseMinutesServesCachedTier is the tl1496b gate
// (provider-poll 同类面·两裁定)：the 5-minute reaper tick is a config re-read
// face — it used to pay a full desktop user-config load every beat. The
// threshold now resolves through the task-609 config snapshot, so the pins
// mirror the effort read face's contract: the first call may pay one full
// load, warm calls are snapshot-served (zero loads), and a config write shows
// up on the very next call with exactly one fingerprint-invalidated reload.
func TestDetachedIdleReleaseMinutesServesCachedTier(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Desktop.DetachedIdleReleaseMinutes = 42
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()

	var loads atomic.Int32
	orig := configLoadForRoot
	configLoadForRoot = func(root string) (*config.Config, error) {
		loads.Add(1)
		return orig(root)
	}
	t.Cleanup(func() { configLoadForRoot = orig })

	if got := app.detachedIdleReleaseMinutes(); got != 42 {
		t.Fatalf("prime read = %d, want 42", got)
	}
	primed := loads.Load()
	if primed == 0 {
		t.Fatal("prime read never loaded the config from disk")
	}

	// Warm ticks: the snapshot serves them all; a single reload here would
	// mean the reaper still pays the per-beat full load this change removes.
	for i := 0; i < 3; i++ {
		if got := app.detachedIdleReleaseMinutes(); got != 42 {
			t.Fatalf("warm read %d = %d, want 42", i, got)
		}
	}
	if now := loads.Load(); now != primed {
		t.Fatalf("config loads = %d after prime + 3 warm reads (primed %d), want no reload on warm reads", now, primed)
	}

	// A settings save (the unchanged write path) shows up on the very next
	// tick with exactly one reload — the cached tier follows the config, it is
	// not a stale write-through copy.
	cfg.Desktop.DetachedIdleReleaseMinutes = 90
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	if got := app.detachedIdleReleaseMinutes(); got != 90 {
		t.Fatalf("read after config change = %d, want 90", got)
	}
	if now := loads.Load(); now != primed+1 {
		t.Fatalf("config loads after change = %d, want exactly one reload (primed %d)", now, primed)
	}
}

// TestDetachedIdleReleaseMinutesEnvFallbackWhenConfigUnset keeps the O4 gate
// honest on the cached path: with no positive config value the env override
// still wins (one-process debugging), and unsetting both keeps the
// never-release default (fork rule 2).
func TestDetachedIdleReleaseMinutesEnvFallbackWhenConfigUnset(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := config.Default().SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()

	t.Setenv("REASONIX_DETACHED_IDLE_RELEASE_MINUTES", "7")
	if got := app.detachedIdleReleaseMinutes(); got != 7 {
		t.Fatalf("env override read = %d, want 7", got)
	}
	t.Setenv("REASONIX_DETACHED_IDLE_RELEASE_MINUTES", "")
	if got := app.detachedIdleReleaseMinutes(); got != 0 {
		t.Fatalf("unset read = %d, want 0 (never-release default)", got)
	}
}
