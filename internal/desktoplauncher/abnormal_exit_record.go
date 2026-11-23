package desktoplauncher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

// Task 736 (issue #39): the resident launcher is the only witness of the
// actual death moment of an abnormally exiting desktop. The lifecycle record
// the desktop leaves behind carries only its last phase write — for a long
// session that is hours before the death, which is why abnormal_exit.v2
// reports could not attribute when the process died. At Wait() time the
// launcher persists this consume-once record; the next desktop instance
// matches it by PID (desktop/lifecycle_diagnostics.go, same directory) and
// reports occurredAt=exitedAt — the actual death moment — plus the launcher's
// exit code. The JSON mirrors the desktop-side decoder; keep both in lockstep.
//
// tl1786 (包111 exit=1 静默退出零归因): the record gains additive attribution
// fields collected by the launcher at death time — the exit-code class
// (Reason), the dead PID's crash-fatal sink content when the Go runtime left
// a dump (Stack), and the dead PID's last lifecycle phase (lifecyclePhase /
// wedgePoint). SchemaVersion STAYS 1 on purpose: the desktop decoder
// (applyLauncherDeathEvidence) refuses records whose schemaVersion it does
// not know and preserves them verbatim, so a bump would make every
// already-running desktop never consume these records; additive optional
// fields decode as empty there instead. Reason and Stack are always
// serialized (even empty) so an incident reader can tell "no dump existed"
// apart from "field missing because this launcher predates tl1786".
type abnormalExitRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	PID           int    `json:"pid"`
	ExitCode      int    `json:"exitCode"`
	ExitedAt      string `json:"exitedAt"` // RFC3339Nano, launcher clock
	// Reason classifies the exit code into the evidence-backed death classes
	// (tl1786). Always written.
	Reason string `json:"reason"`
	// Stack carries the dead PID's crash-fatal sink dump (head-capped) when
	// the Go runtime left one; empty means the sink was absent or header-only.
	Stack string `json:"stack"`
	// LifecyclePhase/WedgePoint mirror the dead PID's lifecycle record —
	// where the desktop last marked progress before the death.
	LifecyclePhase string `json:"lifecyclePhase,omitempty"`
	WedgePoint     string `json:"wedgePoint,omitempty"`
	// CrashFatalSeen is true when a crash-fatal sink existed for the dead
	// PID — including the header-only stub an os.Exit/external kill leaves
	// behind (the runtime only appends a dump on real fatal deaths).
	CrashFatalSeen bool `json:"crashFatalSeen,omitempty"`
}

// abnormalExitRecordPath resolves the record under the user state dir's
// lifecycle directory, next to the per-process lifecycle records it
// attributes. Overridable so tests can point it at a temp dir.
var abnormalExitRecordPath = func() string {
	return filepath.Join(config.MemoryUserDir(), "diagnostics", "lifecycle", "last-abnormal-exit.json")
}

// crashFatalSinkPathFor resolves the per-PID fatal-crash sink the desktop
// installs via debug.SetCrashOutput (desktop/crash_fatal.go — keep the
// <user>/crash-fatal/<pid>.log shape in lockstep). The launcher reads it at
// death time because os.Exit and external TerminateProcess kills skip every
// defer: the sink survives untouched, and the launcher is the first witness.
var crashFatalSinkPathFor = func(pid int) string {
	return filepath.Join(config.MemoryUserDir(), "crash-fatal", strconv.Itoa(pid)+".log")
}

// lifecycleRecordsDir resolves the per-process lifecycle record directory
// (desktop/lifecycle_diagnostics.go "<pid>-<runId>.json" — keep in lockstep).
var lifecycleRecordsDir = func() string {
	return filepath.Join(config.MemoryUserDir(), "diagnostics", "lifecycle")
}

const (
	// fatalCrashSinkHeaderPrefix marks the provenance line the desktop writes
	// when creating the sink (desktop/crash_fatal.go task 377 — lockstep).
	fatalCrashSinkHeaderPrefix = "reasonix fatal-crash sink"
	// maxAbnormalExitStackBytes caps the copied sink dump. The head keeps
	// goroutine 1 and the classification lines, which is where attribution
	// lives; the desktop's own next-boot capture queues the scrubbed full
	// report anyway, so this record only needs the first screens.
	maxAbnormalExitStackBytes = 32 << 10
)

