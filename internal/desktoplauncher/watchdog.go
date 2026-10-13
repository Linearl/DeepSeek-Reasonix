package desktoplauncher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"reasonix/internal/config"
)

// 任务 404（launcher watchdog）：the 0930 silent crash (desktop dead under
// Modern Standby, three-channel silence: no log tail, no WER event, no dump)
// had no recovery face — the packaged launcher started the desktop and exited
// immediately, so nothing was left to notice. The detach path now keeps the
// launcher resident as the desktop's parent: it waits for the desktop to exit,
// and when that exit was NOT a deliberate one it restarts the desktop exactly
// once, after a backoff.
//
// Division of labor (why one restart cannot double-launch):
//   - Every deliberate restart path — update publish, version switch, plain
//     restart (restart_update.go), superseded relaunch, second-instance yield,
//     normal quit — exits the desktop with code 0. A resident launcher sees 0
//     and stays out: the update handoff spawns its own launcher, so a 0 exit
//     must never be re-armed here.
//   - Non-zero exits are the silent-death class (native fault, forced
//     termination, Go fatal). One restart is the whole budget: if the
//     restarted desktop dies abnormally too, the launcher records it and
//     exits — a crash loop must never become a launcher-driven respawn loop.
//     Counting beyond one, choosing previous versions, or a product safe mode
//     stay out of this package's charter.
//
// Known tradeoff: the shutdown watchdog's forced exit (desktop/shutdown.go
// "wedged" teardown, exit 1) also lands in the non-zero class and therefore
// reopens the app once. That path is rare, visible (the window comes back),
// and strictly less costly than missing a silent death — the class this
// watchdog exists for.
const (
	// watchdogEnvOff is the kill switch: REASONIX_LAUNCHER_WATCHDOG=off
	// restores the pre-404 start-and-exit behavior byte for byte.
	watchdogEnvOff = "off"
)

// watchdogRestartDelay is the backoff between an abnormal exit and the one
// allowed restart — long enough to let a dying generation release the
// servepool gateway port and session locks, short enough that the recovery
// reads as the app coming back on its own. Var so tests can shrink it.
var watchdogRestartDelay = 5 * time.Second

// watchdogDisabled reports the kill switch state. Var so tests can flip it.
var watchdogDisabled = func() bool {
	return os.Getenv("REASONIX_LAUNCHER_WATCHDOG") == watchdogEnvOff
}

// superviseDesktop is the Run-side entry: watch the just-started desktop and
// maybe use the one restart. installRoot re-resolves current.json at restart
// time — a mid-flight update that moved the pointer must not resurrect a
// stale binary.
func superviseDesktop(gen1 *exec.Cmd, installRoot, desktopPath string, args []string) int {
	if watchdogDisabled() {
		return 0
	}
	return supervise(gen1, func() (*exec.Cmd, error) {
		return restartCommand(installRoot, desktopPath, args)
	})
}

// superviseMarkerVouches is the launcher-side read of the update-restart
// marker (task 876). The marker shape mirrors the desktop writer
// (desktop/update_restart_marker.go) — pid + unix seconds; keep both in
// lockstep, like the abnormal-exit record pair. Read-only: consume-once is
// the relaunched desktop's resume gate business, not the launcher's.
func superviseMarkerVouches(deadPID int) bool {
	if deadPID <= 0 {
		return false
	}
	path := filepath.Join(config.MemoryUserDir(), "update-restart-marker.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var marker struct {
		PID int   `json:"pid"`
		At  int64 `json:"at"`
	}
	if json.Unmarshal(body, &marker) != nil || marker.PID <= 0 || marker.PID != deadPID {
		return false
	}
	return time.Now().Unix()-marker.At < 10*60
}

