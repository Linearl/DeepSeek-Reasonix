// tl1733 Block1 acceptance tests: the injected background index renders slim
// (A1-A3), the zero-recall cutoff honors its maturity gate and cold-start
// exemptions (B1-B3), the 20K-rune budget admits most-recently-recalled first
// (C1), the adaptive notice appears only on heavy trims (F1), and the
// measurement (G1) prices the section with the host's cold token calibration.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// injectionTestNow anchors every cutoff decision in the tests so aging windows
// never flap as the clock moves.
var injectionTestNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// writeMatureLedger drops a usage-counts.json whose StartedAt sits days days
// in the past — the data-maturity gate's "accumulated long enough" state.
// Writing the file (instead of recording live events) keeps StartedAt exact.
func writeMatureLedger(t *testing.T, s Store, days int) {
	t.Helper()
	path := UsageLedgerPath(s)
	if path == "" {
		t.Fatal("no usage ledger path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := usageFile{
		Version:   1,
		StartedAt: injectionTestNow.Add(-time.Duration(days) * 24 * time.Hour),
		Memories:  map[string]*usageEntry{},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// recallLedger stamps one recall for id at now-offset inside a fresh ledger
// file, replacing any existing one (tests write the exact state they need).
func recallLedger(t *testing.T, s Store, id string, at time.Time) {
	t.Helper()
	path := UsageLedgerPath(s)
	data := usageFile{
		Version:   1,
		StartedAt: injectionTestNow.Add(-15 * 24 * time.Hour),
		Memories: map[string]*usageEntry{
			id: {RecallTotal: 1, LastRecallAt: at},
		},
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// The process-resident handle caches the ledger on first read; a fresh
	// registry entry for this path makes the next render re-read the file.
	if u := usageFor(s); u != nil {
		u.mu.Lock()
		u.loaded = false
		u.mu.Unlock()
	}
}

// A1: injected lines carry title + scope/type marker + description only — the
// file reference leaves the injected form (it is re-derivable via the memory
// tool; the task book verified every consumer self-carries the reference).
func TestInjectionIndexLineDropsFileReference(t *testing.T) {
	store := recallTestStore(t)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-proj", Name: "deploy-region", Title: "Deploy region",
		Description: "This project deploys to eu-central-1", Type: TypeProject,
		Scope: FactScopeProject, Body: "eu-central-1",
	})
	all := store.ListAll()
	out := store.RenderInjectionIndex(all, injectionTestNow)

	if !strings.Contains(out, "- Deploy region — [project/project] This project deploys to eu-central-1") {
		t.Fatalf("injected line not in slim form:\n%s", out)
	}
	if strings.Contains(out, ".md)") || strings.Contains(out, "](") {
		t.Fatalf("injected index must not carry file references:\n%s", out)
	}
}

// A2: the slimming pipeline must not touch the source of truth — IndexFrom
// keeps the qualified-link form (tool layers, disk MEMORY.md, legacy tests all
// read that shape).
func TestSlimLeavesIndexFromUntouched(t *testing.T) {
	store := recallTestStore(t)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-proj", Name: "deploy-region", Title: "Deploy region",
		Description: "This project deploys to eu-central-1", Type: TypeProject,
		Scope: FactScopeProject, Body: "eu-central-1",
	})
	all := store.ListAll()
	full := store.IndexFrom(all)
	if !strings.Contains(full, "](project/deploy-region.md)") {
		t.Fatalf("IndexFrom must keep the qualified link (untouched source of truth):\n%s", full)
	}
	if strings.Contains(full, "…") && !strings.Contains(full, "eu-central-1") {
		t.Fatalf("IndexFrom must not truncate descriptions:\n%s", full)
	}
}

// A3: descriptions over 150 runes truncate at render with an ellipsis; shorter
// ones pass through untouched.
func TestInjectionDescriptionTruncated(t *testing.T) {
	store := recallTestStore(t)
	long := strings.Repeat("长", 200)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-long", Name: "long-desc", Title: "Long desc",
		Description: long, Type: TypeProject, Scope: FactScopeProject, Body: "b",
	})
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-short", Name: "short-desc", Title: "Short desc",
		Description: "short one", Type: TypeProject, Scope: FactScopeProject, Body: "b",
	})
	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)

	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "Long desc") {
			desc := line[strings.Index(line, "] ")+2:]
			if got := len([]rune(desc)); got != InjectionDescriptionRunes+1 { // + ellipsis
				t.Fatalf("long description rendered %d runes, want %d+ellipsis: %s", got, InjectionDescriptionRunes, line)
			}
			if !strings.HasSuffix(desc, "…") {
				t.Fatalf("truncated description missing ellipsis: %s", line)
			}
		}
		if strings.Contains(line, "Short desc") && !strings.Contains(line, "short one") {
			t.Fatalf("short description altered: %s", line)
		}
	}
}

