package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
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
type rollingLogWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func newRollingLogWriter(path string) *rollingLogWriter {
	return &rollingLogWriter{path: path}
}

func (w *rollingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureLocked(); err != nil {
		return 0, err
	}
	if w.size+int64(len(p)) > desktopLogMaxSizeBytes {
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
	for i := desktopLogMaxBackups - 1; i >= 1; i-- {
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
	slog.SetDefault(slog.New(slog.NewTextHandler(w, nil)))
}
