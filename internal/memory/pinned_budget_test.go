package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures under testdata/pinned-budget are byte-for-byte copies of real
// user-store samples (tl 1192, 2026-10-27): user-work-profile rev1 (the August
// legacy version, 2378B) and its current 675-rune body, plus the three-revision
// chain of open-source-option-research-method (2340/2634/2715B). Tests copy
// them into t.TempDir() stores and never touch the real store.

const testdataDir = "testdata/pinned-budget"

func loadSample(t *testing.T, name string) (Memory, []byte) {
	t.Helper()
	path := filepath.Join(testdataDir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample %s: %v", name, err)
	}
	m, ok := loadMemory(path)
	if !ok {
		t.Fatalf("sample %s failed to parse", name)
	}
	return m, raw
}

func writeSampleRaw(t *testing.T, dir, sampleName, fileName string) {
	t.Helper()
	_, raw := loadSample(t, sampleName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRawFact lands a fact file directly on disk, bypassing the save path —
// exactly how legacy guidance actually accumulated past the budget while the
// write-time-only check watched. Used to build over-budget fixtures.
func writeRawFact(t *testing.T, dir string, m Memory) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, m.Name+".md"), []byte(render(m, m.Name)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func implicitPinnedCount(t *testing.T, store Store) int {
	t.Helper()
	n := 0
	for _, fact := range store.ListAll() {
		if NormalizeActivation(string(fact.Activation)) == "" && ResolveActivation(fact) == ActivationPinned {
			n++
		}
	}
	return n
}

// The budget cap is part of the defect-fix contract: the ratchet repair must
// not be achieved by moving the limit.
func TestPinnedGuidanceBudgetCharsUnchanged(t *testing.T) {
	if PinnedGuidanceBudgetChars != 1500 {
		t.Fatalf("PinnedGuidanceBudgetChars = %d, want 1500 (fix must not move the cap)", PinnedGuidanceBudgetChars)
	}
}

// Acceptance 1 (real rev1 sample): on a store already over budget, only a write
// that GROWS the pinned total is rejected, and the rejection is actionable —
// total/limit/deficit, one ledger row per currently pinned fact, and a
// copyable /memory unpin line naming a real entry.
func TestPinnedBudgetRejectsGrowthWithActionableLedger(t *testing.T) {
	store := recallTestStore(t)
	// Build under budget through the save path, then drop the real legacy
	// sample in raw so the store crosses the cap the way legacy data did.
	if _, err := store.SaveWithOptions(Memory{
		Name: "explicit-pin", Description: "explicitly pinned",
		Activation: ActivationPinned, Body: strings.Repeat("x", 100),
	}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveWithOptions(Memory{
		Name: "extra-feedback", Description: "legacy feedback",
		Type: TypeFeedback, Scope: FactScopeGlobal, Body: strings.Repeat("回", 700),
	}, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	writeSampleRaw(t, store.GlobalDir, "user-work-profile-rev1.md", "user-work-profile.md")
	facts, chars := store.PinnedGuidanceUsage()
	if facts != 3 || chars != 967+700+100 {
		t.Fatalf("usage = (%d, %d), want (3, 1767)", facts, chars)
	}

	_, err := store.SaveWithOptions(Memory{
		Name: "new-pref", Description: "another pinned fact",
		Activation: ActivationPinned, Body: strings.Repeat("y", 200),
	}, SaveOptions{})
	if err == nil {
		t.Fatal("a total-growing pin on an over-budget store must be rejected")
	}
	msg := err.Error()
	for _, want := range []string{
		"total=1967", "limit=1500", "deficit=467",
		"(deficit 467)",
		"REASONIX.md",
		"/memory unpin user-work-profile", // largest real entry, copyable
		"| yes", // legacy rows are marked
		"| no",  // explicit rows too
		"2026-08-07", // real sample updated_at leaks into the ledger
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("rejection missing %q:\n%s", want, msg)
		}
	}
	rows := 0
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, "  ") && strings.Contains(line, " | ") {
			rows++
		}
	}
	if rows != 3 {
		t.Fatalf("ledger rows = %d, want one per currently pinned fact (3):\n%s", rows, msg)
	}
}

// Acceptance 2 (real 675-rune body): on an over-budget store, shrinking or
// replacing a pinned body always passes, and the fact stays pinned. The
// 2026-10-27 incident — a user-work-profile update rejected, "fixed" by
// downgrading it to relevant — must stay impossible.
func TestPinnedBudgetAllowsReplacementShrink675To586(t *testing.T) {
	store := recallTestStore(t)
	sample, _ := loadSample(t, "user-work-profile-current.md")
	if got := BodyChars(sample); got != 675 {
		t.Fatalf("sample body = %d runes, want the real 675", got)
	}
	saved, err := store.SaveWithOptions(Memory{
		Name: "user-work-profile", Description: sample.Description,
		Title: sample.Title, Type: TypeUser, Scope: FactScopeGlobal, Body: sample.Body,
	}, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ResolveActivation(saved.Memory) != ActivationPinned {
		t.Fatal("legacy global user fact must load virtually pinned")
	}
	// Companion lands raw: 675+1100 crosses the cap, which a validated save
	// would refuse — the exact accumulation path the defect left unwatched.
	writeRawFact(t, store.GlobalDir, Memory{
		Name: "companion", Description: "legacy feedback",
		Type: TypeFeedback, Scope: FactScopeGlobal, Body: strings.Repeat("回", 1100),
	})
	facts, chars := store.PinnedGuidanceUsage()
	if facts != 2 || chars != 675+1100 {
		t.Fatalf("usage = (%d, %d), want (2, 1775) — over budget", facts, chars)
	}

	// Growing the body on the over-budget store stays rejected (the incident).
	// Updates must carry Type (the save contract does not inherit it); a
	// caller omitting it re-classifies the fact and drops it from guidance.
	if _, err := store.SaveWithOptions(Memory{
		ID: saved.Memory.ID, Type: TypeUser, Body: sample.Body + strings.Repeat("增", 100),
	}, SaveOptions{}); err == nil {
		t.Fatal("a total-growing update must stay rejected")
	}

	// The curation write — 675 -> 586 runes — passes and stays pinned even
	// though 586+1100 is still over the cap: the total shrinks, so the
	// replacement rule lets curation proceed.
	trimmed := strings.Repeat("实", 586)
	result, err := store.SaveWithOptions(Memory{
		ID: saved.Memory.ID, Type: TypeUser, Body: trimmed,
	}, SaveOptions{})
	if err != nil {
		t.Fatalf("replacement shrink must pass on an over-budget store: %v", err)
	}
	if ResolveActivation(result.Memory) != ActivationPinned {
		t.Fatalf("replacement must keep the fact pinned, got %+v", result.Memory)
	}
	if got := BodyChars(result.Memory); got != 586 {
		t.Fatalf("body = %d runes, want 586", got)
	}
	facts, chars = store.PinnedGuidanceUsage()
	if facts != 2 || chars != 586+1100 {
		t.Fatalf("usage after shrink = (%d, %d), want (2, 1686)", facts, chars)
	}
	// The pre-shrink body is recoverable through the normal revision chain.
	revisions := store.Revisions(saved.Memory.ID)
	if len(revisions) != 1 || BodyChars(revisions[0]) != 675 {
		t.Fatalf("revisions = %+v, want the 675-rune pre-shrink state", revisions)
	}
}

// Acceptance 4 (real rev1 sample): materialization records explicit
// activation: pinned without changing what rides session-context, keeps a
// rollback revision, and restores cleanly — on a store that is over budget,
// because a body-for-body rewrite never grows the total.
func TestMaterializeLegacyPinnedKeepsInjectionAndRollback(t *testing.T) {
	store := recallTestStore(t)
	writeSampleRaw(t, store.GlobalDir, "user-work-profile-rev1.md", "user-work-profile.md")
	writeRawFact(t, store.GlobalDir, Memory{
		Name: "extra-feedback", Description: "legacy feedback",
		Type: TypeFeedback, Scope: FactScopeGlobal, Body: strings.Repeat("回", 700),
	})
	facts, chars := store.PinnedGuidanceUsage()
	if facts != 2 || chars != 967+700 {
		t.Fatalf("usage = (%d, %d), want (2, 1667)", facts, chars)
	}
	if implicit := implicitPinnedCount(t, store); implicit != 2 {
		t.Fatalf("implicit-pinned = %d, want 2", implicit)
	}

	n, err := store.MaterializeLegacyPinned()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("materialized = %d, want 2", n)
	}
	if implicit := implicitPinnedCount(t, store); implicit != 0 {
		t.Fatalf("implicit-pinned after materialization = %d, want 0", implicit)
	}
	afterFacts, afterChars := store.PinnedGuidanceUsage()
	if afterFacts != facts || afterChars != chars {
		t.Fatalf("usage changed: (%d, %d) -> (%d, %d); injection must be unchanged",
			facts, chars, afterFacts, afterChars)
	}
	active, ok := store.Read("user-work-profile")
	if !ok || NormalizeActivation(string(active.Activation)) != ActivationPinned {
		t.Fatalf("materialized fact must carry explicit pinned, got %+v", active)
	}
	// Each rewrite left the pre-materialization state in .revisions.
	revPath := filepath.Join(store.GlobalDir, ".revisions", active.ID, "000000001.md")
	snap, err := os.ReadFile(revPath)
	if err != nil {
		t.Fatalf("rollback revision missing: %v", err)
	}
	_, sampleRaw := loadSample(t, "user-work-profile-rev1.md")
	if !bytes.Equal(snap, sampleRaw) {
		t.Fatal("rollback revision must be the untouched pre-materialization file")
	}

	// Restore rolls back through the same over-budget store (replacement rule).
	restored, err := store.Restore("user-work-profile", 1)
	if err != nil {
		t.Fatalf("restore after materialization must pass: %v", err)
	}
	if BodyChars(restored.Memory) != 967 {
		t.Fatalf("restored body = %d runes, want 967", BodyChars(restored.Memory))
	}
	if ResolveActivation(restored.Memory) != ActivationPinned {
		t.Fatalf("restored fact must stay pinned, got %+v", restored.Memory)
	}

	// Idempotent: nothing implicit left to materialize.
	if n, err := store.MaterializeLegacyPinned(); err != nil || n != 0 {
		t.Fatalf("second run = (%d, %v), want (0, nil)", n, err)
	}
}

// The rollback path materialization relies on: a real multi-revision chain
// (open-source-option-research-method, 3 historical versions) stays intact —
// updates snapshot each prior state by exact bytes and restore picks any link.
func TestRevisionChainFromRealSampleStaysRestorable(t *testing.T) {
	store := recallTestStore(t)
	writeSampleRaw(t, store.GlobalDir, "revision-chain-r1.md", "open-source-option-research-method.md")
	r1, r1raw := loadSample(t, "revision-chain-r1.md")
	if len(r1raw) != 2340 {
		t.Fatalf("r1 sample = %dB, want the real 2340", len(r1raw))
	}
	r2, r2raw := loadSample(t, "revision-chain-r2.md")
	r3, r3raw := loadSample(t, "revision-chain-r3.md")
	if len(r2raw) != 2634 || len(r3raw) != 2715 {
		t.Fatalf("chain samples = %dB/%dB, want the real 2634/2715", len(r2raw), len(r3raw))
	}

	for _, update := range []Memory{r2, r3} {
		if _, err := store.SaveWithOptions(Memory{
			ID:   r1.ID,
			Body: update.Body,
		}, SaveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	active, ok := store.Read(r1.ID)
	if !ok || active.Revision != 3 || BodyChars(active) != BodyChars(r3) {
		t.Fatalf("active = %+v, want revision 3 with the r3 body", active)
	}
	rev1Path := filepath.Join(store.GlobalDir, ".revisions", r1.ID, "000000001.md")
	snap, err := os.ReadFile(rev1Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap, r1raw) {
		t.Fatal("revision 1 snapshot must be the byte-exact real sample")
	}
	if revisions := store.Revisions(r1.ID); len(revisions) != 2 {
		t.Fatalf("history = %d entries, want 2", len(revisions))
	}
	restored, err := store.Restore(r1.Name, 2)
	if err != nil {
		t.Fatal(err)
	}
	if BodyChars(restored.Memory) != BodyChars(r2) {
		t.Fatal("restore revision 2 must return the r2 body")
	}
}