// supervise watches generation 1 and, on an abnormal exit, restarts once after
// the backoff. restart builds generation 2 lazily — only an abnormal first
// death pays for the resolution work. The returned int is the launcher's own
// process exit code: 0 when the last watched generation ended deliberately.
func supervise(gen1 *exec.Cmd, restart func() (*exec.Cmd, error)) int {
	if err := gen1.Wait(); err == nil {
		return 0
	}
	code := exitCodeOf(gen1)
	// Task 876 (847 family, exit-code dimension): a forced update restart
	// (grace window expired → forced quit) can still exit non-zero, and the
	// pre-876 launcher then recorded it as an abnormal death — the next boot
	// surfaced a genuine-looking "crash report" for a PLANNED handoff. The
	// update-restart marker is the exit's own witness: the forced-through
	// path writes it right before quitting (desktop/update_restart_marker.go,
	// "publish"), the PID cross-check is the dead process, and the 10-minute
	// fresh window bounds the vouch exactly like desktop's lifecycle peek.
	// A marker hit means: do not record death evidence and do not burn the
	// one restart — the desktop family relaunches itself through its own
	// launcher handoff, so a watchdog restart here would double-launch.
	if superviseMarkerVouches(gen1.Process.Pid) {
		slog.Info("launcher: desktop exited non-zero with a fresh update-restart marker; planned handoff, no death record, no restart",
			"pid", gen1.Process.Pid, "exit", code)
		appendDesktopLogLine(fmt.Sprintf(
			"launcher: planned update-restart exit pid=%d exit=%d (marker vouched); no abnormal-exit record, no watchdog restart",
			gen1.Process.Pid, code))
		return 0
	}
	slog.Warn("launcher: desktop exited abnormally; restarting once after backoff",
		"pid", gen1.Process.Pid, "exit", code, "backoff", watchdogRestartDelay.String())
	// Task 736 (issue #39): persist the death observation so the next desktop
	// instance can report occurredAt=actual death moment + exit code instead
	// of the lifecycle record's stale last-phase-write timestamp.
	writeAbnormalExitRecord(gen1.Process.Pid, code, time.Now())
	// Channel 3 (task 304) precedent: the launcher's slog dies with the GUI
	// process unless the same line lands in the desktop rolling log, where the
	// incident readers actually look. Failure is logged nowhere and hurts
	// nothing — the slog copy above is the primary record (task 404 points the
	// launcher's own copy at logs/launcher/launcher.log).
	appendDesktopLogLine(fmt.Sprintf(
		"launcher: desktop exited abnormally; restarting once after backoff pid=%d exit=%d backoff=%s",
		gen1.Process.Pid, code, watchdogRestartDelay))

	time.Sleep(watchdogRestartDelay)

	gen2, err := restart()
	if err != nil {
		slog.Error("launcher: watchdog restart failed", "err", err)
		appendDesktopLogLine(fmt.Sprintf("launcher: watchdog restart failed err=%v", err))
		return 1
	}
	waitErr := gen2.Wait()
	// A watchdog must never panic the process it is guarding, even if a
	// restart builder ever returns an unstarted command (pid unknown then).
	pid := 0
	if gen2.Process != nil {
		pid = gen2.Process.Pid
	}
	if waitErr != nil {
		// The one-restart budget is spent. Record and leave the scene alone.
		gen2Code := exitCodeOf(gen2)
		writeAbnormalExitRecord(pid, gen2Code, time.Now())
		slog.Warn("launcher: restarted desktop exited abnormally; restart budget spent, not restarting again",
			"pid", pid, "exit", gen2Code)
		return gen2Code
	}
	return 0
}

// restartCommand builds generation 2 exactly the way Run built generation 1
// and starts it. The desktop path is re-resolved from current.json so a
// pointer move during the dead generation (update committed, then crash)
// boots the newly active desktop; a failed re-resolution falls back to the
// path that was already running — a corrupt pointer should not convert a
// restartable crash into an abandoned desktop.
func restartCommand(installRoot, desktopPath string, args []string) (*exec.Cmd, error) {
	cmd := buildRestartCommand(installRoot, desktopPath, args)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func buildRestartCommand(installRoot, desktopPath string, args []string) *exec.Cmd {
	path := desktopPath
	if resolved, err := ResolveDesktopPath(installRoot); err == nil {
		path = resolved
	} else {
		slog.Warn("launcher: current.json re-resolution failed; restarting the previously running desktop",
			"err", err)
	}
	cmd := exec.Command(path, StripLegacyLaunchArgs(args)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Dir = installRoot
	return cmd
}

// exitCodeOf maps a finished command to its process exit code. After Wait the
// ProcessState is settled; a force-terminated process reports the raw uint32
// (4294967295) or -1 depending on how it died — both read as "terminated, not
// a clean exit", normalized to -1 so incident logs stay readable. Everything
// that is not a clean exit is abnormal.
func exitCodeOf(cmd *exec.Cmd) int {
	if cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code != -1 {
			if code == int(^uint32(0)) {
				return -1
			}
			return code
		}
	}
	return 1
}
