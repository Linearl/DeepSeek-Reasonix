package main

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	webView2RecoveryNotApplicable = "not_applicable"
	webView2RecoverySucceeded     = "reload_succeeded"
	webView2RecoveryFailed        = "reload_failed"
	webView2RecoveryPending       = "reload_navigation_succeeded"
	webView2RecoverySuppressed    = "reload_suppressed"
	// tl1589 (issue #93): the native edge layer (third_party go-webview2)
	// subdivides the renderer recovery failures into three attributable
	// paths. They used to converge on the single string reload_failed,
	// which left one telemetry payload unable to tell WHERE the recovery
	// died — the Reload call erroring, the wait window elapsing with no
	// navigation (the structural outcome for a hung renderer), or the
	// navigation completing onto a failure page. The coordinator treats all
	// three like the old reload_failed (escalate to the controlled restart)
	// and the outcome classification stays exception/recovery_failed.
	webView2RecoveryReloadError   = "reload_error"
	webView2RecoveryReloadTimeout = "reload_timeout"
	webView2RecoveryNavFailed     = "reload_nav_failed"
	// webView2RecoveryBrowserRestart names the recovery a browser-process
	// failure actually gets: the state machine answers kind 0 with the
	// restart flow (webview2_recovery.go), because a browser process cannot
	// be reloaded in place. tl1530 C 档: these reports used to say
	// not_applicable — the renderer reload vocabulary leaking onto a kind it
	// does not describe — which read as "nothing was done about this failure"
	// while the host was tearing down for a relaunch.
	webView2RecoveryBrowserRestart = "restart"

	// exitCodeTerminatedExternally is Windows' DBG_TERMINATE_PROCESS: the
	// process was force-terminated by TerminateProcess (session shutdown,
	// Task Manager, an optimizer or security product), not a WebView2 crash.
	// Real crashes carry NTSTATUS codes instead (0xC0000005 access violation,
	// 0xC0000409 stack buffer overrun).
	exitCodeTerminatedExternally = 0x40010004
	// exitCodeStillActive is Windows' STILL_ACTIVE; a finished process cannot
	// legitimately report it, so it says the exit code was never really
	// observed. It is excluded from the fingerprint to keep that distinction.
	exitCodeStillActive = 259
)

// externallyTerminatedBrowser reports whether a browser-process exit code
// means "killed by someone else" rather than "crashed by itself". The exit
// code is the only evidence available; anything unknown stays a crash so
// genuine failures are never silently reclassified.
func externallyTerminatedBrowser(event webView2NativeEvent) bool {
	return event.Kind == 0 && event.ExitCodeAvailable && event.ExitCode == exitCodeTerminatedExternally
}

type webView2Diagnostic struct {
	Kind                string `json:"kind"`
	Reason              string `json:"reason"`
	ExitCode            *int32 `json:"exitCode,omitempty"`
	ProcessDescription  string `json:"processDescription,omitempty"`
	FailureSourceModule string `json:"failureSourceModule,omitempty"`
	RuntimeVersion      string `json:"runtimeVersion"`
	GPUDisabled         bool   `json:"gpuDisabled"`
	Recovery            string `json:"recovery"`
}

