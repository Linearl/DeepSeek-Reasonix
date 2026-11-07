package checkpoint

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// tl1476 rewind preflight classification.
//
// PrepareRewind reports which files a rewind would touch and which of them
// conflict with the precheck (path_unsafe, missing_payload, manual_edit, ...).
// The confirmation dialog additionally needs a per-file safe/unsafe verdict so
// the user sees WHY a rewind is blocked before attempting it, including
// write-permission problems the conflict precheck does not cover. The verdict
// is advisory: CanFiles and the commit path keep their existing semantics.

// preflightFileSafety classifies each rewind target as safe or unsafe.
// Conflict-registered files inherit their conflict reason verbatim so the UI
// can label exactly what blocks them (workspace escape, missing payload, ...).
// Every other target gets a conservative write-access probe: clear
// permission-denied errors mark the file unsafe (permission_denied); anything
// ambiguous stays safe because the commit path re-runs the authoritative
// precheck and compensates partial failures.
func (s *Store) preflightFileSafety(files []string, conflicts []RewindConflict) []RewindFileSafety {
	if s == nil || len(files) == 0 {
		return nil
	}
	conflictByPath := make(map[string]string, len(conflicts))
	for _, c := range conflicts {
		if c.Path != "" && conflictByPath[c.Path] == "" {
			conflictByPath[c.Path] = c.Reason
		}
	}
	out := make([]RewindFileSafety, 0, len(files))
	for _, p := range files {
		if p == "" {
			continue
		}
		if reason := conflictByPath[p]; reason != "" {
			out = append(out, RewindFileSafety{Path: p, Safe: false, Reason: reason})
			continue
		}
		abs, err := safePath(s.root, p)
		if err != nil {
			out = append(out, RewindFileSafety{Path: p, Safe: false, Reason: ConflictPathUnsafe})
			continue
		}
		if probeRewindWriteDenied(abs) {
			out = append(out, RewindFileSafety{Path: p, Safe: false, Reason: ConflictPermissionDenied})
			continue
		}
		out = append(out, RewindFileSafety{Path: p, Safe: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// probeRewindWriteDenied reports whether the filesystem currently denies the
// write access the rewind needs. A target that exists must be writable; a
// missing target will be recreated, which needs a writable parent directory.
// Only explicit permission errors count — every other stat/open failure is
// left to the commit path to report.
func probeRewindWriteDenied(abs string) bool {
	if info, err := os.Lstat(abs); err == nil {
		if info.IsDir() {
			return false
		}
		f, openErr := os.OpenFile(abs, os.O_WRONLY, 0)
		if openErr == nil {
			_ = f.Close()
			return false
		}
		return isPermissionErr(openErr)
	}
	probe, err := os.CreateTemp(filepath.Dir(abs), ".reasonix-rewind-probe-*")
	if err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		return false
	}
	if errors.Is(err, fs.ErrExist) {
		return false
	}
	return isPermissionErr(err)
}

func isPermissionErr(err error) bool {
	return errors.Is(err, fs.ErrPermission)
}
