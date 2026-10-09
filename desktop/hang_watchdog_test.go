package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

// stubMainThreadProbe swaps the live message-loop probe for a canned answer
// and restores the previous value on cleanup.
func stubMainThreadProbe(t *testing.T, fn func() mainThreadProbeResult) {
	t.Helper()
	old := probeMainThread
	probeMainThread = fn
	t.Cleanup(func() { probeMainThread = old })
}

// restoreWatchdogGlobals keeps the package-level watchdog state from leaking
// between tests and clears the probe history ring.
func restoreWatchdogGlobals(t *testing.T) {
	t.Helper()
	oldBase := mainThreadClockBase
	oldElapsed := mainThreadLastHeartbeatElapsed.Load()
	oldWall := mainThreadLastHeartbeatWall.Load()
	oldReported := mainThreadHangReported.Load()
	oldPause := mainThreadLastPauseAt.Load()
	t.Cleanup(func() {
		mainThreadClockBase = oldBase
		mainThreadLastHeartbeatElapsed.Store(oldElapsed)
		mainThreadLastHeartbeatWall.Store(oldWall)
		mainThreadHangReported.Store(oldReported)
		mainThreadLastPauseAt.Store(oldPause)
	})
	mainThreadProbeLog.mu.Lock()
	mainThreadProbeLog.fill = 0
	mainThreadProbeLog.next = 0
	mainThreadProbeLog.mu.Unlock()
}

func TestHangAgeBucket(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{12 * time.Second, "s_10_15"},
		{16 * time.Second, "s_15_30"},
		{45 * time.Second, "s_30_60"},
		{2 * time.Minute, "m_1_5"},
		{10 * time.Minute, "m_5_plus"},
	}
	for _, c := range cases {
		if got := hangAgeBucket(c.age); got != c.want {
			t.Errorf("hangAgeBucket(%s) = %q, want %q", c.age, got, c.want)
		}
	}
}

func TestMainThreadHangReportIsStructuredPerformanceReport(t *testing.T) {
	restoreWatchdogGlobals(t)
	stubMainThreadProbe(t, func() mainThreadProbeResult {
		return mainThreadProbeResult{Status: mainThreadProbeHung, Detail: "SendMessageTimeoutW(WM_NULL) timed out after 2000ms", Duration: 2 * time.Second}
	})
	last := time.Date(2026, 7, 4, 9, 19, 11, 0, time.UTC)
	observed := last.Add(16 * time.Second)

	probe := mainThreadProbeResult{
		Status:   mainThreadProbeHung,
		Detail:   "SendMessageTimeoutW(WM_NULL) timed out after 2000ms",
		Duration: 2 * time.Second,
	}
	r := mainThreadHangReport(16*time.Second, last, observed, probe)

	wantLabel, wantErrorType, _, _ := mainThreadDiagnosticIdentity()
	if r.Kind != "performance" || r.Source != "native.watchdog" || r.Label != wantLabel {
		t.Fatalf("unexpected report identity: %+v", r)
	}
	if r.SchemaVersion != 2 || r.ErrorType != wantErrorType || r.TopFrame == "" || r.OccurredAt == "" {
		t.Fatalf("structured fields missing: %+v", r)
	}
	for _, want := range []string{
		"Reasonix detected",
		"last heartbeat:",
		"bucket: s_15_30",
		"--- live probe ---",
		"status: hung",
		"SendMessageTimeoutW(WM_NULL) timed out after 2000ms",
		"--- recent probes (oldest first) ---",
		"--- main goroutine ---",
		"goroutine 1",
	} {
		if !strings.Contains(r.Message, want) {
			t.Fatalf("hang report message missing %q:\n%s", want, r.Message)
		}
	}
	if !strings.Contains(r.Stack, "goroutine ") {
		t.Fatalf("report stack missing goroutine dump:\n%s", r.Stack)
	}
	if got := strings.Count(r.Message, nativeRuntimeContextMarker); got != 1 {
		t.Fatalf("native runtime context marker count = %d, want 1:\n%s", got, r.Message)
	}
}

func TestMainThreadHeartbeatAgeIgnoresWallClockJump(t *testing.T) {
	oldBase := mainThreadClockBase
	oldElapsed := mainThreadLastHeartbeatElapsed.Load()
	oldWall := mainThreadLastHeartbeatWall.Load()
	t.Cleanup(func() {
		mainThreadClockBase = oldBase
		mainThreadLastHeartbeatElapsed.Store(oldElapsed)
		mainThreadLastHeartbeatWall.Store(oldWall)
	})

	base := time.Now()
	mainThreadClockBase = base
	mainThreadLastHeartbeatElapsed.Store(int64(time.Second))
	mainThreadLastHeartbeatWall.Store(base.Add(-time.Hour).UnixNano())

	age, _, ok := mainThreadHeartbeatAge(base.Add(2 * time.Second))
	if !ok {
		t.Fatal("expected heartbeat age")
	}
	if age != time.Second {
		t.Fatalf("age = %s, want monotonic elapsed 1s despite wall-clock jump", age)
	}
}

