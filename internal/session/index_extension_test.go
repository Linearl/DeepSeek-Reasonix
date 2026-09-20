package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func appendDiagnosticCommits(t *testing.T, session *Session, from, count int) {
	t.Helper()
	for i := from; i < from+count; i++ {
		payload, _ := json.Marshal(map[string]int{"number": i})
		if _, err := session.Append(t.Context(), Batch{
			OperationID: "diagnostic-" + strconv.Itoa(i),
			Events:      []Event{{Kind: "diagnostic/test", Optional: true, Payload: payload}},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func readSparseIndexFile(t *testing.T, dir string) sparseIndex {
	t.Helper()
	data, err := os.ReadFile(sparseIndexPath(dir))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index sparseIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	return index
}

// staleIndexFromLog builds a session of the given size, indexes it, and returns
// that index. Writing it back later reproduces what a second process sees when
// the log grew behind its back: an index that describes a prefix of the log.
func staleIndexFromLog(t *testing.T, dir string, commits int) sparseIndex {
	t.Helper()
	store, err := Open(dir, filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	appendDiagnosticCommits(t, store, 0, commits)
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommitPage(context.Background(), dir, 0, 1); err != nil {
		t.Fatal(err)
	}
	return readSparseIndexFile(t, dir)
}

func counterSnapshot() (rebuilds, extensions, hits uint64) {
	return sparseIndexRebuilds.Load(), sparseIndexExtensions.Load(), sparseIndexMemoryHits.Load()
}

// TestSparseIndexExtendsOnAppendedTail is the read-amplification regression: on an
// append-only log a stale index must be extended by the appended tail, not rebuilt
// from the start. Before this change every page of a paged read rebuilt the whole
// index — the multiplier behind a 116 MB log read as 3.7 GB.
func TestSparseIndexExtendsOnAppendedTail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "extend")
	stale := staleIndexFromLog(t, dir, 600)

	rebuildsBefore, extensionsBefore, hitsBefore := counterSnapshot()

	grower, err := Open(dir, "extend")
	if err != nil {
		t.Fatal(err)
	}
	appendDiagnosticCommits(t, grower, 600, 200)
	if err := grower.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeSparseIndex(dir, stale)

	if _, err := readCommitPage(context.Background(), dir, 0, 1); err != nil {
		t.Fatal(err)
	}
	rebuilds, extensions, hits := counterSnapshot()
	if extensions == extensionsBefore {
		t.Fatalf("expected the tail to be scanned incrementally (extensions %d -> %d, hits %d -> %d, rebuilds %d -> %d)",
			extensionsBefore, extensions, hitsBefore, hits, rebuildsBefore, rebuilds)
	}
	if rebuilds != rebuildsBefore {
		t.Fatalf("an appended tail must not trigger a rebuild (rebuilds %d -> %d)", rebuildsBefore, rebuilds)
	}

	extended := readSparseIndexFile(t, dir)
	if extended.LastSequence != 800 || extended.CommitCount != 800 {
		t.Fatalf("extended index = lastSequence %d commits %d, want 800/800", extended.LastSequence, extended.CommitCount)
	}
	if len(extended.Entries) < 4 || extended.Entries[0].FirstSequence != 1 || extended.Entries[0].Offset != 0 {
		t.Fatalf("extended entries look wrong: %+v", extended.Entries)
	}
	for i := 1; i < len(extended.Entries); i++ {
		if extended.Entries[i].FirstSequence <= extended.Entries[i-1].FirstSequence || extended.Entries[i].Offset <= extended.Entries[i-1].Offset {
			t.Fatalf("entries not monotonic at %d: %+v", i, extended.Entries)
		}
	}
	if extended.LogSize == stale.LogSize {
		t.Fatal("the extended index still claims the old log size")
	}
	tail, err := readCommitPage(context.Background(), dir, 799, 5)
	if err != nil || len(tail.Commits) != 1 || tail.Commits[0].LastSequence() != 800 {
		t.Fatalf("tail page = %+v err=%v", tail, err)
	}
}

// TestSparseIndexRepeatedReadsUseMemoryCache covers the other half of the
// amplification: one snapshot reads page after page, and each page used to reopen
// the log, re-stat it and decode the index from disk again.
func TestSparseIndexRepeatedReadsUseMemoryCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cached")
	_ = staleIndexFromLog(t, dir, 300)

	_, _, hitsBefore := counterSnapshot()
	for i := 0; i < 12; i++ {
		if _, err := readCommitPage(context.Background(), dir, uint64(i*25), 25); err != nil {
			t.Fatal(err)
		}
	}
	_, _, hits := counterSnapshot()
	if hits-hitsBefore < 11 {
		t.Fatalf("expected the repeated reads to be served from memory (hits %d -> %d)", hitsBefore, hits)
	}
}

// TestSparseIndexExtensionSeesCommitsAppendedAfterwards pins the append race: an
// extension must describe a consistent index, and a later read must pick up what
// was appended after it.
func TestSparseIndexExtensionSeesCommitsAppendedAfterwards(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "race")
	stale := staleIndexFromLog(t, dir, 400)

	grower, err := Open(dir, "race")
	if err != nil {
		t.Fatal(err)
	}
	appendDiagnosticCommits(t, grower, 400, 40)
	if err := grower.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeSparseIndex(dir, stale)

	after, err := readCommitPage(context.Background(), dir, 400, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Commits) != 40 {
		t.Fatalf("appended commits are missing: got %d, want 40", len(after.Commits))
	}
	if after.Commits[len(after.Commits)-1].LastSequence() != 440 {
		t.Fatalf("last appended sequence = %d, want 440", after.Commits[len(after.Commits)-1].LastSequence())
	}
	page, err := readCommitPage(context.Background(), dir, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) != 440 {
		t.Fatalf("full read returned %d commits, want 440", len(page.Commits))
	}
}

// TestSparseIndexRejectsUnrelatedLog keeps the safety property: a file whose head
// changed is not an extension but a different log, so it must never be reused.
func TestSparseIndexRejectsUnrelatedLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "replaced")
	stale := staleIndexFromLog(t, dir, 100)
	rebuildsBefore := sparseIndexRebuilds.Load()

	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := logPathForManifest(dir, manifest)
	if err := os.WriteFile(logPath, []byte("completely different content, longer than the previous head bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSparseIndex(dir, stale)

	// Replacing the log with unrelated bytes may simply be unreadable (an error is
	// the honest answer and means nothing was trusted). If it did read, it must
	// have rebuilt rather than reused the stale index.
	if _, err := readCommitPage(context.Background(), dir, 0, 10); err == nil {
		if sparseIndexRebuilds.Load() == rebuildsBefore {
			t.Fatal("a different head was served without a rebuild")
		}
		rebuilt := readSparseIndexFile(t, dir)
		if rebuilt.LastSequence == stale.LastSequence && rebuilt.LogSize == stale.LogSize && rebuilt.LogIdentity == stale.LogIdentity {
			t.Fatal("stale index was reused for a log with a different head")
		}
	}
}
