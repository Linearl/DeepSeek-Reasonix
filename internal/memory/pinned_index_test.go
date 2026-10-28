package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Plan E (tl1193) test pins. Four contract classes, each asserted in BOTH
// switch states so the default-off path is pinned to byte-identical legacy
// behavior while the on path pins the migrated semantics:
//  1. standing index line rides session-context (on) / body rides (off)
//  2. body does NOT ride session-context (on) / body rides (off)
//  3. body is recallable on demand (on) — recall pool is the body's channel
//  4. legacy store migrates with zero data changes (files stay the truth)

// savePinned lands a pinned fact through the normal save path.
func savePinned(t *testing.T, store Store, name, description, body string) {
	t.Helper()
	_, err := store.Save(Memory{
		Name: name, Description: description, Type: TypeFeedback,
		Scope: FactScopeGlobal, Activation: ActivationPinned, Body: body,
	})
	if err != nil {
		t.Fatalf("save pinned %s: %v", name, err)
	}
}

// pinnedIndexTestStore builds a user+project store with one git-rooted project
// dir, mirroring the Load-test fixtures in memory_test.go.
func pinnedIndexTestStore(t *testing.T) (Store, string, string) {
	t.Helper()
	root := t.TempDir()
	user := filepath.Join(root, "user")
	proj := filepath.Join(root, "project")
	mustMkdir(t, filepath.Join(proj, ".git"))
	return StoreFor(user, proj), user, proj
}

// 1+2. Standing-tier form flips with the switch: on = one index line per
// pinned fact, description present, body absent; off = legacy full body.
func TestPinnedIndexModeStandingTierForm(t *testing.T) {
	store, user, proj := pinnedIndexTestStore(t)
	savePinned(t, store, "alpha-rule", "alpha standing description", "PINNED_ALPHA_BODY_UNIQUE")
	savePinned(t, store, "beta-rule", "beta standing description", "PINNED_BETA_BODY_UNIQUE")

	load := func() *Set {
		return Load(Options{CWD: proj, UserDir: user})
	}

	// Switch ON: index lines stand, bodies do not.
	t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "1")
	block := load().BackgroundDataBlock()
	for _, want := range []string{
		"### Pinned preferences and feedback",
		"](global/alpha-rule.md)",
		"[global/feedback pinned]",
		"alpha standing description",
		"beta standing description",
		"`memory` tool",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("index-mode block missing %q:\n%s", want, block)
		}
	}
	for _, body := range []string{"PINNED_ALPHA_BODY_UNIQUE", "PINNED_BETA_BODY_UNIQUE"} {
		if strings.Contains(block, body) {
			t.Fatalf("index-mode block must not carry body %q:\n%s", body, block)
		}
	}

	// Switch OFF (explicit override — t.Setenv registers on the parent and
	// subtests inherit its value): legacy full-body form, no index wording.
	t.Run("off keeps legacy body form", func(t *testing.T) {
		t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "")
		block := load().BackgroundDataBlock()
		for _, want := range []string{"PINNED_ALPHA_BODY_UNIQUE", "PINNED_BETA_BODY_UNIQUE"} {
			if !strings.Contains(block, want) {
				t.Fatalf("legacy block missing body %q:\n%s", want, block)
			}
		}
		if strings.Contains(block, "`memory` tool (reference in parentheses)") {
			t.Fatalf("legacy block must not carry the index-mode wording:\n%s", block)
		}
	})
}

// 2b. The per-line description is bounded to PinnedIndexDescriptionRunes at
// render time; a longer description is truncated, never lost (body retrievable).
func TestPinnedIndexLineTruncatesDescription(t *testing.T) {
	long := strings.Repeat("长", 300)
	store, _, _ := pinnedIndexTestStore(t)
	savePinned(t, store, "long-desc", long, "BODY")
	if got := renderPinnedIndexLine(store.List()[0]); strings.Count(got, "长") > PinnedIndexDescriptionRunes {
		t.Fatalf("index line carried %d runes of a %d-rune description: %q",
			strings.Count(got, "长"), len([]rune(long)), got)
	}
	if !strings.HasSuffix(renderPinnedIndexLine(store.List()[0]), "…") {
		t.Fatal("truncated line must end with an ellipsis")
	}
}

// 3. On-demand recall: with the mode on, the pinned body left the standing
// prefix and must come back through AutoRecall — the pool is its channel.
func TestPinnedIndexModeBodyRecallable(t *testing.T) {
	t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "1")
	store, user, proj := pinnedIndexTestStore(t)
	savePinned(t, store, "deploy-token", "deploy token guidance",
		"Rotate the ZEBRA-QUANTUM-9X credential before every release.")

	set := Load(Options{CWD: proj, UserDir: user})
	result := set.AutoRecall("ZEBRA-QUANTUM-9X", RecallOptions{})
	if len(result.Hits) == 0 {
		t.Fatalf("pinned body must be recallable on demand, got suppressed=%q hits=%+v", result.Suppressed, result.Hits)
	}
	found := false
	for _, hit := range result.Hits {
		if hit.Memory.Name == "deploy-token" && strings.Contains(hit.Memory.Body, "ZEBRA-QUANTUM-9X") {
			found = true
		}
	}
	if !found {
		t.Fatalf("recall must return the pinned fact with full body: %+v", result.Hits)
	}
}

