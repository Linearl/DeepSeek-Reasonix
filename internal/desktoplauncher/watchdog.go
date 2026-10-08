package desktoplauncher

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
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

// supervise watches generation 1 and, on an abnormal exit, restarts once after
// the backoff. restart builds generation 2 lazily — only an abnormal first
// death pays for the resolution work. The returned int is the launcher's own
// process exit code: 0 when the last watched generation ended deliberately.
func supervise(gen1 *exec.Cmd, restart func() (*exec.Cmd, error)) int {
	if err := gen1.Wait(); err == nil {
		return 0
	}
	code := exitCodeOf(gen1)
	slog.Warn("launcher: desktop exited abnormally; restarting once after backoff",
		"pid", gen1.Process.Pid, "exit", code, "backoff", watchdogRestartDelay.String())
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
		slog.Warn("launcher: restarted desktop exited abnormally; restart budget spent, not restarting again",
			"pid", pid, "exit", exitCodeOf(gen2))
		return exitCodeOf(gen2)
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
