package boot

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/environment"
	"reasonix/internal/event"
	"reasonix/internal/sandbox"
)

var (
	shellLabelBashShell       = sandbox.Shell{Kind: sandbox.ShellBash, Path: "/usr/bin/bash"}
	shellLabelPowerShellShell = sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}
)

// captureShellLabelWarnLog redirects the default slog logger into a buffer for
// the duration of the test so flap warnings can be counted.
func captureShellLabelWarnLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// TestShellLabelStableAcrossRestarts covers the tl1614 acceptance "连续 ≥3 次
// 重启（同配置），- Shell: 值不变": every stableShellLabel call models one boot
// of a fresh process — the only carried state is the snapshot file on disk.
// After the first boot pins "bash", three later boots resolving a flapped
// PowerShell environment must keep rendering the persisted label.
func TestShellLabelStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first := stableShellLabel(shellLabelBashShell, "", "", dir, event.Discard)
	if first != "bash" {
		t.Fatalf("first boot label = %q, want bash", first)
	}
	for restart := 1; restart <= 3; restart++ {
		got := stableShellLabel(shellLabelPowerShellShell, "", "", dir, event.Discard)
		if got != first {
			t.Fatalf("restart %d label = %q, want stable %q", restart, got, first)
		}
		snap, ok := loadShellLabelSnapshot(dir)
		if !ok || snap.Label != first {
			t.Fatalf("restart %d persisted label = %q (loaded %v), want stable %q", restart, snap.Label, ok, first)
		}
	}
}

// TestShellLabelFirstBootPersistsResolution checks the tl1614 C anchor: the
// first boot writes the resolved label plus the [tools.shell] config pair to
// the snapshot, and an empty cache dir degrades to a pure resolution without
// writing anything.
func TestShellLabelFirstBootPersistsResolution(t *testing.T) {
	dir := t.TempDir()
	got := stableShellLabel(shellLabelBashShell, "Auto", ` ~/bin/bash `, dir, event.Discard)
	if got != "/usr/bin/bash" {
		t.Fatalf("first boot label = %q, want /usr/bin/bash", got)
	}
	snap, ok := loadShellLabelSnapshot(dir)
	if !ok {
		t.Fatal("first boot did not persist the shell label snapshot")
	}
	if snap.Label != "/usr/bin/bash" || snap.Prefer != "auto" || snap.Path != "~/bin/bash" {
		t.Fatalf("persisted snapshot = %+v, want bound-path label with normalized config key", snap)
	}
	// Empty cache dir: no persistence possible, resolution still returned.
	if got := stableShellLabel(shellLabelBashShell, "", "", "", event.Discard); got != "bash" {
		t.Fatalf("empty cache dir label = %q, want bash", got)
	}
}