// 4. Legacy-store migration is lazy and lossless: real 1192 fixture
// (user-work-profile rev1 — legacy virtual-pinned, no activation field) loads
// unchanged, renders its index line, keeps the full body readable, and stays
// recallable. No migration script touches the files.
func TestPinnedIndexModeLegacyStoreZeroMigration(t *testing.T) {
	t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "1")
	raw, err := os.ReadFile(filepath.Join(testdataDir, "user-work-profile-rev1.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	want, ok := loadMemory(filepath.Join(testdataDir, "user-work-profile-rev1.md"))
	if !ok {
		t.Fatal("fixture failed to parse")
	}
	store, user, proj := pinnedIndexTestStore(t)
	mustMkdir(t, store.GlobalDir)
	if err := os.WriteFile(filepath.Join(store.GlobalDir, "user-work-profile.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	set := Load(Options{CWD: proj, UserDir: user})
	if len(set.PinnedGuidance) != 1 {
		t.Fatalf("legacy pinned must surface as guidance, got %+v", set.PinnedGuidance)
	}
	got := set.PinnedGuidance[0]
	if got.ID != want.ID || got.Revision != want.Revision || got.Body != want.Body {
		t.Fatalf("lazy migration mutated data: id %q→%q rev %d→%d body %d→%d runes",
			want.ID, got.ID, want.Revision, got.Revision,
			len([]rune(want.Body)), len([]rune(got.Body)))
	}
	block := set.BackgroundDataBlock()
	// The tool-accepted reference rides the line regardless of title/name.
	if !strings.Contains(block, "](global/user-work-profile.md)") {
		t.Fatalf("standing index line missing for legacy fact:\n%s", block)
	}
	if strings.Contains(block, "声学/降噪硬件工程师") {
		t.Fatal("legacy body must not ride the standing prefix in index mode")
	}
	read, ok := store.Read("user-work-profile")
	if !ok || !strings.Contains(read.Body, "声学/降噪硬件工程师") {
		t.Fatalf("memory read must return the complete legacy body: ok=%v", ok)
	}
}

// 5. Write-time quota (on state): net growth past PinnedIndexMaxEntries is
// rejected with an actionable ledger; replacing an already-pinned fact always
// passes; off state keeps the 1192 body budget on the same fixture.
func TestPinnedIndexQuotaRejectsNetGrowthOnly(t *testing.T) {
	t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "1")
	store, _, _ := pinnedIndexTestStore(t)
	for i := 0; i < PinnedIndexMaxEntries; i++ {
		savePinned(t, store, fmt.Sprintf("quota-fact-%02d", i), "quota filler", "body")
	}

	_, err := store.Save(Memory{Name: "one-too-many", Description: "over quota", Type: TypeFeedback,
		Scope: FactScopeGlobal, Activation: ActivationPinned, Body: "body"})
	if err == nil {
		t.Fatal("net growth past the entry cap must be rejected")
	}
	for _, want := range []string{
		"over the 12-entry budget",
		"quota-fact-00",
		"/memory unpin",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection missing %q:\n%s", want, err)
		}
	}

	// Replacing a pinned fact never worsens the count: always passes.
	if _, err := store.Save(Memory{Name: "quota-fact-00", Description: "replaced", Type: TypeFeedback,
		Scope: FactScopeGlobal, Activation: ActivationPinned, Body: "new body"}); err != nil {
		t.Fatalf("replacement must pass: %v", err)
	}

	// Off state (explicit env override — subtests inherit the parent's
	// t.Setenv), same fixture: the 13th small pin is governed by the body
	// budget, not the count — legacy behavior intact.
	t.Run("off keeps body budget semantics", func(t *testing.T) {
		t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "")
		off, _, _ := pinnedIndexTestStore(t)
		for i := 0; i < PinnedIndexMaxEntries; i++ {
			savePinned(t, off, fmt.Sprintf("quota-fact-%02d", i), "quota filler", "body")
		}
		if _, err := off.Save(Memory{Name: "one-too-many", Description: "over quota", Type: TypeFeedback,
			Scope: FactScopeGlobal, Activation: ActivationPinned, Body: "body"}); err != nil {
			t.Fatalf("off state must keep budget-only gating: %v", err)
		}
	})
}

// Determinism: the same snapshot renders byte-identical index blocks (cache
// prefix stability, mirroring the legacy form's pin in memory_test.go).
func TestPinnedIndexBlockDeterministic(t *testing.T) {
	t.Setenv("REASONIX_EXPERIMENT_TL1193_PINNED_INDEX", "1")
	store, user, proj := pinnedIndexTestStore(t)
	savePinned(t, store, "alpha-rule", "alpha standing description", "A")
	savePinned(t, store, "beta-rule", "beta standing description", "B")
	set := Load(Options{CWD: proj, UserDir: user})
	if again := set.BackgroundDataBlock(); again != set.BackgroundDataBlock() {
		t.Fatal("index block must render deterministically for one snapshot")
	}
	// Most-recently-updated first, matching the legacy form's ordering pin:
	// beta saved second is newer and must render before alpha.
	b := pinnedIndexBlock(set.PinnedGuidance)
	if strings.Index(b, "beta-rule") > strings.Index(b, "alpha-rule") {
		t.Fatalf("pinned index must keep UpdatedAt-descending order (beta newer):\n%s", b)
	}
}

func fmtSprintf(format string, n int) string {
	return strings.ReplaceAll(format, "%02d", pad2(n))
}

func pad2(n int) string {
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}