type webRuntimeDiagnostic struct {
	Engine string `json:"engine"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	// ExitCodeClass is a hand-free semantic bucket for ExitCode
	// ("terminated_externally" for now) so reports read without decoding hex.
	ExitCodeClass       string `json:"exitCodeClass,omitempty"`
	ExitCode            *int32 `json:"exitCode,omitempty"`
	ProcessDescription  string `json:"processDescription,omitempty"`
	FailureSourceModule string `json:"failureSourceModule,omitempty"`
	RuntimeVersion      string `json:"runtimeVersion"`
	GPUMode             string `json:"gpuMode"`
	CompatibilityMode   bool   `json:"compatibilityMode,omitempty"`
	Recovery            string `json:"recovery"`
}

type webView2NativeEvent struct {
	Kind                int
	Reason              int
	ReasonAvailable     bool
	ExitCode            int32
	ExitCodeAvailable   bool
	ProcessDescription  string
	FailureSourceModule string
	Recovery            string
}

var nativeWebView2ObserverInstalled atomic.Bool

const maxWebRuntimeDropMetricBatch = 1_000_000

// recordDroppedWebRuntimeEvents turns bounded-queue pressure into an explicit
// counter from the background consumer. Native callbacks only increment the
// atomic and therefore never perform disk or network work.
func recordDroppedWebRuntimeEvents(app *App, engine string, dropped *atomic.Uint64) {
	if app == nil || dropped == nil {
		return
	}
	count := dropped.Swap(0)
	if count == 0 {
		return
	}
	if count > maxWebRuntimeDropMetricBatch {
		dropped.Add(count - maxWebRuntimeDropMetricBatch)
		count = maxWebRuntimeDropMetricBatch
	}
	app.recordDiagnosticMetricCount("desktop_web_runtime_dropped", engine, int(count))
}

func webView2NativeObserverInstalled() bool {
	return nativeWebView2ObserverInstalled.Load()
}

func webView2ProcessReasonBucket(reason int, available bool) string {
	if !available {
		return "unknown"
	}
	names := []string{
		"unexpected",
		"unresponsive",
		"terminated",
		"crashed",
		"launch_failed",
		"out_of_memory",
		"profile_deleted",
		"normal_exit",
		"abnormal_exit",
		"integrity_failure",
	}
	if reason < 0 || reason >= len(names) {
		return "unknown"
	}
	return names[reason]
}

func sanitizeFailureSourceModule(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if value == "" {
		return ""
	}
	return sanitizeCrashField(path.Base(value), 255)
}

func normalizeWebView2Recovery(value string) string {
	switch value {
	case webView2RecoverySucceeded, webView2RecoveryFailed, webView2RecoveryPending, webView2RecoverySuppressed,
		webView2RecoveryReloadError, webView2RecoveryReloadTimeout, webView2RecoveryNavFailed:
		return value
	default:
		return webView2RecoveryNotApplicable
	}
}

func webView2Outcome(event webView2NativeEvent) (reportKind, outcome string) {
	switch event.Kind {
	case 0:
		if externallyTerminatedBrowser(event) {
			// Not a crash: keep it out of the crash bucket and the
			// fatal_app_exit outcome so shutdown-driven terminations cannot
			// drown real browser crashes in the crash rate.
			return "performance", "terminated_externally"
		}
		return "crash", "fatal_app_exit"
	case 1, 2:
		switch event.Recovery {
		case webView2RecoverySucceeded:
			return "performance", "recovered"
		case webView2RecoveryPending:
			return "performance", "reload_pending"
		case webView2RecoveryFailed, webView2RecoverySuppressed,
			webView2RecoveryReloadError, webView2RecoveryReloadTimeout, webView2RecoveryNavFailed:
			// tl1589: the three subdivided failure paths keep the exact
			// escalation semantics of the reload_failed they replace — a
			// failed renderer recovery is an exception, never a degraded
			// performance note.
			return "exception", "recovery_failed"
		default:
			return "performance", "degraded"
		}
	default:
		return "performance", "degraded"
	}
}

func webView2NativeFailureReport(event webView2NativeEvent, runtimeVersion string, gpuDisabled bool) (crashReport, string) {
	reportKind, outcome := webView2Outcome(event)
	kind := webView2ProcessKindBucket(event.Kind)
	reason := webView2ProcessReasonBucket(event.Reason, event.ReasonAvailable)
	recovery := normalizeWebView2Recovery(event.Recovery)
	if event.Kind == 0 {
		// A browser-process exit has no in-place recovery: the state machine's
		// answer is always the restart flow (suppressed only while the host is
		// already exiting, which the recovery journal records separately).
		// tl1530 C 档: report what actually happens instead of the renderer
		// reload vocabulary's not_applicable.
		recovery = webView2RecoveryBrowserRestart
	}
	runtimeVersion = sanitizeCrashField(runtimeVersion, 128)
	if runtimeVersion == "" {
		runtimeVersion = "unknown"
	}
	gpuMode := "enabled"
	if gpuDisabled {
		gpuMode = "disabled"
	}
	diagnostic := &webRuntimeDiagnostic{
		Engine:              "webview2",
		Kind:                kind,
		Reason:              reason,
		ProcessDescription:  sanitizeCrashText(event.ProcessDescription, 255),
		FailureSourceModule: sanitizeFailureSourceModule(event.FailureSourceModule),
		RuntimeVersion:      runtimeVersion,
		GPUMode:             gpuMode,
		Recovery:            recovery,
	}
	if event.ExitCodeAvailable {
		exitCode := event.ExitCode
		diagnostic.ExitCode = &exitCode
	}
	if externallyTerminatedBrowser(event) {
		// A human-readable bucket next to the raw code: every new fingerprint
		// must otherwise be decoded by hand before it can be triaged.
		diagnostic.ExitCodeClass = "terminated_externally"
	}

	fingerprintExitCode := "unknown"
	if event.ExitCodeAvailable && !(event.Kind == 2 && event.ExitCode == exitCodeStillActive) {
		fingerprintExitCode = strconv.FormatInt(int64(event.ExitCode), 10)
	}
	// tl1589 (issue #93): renderer failures carry the recovery path in the
	// fingerprint, so the three subdivided failure outcomes group separately
	// and a single report is attributable without opening the payload.
	// Browser (kind 0) and diagnostic-only kinds keep the old shape: their
	// recovery value is constant (restart / not_applicable), so the extra
	// segment would only churn every existing fingerprint for nothing.
	fingerprintSegments := []string{"web.runtime", "webview2", kind, reason}
	if event.Kind == 1 || event.Kind == 2 {
		fingerprintSegments = append(fingerprintSegments, recovery)
	}
	fingerprintSegments = append(fingerprintSegments, fingerprintExitCode)
	report := baseCrashReport(reportKind)
	report.SchemaVersion = 3
	report.Source = "web.runtime.native"
	report.Label = "windows.webview2.process_failed"
	report.ErrorType = "WebView2ProcessFailed"
	report.ErrorMessage = sanitizeCrashText(fmt.Sprintf("WebView2 %s: %s (%s).", kind, reason, outcome), maxCrashFieldBytes)
	report.TopFrame = "webview2.process." + kind
	report.FingerprintHint = strings.Join(fingerprintSegments, ".")
	report.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	report.WebRuntime = diagnostic
	report.Message = sanitizeCrashText(fmt.Sprintf(`[windows.webview2.process_failed]

WebView2 reported a native process failure.

process kind: %s
reason: %s
exit code: %s
exit code class: %s
runtime version: %s
GPU mode: %s
recovery: %s
process description: %s
failure source module: %s`, kind, reason, fingerprintExitCode, exitCodeClassLine(diagnostic.ExitCodeClass), runtimeVersion, gpuMode, recovery, diagnostic.ProcessDescription, diagnostic.FailureSourceModule), maxCrashDetailBytes)
	return report, outcome
}

// exitCodeClassLine keeps the report text stable when no class was assigned
// instead of leaking an empty line into every WebView2 report.
func exitCodeClassLine(class string) string {
	if class == "" {
		return "unknown"
	}
	return class
}
