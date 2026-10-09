package main

import (
	"context"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Timing knobs for the main-thread hang watchdog. Vars instead of consts only
// so the suspend/resume acceptance experiment (task 696) can run the REAL
// watchdog in a re-exec'd test child with a scaled clock; production never
// writes them.
var (
	mainThreadHeartbeatInterval = time.Second
	mainThreadHangThreshold     = 12 * time.Second
	mainThreadHangCheckInterval = 2 * time.Second
	// mainThreadSleepSkip only gates the legacy no-probe fallback in
	// resetMainThreadHeartbeatAfterGap. Platforms with a live message-loop
	// probe (windows) never take that path: any watchdog gap beyond the hang
	// threshold is re-probed and judged live instead (issue #38 — the old
	// blanket 30s exemption inverted the thresholds, so a 12–30s process
	// suspend was always reported while >30s was silently absorbed).
	mainThreadSleepSkip = 30 * time.Second
	// mainThreadProbeTimeout bounds one live message-loop probe. The probe
	// runs on the watchdog goroutine and never blocks the UI thread.
	mainThreadProbeTimeout = 2 * time.Second
)

const (
	// nativeRuntimeContextMarker delimits the embedded runtime snapshot inside
	// a performance report message. The hang template embeds the context
	// itself and appendNativeResourceContext shares this marker so it can be
	// idempotent: issue #38 shipped payloads with two segments — one from the
	// template, one re-appended on a round-trip — whose regressed gc counter
	// made the report look like it came from two runs.
	nativeRuntimeContextMarker = "--- native runtime context ---"
	// mainThreadProbeHistorySize probes kept for the hang report. At one probe
	// per heartbeat tick this covers the last ~12s — enough to show whether
	// the message loop answered continuously (real hang) or the probe record
	// itself froze and resumed with the process (pause).
	mainThreadProbeHistorySize = 12
	// Byte caps for the stack evidence attached to a hang report.
	maxHangReportStackBytes     = maxCrashStackBytes
	maxHangReportMainStackBytes = 2 << 10
	maxHangReportStackBuffer    = 64 << 10
)

// probeMainThread asks the platform whether the UI message loop still answers,
// synchronously, from the watchdog goroutine. nil on platforms without a live
// probe (macOS/linux): there the watchdog keeps the legacy age-only judgment
// plus the blanket sleep-gap reset, which is exactly the pre-task-696 behavior.
var probeMainThread func() mainThreadProbeResult

type mainThreadProbeStatus uint8

const (
	mainThreadProbeResponsive mainThreadProbeStatus = iota
	mainThreadProbeHung
	mainThreadProbeInconclusive
)

func (s mainThreadProbeStatus) String() string {
	switch s {
	case mainThreadProbeResponsive:
		return "responsive"
	case mainThreadProbeHung:
		return "hung"
	default:
		return "inconclusive"
	}
}

// mainThreadProbeResult is one live observation of the UI message loop.
type mainThreadProbeResult struct {
	Status   mainThreadProbeStatus
	Detail   string
	Duration time.Duration
	At       time.Time
}

func (r mainThreadProbeResult) describe() string {
	if r.Detail == "" {
		return r.Status.String()
	}
	return r.Status.String() + ": " + r.Detail
}

// probe history ring: the most recent probes (heartbeat-side and watchdog-side
// alike) so a hang report can show the shape that distinguishes a real hang
// from a suspend artifact (issue #38).
var mainThreadProbeLog = struct {
	mu   sync.Mutex
	ring [mainThreadProbeHistorySize]mainThreadProbeResult
	fill int
	next int
}{}

func recordMainThreadProbeOutcome(r mainThreadProbeResult) {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	mainThreadProbeLog.mu.Lock()
	defer mainThreadProbeLog.mu.Unlock()
	mainThreadProbeLog.ring[mainThreadProbeLog.next] = r
	mainThreadProbeLog.next = (mainThreadProbeLog.next + 1) % mainThreadProbeHistorySize
	if mainThreadProbeLog.fill < mainThreadProbeHistorySize {
		mainThreadProbeLog.fill++
	}
}

func mainThreadProbeHistoryDump() string {
	mainThreadProbeLog.mu.Lock()
	defer mainThreadProbeLog.mu.Unlock()
	if mainThreadProbeLog.fill == 0 {
		return "(no probes recorded)"
	}
	var b strings.Builder
	for i := 0; i < mainThreadProbeLog.fill; i++ {
		idx := (mainThreadProbeLog.next - mainThreadProbeLog.fill + i + mainThreadProbeHistorySize) % mainThreadProbeHistorySize
		r := mainThreadProbeLog.ring[idx]
		fmt.Fprintf(&b, "%s %s %s\n",
			r.At.Format("15:04:05.000"),
			r.Duration.Round(time.Microsecond),
			r.describe(),
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

var (
	mainThreadClockBase            = time.Now()
	mainThreadLastHeartbeatElapsed atomic.Int64
	mainThreadLastHeartbeatWall    atomic.Int64
	mainThreadHangReported         atomic.Bool
	// mainThreadLastPauseAt marks the wall time of the most recent stale
	// heartbeat that was closed as a pause (suspend/sleep artifact) instead of
	// reported as a hang (issue #38). Greppable substitute for the old silent
	// self-written heartbeat: a pause is now a deliberate, observable marker.
	mainThreadLastPauseAt atomic.Int64
)

func recordMainThreadHeartbeat(t time.Time) {
	elapsed := max(t.Sub(mainThreadClockBase), 0)
	mainThreadLastHeartbeatElapsed.Store(int64(elapsed))
	mainThreadLastHeartbeatWall.Store(t.UnixNano())
}

func mainThreadHeartbeatAge(now time.Time) (time.Duration, time.Time, bool) {
	lastElapsed := time.Duration(mainThreadLastHeartbeatElapsed.Load())
	lastWall := mainThreadLastHeartbeatWall.Load()
	if lastWall <= 0 {
		return 0, time.Time{}, false
	}
	age := max(now.Sub(mainThreadClockBase)-lastElapsed, 0)
	return age, time.Unix(0, lastWall), true
}

// resetMainThreadHeartbeatAfterGap is the legacy no-probe fallback (platforms
// without a live message-loop probe): after a watchdog gap beyond the sleep
// threshold, treat the gap as suspend/sleep and restart the observation epoch.
// There is no way to verify the window live here, so the gap is absorbed
// blindly — platforms with a probe record a verified pause instead.
func resetMainThreadHeartbeatAfterGap(gap time.Duration, now time.Time) bool {
	if gap <= mainThreadSleepSkip {
		return false
	}
	// The native UI heartbeat and this Go ticker are both suspended while the
	// machine sleeps. Treat wake as a fresh observation epoch so the sleep gap
	// cannot be reported as a multi-minute UI-thread hang on the next tick.
	recordMainThreadHeartbeat(now)
	return true
}

// recordMainThreadPaused closes a stale-heartbeat observation as a pause: it
// re-baselines the heartbeat epoch and leaves a paused marker (log + timestamp)
// instead of writing a hang report (issue #38). Unlike the old unconditional
// sleep reset, this only runs after a live probe cleared the window, so the
// epoch restart is verified rather than fabricated.
func recordMainThreadPaused(at time.Time, age time.Duration, probe mainThreadProbeResult) {
	recordMainThreadHeartbeat(at)
	mainThreadLastPauseAt.Store(at.UnixNano())
	slog.Info("desktop: stale main-thread heartbeat resolved as pause",
		"staleAge", age.Round(time.Millisecond).String(),
		"probe", probe.describe(),
		"probeElapsed", probe.Duration.Round(time.Millisecond).String(),
	)
}

func (a *App) startMainThreadWatchdog() {
	if !mainThreadWatchdogSupported() {
		return
	}
	a.hangWatchdogMu.Lock()
	if a.hangWatchdogCancel != nil {
		a.hangWatchdogMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.hangWatchdogCancel = cancel
	mainThreadHangReported.Store(false)
	recordMainThreadHeartbeat(time.Now())
	startNativeMainThreadHeartbeat(uint64(mainThreadHeartbeatInterval / time.Millisecond))
	a.hangWatchdogMu.Unlock()

	a.goSafe("mainThreadHangWatchdog", func() {
		a.watchMainThreadHeartbeat(ctx)
	})
}

func (a *App) stopMainThreadWatchdog() {
	if !mainThreadWatchdogSupported() {
		return
	}
	a.hangWatchdogMu.Lock()
	cancel := a.hangWatchdogCancel
	a.hangWatchdogCancel = nil
	a.hangWatchdogMu.Unlock()
	if cancel != nil {
		cancel()
	}
	stopNativeMainThreadHeartbeat()
}

func (a *App) watchMainThreadHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(mainThreadHangCheckInterval)
	defer ticker.Stop()
	lastCheck := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			gap := now.Sub(lastCheck)
			lastCheck = now
			a.mainThreadWatchdogTick(now, gap)
		}
	}
}

// mainThreadWatchdogTick judges one watchdog observation. gap is the wall time
// since the previous tick — it exceeds the hang threshold whenever the process
// or the machine was suspended between ticks (issue #38), because the native
// heartbeat and this ticker freeze together and cannot catch up on resume.
// A stale heartbeat alone is no longer evidence of a hang: platforms with a
// live probe re-check the message loop synchronously and close the observation
// as a pause unless the probe still finds the window unresponsive.
func (a *App) mainThreadWatchdogTick(now time.Time, gap time.Duration) {
	if probeMainThread == nil && resetMainThreadHeartbeatAfterGap(gap, now) {
		return
	}
	age, last, ok := mainThreadHeartbeatAge(now)
	if !ok || age < mainThreadHangThreshold {
		return
	}
	probe := mainThreadProbeResult{}
	if probeMainThread != nil {
		probe = probeMainThread()
		recordMainThreadProbeOutcome(probe)
		if probe.Status != mainThreadProbeHung {
			recordMainThreadPaused(now, age, probe)
			return
		}
	}
	if mainThreadHangReported.CompareAndSwap(false, true) {
		a.recordMainThreadHang(age, last, now, probe)
	}
}

func (a *App) recordMainThreadHang(age time.Duration, lastHeartbeat, observedAt time.Time, probe mainThreadProbeResult) {
	report := mainThreadHangReport(age, lastHeartbeat, observedAt, probe)
	wrote := writePendingReport(report, true)
	if m := a.metrics.Load(); m != nil {
		m.inc("desktop_hang", mainThreadMetricBucket())
		m.inc("desktop_hang_age", hangAgeBucket(age))
		m.persist()
	}
	attrs := []any{
		"age", age.Round(time.Millisecond).String(),
		"lastHeartbeat", lastHeartbeat.Format(time.RFC3339),
		"probed", probeMainThread != nil,
	}
	if probeMainThread != nil {
		// Only quote the probe verdict when a live probe actually ran; the
		// zero result would read as "responsive" and mislead a postmortem.
		attrs = append(attrs, "probe", probe.describe(), "probeElapsed", probe.Duration.Round(time.Millisecond).String())
	}
	attrs = append(attrs, "stackBytes", len(report.Stack), "pendingReport", wrote)
	slog.Warn("desktop: native UI thread heartbeat stalled", attrs...)
}

// captureMainThreadHangStack snapshots all goroutines at report time so a real
// hang carries Go-side context (which goroutine held what) instead of the old
// frame-less "windows.ui_thread.heartbeat" only.
func captureMainThreadHangStack() string {
	buf := make([]byte, maxHangReportStackBuffer)
	n := goruntime.Stack(buf, true)
	return string(buf[:n])
}

// mainGoroutineStackFromDump extracts the "goroutine 1" block (header and
// frames) from a full runtime.Stack(true) dump for the report message.
func mainGoroutineStackFromDump(dump string, max int) string {
	var b strings.Builder
	inMain := false
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "goroutine ") {
			if inMain && b.Len() > 0 {
				break // the block after goroutine 1 ended
			}
			inMain = strings.HasPrefix(line, "goroutine 1 ") || strings.HasPrefix(line, "goroutine 1[")
			if !inMain {
				continue
			}
		}
		if inMain {
			b.WriteString(line)
			b.WriteString("\n")
			if b.Len() > max {
				break
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func mainThreadHangReport(age time.Duration, lastHeartbeat, observedAt time.Time, probe mainThreadProbeResult) crashReport {
	label, errorType, platformName, topFrame := mainThreadDiagnosticIdentity()
	age = age.Round(time.Second)
	stack := captureMainThreadHangStack()
	probeSection := ""
	if probeMainThread != nil {
		probeSection = fmt.Sprintf(`--- live probe ---
status: %s
detail: %s
elapsed: %s

--- recent probes (oldest first) ---
%s

--- main goroutine ---
%s`,
			probe.Status,
			probe.Detail,
			probe.Duration.Round(time.Millisecond),
			mainThreadProbeHistoryDump(),
			mainGoroutineStackFromDump(stack, maxHangReportMainStackBytes),
		)
	}
	message := fmt.Sprintf(`[%s]

Reasonix detected that the %s UI-thread heartbeat stopped for %s.

--- watchdog context ---
last heartbeat: %s
observed at: %s
threshold: %s
bucket: %s

%s

--- native runtime context ---
%s`,
		label,
		platformName,
		age,
		lastHeartbeat.UTC().Format(time.RFC3339),
		observedAt.UTC().Format(time.RFC3339),
		mainThreadHangThreshold,
		hangAgeBucket(age),
		probeSection,
		nativeResourceContext(),
	)
	report := baseCrashReport("performance")
	report.SchemaVersion = 2
	report.Source = "native.watchdog"
	report.Label = label
	report.ErrorType = errorType
	report.ErrorMessage = sanitizeCrashText(platformName+" UI thread heartbeat stopped; the native/Wails message loop may be blocked.", maxCrashFieldBytes)
	report.TopFrame = topFrame
	report.Stack = sanitizeCrashText(stack, maxHangReportStackBytes)
	report.OccurredAt = observedAt.UTC().Format(time.RFC3339)
	report.Message = sanitizeCrashText(message, maxCrashDetailBytes)
	return report
}

func mainThreadDiagnosticIdentity() (label, errorType, platformName, topFrame string) {
	if goruntime.GOOS == "windows" {
		return "windows.ui_thread.hang", "WindowsUIThreadHang", "Windows", "windows.ui_thread.heartbeat"
	}
	return "mac.main_thread.hang", "MacMainThreadHang", "macOS", "mac.main_thread.heartbeat"
}

func mainThreadMetricBucket() string {
	if goruntime.GOOS == "windows" {
		return "windows_ui_thread"
	}
	return "main_thread"
}

func hangAgeBucket(age time.Duration) string {
	seconds := age.Seconds()
	switch {
	case seconds < 15:
		return "s_10_15"
	case seconds < 30:
		return "s_15_30"
	case seconds < 60:
		return "s_30_60"
	case seconds < 300:
		return "m_1_5"
	default:
		return "m_5_plus"
	}
}
