package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// tl1193 resident-index pins: the built recall index is shared across
// snapshots behind the store's stat fingerprint. Two directions are pinned —
// unchanged pool REUSES the resident index (no rebuild), any pool file change
// INVALIDATES it (rebuild picks the new content up).

func recallTestSet(t *testing.T, user, proj string) *Set {
	t.Helper()
	return Load(Options{CWD: proj, UserDir: user})
}

// Same pool, two snapshots: the second snapshot's first recall must reuse the
// resident index instead of rebuilding it. Probed with a relevant fact so the
// pin covers the default (plan E off) state, where pinned facts are excluded
// from the pool by design.
func TestRecallCacheReusesResidentIndexAcrossSnapshots(t *testing.T) {
	store, user, proj := pinnedIndexTestStore(t)
	if _, err := store.Save(Memory{Name: "cache-fact", Description: "cache probe description",
		Type: TypeFeedback, Scope: FactScopeGlobal, Activation: ActivationRelevant,
		Body: "Resident index probe OTTER-CANYON-77."}); err != nil {
		t.Fatal(err)
	}
	set1 := recallTestSet(t, user, proj)
	if r := set1.AutoRecall("OTTER-CANYON-77", RecallOptions{}); len(r.Hits) == 0 {
		t.Fatalf("first recall must hit: %+v", r)
	}
	first := LastRecallIndexBuildStats()
	if first == nil {
		t.Fatal("first recall must have built an index")
	}

	// A fresh snapshot of the unchanged pool: same fingerprint, same index.
	set2 := recallTestSet(t, user, proj)
	if r := set2.AutoRecall("OTTER-CANYON-77", RecallOptions{}); len(r.Hits) == 0 {
		t.Fatalf("resident recall must still hit: %+v", r)
	}
	if again := LastRecallIndexBuildStats(); again != first {
		t.Fatalf("unchanged pool must reuse the resident index, but a rebuild happened: %v vs %v", first, again)
	}
}

// A write that changes a fact file changes the fingerprint: the next recall
// rebuilds and sees the new content.
func TestRecallCacheInvalidatesOnPoolChange(t *testing.T) {
	store, user, proj := pinnedIndexTestStore(t)
	if _, err := store.Save(Memory{Name: "cache-fact", Description: "cache probe description",
		Type: TypeFeedback, Scope: FactScopeGlobal, Activation: ActivationRelevant,
		Body: "Resident index probe OTTER-CANYON-77."}); err != nil {
		t.Fatal(err)
	}
	set1 := recallTestSet(t, user, proj)
	if r := set1.AutoRecall("OTTER-CANYON-77", RecallOptions{}); len(r.Hits) == 0 {
		t.Fatalf("first recall must hit: %+v", r)
	}
	first := LastRecallIndexBuildStats()

	// Same file name, different content — the fingerprint's mtime+size leg
	// must catch it.
	if _, err := store.Save(Memory{Name: "cache-fact", Description: "cache probe description",
		Type: TypeFeedback, Scope: FactScopeGlobal, Activation: ActivationRelevant,
		Body: "Resident index probe OTTER-CANYON-77. Added HORIZON-LARCH-42 post-build."}); err != nil {
		t.Fatal(err)
	}
	set2 := recallTestSet(t, user, proj)
	if r := set2.AutoRecall("HORIZON-LARCH-42", RecallOptions{}); len(r.Hits) == 0 {
		t.Fatalf("changed pool must be rebuilt and recallable: %+v", r.Suppressed)
	}
	if again := LastRecallIndexBuildStats(); again == first {
		t.Fatal("changed pool must trigger a rebuild, but the resident index was served")
	}
}

// The fingerprint is stat-only and content-sensitive: rewriting the same file
// with different bytes flips it; rewriting identical bytes keeps it.
func TestStoreFingerprintTracksContentChanges(t *testing.T) {
	store, _, _ := pinnedIndexTestStore(t)
	if store.fingerprint() == "" {
		t.Fatal("a real store must have a fingerprint")
	}
	savePinned(t, store, "fp-fact", "fingerprint probe", "VERSION-ONE")
	fp1 := store.fingerprint()

	// Different content, same name: fingerprint must move.
	if _, err := store.Save(Memory{Name: "fp-fact", Description: "fingerprint probe",
		Type: TypeFeedback, Scope: FactScopeGlobal, Activation: ActivationPinned,
		Body: "VERSION-TWO"}); err != nil {
		t.Fatal(err)
	}
	fp2 := store.fingerprint()
	if fp2 == fp1 {
		t.Fatal("content change must change the fingerprint")
	}

	// Unrelated file added: fingerprint must move too.
	savePinned(t, store, "fp-other", "second probe", "OTHER")
	if store.fingerprint() == fp2 {
		t.Fatal("pool membership change must change the fingerprint")
	}

	// A non-.md sidecar (e.g. .bak) must NOT move the fingerprint: every
	// reader filters on ".md".
	if err := os.WriteFile(filepath.Join(store.GlobalDir, "stray.md.bak"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := store.fingerprint()
	if err := os.WriteFile(filepath.Join(store.GlobalDir, "another.md.bak"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if store.fingerprint() != before {
		t.Fatal("non-.md files must not affect the fingerprint")
	}
}
