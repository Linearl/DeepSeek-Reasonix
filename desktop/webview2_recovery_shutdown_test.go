package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newWebView2ShutdownTestCoordinator builds a coordinator whose relaunch work
// is inert: the journal lands in a temp dir and the App is a bare value. That
// keeps the tests on the decision the code makes (restart or not) instead of
// on Wails, the window state or the launcher.
func newWebView2ShutdownTestCoordinator(t *testing.T, app *App, journalPath string, now func() time.Time) *webView2RecoveryCoordinator {
	t.Helper()
	return &webView2RecoveryCoordinator{
		app:     app,
		journal: webView2RecoveryJournal{path: filepath.Join(journalPath), now: now},
		now:     now,
	}
}

func restartSuppressedInJournal(t *testing.T, journalPath string) bool {
	t.Helper()
	file, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	return strings.Contains(string(file), "restart_suppressed")
}

// Task 1041: a system shutdown kills WebView2's browser process, so the
// browser failure arrives while the host is already exiting. Requesting a
// relaunch there resurrects the app into a machine that is powering off.
func TestWebView2RequestRestartSuppressedWhileShuttingDown(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "webview2-recovery.jsonl")
	app := &App{}
	app.shuttingDown.Store(true)
	now := time.Date(2026, 10, 21, 0, 41, 38, 0, time.UTC)
	c := newWebView2ShutdownTestCoordinator(t, app, journalPath, func() time.Time { return now })

	c.requestRestart("browser_process_exited.not_applicable")

	if !restartSuppressedInJournal(t, journalPath) {
		t.Fatal("shutting-down restart was not recorded as suppressed")
	}

	// The guard blocks every entry point, not just the browser one: the timer
	// for an unready renderer fires after shutdown all the same.
	app.forceQuit.Store(true)
	c.requestRestart("renderer_ready_timeout")
	if count := strings.Count(readFile(t, journalPath), "restart_suppressed"); count != 2 {
		t.Fatalf("force-quit restart not suppressed, journal=%s", readFile(t, journalPath))
	}
}

func TestWebView2RequestRestartSuppressedWhileForceQuitting(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "webview2-recovery.jsonl")
	app := &App{}
	app.forceQuit.Store(true)
	now := time.Date(2026, 10, 21, 0, 41, 38, 0, time.UTC)
	c := newWebView2ShutdownTestCoordinator(t, app, journalPath, func() time.Time { return now })

	c.requestRestart("browser_process_exited.not_applicable")

	if !restartSuppressedInJournal(t, journalPath) {
		t.Fatal("force-quit restart was not recorded as suppressed")
	}
}

// Task 1041: the suppression must be limited to an exiting host. A live host
// still reaches the restart decision, so the guard cannot turn every browser
// crash into a permanent no-recovery state.
func TestWebView2RequestRestartReachesGuardWhenHostIsAlive(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "webview2-recovery.jsonl")
	now := time.Date(2026, 10, 21, 0, 41, 38, 0, time.UTC)
	c := newWebView2ShutdownTestCoordinator(t, &App{}, journalPath, func() time.Time { return now })

	c.requestRestart("browser_process_exited.not_applicable")

	entries := readFile(t, journalPath)
	if strings.Contains(entries, "restart_suppressed") {
		t.Fatalf("live host suppressed the restart: %s", entries)
	}
	if !strings.Contains(entries, "auto_restart") && !strings.Contains(entries, "restart_blocked") {
		t.Fatalf("live host never reached the restart guard: %s", entries)
	}
}

// Task 1041: the browser failure arrives while the host is exiting, so the
// diagnostic must be kept while no relaunch is requested. This is the
// shutdown path end to end, minus the launcher.
func TestWebView2BrowserFailureDuringShutdownKeepsDiagnosticsWithoutRestart(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "webview2-recovery.jsonl")
	app := &App{}
	app.shuttingDown.Store(true)
	now := time.Date(2026, 10, 21, 0, 41, 38, 0, time.UTC)
	c := newWebView2ShutdownTestCoordinator(t, app, journalPath, func() time.Time { return now })

	// 0x40010004: the shutdown kill that opened issue #63.
	c.nativeFailure(webView2NativeEvent{Kind: 0, Reason: 0, ReasonAvailable: true, ExitCode: exitCodeTerminatedExternally, ExitCodeAvailable: true})

	if !restartSuppressedInJournal(t, journalPath) {
		t.Fatalf("shutdown browser failure requested a restart: %s", readFile(t, journalPath))
	}
	// The state machine still records the failure, so nothing downstream that
	// reads the phase loses the event to the suppression.
	if c.state.Phase != webView2RecoveryRestarting {
		t.Fatalf("phase = %q, diagnostics state was dropped", c.state.Phase)
	}
	// The browser event keeps its diagnostic classification even when the
	// restart is suppressed: telemetry must still see the shutdown kill.
	report, outcome := webView2NativeFailureReport(webView2NativeEvent{Kind: 0, Reason: 0, ReasonAvailable: true, ExitCode: exitCodeTerminatedExternally, ExitCodeAvailable: true}, "155.0.4283.62", false)
	if report.Kind != "performance" || outcome != "terminated_externally" {
		t.Fatalf("shutdown kill classified as kind=%q outcome=%q", report.Kind, outcome)
	}
}

// Task 1041: a browser crash on a healthy host keeps the whole recovery path.
// The guard must not turn "shutdown suppression" into "never recover".
func TestWebView2BrowserCrashOnHealthyHostStillRequestsRestart(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "webview2-recovery.jsonl")
	now := time.Date(2026, 10, 21, 0, 41, 38, 0, time.UTC)
	c := newWebView2ShutdownTestCoordinator(t, &App{}, journalPath, func() time.Time { return now })

	c.nativeFailure(webView2NativeEvent{Kind: 0, Reason: 3, ReasonAvailable: true, ExitCode: -1073741819, ExitCodeAvailable: true})

	entries := readFile(t, journalPath)
	if strings.Contains(entries, "restart_suppressed") {
		t.Fatalf("healthy host suppressed a crash restart: %s", entries)
	}
	if !strings.Contains(entries, "auto_restart") && !strings.Contains(entries, "restart_blocked") {
		t.Fatalf("healthy host never reached the restart guard: %s", entries)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
