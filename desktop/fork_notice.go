package main

import (
	"strings"

	"reasonix/internal/config"
)

// Fork first-launch notice (task 670, user instruction 20261009).
//
// After an update (or a fast version switch) lands a NEW version tree, the
// first launch of that version raises a small dialog: "this is the fork
// build", with a button into Settings → 实验室 and a 「下次不提醒」 opt-out.
// The same version never prompts twice, however the dialog was closed.
//
// State lives in the user config ([desktop], NOT experimental_ keys — the
// mute is a stored user answer, and the switch defaults ON by explicit user
// ruling: a pure UI notice with no behaviour change):
//
//	fork_notice               *bool  nil-means-on switch (task 265 shape)
//	fork_notice_muted         bool   「下次不提醒」 preference
//	fork_notice_last_version  string version tree the notice last fired for
//
// The gate mirrors the task-277 chime shape: decide from (running version !=
// last-notified version), and let the frontend acknowledge (write the
// version) as soon as the dialog is actually raised — write-before-show, so
// a crash can never turn into a nag loop.

// ForkNoticeState is what the frontend needs to decide whether to raise the
// fork first-launch notice this launch.
type ForkNoticeState struct {
	// ShouldShow is the single decision bit: switch on, not muted, this is a
	// versioned install, and the running version was never notified before.
	ShouldShow bool `json:"shouldShow"`
	// Version is the running version-tree name ("" for non-versioned installs,
	// which never prompt). The frontend echoes it back via AcknowledgeForkNotice.
	Version string `json:"version"`
	// Enabled is the resolved (nil-means-on) lab switch, for diagnostics/tests.
	Enabled bool `json:"enabled"`
	// Muted is the stored 「下次不提醒」 preference, for diagnostics/tests.
	Muted bool `json:"muted"`
}

// forkNoticeShouldShow is the pure gate (unit-tested in fork_notice_test.go):
// enabled switch, not muted, a running version to speak of, and that version
// different from the last notified one.
func forkNoticeShouldShow(enabled, muted bool, lastVersion, runningVersion string) bool {
	if !enabled || muted {
		return false
	}
	version := strings.TrimSpace(runningVersion)
	if version == "" {
		// Not a versioned install (dev tree, bare binary): the "new version
		// landed" premise cannot be established, so stay silent.
		return false
	}
	return lastVersion != version
}

// GetForkNoticeState resolves the notice gate for this launch. Read errors
// resolve to "don't show" — a broken config must never nag.
func (a *App) GetForkNoticeState() ForkNoticeState {
	state := ForkNoticeState{}
	if running := strings.TrimSpace(versionSwitchRunningVersion()); running != "" {
		state.Version = running
	}
	cfg, err := config.LoadUserConfigReadOnly()
	if err != nil || cfg == nil {
		return state
	}
	state.Enabled = cfg.DesktopForkNoticeEnabled()
	state.Muted = cfg.Desktop.ForkNoticeMuted
	state.ShouldShow = forkNoticeShouldShow(state.Enabled, state.Muted, cfg.Desktop.ForkNoticeLastVersion, state.Version)
	return state
}

// AcknowledgeForkNotice records that the notice fired for this version. The
// frontend calls it when the dialog is actually raised (write-before-show),
// so the same version never prompts twice across restarts.
func (a *App) AcknowledgeForkNotice(version string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.AcknowledgeForkNoticeVersion(version) })
}

// DismissForkNoticeForever stores the 「下次不提醒」 answer: no future version
// swap raises the notice again until the lab switch is turned off and back on.
func (a *App) DismissForkNoticeForever() error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopForkNoticeMuted(true) })
}
