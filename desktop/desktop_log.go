package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/config"
)

const (
	desktopLogDirName      = "logs"
	desktopLogSubDir       = "desktop"
	desktopLogFileName     = "desktop.log"
	desktopLogMaxSizeBytes = 4 << 20 // 4MB per file, then roll
	desktopLogMaxBackups   = 25      // current + 24 rolled: 100MB ceiling
)

// rollingLogWriter appends to desktop.log and rolls it in place when the file
// reaches desktopLogMaxSizeBytes: desktop.log → desktop.log.1 → … →
// desktop.log.<n-1>, keeping at most desktopLogMaxBackups files on disk. It is
// dependency-free by design so the desktop build keeps its module surface.
//
// tl 1097: the per-file size and backup-count limits become per-writer fields
// (zero values fall back to the full-log constants) so the new alert-log writer
// can use 1MB/份 with a higher backup ceiling while the full log keeps the
// exact same 4MB/25 behaviour — the rolling algorithm itself is unchanged.
type rollingLogWriter struct {
	mu           sync.Mutex
	path         string
	f            *os.File
	size         int64
	maxSizeBytes int64 // 0 = desktopLogMaxSizeBytes (full-log default)
	maxBackups   int   // 0 = desktopLogMaxBackups (full-log default)
}

func newRollingLogWriter(path string) *rollingLogWriter {
	return &rollingLogWriter{path: path}
}

// newRollingLogWriterWithMax returns a rollingLogWriter with per-file size and
// backup-count limits other than the full-log defaults (tl 1097: the alert log
// uses 1MB/份 and a higher backup ceiling, while the full log keeps 4MB/25
// unchanged). The rolling algorithm is byte-for-byte the same code path.
func newRollingLogWriterWithMax(path string, maxSizeBytes int64, maxBackups int) *rollingLogWriter {
	return &rollingLogWriter{path: path, maxSizeBytes: maxSizeBytes, maxBackups: maxBackups}
}

func (w *rollingLogWriter) effectiveMaxSizeBytes() int64 {
	if w.maxSizeBytes > 0 {
		return w.maxSizeBytes
	}
	return desktopLogMaxSizeBytes
}

func (w *rollingLogWriter) effectiveMaxBackups() int {
	if w.maxBackups > 0 {
		return w.maxBackups
	}
	return desktopLogMaxBackups
}

func (w *rollingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureLocked(); err != nil {
		return 0, err
	}
	if w.size+int64(len(p)) > w.effectiveMaxSizeBytes() {
		w.rotateLocked()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rollingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

func (w *rollingLogWriter) ensureLocked() error {
	if w.f != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.size = info.Size()
	return nil
}

func (w *rollingLogWriter) rotateLocked() {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	// Shift the existing rolled files down; the oldest falls off the cliff.
	for i := w.effectiveMaxBackups() - 1; i >= 1; i-- {
		older := w.rolledPath(i)
		newer := w.rolledPath(i - 1)
		if i == 1 {
			newer = w.path
		}
		if _, err := os.Stat(newer); err != nil {
			continue
		}
		_ = os.Remove(older)
		_ = os.Rename(newer, older)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	w.f = f
	w.size = 0
}

func (w *rollingLogWriter) rolledPath(i int) string {
	return fmt.Sprintf("%s.%d", w.path, i)
}

// installDesktopLogging redirects the standard log package and the default
// slog logger to a rolling file under logs\desktop\. Every panic recovered by
// goSafe/safego, http.Server error logs, and serve/servepool slog lines then
// survive process death for post-mortem triage. Call it as early as possible
// in main — before any goroutine that could panic is launched.
func installDesktopLogging() {
	w := newRollingLogWriter(filepath.Join(config.MemoryUserDir(), desktopLogDirName, desktopLogSubDir, desktopLogFileName))
	if err := w.ensureLocked(); err != nil {
		// No writable log dir: keep the default stderr behavior rather than
		// breaking startup over diagnostics.
		return
	}
	log.SetOutput(w)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	// slog defaults to stderr too, and the serve/servepool layers log via
	// slog — rebind the default logger so those lines land in the file.
	//
	// The level is configurable because the fork's own features (serve pool, remote
	// tabs, session handoff, authorized write roots) are the ones that fail in ways
	// nothing else observes, and their detail lives at Debug. The default stays Info
	// so an ordinary log is not buried; REASONIX_DESKTOP_LOG=debug is what a bug
	// report should be reproduced under.
	fullHandler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: desktopLogLevel()})
	// tl 1097: after the full-log writer is in place, layer the WARN/ERROR-only
	// alert-log writer on top (desktop-alerts.log, same directory). The full
	// handler keeps writing every level to desktop.log byte-for-byte unchanged.
	installDesktopAlertLogging()
	slog.SetDefault(slog.New(desktopAlertSplitHandler(fullHandler, desktopAlertLogWriter())))
}

// desktopAlertSplitHandler layers the alert (WARN/ERROR-only) writer on top of
// the full-log handler. If the alert writer failed to install (e.g. read-only
// log dir, broken config), it returns the full handler unchanged — the alert
// path is best-effort and must never break the full log.
func desktopAlertSplitHandler(full slog.Handler, alert *alertLogLevelWriter) slog.Handler {
	if alert == nil {
		return full
	}
	return &compositeSlogHandler{
		full:  full,
		alert: slog.NewTextHandler(alert, &slog.HandlerOptions{Level: slog.LevelWarn}),
	}
}

// desktopLogLevel reads REASONIX_DESKTOP_LOG (info|debug|trace; default info).
// An environment variable rather than a setting because the interesting failures
// happen before or without the settings UI — a remote tab that will not connect, a
// serve pool that will not start — and the user is already editing the environment
// to reproduce them. Unknown values fall back to info rather than silencing logs.
func desktopLogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REASONIX_DESKTOP_LOG"))) {
	case "debug":
		return slog.LevelDebug
	case "trace":
		return slog.LevelDebug - 4
	default:
		return slog.LevelInfo
	}
}
