package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Task 196 follow-up: the events alert must measure live growth, and a recycle
// directory is storage the user chose to keep. A .trash directory must be
// skipped at any depth, and everything outside it must keep counting exactly as
// before (the totals are the other half of the same sample line).
func TestWalkProjectsBytesSkipsTrashAtAnyDepth(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, size int) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("projects/sessions/live.events.jsonl", 100)
	write("projects/sessions/live.jsonl", 50)
	write("projects/sessions/.trash/dead.events.jsonl", 300)
	write("projects/sessions/.trash/nested/deeper.events.jsonl", 500)
	write("projects/other/.trash/also-dead.events.jsonl", 700)

	totalMB, eventsMB := walkProjectsBytesMB(root)

	bytesOf := func(mb float64) int64 {
		return int64(math.Round(mb * (1 << 20)))
	}
	if got := bytesOf(eventsMB); got != 100 {
		t.Fatalf("eventsMB = %.10f (%d bytes), want exactly the 100 live bytes - .trash leaked in", eventsMB, got)
	}
	if got := bytesOf(totalMB); got != 150 {
		t.Fatalf("totalMB = %.10f (%d bytes), want exactly the 150 live bytes - .trash leaked in", totalMB, got)
	}
}

// An empty root is the degenerate case the early return exists for; the .trash
// rule must not change it.
func TestWalkProjectsBytesEmptyRoot(t *testing.T) {
	totalMB, eventsMB := walkProjectsBytesMB("")
	if totalMB != 0 || eventsMB != 0 {
		t.Fatalf("empty root = (%v, %v), want (0, 0)", totalMB, eventsMB)
	}
}