// TestShellLabelSnapshotLossReanchorsSilently covers the safety-net path: a
// wiped or unreadable snapshot must behave like a first boot — adopt the fresh
// resolution, persist it, and not fire the mismatch warning (there is no
// comparable previous value).
func TestShellLabelSnapshotLossReanchorsSilently(t *testing.T) {
	dir := t.TempDir()
	if _, ok := loadShellLabelSnapshot(dir); ok {
		t.Fatal("fresh temp dir unexpectedly has a shell label snapshot")
	}
	buf := captureShellLabelWarnLog(t)
	got := stableShellLabel(shellLabelPowerShellShell, "", "", dir, event.Discard)
	if got != "powershell" {
		t.Fatalf("post-loss label = %q, want powershell", got)
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 0 {
		t.Fatalf("snapshot loss fired %d warnings, want 0", n)
	}
}

// TestShellLabelConfigChangeUpdatesWithoutWarning covers the tl1614 acceptance
// "改 Tools.Shell.Path 后重启，label 正确更新且不误告警": an explicit config
// change (path or prefer) re-anchors the snapshot to the fresh resolution with
// zero warnings, and the config that landed stays stable on the next boot.
func TestShellLabelConfigChangeUpdatesWithoutWarning(t *testing.T) {
	dir := t.TempDir()
	if got := stableShellLabel(shellLabelBashShell, "auto", "", dir, event.Discard); got != "bash" {
		t.Fatalf("anchor label = %q, want bash", got)
	}
	buf := captureShellLabelWarnLog(t)

	// Explicit path change: the freshly bound executable becomes the label.
	got := stableShellLabel(sandbox.Shell{Kind: sandbox.ShellBash, Path: `/opt/new/bash`}, "auto", `/opt/new/bash`, dir, event.Discard)
	if got != `/opt/new/bash` {
		t.Fatalf("path-change label = %q, want /opt/new/bash", got)
	}
	// Explicit prefer change: powershell preference flips the kind label.
	if got := stableShellLabel(shellLabelPowerShellShell, "powershell", "", dir, event.Discard); got != "powershell" {
		t.Fatalf("prefer-change label = %q, want powershell", got)
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 0 {
		t.Fatalf("config changes fired %d warnings, want 0", n)
	}

	// The new config persists: a follow-up boot under the same pair stays put.
	if got := stableShellLabel(shellLabelPowerShellShell, "powershell", "", dir, event.Discard); got != "powershell" {
		t.Fatalf("follow-up label = %q, want powershell", got)
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 0 {
		t.Fatalf("follow-up boot fired %d warnings, want 0", n)
	}
}

// TestShellLabelFlapReportsWarnAndNoticeOnce is the tl1614 B injection test:
// with a persisted value that disagrees with the boot resolution under
// unchanged config, exactly one slog warning and one frontend notice fire,
// both carrying the from→to values and the report hint, and the persisted
// label keeps rendering.
func TestShellLabelFlapReportsWarnAndNoticeOnce(t *testing.T) {
	dir := t.TempDir()
	saveShellLabelSnapshot(dir, shellLabelSnapshot{
		Version:  shellLabelSnapshotVersion,
		Prefer:   "auto",
		Path:     "",
		Label:    "bash",
		StoredAt: time.Now(),
	})
	buf := captureShellLabelWarnLog(t)
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })

	got := stableShellLabel(shellLabelPowerShellShell, "", "", dir, sink)
	if got != "bash" {
		t.Fatalf("flap label = %q, want persisted bash", got)
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("flap fired %d slog warnings, want 1\nlog: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "from=bash") || !strings.Contains(buf.String(), "to=powershell") {
		t.Fatalf("flap warning missing from/to values\nlog: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "please report it") {
		t.Fatalf("flap warning missing report hint\nlog: %s", buf.String())
	}
	if len(events) != 1 {
		t.Fatalf("flap emitted %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != event.Notice || e.Level != event.LevelWarn {
		t.Fatalf("flap event kind=%v level=%v, want Notice/Warn", e.Kind, e.Level)
	}
	for _, want := range []string{"bash", "powershell", "not a configuration change", "please report it"} {
		if !strings.Contains(e.Text, want) {
			t.Fatalf("flap notice text %q missing %q", e.Text, want)
		}
	}
}

// TestShellLabelSnapshotSeparateFromProbeSnapshots covers the tl1614 acceptance
// "既有 tools probe 防抖零回退（快照键分域）": the shell label snapshot and the
// probe snapshots share the SnapshotDir root without touching each other's
// files — a probe run neither reads nor clobbers the label, and the label
// debounce keeps working after probe writes.
func TestShellLabelSnapshotSeparateFromProbeSnapshots(t *testing.T) {
	dir := t.TempDir()
	anchored := stableShellLabel(shellLabelBashShell, "", "", dir, event.Discard)
	if anchored != "bash" {
		t.Fatalf("anchor label = %q, want bash", anchored)
	}

	results := environment.RunProbesWithOptions(context.Background(), []string{"go version"}, environment.ProbeOptions{SnapshotDir: dir})
	if len(results) != 1 || !results[0].Found {
		t.Fatalf("probe results = %+v, want a found go version probe", results)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "environment"))
	if err != nil {
		t.Fatalf("read environment snapshot dir: %v", err)
	}
	probeFiles := 0
	labelFile := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "probes-") && strings.HasSuffix(name, ".json") {
			probeFiles++
		}
		if name == "shell-label.json" {
			labelFile++
		}
	}
	if probeFiles != 1 || labelFile != 1 {
		t.Fatalf("snapshot dir entries = %d probes files, %d label files, want 1 each", probeFiles, labelFile)
	}

	// The label debounce still reads its own domain after the probe write and
	// still rejects a flapped resolution.
	if got := stableShellLabel(shellLabelBashShell, "", "", dir, event.Discard); got != anchored {
		t.Fatalf("post-probe label = %q, want stable %q", got, anchored)
	}
	if got := stableShellLabel(shellLabelPowerShellShell, "", "", dir, event.Discard); got != anchored {
		t.Fatalf("post-probe flap label = %q, want persisted %q", got, anchored)
	}
	// And the domain boundary is structural: the label snapshot path is a fixed
	// name the probes-<hash> namespace can never produce.
	if base := filepath.Base(shellLabelSnapshotPath(dir)); base != "shell-label.json" {
		t.Fatalf("shell label snapshot base = %q, want shell-label.json", base)
	}
}
