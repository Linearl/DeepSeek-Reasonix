package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/memory"
)

// tl 1192: the pinned budget must be visible in /memory before it blocks a
// write — a summary line, per-fact char counts, and an explicit legacy marker
// for facts that only pin through the pre-field default. Fixture bodies use
// the real user-work-profile sizes (675 runes current, 1500-char budget).
func TestMemorySummaryShowsPinnedUsageAndLegacyMarker(t *testing.T) {
	root := t.TempDir()
	store := memory.Store{
		Dir:       filepath.Join(root, "project"),
		GlobalDir: filepath.Join(root, "global"),
	}
	if _, err := store.SaveWithOptions(memory.Memory{
		Name: "user-work-profile", Title: "User work profile",
		Description: "legacy global preference", Type: memory.TypeUser,
		Scope: memory.FactScopeGlobal, Body: strings.Repeat("实", 675),
	}, memory.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveWithOptions(memory.Memory{
		Name: "relevant-note", Title: "Retrieval only",
		Description: "not pinned", Body: "stays out of the prefix",
	}, memory.SaveOptions{}); err != nil {
		t.Fatal(err)
	}

	c := New(Options{Memory: &memory.Set{Store: store}})
	out := MemoryCommandText(c, "")
	for _, want := range []string{
		"pinned: 1 facts, 675 / 1500 chars (45%)",
		"activation=pinned(legacy) chars=675",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("/memory output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "relevant-note") && strings.Contains(out, "chars=") &&
		!strings.Contains(out, "activation=pinned") {
		t.Fatalf("relevant facts must not carry char counts:\n%s", out)
	}

	// Materialization records the explicit choice: same usage, legacy marker
	// cleared, char count kept.
	n, err := store.MaterializeLegacyPinned()
	if err != nil || n != 1 {
		t.Fatalf("materialize = (%d, %v), want (1, nil)", n, err)
	}
	out = MemoryCommandText(c, "")
	if !strings.Contains(out, "activation=pinned chars=675") {
		t.Fatalf("/memory output missing explicit pinned chars:\n%s", out)
	}
	if strings.Contains(out, "(legacy)") {
		t.Fatalf("legacy marker must clear after materialization:\n%s", out)
	}
}

// The materialized file must be re-readable with the explicit field persisted.
func TestMaterializedFactPersistsActivation(t *testing.T) {
	root := t.TempDir()
	store := memory.Store{GlobalDir: filepath.Join(root, "global")}
	if _, err := store.SaveWithOptions(memory.Memory{
		Name: "legacy-pref", Description: "legacy", Type: memory.TypeUser,
		Scope: memory.FactScopeGlobal, Body: "old habit",
	}, memory.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if n, err := store.MaterializeLegacyPinned(); err != nil || n != 1 {
		t.Fatalf("materialize = (%d, %v), want (1, nil)", n, err)
	}
	raw, err := os.ReadFile(filepath.Join(store.GlobalDir, "legacy-pref.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "activation: pinned") {
		t.Fatalf("materialized file must persist activation: pinned:\n%s", raw)
	}
}
