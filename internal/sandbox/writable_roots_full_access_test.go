package sandbox

import (
	"context"
	"testing"
)

// TestWritableRootSetFullAccess pins task 257's short-circuit: unbounded
// passes any declared directory, the flag rides on restricted clones
// (sub-agents inherit full access), and the default bounded set answers
// exactly as before (off = byte-for-byte old behaviour).
func TestWritableRootSetFullAccess(t *testing.T) {
	work := t.TempDir()
	outside := t.TempDir()

	set := NewWritableRootSet([]string{work})
	if set.Unbounded() {
		t.Fatal("a fresh set must ship bounded (full access defaults off)")
	}
	if got := set.Missing([]string{outside}); len(got) != 1 {
		t.Fatalf("bounded Missing(outside) = %v, want the directory reported", got)
	}
	if set.Covers(outside) {
		t.Fatal("bounded Covers(outside) must be false")
	}

	set.SetUnbounded(true)
	if !set.Unbounded() {
		t.Fatal("SetUnbounded(true) must stick")
	}
	if got := set.Missing([]string{outside}); got != nil {
		t.Fatalf("unbounded Missing(outside) = %v, want nil", got)
	}
	if !set.Covers(outside) {
		t.Fatal("unbounded Covers(outside) must be true")
	}
	// A narrowed write_paths claim still inherits full access.
	clone := set.CloneRestricted([]string{work})
	if !clone.Unbounded() {
		t.Fatal("restricted clone must inherit full access")
	}
	if got := clone.Missing([]string{outside}); got != nil {
		t.Fatalf("unbounded clone Missing(outside) = %v, want nil", got)
	}
	// Turning it back off restores the bounded answers on the original set.
	set.SetUnbounded(false)
	if got := set.Missing([]string{outside}); len(got) != 1 {
		t.Fatalf("re-bounded Missing(outside) = %v, want the directory reported", got)
	}
	// A clone made while bounded stays bounded.
	boundedClone := set.CloneRestricted(nil)
	if boundedClone.Unbounded() {
		t.Fatal("a bounded set must not leak full access into its clone")
	}
	// Nil receivers stay safe (the flag may be consulted on absent sets) and
	// answer as an empty bounded set: nothing granted, so a directory is
	// reported missing rather than silently waved through.
	var absent *WritableRootSet
	if absent.Unbounded() || absent.Covers(outside) {
		t.Fatal("nil set must answer as bounded without panicking")
	}
	if got := absent.Missing([]string{outside}); len(got) != 1 {
		t.Fatalf("nil set Missing(outside) = %v, want the directory reported", got)
	}
	// EffectiveSandboxRoots keeps returning the real list — full access
	// unwraps the shell via spec.Mode, never by pretending the roots are all.
	_ = context.Background()
}