// classifyAbnormalExit maps the observed exit code to the death class the
// fork's exit surfaces can actually produce (tl1786 全仓 grep 结论). The
// wording names the desktop-side paths so the incident reader can go from
// the class to a checklist without re-deriving the map.
func classifyAbnormalExit(code int) string {
	switch {
	case code == 0:
		return "exit=0 recorded as abnormal; unexpected — deliberate exits never reach this record"
	case code == 1:
		return "exit=1: explicit process exit or external TerminateProcess-class kill (taskkill /F, optimizer, security product); every in-desktop exit-1 path (shutdown watchdog, webview2 errorCallback) logs to desktop.log first, so no such line points to the external class"
	case code == -1:
		return "exit=-1 (max DWORD): wails WebView2 browser-process-crash dialog path (os.Exit(-1)) or force-termination with 0xFFFFFFFF"
	case code == 3:
		return "exit=3: home-migration double-instance refusal (task 846)"
	case code == 259:
		return "exit=259 (STILL_ACTIVE): no settled exit code was observed"
	case code < 0:
		return fmt.Sprintf("exit=%d: native fault (NTSTATUS as signed int)", code)
	default:
		return fmt.Sprintf("exit=%d: nonzero exit with no known desktop sender", code)
	}
}

// readCrashFatalStack returns the dead PID's sink dump (header stripped,
// head-capped) and whether a sink existed at all. A header-only stub is
// evidence too: it says the process died WITHOUT a Go runtime fatal (the
// runtime appends the dump only on real fatal deaths), which is exactly the
// os.Exit / external-kill signature of the tl1786 incident.
func readCrashFatalStack(pid int) (stack string, seen bool) {
	body, err := os.ReadFile(crashFatalSinkPathFor(pid))
	if err != nil {
		return "", false
	}
	text := stripFatalSinkHeader(string(body))
	if strings.TrimSpace(text) == "" {
		return "", true
	}
	if len(text) > maxAbnormalExitStackBytes {
		// Cut on a line boundary so the JSON never carries a torn frame line.
		text = text[:maxAbnormalExitStackBytes]
		if idx := strings.LastIndexByte(text, '\n'); idx > 0 {
			text = text[:idx+1]
		}
	}
	return text, true
}

// stripFatalSinkHeader removes the provenance line installFatalCrashOutput
// writes (desktop/crash_fatal.go task 377 shape — lockstep).
func stripFatalSinkHeader(raw string) string {
	if !strings.HasPrefix(raw, fatalCrashSinkHeaderPrefix) {
		return raw
	}
	if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
		return raw[idx+1:]
	}
	return ""
}

// readLifecycleEvidence returns the dead PID's last lifecycle phase (and the
// shutdown wedge point when the record has one) by scanning the lifecycle
// directory for the "<pid>-<runId>.json" record. Best effort: an unreadable
// record contributes empty strings, never a failed write.
func readLifecycleEvidence(pid int) (phase, wedgePoint string) {
	dir := lifecycleRecordsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	prefix := strconv.Itoa(pid) + "-"
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var state struct {
			Phase      string `json:"phase"`
			WedgePoint string `json:"wedgePoint,omitempty"`
		}
		if json.Unmarshal(body, &state) != nil {
			continue
		}
		return state.Phase, state.WedgePoint
	}
	return "", ""
}

// writeAbnormalExitRecord persists the launcher's death observation with the
// tl1786 attribution fields, then mirrors one attribution line into the
// desktop rolling log so the incident's usual reading place carries the
// class without opening the JSON. Best effort: a failed record must never
// break the supervision/restart flow.
func writeAbnormalExitRecord(pid, exitCode int, exitedAt time.Time) {
	path := abnormalExitRecordPath()
	if path == "" || pid <= 0 {
		return
	}
	stack, sinkSeen := readCrashFatalStack(pid)
	phase, wedgePoint := readLifecycleEvidence(pid)
	reason := classifyAbnormalExit(exitCode)
	body, err := json.Marshal(abnormalExitRecord{
		SchemaVersion:  1,
		PID:            pid,
		ExitCode:       exitCode,
		ExitedAt:       exitedAt.UTC().Format(time.RFC3339Nano),
		Reason:         reason,
		Stack:          stack,
		LifecyclePhase: phase,
		WedgePoint:     wedgePoint,
		CrashFatalSeen: sinkSeen,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if err := fileutil.AtomicWriteFile(path, body, 0o600); err != nil {
		// The launcher's slog copy is the primary record (task 404); the
		// failure must be greppable but never fatal.
		slog.Warn("launcher: write abnormal-exit record failed", "err", err)
		return
	}
	// tl1786: put the attribution in the desktop.log mirror too — the WARN
	// line above it names the death, this line names the class and the
	// evidence the record carries (slog-shaped like every launcher mirror).
	appendDesktopLogLine(fmt.Sprintf(
		"launcher: abnormal-exit attribution pid=%d exit=%d reason=%q lifecycle_phase=%q wedge_point=%q crash_fatal_seen=%t stack_bytes=%d",
		pid, exitCode, reason, phase, wedgePoint, sinkSeen, len(stack)))
}