func TestResetMainThreadHeartbeatAfterGapLegacyFallback(t *testing.T) {
	restoreWatchdogGlobals(t)
	base := time.Now()
	mainThreadClockBase = base
	recordMainThreadHeartbeat(base)

	if resetMainThreadHeartbeatAfterGap(mainThreadHangCheckInterval, base.Add(mainThreadHangCheckInterval)) {
		t.Fatal("ordinary watchdog interval was treated as sleep")
	}
	wake := base.Add(8 * time.Hour)
	if !resetMainThreadHeartbeatAfterGap(8*time.Hour, wake) {
		t.Fatal("expected sleep gap to reset the heartbeat")
	}
	age, _, ok := mainThreadHeartbeatAge(wake.Add(mainThreadHangCheckInterval))
	if !ok || age != mainThreadHangCheckInterval {
		t.Fatalf("age after wake = %s, ok=%v; want %s", age, ok, mainThreadHangCheckInterval)
	}
}

// TestWatchdogTreatsLongGapAsPauseAfterProbe pins the wake-from-suspend path
// (issue #38): the stale age on wake is re-probed live and closed as a paused
// marker, never as a hang report.
func TestWatchdogTreatsLongGapAsPauseAfterProbe(t *testing.T) {
	restoreWatchdogGlobals(t)
	t.Cleanup(removeAllPendingCrashes)
	stubMainThreadProbe(t, func() mainThreadProbeResult {
		return mainThreadProbeResult{Status: mainThreadProbeResponsive, Detail: "SendMessageTimeoutW(WM_NULL) answered within 2000ms"}
	})

	base := time.Now()
	mainThreadClockBase = base
	recordMainThreadHeartbeat(base)

	wake := base.Add(8 * time.Hour)
	NewApp().mainThreadWatchdogTick(wake, 8*time.Hour)

	if _, ok := readPending(t); ok {
		t.Fatal("wake from suspend must not produce a hang report")
	}
	if mainThreadLastPauseAt.Load() != wake.UnixNano() {
		t.Fatal("wake from suspend must leave a paused marker")
	}
	age, _, ok := mainThreadHeartbeatAge(wake.Add(mainThreadHangCheckInterval))
	if !ok || age != mainThreadHangCheckInterval {
		t.Fatalf("age after pause re-baseline = %s, ok=%v; want %s", age, ok, mainThreadHangCheckInterval)
	}
}

// TestWatchdogPauseIn12To30sBandSuppressesReport is the acceptance-c case for
// issue #38: a 12s<gap<=30s freeze (process suspended and resumed) used to be
// reported unconditionally because the sleep exemption only kicked in past
// 30s. With the live probe it must close as a pause.
func TestWatchdogPauseIn12To30sBandSuppressesReport(t *testing.T) {
	restoreWatchdogGlobals(t)
	t.Cleanup(func() {
		removeAllPendingCrashes()
		os.Remove(filepath.Join(config.MemoryUserDir(), metricsPendingFile))
	})
	stubMainThreadProbe(t, func() mainThreadProbeResult {
		return mainThreadProbeResult{Status: mainThreadProbeResponsive, Detail: "SendMessageTimeoutW(WM_NULL) answered within 2000ms"}
	})

	for _, gap := range []time.Duration{13 * time.Second, 29 * time.Second} {
		base := time.Now()
		mainThreadClockBase = base
		recordMainThreadHeartbeat(base)
		mainThreadLastPauseAt.Store(0)
		mainThreadHangReported.Store(false)

		resumedAt := base.Add(gap)
		NewApp().mainThreadWatchdogTick(resumedAt, gap)

		if _, ok := readPending(t); ok {
			t.Fatalf("gap %s: suspend artifact must not produce a hang report", gap)
		}
		if mainThreadLastPauseAt.Load() != resumedAt.UnixNano() {
			t.Fatalf("gap %s: paused marker not recorded", gap)
		}
		age, _, ok := mainThreadHeartbeatAge(resumedAt.Add(mainThreadHangCheckInterval))
		if !ok || age != mainThreadHangCheckInterval {
			t.Fatalf("gap %s: age after re-baseline = %s, ok=%v; want %s", gap, age, ok, mainThreadHangCheckInterval)
		}
	}
}

