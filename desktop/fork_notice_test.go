package main

import "testing"

// Task 670: the pure gate. The notice fires exactly once per version tree:
// enabled switch + not muted + a versioned install + never notified before.
// Every other combination stays silent.
func TestForkNoticeShouldShow(t *testing.T) {
	const (
		running = "v1.38.3-20261009-1031"
		older   = "v1.38.3-20261009-0251"
	)
	cases := []struct {
		name        string
		enabled     bool
		muted       bool
		lastVersion string
		runningVer  string
		want        bool
	}{
		{"new version first launch", true, false, older, running, true},
		{"same version second launch", true, false, running, running, false},
		{"switch off", false, false, older, running, false},
		{"muted wins over new version", true, true, older, running, false},
		{"non-versioned install never prompts", true, false, "", "", false},
		{"blank running version treated as non-versioned", true, false, older, "  ", false},
		{"no record yet (fresh install)", true, false, "", running, true},
	}
	for _, tc := range cases {
		if got := forkNoticeShouldShow(tc.enabled, tc.muted, tc.lastVersion, tc.runningVer); got != tc.want {
			t.Fatalf("%s: forkNoticeShouldShow = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Task 670: the full launch loop against an isolated REASONIX_HOME — fresh
// environment resolves to the default-on switch; acknowledging the raised
// dialog pins the version so the same tree never prompts again; 「下次不提醒」
// silences even a future version swap.
func TestForkNoticeLaunchLoop(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	origRunning := versionSwitchRunningVersion
	defer func() { versionSwitchRunningVersion = origRunning }()
	versionSwitchRunningVersion = func() string { return "v1.38.3-20261009-1031" }

	a := &App{}
	first := a.GetForkNoticeState()
	if !first.ShouldShow {
		t.Fatalf("first launch of a new version must show the notice, got %+v", first)
	}
	if !first.Enabled || first.Muted {
		t.Fatalf("fresh environment must be enabled and unmuted, got %+v", first)
	}

	if err := a.AcknowledgeForkNotice(first.Version); err != nil {
		t.Fatalf("AcknowledgeForkNotice: %v", err)
	}
	second := a.GetForkNoticeState()
	if second.ShouldShow {
		t.Fatalf("same version must never re-prompt after acknowledge, got %+v", second)
	}

	// A future version swap shows again — unless the user asked to be muted.
	versionSwitchRunningVersion = func() string { return "v1.38.4-20261010-0000" }
	swapped := a.GetForkNoticeState()
	if !swapped.ShouldShow {
		t.Fatalf("version swap must re-arm the notice, got %+v", swapped)
	}
	if err := a.DismissForkNoticeForever(); err != nil {
		t.Fatalf("DismissForkNoticeForever: %v", err)
	}
	muted := a.GetForkNoticeState()
	if muted.ShouldShow || !muted.Muted {
		t.Fatalf("muted environment must never show the notice, got %+v", muted)
	}
}
