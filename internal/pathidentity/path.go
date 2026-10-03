package pathidentity

import (
	"path/filepath"
	"runtime"
	"strings"

	"reasonix/internal/sandbox"
)

// Canonical returns the physical, absolute path key used by session leases.
func Canonical(path string) string {
	key := filepath.Clean(strings.TrimSpace(path))
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	// Resolve physical identity, not just spelling. Otherwise a symlink or
	// junction alias can acquire a second sidecar lock for the same transcript.
	//
	// Task 460: the walk is delegated to the shared bounded canonical engine
	// (internal/sandbox/canonical.go, task 455fix) instead of the previous
	// unbounded per-request EvalSymlinks cascade. The engine performs the same
	// deepest-existing-ancestor walk with the missing tail re-appended, so the
	// key form is unchanged, but a dead network path now costs one 250ms
	// budget per 30s TTL instead of a full SMB reconnect on every save-path
	// canonicalization and every single-instance identity check — and
	// concurrent callers share one walk.
	if resolved, err := sandbox.ResolveAbsPath(key); err == nil && resolved != "" {
		key = resolved
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(strings.ToUpper(key), `\\?\UNC\`) {
			key = `\\` + key[len(`\\?\UNC\`):]
		} else {
			key = strings.TrimPrefix(key, `\\?\`)
		}
		key = strings.ToLower(key)
	}
	return key
}
