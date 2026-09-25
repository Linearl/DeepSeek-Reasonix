package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

// WritableRootSet is the session-scoped writable directory manager. Baseline
// is the workspace plus configured allow_write and --add-dir roots. Session
// holds directories approved for the rest of this logical session. Per-call
// roots ride on the execution context and never leak to other tool calls.
type WritableRootSet struct {
	mu       sync.RWMutex
	baseline []string
	session  []string
	// unbounded short-circuits every coverage check: full access (yolo,
	// task 257) passes any declared directory instead of consulting the
	// roots. Boot sets it once from the experimental_full_access switch;
	// session grants still record normally underneath.
	unbounded bool
}

// NewWritableRootSet builds a set with the given baseline roots.
func NewWritableRootSet(baseline []string) *WritableRootSet {
	return &WritableRootSet{baseline: CollapseWriteRoots(canonicalDirs(baseline))}
}

// ReplaceBaseline swaps the configured roots (workspace, allow_write, --add-dir)
// without dropping session grants.
func (s *WritableRootSet) ReplaceBaseline(roots []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.baseline = CollapseWriteRoots(canonicalDirs(roots))
	s.mu.Unlock()
}

// GrantVerifiedBaseline adds already-verified absolute identities to the
// persistent baseline. They survive ClearSession and are not re-resolved.
func (s *WritableRootSet) GrantVerifiedBaseline(dirs []string) {
	if s == nil || len(dirs) == 0 {
		return
	}
	s.mu.Lock()
	s.baseline = CollapseWriteRoots(append(append([]string{}, s.baseline...), verifiedDirs(dirs)...))
	s.mu.Unlock()
}

// GrantSession adds directories to the session grant set.
func (s *WritableRootSet) GrantSession(dirs []string) {
	if s == nil || len(dirs) == 0 {
		return
	}
	s.mu.Lock()
	s.session = CollapseWriteRoots(append(append([]string{}, s.session...), canonicalDirs(dirs)...))
	s.mu.Unlock()
}

// GrantVerifiedSession adds already-verified absolute identities without
// following their path components again after the user approved them.
func (s *WritableRootSet) GrantVerifiedSession(dirs []string) {
	if s == nil || len(dirs) == 0 {
		return
	}
	s.mu.Lock()
	s.session = CollapseWriteRoots(append(append([]string{}, s.session...), verifiedDirs(dirs)...))
	s.mu.Unlock()
}

// ClearSession drops session grants. Project baseline is left intact.
func (s *WritableRootSet) ClearSession() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.session = nil
	s.mu.Unlock()
}

// RemoveSessionRoot removes one path from the session grant set (canonical /
// path-within comparison). It is a no-op when the root is not present.
func (s *WritableRootSet) RemoveSessionRoot(dir string) {
	if s == nil || strings.TrimSpace(dir) == "" {
		return
	}
	target := canonicalDir(dir)
	if target == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.session[:0]
	changed := false
	for _, root := range s.session {
		if canonicalDir(root) == target {
			changed = true
			continue
		}
		out = append(out, root)
	}
	if changed {
		s.session = CollapseWriteRoots(out)
	}
}

// RemoveBaselineDir removes one path from the persistent baseline (task 157.A).
// Symmetric to RemoveSessionRoot: config-backed add/remove entries must take
// effect on the live set instead of waiting for the next LoadForRoot. The
// baseline is a collapsed set, so this drops the matching identity only —
// boot-only extras (--add-dir) and unrelated roots are preserved, and an entry
// already folded under a covering parent stays covered (same semantics as the
// config it mirrors). No-op when the root is not present.
func (s *WritableRootSet) RemoveBaselineDir(dir string) {
	if s == nil || strings.TrimSpace(dir) == "" {
		return
	}
	target := canonicalDir(dir)
	if target == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.baseline[:0]
	changed := false
	for _, root := range s.baseline {
		if canonicalDir(root) == target {
			changed = true
			continue
		}
		out = append(out, root)
	}
	if changed {
		s.baseline = CollapseWriteRoots(out)
	}
}

// SessionRoots returns a copy of the session-approved directories.
func (s *WritableRootSet) SessionRoots() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.session...)
}

// Snapshot returns baseline plus session grants, collapsed.
func (s *WritableRootSet) Snapshot() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CollapseWriteRoots(append(append([]string{}, s.baseline...), s.session...))
}

