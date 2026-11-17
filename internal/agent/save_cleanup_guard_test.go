package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tl1681 — delayed-cleanup marker path guard. Three rejection states per the
// acceptance criteria (../ traversal, absolute-path injection, symlink escape)
// plus the zero-regression proof that clean spellings keep the exact pre-guard
// marker path and the full Mark/Is/Clear cycle.

// isolateSessionState points REASONIX_STATE_HOME at a fresh directory so the
// guard's configured storage roots derive inside the test's own space.
func isolateSessionState(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", home)
	return home
}

func TestCleanupPendingGuardRejectsTraversalEscape(t *testing.T) {
	home := isolateSessionState(t)
	escapee := filepath.Join(home, "evil")

	// The traversal payload crosses out of <state>/sessions through dot
	// segments; the canonical target lands outside every configured root.
	// String-built on purpose: filepath.Join would Clean the dot segments
	// away before the guard ever sees them.
	raw := filepath.Join(home, "sessions", "good.jsonl") + "/../../evil.jsonl"
	if filepath.Clean(raw) != escapee+".jsonl" {
		t.Fatalf("test setup: canonical form of %q = %q, want %q", raw, filepath.Clean(raw), escapee+".jsonl")
	}

	if err := MarkCleanupPending(raw, "delete"); err == nil {
		t.Fatal("MarkCleanupPending with ../ traversal escaped roots = nil error, want rejection")
	}
	if IsCleanupPending(raw) {
		t.Fatal("IsCleanupPending with ../ traversal = true, want false (guard rejection)")
	}
	if err := ClearCleanupPending(raw); err == nil {
		t.Fatal("ClearCleanupPending with ../ traversal = nil error, want rejection")
	}
	// Nothing may have been published at the escaped location.
	if _, err := os.Stat(escapee + ".cleanup-pending.json"); !os.IsNotExist(err) {
		t.Fatalf("escaped marker exists after rejected Mark: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("rejected Mark must not create storage directories: %v", err)
	}
}

func TestCleanupPendingGuardRejectsAnyTraversalSpelling(t *testing.T) {
	isolateSessionState(t)
	// Acceptance criterion is literal: a sessionPath carrying dot segments is
	// rejected even when Clean would keep it inside a storage root. Production
	// callers only ever pass enumeration output (clean spellings), so the
	// strict gate has no legitimate traffic to turn away. The raw spelling is
	// string-built: filepath.Join would Clean the dot segment away first.
	raw := filepath.Join(t.TempDir(), "a.jsonl") + "/../b.jsonl"
	if filepath.Clean(raw) == filepath.FromSlash(raw) {
		t.Fatalf("test setup: %q must carry a dot segment", raw)
	}
	if err := MarkCleanupPending(raw, "delete"); err == nil {
		t.Fatal("MarkCleanupPending with in-root ../ spelling = nil error, want rejection")
	}
}

func TestCleanupPendingGuardRejectsAbsoluteInjection(t *testing.T) {
	isolateSessionState(t)
	var cases []string
	if runtime.GOOS == "windows" {
		// UNC shares, device paths and drive-relative spellings reach beyond
		// the local drive namespace and land outside every configured root.
		cases = append(cases, `\\server\share\evil.jsonl`, `\\?\C:\evil.jsonl`, `C:evil.jsonl`)
	} else {
		// A doubled leading separator is a spelling Clean rewrites (gate 1)
		// whose canonical form points at the filesystem root.
		cases = append(cases, "//evil.jsonl")
	}
	for _, raw := range cases {
		if err := MarkCleanupPending(raw, "delete"); err == nil {
			t.Errorf("MarkCleanupPending(%q) = nil error, want rejection", raw)
		}
		if IsCleanupPending(raw) {
			t.Errorf("IsCleanupPending(%q) = true, want false (guard rejection)", raw)
		}
		if err := ClearCleanupPending(raw); err == nil {
			t.Errorf("ClearCleanupPending(%q) = nil error, want rejection", raw)
		}
	}
}

func TestNonLocalPathSpelling(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"plain.jsonl", false},
		{"sessions/plain.jsonl", false},
		{`\\server\share\evil.jsonl`, runtime.GOOS == "windows"},
		{`\\?\C:\evil.jsonl`, runtime.GOOS == "windows"},
		{`C:evil.jsonl`, runtime.GOOS == "windows"},
		{`C:\evil.jsonl`, false}, // local absolute: not a non-local shape
		{"/abs/local.jsonl", false},
	}
	for _, tc := range cases {
		if got := nonLocalPathSpelling(tc.in); got != tc.want {
			t.Errorf("nonLocalPathSpelling(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCleanupPendingGuardRejectsSymlinkEscape(t *testing.T) {
	home := isolateSessionState(t)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sessions, "link")
	if runtime.GOOS == "windows" {
		// Junctions need no privilege where symbolic links do; failure is
		// fatal — a skipped escape scenario equals an untested one.
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput()
		if err != nil {
			t.Fatalf("mklink /J %s -> %s: %v (%s)", link, outside, err, strings.TrimSpace(string(out)))
		}
	} else {
		if err := os.Symlink(outside, link); err != nil {
			t.Fatalf("os.Symlink %s -> %s: %v", link, outside, err)
		}
	}

	// The spelling is clean and lexically inside the configured root; only the
	// physical identity walk (gate 3, write/delete sinks) can see that the
	// directory component is an alias pointing outside the root.
	raw := filepath.Join(link, "evil.jsonl")
	if err := MarkCleanupPending(raw, "delete"); err == nil {
		t.Fatal("MarkCleanupPending through root-internal symlink = nil error, want rejection")
	}
	if err := ClearCleanupPending(raw); err == nil {
		t.Fatal("ClearCleanupPending through root-internal symlink = nil error, want rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.cleanup-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("marker leaked through symlink into %s: %v", outside, err)
	}
}

func TestCleanupPendingGuardKeepsCleanPathsUnchanged(t *testing.T) {
	// No state-home override here on purpose: this mirrors the ~70 existing
	// call sites (package tests and external enumerations) whose session paths
	// are clean absolute paths outside every configured root. Their marker
	// spelling and the full Mark/Is/Clear cycle must stay byte-identical.
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.jsonl")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := CleanupPendingPath(path)
	if want == "" {
		t.Fatal("CleanupPendingPath returned empty for a clean path")
	}

	if err := MarkCleanupPending(path, "delete"); err != nil {
		t.Fatalf("MarkCleanupPending: %v", err)
	}
	if got := CleanupPendingPath(path); got != want {
		t.Fatalf("marker spelling changed by guard: %q, want %q", got, want)
	}
	if !IsCleanupPending(path) {
		t.Fatal("IsCleanupPending = false after Mark, want true")
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("marker file missing at pre-guard spelling: %v", err)
	}
	if err := ClearCleanupPending(path); err != nil {
		t.Fatalf("ClearCleanupPending: %v", err)
	}
	if IsCleanupPending(path) {
		t.Fatal("IsCleanupPending = true after Clear, want false")
	}
}

func TestCleanupPendingGuardSanctionsTraversalFreePathsInsideRoots(t *testing.T) {
	home := isolateSessionState(t)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, "inside.jsonl")

	// A clean path inside the configured storage root passes every gate.
	if err := MarkCleanupPending(path, "delete"); err != nil {
		t.Fatalf("MarkCleanupPending inside configured root: %v", err)
	}
	if !IsCleanupPending(path) {
		t.Fatal("IsCleanupPending = false for in-root marker, want true")
	}
	if err := ClearCleanupPending(path); err != nil {
		t.Fatalf("ClearCleanupPending inside configured root: %v", err)
	}
}

func TestCleanupPendingGuardEmptyPathNoop(t *testing.T) {
	if err := MarkCleanupPending("", "delete"); err != nil {
		t.Fatalf("MarkCleanupPending(\"\") = %v, want nil", err)
	}
	if err := MarkCleanupPending("   ", "delete"); err != nil {
		t.Fatalf("MarkCleanupPending(\"   \") = %v, want nil", err)
	}
	if IsCleanupPending("") {
		t.Fatal("IsCleanupPending(\"\") = true, want false")
	}
	if err := ClearCleanupPending(""); err != nil {
		t.Fatalf("ClearCleanupPending(\"\") = %v, want nil", err)
	}
}
