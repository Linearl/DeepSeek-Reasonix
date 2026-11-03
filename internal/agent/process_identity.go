package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// Process identity resolution for lease-holder reporting (tl1367 line 1).
//
// Until now a lease refusal named only the holder pid; the user (and the
// desktop log) could not tell WHICH program that pid was, so a stale record
// whose pid had been reused by an unrelated process read as a live foreign
// Reasonix window (the 2026-11-03 archive-refusal incident shape). These
// helpers resolve the pid's executable identity so messages and logs can say
// who is holding the session, and the holder classification can tell a live
// Reasonix holder from a pid that was silently reused.

// ProcessImageName is the resolvable (name, full image path) pair for pid.
// Either part is empty when the platform cannot answer (permission denied,
// process gone, non-Windows without /proc). Var seam for tests.
var ProcessImageName = defaultProcessImageName

// ProcessLooksLikeSelf reports whether pid's executable is plausibly this
// application: the same image path as this process, or a binary whose name
// contains "reasonix" (case-insensitive — covers installed copies at other
// paths and dev builds). Callers must treat a false answer as "pid possibly
// reused by an unrelated program", never as proof of death: the OS lease lock
// stays the only liveness arbiter, so a misclassification can at worst change
// the wording, never break mutual exclusion.
func ProcessLooksLikeSelf(pid int) bool {
	if pid <= 0 {
		return false
	}
	name, path := ProcessImageName(pid)
	if name == "" && path == "" {
		// Identity unresolvable: fall back to "plausibly us" so the message
		// stays in the live-holder family (conservative, same as the pre-tl1367
		// classification).
		return true
	}
	if self, err := os.Executable(); err == nil && path != "" && strings.EqualFold(path, self) {
		return true
	}
	return strings.Contains(strings.ToLower(name), "reasonix")
}

// ReasonixProcessLabel renders the holder's process identity for a user-facing
// message: "name (path)" with graceful degradation when the platform cannot
// resolve either part. The image path identifies the program — it is not a
// session path and leaks nothing about the session (tl1367 line 1 explicitly
// requests process name + path in the refusal text).
func ReasonixProcessLabel(pid int) string {
	name, path := ProcessImageName(pid)
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	switch {
	case name != "" && path != "":
		return name + " (" + path + ")"
	case name != "":
		return name
	case path != "":
		return filepath.Base(path) + " (" + path + ")"
	default:
		return ""
	}
}