// Effective returns baseline + session + per-call roots from ctx.
func (s *WritableRootSet) Effective(ctx context.Context) []string {
	return CollapseWriteRoots(append(s.Snapshot(), PerCallWriteRoots(ctx)...))
}

// EffectiveSandboxRoots omits any approved root whose identity has changed.
// Bash uses this fail-closed view when constructing its OS sandbox.
func (s *WritableRootSet) EffectiveSandboxRoots(ctx context.Context) []string {
	return stableWriteRoots(s.Effective(ctx))
}

// SetUnbounded toggles full access (task 257): coverage checks answer from
// the flag instead of the root list. Boot sets it once from the config.
func (s *WritableRootSet) SetUnbounded(on bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.unbounded = on
	s.mu.Unlock()
}

// Unbounded reports whether full access (task 257) is active on this set.
func (s *WritableRootSet) Unbounded() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unbounded
}

// Covers reports whether dir is inside the current baseline+session snapshot.
func (s *WritableRootSet) Covers(dir string) bool {
	dir = canonicalDir(dir)
	if dir == "" {
		return false
	}
	if s.Unbounded() {
		return true
	}
	for _, root := range stableWriteRoots(s.Snapshot()) {
		if PathWithin(root, dir) {
			return true
		}
	}
	return false
}

// Missing returns the subset of dirs not already covered by the snapshot.
func (s *WritableRootSet) Missing(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	if s.Unbounded() {
		return nil
	}
	snap := stableWriteRoots(s.Snapshot())
	var missing []string
	for _, dir := range CollapseWriteRoots(canonicalDirs(dirs)) {
		covered := false
		for _, root := range snap {
			if PathWithin(root, dir) {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, dir)
		}
	}
	return missing
}

// CloneRestricted returns a new set whose baseline is the intersection of this
// set's snapshot with cap. The clone has no session grants. An empty cap
// copies the current snapshot (inherit, do not expand).
func (s *WritableRootSet) CloneRestricted(cap []string) *WritableRootSet {
	snap := s.Snapshot()
	var clone *WritableRootSet
	if len(cap) == 0 {
		clone = newVerifiedWritableRootSet(snap)
	} else {
		clone = newVerifiedWritableRootSet(intersectVerifiedWriteRoots(snap, canonicalDirs(cap)))
	}
	// Full access (task 257) is a session-wide grant: sub-agents inherit it
	// even when their write_paths claim is narrower than the parent's roots.
	clone.SetUnbounded(s.Unbounded())
	return clone
}

// IntersectWriteRoots returns directories that sit in both a and b, preferring
// the more specific path when one side is an ancestor of the other.
func IntersectWriteRoots(a, b []string) []string {
	a = CollapseWriteRoots(canonicalDirs(a))
	b = CollapseWriteRoots(canonicalDirs(b))
	return intersectVerifiedWriteRoots(a, b)
}

func intersectVerifiedWriteRoots(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	var out []string
	for _, left := range a {
		for _, right := range b {
			switch {
			case PathWithin(left, right):
				out = append(out, right)
			case PathWithin(right, left):
				out = append(out, left)
			}
		}
	}
	return CollapseWriteRoots(out)
}

type perCallWriteRootsKey struct{}

// WithPerCallWriteRoots stamps once-only writable directories onto ctx.
func WithPerCallWriteRoots(ctx context.Context, dirs []string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	dirs = CollapseWriteRoots(verifiedDirs(dirs))
	if len(dirs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, perCallWriteRootsKey{}, dirs)
}

// PerCallWriteRoots returns once-only writable directories from ctx.
func PerCallWriteRoots(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	dirs, _ := ctx.Value(perCallWriteRootsKey{}).([]string)
	return append([]string(nil), dirs...)
}

func canonicalDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if resolved := canonicalDir(dir); resolved != "" {
			out = append(out, resolved)
		}
	}
	return out
}

func newVerifiedWritableRootSet(baseline []string) *WritableRootSet {
	return &WritableRootSet{baseline: CollapseWriteRoots(verifiedDirs(baseline))}
}

func verifiedDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir != "" && dir != "." && filepath.IsAbs(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// stableWriteRoots drops roots whose current symlink-resolved identity no
// longer matches the identity captured when the root was configured or
// approved. Omitting a stale root makes sandbox construction fail closed.
func stableWriteRoots(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range verifiedDirs(dirs) {
		resolved, err := ResolveAbsPath(dir)
		if err == nil && sameWritePath(dir, resolved) {
			out = append(out, dir)
		}
	}
	return CollapseWriteRoots(out)
}
