package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Parallel full-access experiment (task 127).
//
// Off (default): writes stay inside the workspace and allow_write roots —
// identical to production.
//
// On: product-managed worktree storage (DeliveryWorktreeDir) and an optional
// conventional worktrees root become legal write surfaces for the parent
// session and for sub-agent write_paths. Escape outside those roots is still
// refused. The switch never disables confineWrite globally.

const (
	// EnvParallelFullAccess is the process-local override for the experiment.
	// Values "1", "true", "yes", "on" (case-insensitive) enable it.
	EnvParallelFullAccess = "REASONIX_PARALLEL_FULL_ACCESS"
	// EnvParallelWorktreesRoot names an extra aggregation root (typically
	// github-repo/worktrees) trusted when the experiment is on.
	EnvParallelWorktreesRoot = "REASONIX_PARALLEL_WORKTREES_ROOT"
)

var (
	parallelAccessMu      sync.RWMutex
	parallelAccessCached  *bool
	processParallelAccess atomic.Bool
)

// ResetParallelFullAccessCacheForTest clears the env-read cache so tests can
// flip REASONIX_PARALLEL_FULL_ACCESS without racing other cases.
func ResetParallelFullAccessCacheForTest() {
	parallelAccessMu.Lock()
	parallelAccessCached = nil
	parallelAccessMu.Unlock()
	processParallelAccess.Store(false)
}

// ApplyParallelFullAccess records the resolved config switch for this process.
// Boot calls it once so tool confinement and write-claims share one decision
// without threading *Config through every tool.
func ApplyParallelFullAccess(on bool) {
	processParallelAccess.Store(on)
}

// ParallelFullAccessActive is the process-wide switch tool layers consult.
// It is true when boot applied the config flag or the env override is set.
func ParallelFullAccessActive() bool {
	if processParallelAccess.Load() {
		return true
	}
	return envParallelFullAccess()
}

// ParallelFullAccessEnabled reports whether the experiment is on. Explicit
// config wins over the environment; the environment is the light-path switch
// for CLI/subagent runs that have no desktop settings UI yet.
func (c *Config) ParallelFullAccessEnabled() bool {
	if c != nil && c.Desktop.ExperimentalParallelFullAccess {
		return true
	}
	return envParallelFullAccess()
}

func envParallelFullAccess() bool {
	parallelAccessMu.RLock()
	if parallelAccessCached != nil {
		v := *parallelAccessCached
		parallelAccessMu.RUnlock()
		return v
	}
	parallelAccessMu.RUnlock()
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvParallelFullAccess)))
	on := raw == "1" || raw == "true" || raw == "yes" || raw == "on"
	parallelAccessMu.Lock()
	parallelAccessCached = &on
	parallelAccessMu.Unlock()
	return on
}

// ParallelWorktreeRoots returns the aggregation roots trusted when the
// experiment is on: product-managed worktree storage, the optional env root,
// and (when the workspace sits in a git checkout) a sibling worktrees/
// directory if one exists.
func ParallelWorktreeRoots(workspaceRoot string) []string {
	var roots []string
	if dir := strings.TrimSpace(DeliveryWorktreeDir()); dir != "" {
		roots = append(roots, filepath.Clean(dir))
	}
	if dir := strings.TrimSpace(os.Getenv(EnvParallelWorktreesRoot)); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			roots = append(roots, filepath.Clean(abs))
		} else {
			roots = append(roots, filepath.Clean(dir))
		}
	}
	if sibling := siblingWorktreesRoot(workspaceRoot); sibling != "" {
		roots = append(roots, sibling)
	}
	// Dedup while preserving order.
	seen := map[string]bool{}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		key := strings.ToLower(filepath.Clean(r))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, filepath.Clean(r))
	}
	return out
}

// siblingWorktreesRoot looks for <parent-of-git-common-dir>/worktrees when
// workspaceRoot is inside a git checkout. This matches the fork convention
// github-repo/reasonix + github-repo/worktrees without hardcoding names.
func siblingWorktreesRoot(workspaceRoot string) string {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	// Walk up looking for a .git entry (file or directory).
	dir := abs
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			parent := filepath.Dir(dir)
			candidate := filepath.Join(parent, "worktrees")
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return filepath.Clean(candidate)
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// IsUnderParallelWorktreeRoot reports whether path sits at or below one of the
// trusted aggregation roots. Callers must already know the experiment is on.
func IsUnderParallelWorktreeRoot(path, workspaceRoot string) bool {
	target := strings.TrimSpace(path)
	if target == "" {
		return false
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		abs = filepath.Clean(target)
	}
	abs = filepath.Clean(abs)
	for _, root := range ParallelWorktreeRoots(workspaceRoot) {
		if pathWithinRoot(root, abs) {
			return true
		}
	}
	return false
}