// B1: with a mature ledger, a fact older than 30 days and silent for the whole
// 30-day window drops out of the injected section — but stays fully searchable
// through the on-demand layer.
func TestZeroRecallCutoff(t *testing.T) {
	store := recallTestStore(t)
	// Cold fact: created 40 days ago, never recalled.
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-cold", Name: "cold-fact", Title: "Cold fact",
		Description: "not recalled for 40 days", Type: TypeProject, Scope: FactScopeProject, Body: "b",
		CreatedAt: injectionTestNow.Add(-40 * 24 * time.Hour),
		UpdatedAt: injectionTestNow.Add(-40 * 24 * time.Hour),
	})
	// Live fact: same age, recalled yesterday.
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-live", Name: "live-fact", Title: "Live fact",
		Description: "recalled yesterday", Type: TypeProject, Scope: FactScopeProject, Body: "b",
		CreatedAt: injectionTestNow.Add(-40 * 24 * time.Hour),
		UpdatedAt: injectionTestNow.Add(-40 * 24 * time.Hour),
	})
	writeMatureLedger(t, store, 15)
	recallLedger(t, store, "mem-live", injectionTestNow.Add(-24*time.Hour))

	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)
	if strings.Contains(out, "Cold fact") {
		t.Fatalf("zero-recall fact must drop from the injected section:\n%s", out)
	}
	if !strings.Contains(out, "Live fact") {
		t.Fatalf("recently-recalled fact must stay:\n%s", out)
	}

	// On-demand layer stays complete: the dropped fact is still searchable and
	// readable (the injected-section trim never touches the pool).
	hits, err := searchMemories(context.Background(), store, "cold-fact", "", "", 8)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range hits {
		if hit.Memory.ID == "mem-cold" {
			found = true
		}
	}
	if !found {
		t.Fatalf("trimmed fact must stay searchable, got %v", hits)
	}
	if m, ok := readMemoryByName(store, "project/cold-fact.md"); !ok || m.ID != "mem-cold" {
		t.Fatalf("trimmed fact must stay readable by reference, ok=%v", ok)
	}
}

// B2: the hard data-maturity gate — before the ledger has accumulated for
// UsageMaturityDays, the cutoff is entirely inert (day-zero zero counts must
// never trim in-use memories).
func TestUsageMaturityGate(t *testing.T) {
	store := recallTestStore(t)
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-cold", Name: "cold-fact", Title: "Cold fact",
		Description: "not recalled for 40 days", Type: TypeProject, Scope: FactScopeProject, Body: "b",
		CreatedAt: injectionTestNow.Add(-40 * 24 * time.Hour),
		UpdatedAt: injectionTestNow.Add(-40 * 24 * time.Hour),
	})
	writeMatureLedger(t, store, 5) // ledger younger than the gate

	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)
	if !strings.Contains(out, "Cold fact") {
		t.Fatalf("cutoff must stay inert before the ledger matures (got a trim):\n%s", out)
	}
}

// B3: cold-start exemptions — young facts, unknown-age facts, and pinned facts
// survive regardless of a zero recall count. (Disk facts missing created_at
// fall back to the file mtime in loadMemory, so the unknown-age branch only
// arises for directly-constructed pools; both routes are covered here.)
func TestColdStartExemption(t *testing.T) {
	store := recallTestStore(t)
	// Young fact: created 10 days ago, zero recalls.
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-young", Name: "young-fact", Title: "Young fact",
		Description: "too new to judge", Type: TypeProject, Scope: FactScopeProject, Body: "b",
		CreatedAt: injectionTestNow.Add(-10 * 24 * time.Hour),
		UpdatedAt: injectionTestNow.Add(-10 * 24 * time.Hour),
	})
	// Legacy disk file without created_at: loadMemory stamps it with the file
	// mtime (fresh here), which keeps it inside the cold-start window.
	// (recallTestWrite defaults CreatedAt, so the missing-field case needs a
	// hand-written file.)
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "---\nname: legacy-fact\ntitle: Legacy fact\ndescription: no created_at on disk\nmetadata:\n  type: project\n  scope: project\n---\n\nb\n"
	if err := os.WriteFile(filepath.Join(store.Dir, "legacy-fact.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	// Old + silent + pinned: pinned is exempt from every cut.
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-pinned", Name: "pinned-fact", Title: "Pinned fact",
		Description: "pinned facts never drop", Type: TypeProject, Scope: FactScopeProject, Body: "b",
		CreatedAt:  injectionTestNow.Add(-90 * 24 * time.Hour),
		UpdatedAt:  injectionTestNow.Add(-90 * 24 * time.Hour),
		Activation: ActivationPinned,
	})
	writeMatureLedger(t, store, 15)

	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)
	for _, want := range []string{"Young fact", "Legacy fact", "Pinned fact"} {
		if !strings.Contains(out, want) {
			t.Fatalf("cold-start exemption failed: %q missing from injection:\n%s", want, out)
		}
	}

	// Unknown age (CreatedAt zero) in a directly-constructed pool: no
	// subtraction on missing data.
	unknown := Memory{
		ID: "mem-unknown", Name: "unknown-age", Title: "Unknown age",
		Description: "zero CreatedAt", Type: TypeProject, Scope: FactScopeProject, Body: "b",
	}
	out = store.RenderInjectionIndex([]Memory{unknown}, injectionTestNow)
	if !strings.Contains(out, "Unknown age") {
		t.Fatalf("zero-CreatedAt fact must not be dropped:\n%s", out)
	}
}

