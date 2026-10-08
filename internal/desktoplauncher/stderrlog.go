package desktoplauncher

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"reasonix/internal/config"
)

// 任务 404（stderr 落盘）：the packaged launcher is a -H windowsgui binary, so
// its stderr is the known-silent channel — every fmt.Fprintln(os.Stderr, …)
// diagnostic in this package (resolve/identity/legacy-migrator errors, the
// task-272 relaunch-wait warning from waitfor.go) died with it. The 0930
// silent-crash forensics named exactly this: "launcher 只写 stderr，GUI 进程
// stderr 丢失 = 静默通道".
//
// The packaged launcher therefore points its own stderr at a dedicated rolling
// file, following the internal/baseproc F2 family pattern (rename-once
// rotation, append on rotation failure — losing the file must never lose the
// launch). The desktop child inherits the same file as its fd 2, so its
// pre-installDesktopLogging boot output lands here too.
const (
	// launcherLogFileName is the launcher's own log (logs/launcher/launcher.log,
	// the same home-relative layout as logs/desktop/desktop.log).
	launcherLogFileName = "launcher.log"
	// launcherLogMaxBytes caps one generation — 4 MiB per task 404. Rotation is
	// the family pattern from internal/baseproc/baselog.go: rename the
	// oversized file to <path>.1 at open, one generation deep.
	launcherLogMaxBytes = 4 << 20
)

// launcherLogPath resolves logs/launcher/launcher.log under the user state
// dir. Overridable so tests can point it at a temp dir.
var launcherLogPath = func() string {
	return filepath.Join(config.MemoryUserDir(), "logs", "launcher", launcherLogFileName)
}

// installLauncherLog redirects the launcher process's stderr (and the std log
// sink slog writes through) to the rolling launcher log. Detach-only by
// design: a console-launched launcher keeps talking to its terminal. Returns
// nil when redirection is not applied — a failed log file must never fail the
// launch (the R1 rule from baseproc's resolveStderr), it only costs the
// diagnostics.
func installLauncherLog() *os.File {
	if !detachByDefault() {
		return nil
	}
	f, err := openLauncherLogAt(launcherLogPath(), launcherLogMaxBytes)
	if err != nil {
		// The silent channel stays silent; nothing here is allowed to break
		// the launch. One line may still reach a console on dev runs.
		fmt.Fprintln(os.Stderr, "launcher: open log file:", err)
		return nil
	}
	os.Stderr = f
	log.SetOutput(f)
	return f
}

// openLauncherLogAt opens the log for append, creating the parent directory,
// and rotates an oversized previous generation to <path>.1 first. Rotation is
// best effort: if another process still holds the old file open (Windows
// refuses to rename an open file — the desktop child inherits this handle),
// appending past the cap is strictly better than dropping the log line.
func openLauncherLogAt(path string, maxBytes int64) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("launcher: create log dir: %w", err)
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= maxBytes {
		_ = os.Remove(path + ".1")
		if err := os.Rename(path, path+".1"); err != nil {
			// Keep appending to the current file instead.
			_ = err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("launcher: open log: %w", err)
	}
	return f, nil
}
