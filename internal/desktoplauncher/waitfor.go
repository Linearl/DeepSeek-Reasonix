package desktoplauncher

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/config"
)

// relaunchWaitTimeout bounds how long --wait-for will block on the exiting
// desktop. Long enough for a graceful quit that flushes sessions; short
// enough that a wedged process cannot black-hole the relaunch forever.
const relaunchWaitTimeout = 90 * time.Second

// extractWaitFor peels "--wait-for <pid>" / "--wait-for=<pid>" out of the
// launcher arguments. The token is a handoff between the restarting desktop
// and this launcher; it must never reach the desktop's own flag parsing.
func extractWaitFor(args []string) (int, []string) {
	for i := range args {
		var (
			pid  int
			err  error
			rest []string
		)
		switch {
		case args[i] == "--wait-for" && i+1 < len(args):
			pid, err = strconv.Atoi(args[i+1])
			rest = append(append([]string{}, args[:i]...), args[i+2:]...)
		case strings.HasPrefix(args[i], "--wait-for="):
			pid, err = strconv.Atoi(strings.TrimPrefix(args[i], "--wait-for="))
			rest = append(append([]string{}, args[:i]...), args[i+1:]...)
		default:
			continue
		}
		if err == nil && pid > 0 {
			return pid, rest
		}
		// Malformed handoff token: drop it and start without waiting, so a
		// stale flag can never wedge the normal launch path.
		return 0, rest
	}
	return 0, args
}

// waitForHandoff is the Run-side wrapper: never fails the launch, because a
// timeout only means the previous desktop is wedged and the user still wants
// the new one to come up (the gateway port then keeps its own retry story).
func waitForHandoff(pid int) {
	if pid <= 0 {
		return
	}
	if err := waitForProcessExit(pid, relaunchWaitTimeout); err != nil {
		// Task 272 G3: this branch fired in the incident (previous desktop
		// wedged past 90s) with only a bare stderr line — structured severity
		// so the launcher's own output is greppable; the desktop-side
		// counterpart is the secondInstanceLaunch slog.
		//
		// Task 304 (channel 3): the launcher is its own process, so this slog
		// goes to the launcher's stderr and dies with it - desktop.log never
		// saw the line even though it is exactly the incident's evidence.
		// Mirror the same structured line into the desktop rolling log; the
		// stderr copy stays for the immediate console.
		msg := "launcher: previous desktop did not exit within the relaunch wait; starting the new instance anyway"
		slog.Warn(msg,
			"pid", pid, "timeout", relaunchWaitTimeout.String(), "err", err)
		appendDesktopLogLine(fmt.Sprintf("%s pid=%d timeout=%s err=%s",
			msg, pid, relaunchWaitTimeout, err))
	}
}

// desktopLogPath resolves the same file installDesktopLogging writes (task
// 304 channel 3). Variable so tests can point it at a temp dir.
var desktopLogPath = func() string {
	return filepath.Join(config.MemoryUserDir(), "logs", "desktop", "desktop.log")
}

// appendDesktopLogLine adds one slog-shaped WARN line to the desktop rolling
// log from the launcher process. Short open-append-close on purpose: the
// desktop owns rotation, and holding no descriptor between writes keeps the
// overlap window microsecond-scale (a rotation landing inside it fails that
// one rename, nothing else). Any failure falls back to the stderr copy the
// caller already emitted - diagnostics never break the launch.
func appendDesktopLogLine(line string) {
	path := desktopLogPath()
	if path == "" {
		return
	}
	// The mirror may fire before any desktop ever created logs/desktop (the
	// task-404 watchdog restarts from the launcher alone); create the parent
	// so the evidence is not silently dropped on a first-run machine.
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	// slog TextHandler shape so grep and the readers do not care which
	// process wrote the line.
	fmt.Fprintf(f, "time=%s level=WARN msg=%q source=launcher\n",
		time.Now().Format(time.RFC3339), line)
}
