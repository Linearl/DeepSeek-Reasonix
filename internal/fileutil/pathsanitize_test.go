package fileutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

// tl1373 — targeted tests for the path sanitization entry points. Three
// classes, per the task acceptance criteria: traversal attempts, absolute
// path injection, and legal inputs that must not be rejected or altered.

func TestSanitizeComponentTraversalRejected(t *testing.T) {
	for _, name := range []string{
		"..", "../evil", `..\evil`, "../../etc/passwd", "a/../b", "a/..",
		"./x", ".", "a/b", `a\b`, "/x", `\x`, "x/", `x\`,
	} {
		got, err := SanitizeComponent(name)
		if err == nil {
			t.Errorf("SanitizeComponent(%q) = %q, want error (traversal/separator)", name, got)
		}
		if got != "" {
			t.Errorf("SanitizeComponent(%q) returned %q alongside error", name, got)
		}
	}
}

func TestSanitizeComponentEmptyRejected(t *testing.T) {
	if got, err := SanitizeComponent(""); err == nil || got != "" {
		t.Errorf("SanitizeComponent(\"\") = (%q, %v), want empty+error", got, err)
	}
}

func TestSanitizeComponentAbsoluteInjectionRejected(t *testing.T) {
	cases := []string{"/etc/passwd", `\evil`, "C:\\evil", "/abs"}
	if runtime.GOOS == "windows" {
		cases = append(cases, `\\server\share\x`, `\\?\C:\x`)
	}
	for _, name := range cases {
		if got, err := SanitizeComponent(name); err == nil {
			t.Errorf("SanitizeComponent(%q) = %q, want error (absolute injection)", name, got)
		}
	}
}

func TestSanitizeComponentLegalAccepted(t *testing.T) {
	// (input, want) — want is the component that must join identically to
	// the pre-hardening spelling Join(root, input).
	cases := []struct{ in, want string }{
		{"task-1", "task-1"},
		{"aBc_123", "aBc_123"},
		{"task.v2", "task.v2"},
		{"some id with spaces", "some id with spaces"},
		{"run-1730000000-ab12", "run-1730000000-ab12"},
	}
	if runtime.GOOS != "windows" {
		// A plain name with a backslash is a legal POSIX filename and must
		// keep working on Unix; on Windows it is rejected (separator).
		cases = append(cases, struct{ in, want string }{`back\slash`, `back\slash`})
	}
	for _, tc := range cases {
		got, err := SanitizeComponent(tc.in)
		if err != nil {
			t.Errorf("SanitizeComponent(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SanitizeComponent(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Join equivalence: the sanitized component must land in the same
		// place under any root as the raw legal input did before hardening.
		root := t.TempDir()
		if a, b := filepath.Join(root, got), filepath.Join(root, tc.in); a != b {
			t.Errorf("Join changed place: %q vs %q", a, b)
		}
	}
}

func TestSanitizeComponentResultCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"task-1", "task.v2", "x y"} {
		got, err := SanitizeComponent(name)
		if err != nil {
			t.Fatalf("SanitizeComponent(%q): %v", name, err)
		}
		joined := filepath.Join(root, got)
		if !withinDir(root, joined) {
			t.Errorf("SanitizeComponent(%q): %q escapes root %q", name, joined, root)
		}
	}
}

func withinDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && filepath.IsLocal(rel)
}

func TestSanitizeFilePathTraversalAnchorPreserved(t *testing.T) {
	// Traversal must keep pointing where the raw input pointed — the
	// absolute anchor resolves ".." lexically exactly like the os would.
	cases := []string{"../evil.jsonl", `..\..\etc\passwd`, "a/../b.jsonl", "sub/../x.jsonl"}
	for _, in := range cases {
		got := SanitizeFilePath(in)
		if !filepath.IsAbs(got) {
			t.Errorf("SanitizeFilePath(%q) = %q, want absolute", in, got)
		}
		if want := absEquivalent(in); got != want {
			t.Errorf("SanitizeFilePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFilePathAbsolutePreserved(t *testing.T) {
	cases := []string{
		"C:\\work\\proj\\sessions\\chat.jsonl",
		"C:\\work\\proj\\..\\evil",
		`\\server\share\sessions\chat.jsonl`,
		`\\?\C:\big\drive\chat.jsonl`,
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, "/home/user/sessions/chat.jsonl", "/etc/../etc/passwd")
	}
	for _, in := range cases {
		got := SanitizeFilePath(in)
		if want := absEquivalent(in); got != want {
			t.Errorf("SanitizeFilePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFilePathLegalRelativeUnchanged(t *testing.T) {
	cases := []string{".reasonix/tasks", "sessions/chat.jsonl", "plain.jsonl"}
	for _, in := range cases {
		got := SanitizeFilePath(in)
		if want := absEquivalent(in); got != want {
			t.Errorf("SanitizeFilePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFilePathEmpty(t *testing.T) {
	if got := SanitizeFilePath(""); got != "" {
		t.Errorf("SanitizeFilePath(\"\") = %q, want empty", got)
	}
}

// absEquivalent is the meaning-preserving oracle: Clean + Abs, which is how
// the OS resolves the same input at process level.
func absEquivalent(p string) string {
	c := filepath.Clean(p)
	if abs, err := filepath.Abs(c); err == nil {
		c = abs
	}
	return c
}