// C1: below budget the section keeps the legacy name order; over budget the
// pass admits lines most-recently-recalled first and trims to the budget.
func TestInjectionBudgetFallback(t *testing.T) {
	store := recallTestStore(t)
	// Five fat lines (~6K runes each) blow the 20K budget; only the first
	// three-plus fit. The recalled one must win admission over older ones.
	for i := range 5 {
		desc := strings.Repeat("x", 6000)
		m := Memory{
			ID:    fmt.Sprintf("mem-%02d", i),
			Name:  fmt.Sprintf("fact-%02d", i),
			Title: fmt.Sprintf("Fact %02d", i),
			Description: desc, Type: TypeProject, Scope: FactScopeProject, Body: "b",
			CreatedAt: injectionTestNow.Add(-time.Duration(50-i) * 24 * time.Hour),
			UpdatedAt: injectionTestNow.Add(-time.Duration(50-i) * 24 * time.Hour),
		}
		recallTestWrite(t, store.Dir, m)
	}
	writeMatureLedger(t, store, 15)
	// fact-04 recalled most recently; it sits last in name order but must be
	// admitted by the recency-first pass.
	recallLedger(t, store, "mem-04", injectionTestNow.Add(-24*time.Hour))

	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)
	runes := len([]rune(out))
	if runes > InjectionBudgetRunes+12000 { // first line is always admitted even over budget
		t.Fatalf("budget pass left %d runes, want ≈≤%d", runes, InjectionBudgetRunes)
	}
	if !strings.Contains(out, "Fact 04") {
		t.Fatalf("most recently recalled fact must be admitted first:\n%s", out)
	}

	// Below budget: name order survives (no silent reordering).
	small := recallTestStore(t)
	for i := range 3 {
		recallTestWrite(t, small.Dir, Memory{
			ID:    fmt.Sprintf("mem-%02d", i),
			Name:  fmt.Sprintf("fact-%02d", i),
			Title: fmt.Sprintf("Fact %02d", i),
			Description: "tiny", Type: TypeProject, Scope: FactScopeProject, Body: "b",
		})
	}
	out = small.RenderInjectionIndex(small.ListAll(), injectionTestNow)
	if strings.Index(out, "Fact 00") > strings.Index(out, "Fact 02") {
		t.Fatalf("below-budget section must keep legacy name order:\n%s", out)
	}
}

