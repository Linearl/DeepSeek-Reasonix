package builtin

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/sandbox"
)

// TestRealPathDelegatesToSharedCanonicalizer pins the task 455fix
// convergence: builtin realPath and the sandbox canonicalizer must agree
// bit-for-bit, so boot-time and write-time path canonicalization share one
// timeout budget and one TTL cache. A second walk implementation here would
// re-introduce an unbounded EvalSymlinks hang on a dead network write root
// (task 455: ~21s per boot).
func TestRealPathDelegatesToSharedCanonicalizer(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		real,                                 // existing directory
		filepath.Join(real, "created-later"), // not-yet-existing tail
		filepath.Join(base, "no", "such"),    // deep nonexistent chain
	} {
		got, err := realPath(path)
		if err != nil {
			t.Fatalf("realPath(%q): %v", path, err)
		}
		want, err := sandbox.ResolveAbsPath(path)
		if err != nil {
			t.Fatalf("sandbox.ResolveAbsPath(%q): %v", path, err)
		}
		if got != want {
			t.Fatalf("realPath(%q) = %q, shared canonicalizer says %q — implementations diverged", path, got, want)
		}
	}
}