// TestWatchdogRealHangIn12To30sBandStillReports is the acceptance-d hard line
// (issue #38): the probe fix must not mute real hangs. A stale heartbeat plus
// a probe that still finds the message loop unresponsive must produce a report
// carrying the goroutine stack, the probe verdict, and the probe history.
func TestWatchdogRealHangIn12To30sBandStillReports(t *testing.T) {
	restoreWatchdogGlobals(t)
	t.Cleanup(func() {
		removeAllPendingCrashes()
		os.Remove(filepath.Join(config.MemoryUserDir(), metricsPendingFile))
	})
	probeDetail := "SendMessageTimeoutW(WM_NULL) timed out after 2000ms"
	stubMainThreadProbe(t, func() mainThreadProbeResult {
		return mainThreadProbeResult{Status: mainThreadProbeHung, Detail: probeDetail, Duration: 2 * time.Second}
	})
	app := NewApp()
	app.metrics.Store(newMetricsAggregator(config.MemoryUserDir()))

	base := time.Now()
	mainThreadClockBase = base
	recordMainThreadHeartbeat(base)
	recordMainThreadProbeOutcome(mainThreadProbeResult{
		Status: mainThreadProbeResponsive, Detail: "SendMessageTimeoutW(WM_NULL) answered within 1000ms", At: base.Add(time.Second),
	})

	gap := 13 * time.Second
	now := base.Add(gap)
	app.mainThreadWatchdogTick(now, gap)

	r, ok := readPending(t)
	if !ok {
		t.Fatal("real hang in the 12-30s band must still produce a report")
	}
	if r.Kind != "performance" || r.Source != "native.watchdog" {
		t.Fatalf("pending report = %+v", r)
	}
	if !strings.Contains(r.Stack, "goroutine ") {
		t.Fatal("report must carry a goroutine stack")
	}
	for _, want := range []string{"status: hung", probeDetail, "--- recent probes (oldest first) ---", "SendMessageTimeoutW(WM_NULL) answered within 1000ms"} {
		if !strings.Contains(r.Message, want) {
			t.Fatalf("report message missing %q:\n%s", want, r.Message)
		}
	}
	c := readCounters(filepath.Join(config.MemoryUserDir(), metricsPendingFile))
	metricBucket := mainThreadMetricBucket()
	if got := c["desktop_hang"][metricBucket]; got != 1 {
		t.Fatalf("desktop_hang/%s = %d, want 1", metricBucket, got)
	}
}

// TestWatchdogNoProbeKeepsLegacyBehavior pins the no-probe platforms (macOS,
// linux): the blanket sleep exemption still absorbs long gaps, and a stale
// age still reports without a probe verdict — exactly the pre-task-696
// behavior, so this fix introduces no macOS regression.
func TestWatchdogNoProbeKeepsLegacyBehavior(t *testing.T) {
	restoreWatchdogGlobals(t)
	t.Cleanup(removeAllPendingCrashes)
	stubMainThreadProbe(t, nil) // platforms without a live probe

	base := time.Now()
	mainThreadClockBase = base
	recordMainThreadHeartbeat(base)

	// Long gap: absorbed by the legacy sleep exemption, no paused marker.
	NewApp().mainThreadWatchdogTick(base.Add(8*time.Hour), 8*time.Hour)
	if _, ok := readPending(t); ok {
		t.Fatal("no-probe long gap must stay exempt")
	}
	if mainThreadLastPauseAt.Load() != 0 {
		t.Fatal("legacy sleep reset must not claim the paused marker")
	}

	// Stale age with an ordinary gap: legacy age-only report. Re-base the
	// epoch first — the 8h reset above moved the heartbeat far into the
	// future relative to `base`.
	mainThreadClockBase = base
	recordMainThreadHeartbeat(base)
	removeAllPendingCrashes()
	mainThreadHangReported.Store(false)
	gap := 13 * time.Second
	NewApp().mainThreadWatchdogTick(base.Add(gap), gap)
	r, ok := readPending(t)
	if !ok {
		t.Fatal("no-probe stale age must keep reporting (legacy behavior)")
	}
	if strings.Contains(r.Message, "--- live probe ---") {
		t.Fatal("no-probe report must not fabricate a live probe section")
	}
}

func TestProbeHistoryRingKeepsNewest(t *testing.T) {
	restoreWatchdogGlobals(t)
	base := time.Now()
	for i := range mainThreadProbeHistorySize + 2 {
		recordMainThreadProbeOutcome(mainThreadProbeResult{
			Status: mainThreadProbeResponsive,
			Detail: fmt.Sprintf("probe-%02d", i),
			At:     base.Add(time.Duration(i) * time.Second),
		})
	}
	dump := mainThreadProbeHistoryDump()
	lines := strings.Split(dump, "\n")
	if len(lines) != mainThreadProbeHistorySize {
		t.Fatalf("history lines = %d, want %d:\n%s", len(lines), mainThreadProbeHistorySize, dump)
	}
	if strings.Contains(dump, "probe-00") || strings.Contains(dump, "probe-01") {
		t.Fatal("oldest probes must be evicted")
	}
	newest := fmt.Sprintf("probe-%02d", mainThreadProbeHistorySize+1)
	if !strings.Contains(dump, newest) {
		t.Fatal("newest probe missing")
	}
	if !strings.Contains(dump, "responsive: probe-") {
		t.Fatalf("history lines must carry status and detail:\n%s", dump)
	}
}
