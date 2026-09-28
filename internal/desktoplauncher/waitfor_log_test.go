package desktoplauncher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

// Task 304 channel 3: the launcher's relaunch-wait warning must survive in
// desktop.log, not only on the launcher's stderr (which dies with the
// process). The line is one slog-shaped WARN entry, greppable exactly like
// the desktop-written ones.
func TestAppendDesktopLogLineWritesSlogShapedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.log")
	old := desktopLogPath
	desktopLogPath = func() string { return path }
	t.Cleanup(func() { desktopLogPath = old })

	appendDesktopLogLine("launcher: previous desktop did not exit within the relaunch wait; starting the new instance anyway pid=4242 timeout=90s err=boom")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back desktop.log: %v", err)
	}
	line := string(data)
	for _, want := range []string{"level=WARN", "source=launcher", "pid=4242", "err=boom"} {
		if !strings.Contains(line, want) {
			t.Fatalf("logged line %q missing %q", line, want)
		}
	}
	if !strings.HasPrefix(line, "time=") {
		t.Fatalf("line %q does not start with a slog time field", line)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSuffix(strings.Fields(line[5:])[0], "")); err != nil {
		t.Fatalf("time field not RFC3339: %v (%q)", err, strings.Fields(line[5:])[0])
	}
}

// A launcher that cannot resolve or open the log must not break the launch:
// the helper returns silently and the stderr copy (already emitted by the
// caller) stays the only trace.
func TestAppendDesktopLogLineFailSafe(t *testing.T) {
	old := desktopLogPath
	desktopLogPath = func() string { return filepath.Join(t.TempDir(), "no-such-dir", "nested", "desktop.log") }
	t.Cleanup(func() { desktopLogPath = old })
	appendDesktopLogLine("must not panic")
}

// The resolved path matches the desktop's rolling log location
// (logs/desktop/desktop.log under the memory user dir).
func TestDesktopLogPathMatchesDesktopLayout(t *testing.T) {
	got := desktopLogPath()
	want := filepath.Join(config.MemoryUserDir(), "logs", "desktop", "desktop.log")
	if got != want {
		t.Fatalf("desktopLogPath = %q, want %q", got, want)
	}
}