// F1: the adaptive notice appears only when at least 20 facts were left out,
// states the count, and suggests no action.
func TestInjectionNoticeLine(t *testing.T) {
	store := recallTestStore(t)
	// 25 cold facts (trimmed) + 1 live fact (kept) → notice with count 25.
	for i := range 25 {
		recallTestWrite(t, store.Dir, Memory{
			ID:    fmt.Sprintf("mem-cold-%02d", i),
			Name:  fmt.Sprintf("cold-%02d", i),
			Title: fmt.Sprintf("Cold %02d", i),
			Description: "silent", Type: TypeProject, Scope: FactScopeProject, Body: "b",
			CreatedAt: injectionTestNow.Add(-60 * 24 * time.Hour),
			UpdatedAt: injectionTestNow.Add(-60 * 24 * time.Hour),
		})
	}
	recallTestWrite(t, store.Dir, Memory{
		ID: "mem-live", Name: "live-fact", Title: "Live fact",
		Description: "recalled today", Type: TypeProject, Scope: FactScopeProject, Body: "b",
	})
	writeMatureLedger(t, store, 15)
	recallLedger(t, store, "mem-live", injectionTestNow)

	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)
	if !strings.Contains(out, "已省略 25 条低频记忆（可检索）") {
		t.Fatalf("notice missing on a ≥20 trim:\n%s", out)
	}

	// Light trim (5 facts): no notice.
	light := recallTestStore(t)
	for i := range 5 {
		recallTestWrite(t, light.Dir, Memory{
			ID:    fmt.Sprintf("mem-cold-%02d", i),
			Name:  fmt.Sprintf("cold-%02d", i),
			Title: fmt.Sprintf("Cold %02d", i),
			Description: "silent", Type: TypeProject, Scope: FactScopeProject, Body: "b",
			CreatedAt: injectionTestNow.Add(-60 * 24 * time.Hour),
			UpdatedAt: injectionTestNow.Add(-60 * 24 * time.Hour),
		})
	}
	recallTestWrite(t, light.Dir, Memory{
		ID: "mem-live", Name: "live-fact", Title: "Live fact",
		Description: "recalled today", Type: TypeProject, Scope: FactScopeProject, Body: "b",
	})
	writeMatureLedger(t, light, 15)
	recallLedger(t, light, "mem-live", injectionTestNow)

	out = light.RenderInjectionIndex(light.ListAll(), injectionTestNow)
	if strings.Contains(out, "已省略") {
		t.Fatalf("notice must stay silent under 20 trims:\n%s", out)
	}
}

// G1: US-1 measurement — the injected section, priced with the host's cold
// token calibration (fallbackTokPerChar=0.25 ≈ 4 chars/token, the same
// caliber the context gauge uses before session calibration), must land
// ≤60K tokens on a task-book-shaped pool (908 facts, p50 description 73
// chars, ~7% over 150).
func TestInjectionTokenBudget(t *testing.T) {
	store := recallTestStore(t)
	for i := range 908 {
		desc := strings.Repeat("d", 73)
		if i%14 == 0 { // ~7% over the 150-rune line cap
			desc = strings.Repeat("d", 220)
		}
		recallTestWrite(t, store.Dir, Memory{
			ID:    fmt.Sprintf("mem-%04d", i),
			Name:  fmt.Sprintf("fact-%04d", i),
			Title: fmt.Sprintf("Fact %04d", i),
			Description: desc, Type: TypeProject, Scope: FactScopeProject, Body: "b",
		})
	}
	all := store.ListAll()
	before := store.IndexFrom(append([]Memory(nil), all...))
	after := store.RenderInjectionIndex(all, injectionTestNow)

	beforeTokens := len(before) / 4
	afterTokens := len(after) / 4
	t.Logf("G1 measurement: before=%d chars ≈%d tokens; after=%d chars ≈%d tokens",
		len(before), beforeTokens, len(after), afterTokens)
	if afterTokens > 60000 {
		t.Fatalf("injected section = %d chars ≈ %d tokens, over the 60K budget", len(after), afterTokens)
	}
	if afterTokens >= beforeTokens {
		t.Fatalf("slimming must shrink the section: before=%d after=%d", beforeTokens, afterTokens)
	}
}

// G2: the on-demand layer stays complete under the slim pipeline — the full
// pool stays searchable and a search-returned reference reads back.
func TestSearchFullPoolUnderSlim(t *testing.T) {
	store := recallTestStore(t)
	for i := range 30 {
		recallTestWrite(t, store.Dir, Memory{
			ID:    fmt.Sprintf("mem-%02d", i),
			Name:  fmt.Sprintf("fact-%02d", i),
			Title: fmt.Sprintf("Fact %02d", i),
			Description: fmt.Sprintf("subject number %02d", i), Type: TypeProject, Scope: FactScopeProject, Body: "body",
			CreatedAt: injectionTestNow.Add(-60 * 24 * time.Hour),
			UpdatedAt: injectionTestNow.Add(-60 * 24 * time.Hour),
		})
	}
	writeMatureLedger(t, store, 15)
	recallLedger(t, store, "mem-00", injectionTestNow) // only fact-00 stays in the section

	out := store.RenderInjectionIndex(store.ListAll(), injectionTestNow)
	if strings.Contains(out, "Fact 17") {
		t.Fatalf("precondition broken: cold fact-17 should be trimmed:\n%s", out)
	}
	hits, err := searchMemories(context.Background(), store, "subject number 17", "", "", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("trimmed fact must stay searchable")
	}
	ref := providerMemoryReference(hits[0].Memory)
	if m, ok := readMemoryByName(store, ref); !ok || m.ID != "mem-17" {
		t.Fatalf("search-returned reference %q must read back, ok=%v", ref, ok)
	}
}
