package desktoplauncher

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
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
type abnormalExitRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	PID           int    `json:"pid"`
	ExitCode      int    `json:"exitCode"`
	ExitedAt      string `json:"exitedAt"` // RFC3339Nano, launcher clock
}

// abnormalExitRecordPath resolves the record under the user state dir's
// lifecycle directory, next to the per-process lifecycle records it
// attributes. Overridable so tests can point it at a temp dir.
var abnormalExitRecordPath = func() string {
	return filepath.Join(config.MemoryUserDir(), "diagnostics", "lifecycle", "last-abnormal-exit.json")
}

// writeAbnormalExitRecord persists the launcher's death observation.
// Best effort: a failed record must never break the supervision/restart flow.
func writeAbnormalExitRecord(pid, exitCode int, exitedAt time.Time) {
	path := abnormalExitRecordPath()
	if path == "" || pid <= 0 {
		return
	}
	body, err := json.Marshal(abnormalExitRecord{
		SchemaVersion: 1,
		PID:           pid,
		ExitCode:      exitCode,
		ExitedAt:      exitedAt.UTC().Format(time.RFC3339Nano),
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
	}
}
