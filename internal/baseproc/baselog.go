package baseproc

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"reasonix/internal/config"
)

// Slice S1c — F2 (日志面): the subprocess must never be a black box.
//
// "无日志≠未发生" is a documented failure mode here: the GUI process has no
// console, so before this slice every diagnostic the base subprocess emitted
// (including a raw runtime panic, which the Go runtime writes to fd 2) was
// inherited by the parent's stderr and dropped. This file points that stream
// at a dedicated file instead:
//
//	parent: cmd.Stderr = <REASONIX_HOME>/logs/base/base.log (+ rotation)
//	child : fd 2 IS that file, so slog lines, `base serve: …` diagnostics and
//	        a panic all land on disk; the path is passed along in
//	        REASONIX_BASE_LOG so a child can name its own log.
const (
	// baseLogFileName is the resident base's log (design F2: logs/base.log).
	baseLogFileName = "base.log"
	// defaultBaseLogMaxBytes caps one generation. Rotation is deliberately
	// cheap and one-deep: rename the oversized file to base.log.1 at open, so
	// history survives a restart without pulling in a rotation dependency.
	defaultBaseLogMaxBytes = 2 << 20 // 2 MiB
)

// baseLogEnv passes the log path to the subprocess (F1's explicit-environment
// discipline: the child learns its own log location without guessing).
const baseLogEnv = "REASONIX_BASE_LOG"

// defaultBaseLogPath is <REASONIX_HOME>/logs/base/base.log, the same
// home-relative layout the desktop log uses.
func defaultBaseLogPath() string {
	return filepath.Join(config.MemoryUserDir(), "logs", "base", baseLogFileName)
}

// openBaseLog opens the log for append, creating the parent directory, and
// rotates an oversized previous generation to <path>.1 first. Rotation is
// best effort: if another process still holds the old file (Windows refuses
// to rename an open file), appending forever is strictly better than dropping
// the log line.
func openBaseLog(path string, maxBytes int64) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("baseproc: create log dir: %w", err)
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= maxBytes {
		_ = os.Remove(path + ".1")
		if renameErr := os.Rename(path, path+".1"); renameErr != nil {
			// Keep appending to the current file instead.
			_ = renameErr
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("baseproc: open log: %w", err)
	}
	return f, nil
}

// resolvedStderr is the subprocess's stderr sink plus whatever is needed to
// release it after the process is gone.
type resolvedStderr struct {
	w       io.Writer
	path    string // passed to the child as REASONIX_BASE_LOG ("" when not a file)
	cleanup func()
}

// resolveStderr picks the subprocess log sink:
//
//   - Options.Stderr set → use it verbatim (tests inject a buffer);
//   - otherwise the log file (Options.LogFile, else logs/base/base.log);
//   - if the file cannot be opened, fall back to the inherited stderr with a
//     warning — losing the dedicated file must never fail a spawn (R1).
func resolveStderr(opts Options) resolvedStderr {
	if opts.Stderr != nil {
		return resolvedStderr{w: opts.Stderr, path: opts.LogFile, cleanup: func() {}}
	}
	path := opts.LogFile
	if path == "" {
		path = defaultBaseLogPath()
	}
	f, err := openBaseLog(path, defaultBaseLogMaxBytes)
	if err != nil {
		return resolvedStderr{w: os.Stderr, cleanup: func() {}}
	}
	return resolvedStderr{
		w:       f,
		path:    path,
		cleanup: func() { _ = f.Close() },
	}
}

// withBaseLogEnv adds REASONIX_BASE_LOG to the spawn environment, replacing
// any stale value so the child never reads an outdated path.
func withBaseLogEnv(env []string, path string) []string {
	if path == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if len(kv) > len(baseLogEnv) && kv[:len(baseLogEnv)+1] == baseLogEnv+"=" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, baseLogEnv+"="+path)
}
