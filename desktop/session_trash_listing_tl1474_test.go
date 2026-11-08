package main

// tl1474 pins for the trash listing rewrite.
//
//  1. Equivalence: on one fixture exercising every bucket shape the real
//     trash exhibits (titled, recovery-copy-shaped, meta-less, empty shell,
//     multi-transcript, meta-key≠dirname, covered copy with a live parent),
//     the tl1474 chain must produce EXACTLY the records the pre-tl1474
//     sequential chain produced — field for field. The old chain is
//     replicated verbatim in this test (agent.ListSessions per bucket +
//     trashedSessionDeletedAt re-reads), so any drift in the folded listing,
//     the metadata-only read, or the parallel assembly goes red.
//  2. Reverse validation of the read-mode swap: a bucket whose sidecar carries
//     an unproven recovery preference must be listed even while another
//     holder owns the transcript's save-path lock. The old read mode
//     (agent.ListSessions → preferred proof → LoadSession) stalls on that
//     lock for the 30s budget; the metadata-only read never asks for it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// --- fixture helpers -------------------------------------------------

func tl1474StageBucket(t *testing.T, dir, key string, transcript []byte, withMeta bool) string {
	t.Helper()
	itemDir := filepath.Join(dir, sessionTrashDir, key)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatalf("mkdir trash bucket: %v", err)
	}
	if transcript != nil {
		if err := os.WriteFile(filepath.Join(itemDir, key), transcript, 0o644); err != nil {
			t.Fatalf("write bucket transcript: %v", err)
		}
	}
	if withMeta {
		meta, err := json.MarshalIndent(trashedSessionMeta{Key: key, DeletedAt: 1729000000000}, "", "  ")
		if err != nil {
			t.Fatalf("marshal trash meta: %v", err)
		}
		if err := os.WriteFile(filepath.Join(itemDir, sessionTrashMetaFile), meta, 0o644); err != nil {
			t.Fatalf("write trash meta: %v", err)
		}
	}
	return filepath.Join(itemDir, key)
}

func tl1474Transcript(t *testing.T, dir, name, userLine string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"`+userLine+`"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func tl1474SortByPath(in []SessionMeta) []SessionMeta {
	out := append([]SessionMeta{}, in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// tl1474OldListTrashedSessions replicates the pre-tl1474 sequential chain
// verbatim (agent.ListSessions per bucket, trashedSessionDeletedAt re-reads,
// same skip rules, same sort) so the equivalence pin has a real baseline.
// tl1429d2: the replica tracks the one assembly change since (title tag →
// family grouping fields) so both chains stay field-for-field comparable.
func tl1474OldListTrashedSessions(app *App) []SessionMeta {
	out := []SessionMeta{}
	for _, dir := range app.knownSessionDirs() {
		paths, err := listTrashedSessionFiles(dir)
		if err != nil {
			continue
		}
		titles := loadSessionTitles(dir)
		for _, path := range paths {
			infos, err := agent.ListSessions(filepath.Dir(path))
			if err != nil {
				continue
			}
			deletedAt := trashedSessionDeletedAt(path)
			if len(infos) == 0 {
				shellKey, shellIsCopy := trashedFamilyKey(filepath.Base(path))
				out = append(out, SessionMeta{
					Path:            path,
					TurnsState:      "unknown",
					DeletedAt:       deletedAt,
					LastActivityAt:  deletedAt,
					ModTime:         deletedAt,
					TrashFamilyKey:  shellKey,
					TrashFamilyCopy: shellIsCopy,
				})
				continue
			}
			title := strings.TrimSpace(infos[0].CustomTitle)
			if title == "" {
				title = titles[filepath.Base(path)]
			}
			meta := sessionMetaFromInfo(infos[0], title, false, false, deletedAt, dir)
			// tl1429d2: 标题后缀标识(原 tl1429d 修法,用户打回)换成了同族归组
			// 只读字段;等价基线同步换装,两条链逐字段对齐(含 TrashFamilyKey/
			// TrashFamilyCopy)。
			meta.TrashFamilyKey, meta.TrashFamilyCopy = trashedFamilyKey(filepath.Base(path))
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeletedAt == out[j].DeletedAt {
			return out[i].LastActivityAt > out[j].LastActivityAt
		}
		return out[i].DeletedAt > out[j].DeletedAt
	})
	return out
}

// --- pins --------------------------------------------------------------

func TestTL1474TrashListingEquivalenceAcrossBucketShapes(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	// 1. titled normal bucket (sidecar TopicTitle feeds the tag base path).
	titledKey := "20261024-011346.045066400-tl1474-titled.jsonl"
	titledPath := tl1474StageBucket(t, dir, titledKey, []byte(`{"role":"user","content":"titled turn"}`+"\n"), true)
	if err := agent.SaveBranchMetaPreserveUpdated(titledPath, agent.BranchMeta{TopicTitle: "TL1474 titled topic"}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}

	// 2. recovery-copy-shaped bucket (tl1429d2 family-field path).
	copyKey := "20261024-011346.045066400-tl1474-titled-recovery-aabbccdd11223344.jsonl"
	tl1474StageBucket(t, dir, copyKey, []byte(`{"role":"user","content":"copy turn"}`+"\n"), true)

	// 3. meta-less bucket (tl1429b: transcript only).
	tl1474StageBucket(t, dir, "20261025-020000.000000000-tl1474-metaless.jsonl", []byte(`{"role":"user","content":"metaless turn"}`+"\n"), false)

	// 4. empty-shell bucket (tl1449: meta present, transcript gone).
	tl1474StageBucket(t, dir, "20261026-030000.000000000-tl1474-shell.jsonl", nil, true)

	// 5. multi-transcript meta-less bucket (tl1449: first transcript wins).
	multiKey := "20261027-040000.000000000-tl1474-multi.jsonl"
	multiDir := filepath.Join(dir, sessionTrashDir, multiKey)
	if err := os.MkdirAll(multiDir, 0o755); err != nil {
		t.Fatalf("mkdir multi bucket: %v", err)
	}
	tl1474Transcript(t, multiDir, "20261027-040000.000000000-tl1474-multi.jsonl", "older transcript")
	tl1474Transcript(t, multiDir, "20261027-050000.000000000-tl1474-multi.jsonl", "newer transcript")

	// 6. recovery-shaped bucket with a live parent and a NON-matching fork
	// digest: the coverage proof runs (and is memoized by the tl1474 verdict
	// memo) but fails closed — both chains must agree on RecoveryCopy=false.
	// The covered-true outcome is owned by the agent-side memo pins and the
	// 22b334f22 contract suites; this shape pins old-vs-new agreement when the
	// memo is in the loop (old chain proves live, new chain may read the memo).
	parentStem := "20261028-060000.000000000-tl1474-parent"
	forkContent := `{"role":"user","content":"fork content identical in both"}` + "\n"
	tl1474Transcript(t, dir, parentStem+".jsonl", "fork content identical in both")
	coveredKey := parentStem + "-recovery-9988776655443322.jsonl"
	coveredPath := tl1474StageBucket(t, dir, coveredKey, []byte(forkContent), true)
	if err := agent.SaveBranchMetaPreserveUpdated(coveredPath, agent.BranchMeta{
		Recovered:      true,
		RecoveryDigest: "01deadbeef-not-the-real-fork-digest",
		ParentID:       parentStem,
	}); err != nil {
		t.Fatalf("save covered sidecar: %v", err)
	}

	// 7. meta.Key ≠ dirname bucket (validation falls to the meta match).
	oddDir := filepath.Join(dir, sessionTrashDir, "20261029-070000.000000000-tl1474-odd.jsonl")
	if err := os.MkdirAll(oddDir, 0o755); err != nil {
		t.Fatalf("mkdir odd bucket: %v", err)
	}
	oddInner := "20261029-073000.000000000-tl1474-odd-inner.jsonl"
	tl1474Transcript(t, oddDir, oddInner, "odd key turn")
	oddMeta, err := json.MarshalIndent(trashedSessionMeta{Key: oddInner, DeletedAt: 1729000000001}, "", "  ")
	if err != nil {
		t.Fatalf("marshal odd meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oddDir, sessionTrashMetaFile), oddMeta, 0o644); err != nil {
		t.Fatalf("write odd meta: %v", err)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })

	oldChain := tl1474SortByPath(tl1474OldListTrashedSessions(app))
	newChain := tl1474SortByPath(app.ListTrashedSessions())

	if len(oldChain) != 7 {
		t.Fatalf("fixture baseline broke: old chain listed %d records, want 7 — shapes: %+v", len(oldChain), pathsOf(oldChain))
	}
	if len(newChain) != len(oldChain) {
		t.Fatalf("record count drift: old=%d new=%d", len(oldChain), len(newChain))
	}
	for i := range oldChain {
		if !reflect.DeepEqual(oldChain[i], newChain[i]) {
			t.Fatalf("record drift at %d (path %s):\n  old %+v\n  new %+v", i, oldChain[i].Path, oldChain[i], newChain[i])
		}
	}

	// Spot-check the contract-bearing fields on the shapes that carry them.
	byPath := map[string]SessionMeta{}
	for _, record := range newChain {
		byPath[record.Path] = record
	}
	// tl1429d2: 副本形状走归组字段而非标题标识(打回的旧修法不得回潮)。
	if rec := byPath[copyPathFor(t, dir, copyKey)]; !rec.TrashFamilyCopy || rec.TrashFamilyKey != "20261024-011346.045066400-tl1474-titled" {
		t.Fatalf("copy record lost family grouping fields: copy=%v key=%q", rec.TrashFamilyCopy, rec.TrashFamilyKey)
	}
	if rec := byPath[copyPathFor(t, dir, copyKey)]; strings.Contains(rec.Title, "(副本 recovery-") {
		t.Fatalf("copy record title %q carries the rejected copy tag — tl1429d 标识回潮", rec.Title)
	}
	if rec := byPath[titledPath]; rec.TrashFamilyCopy || rec.TrashFamilyKey != "20261024-011346.045066400-tl1474-titled" {
		t.Fatalf("mainline bucket mis-grouped: copy=%v key=%q", rec.TrashFamilyCopy, rec.TrashFamilyKey)
	}
	if rec := byPath[coveredPath]; rec.RecoveryCopy {
		t.Fatalf("digest-mismatched copy marked RecoveryCopy — 无 coverage 证明不得标记 (22b334f22)")
	}
	if rec := byPath[titledPath]; rec.TopicTitle != "TL1474 titled topic" {
		t.Fatalf("titled bucket lost its topic title: %q", rec.TopicTitle)
	}
}

func copyPathFor(t *testing.T, dir, key string) string {
	t.Helper()
	return filepath.Join(dir, sessionTrashDir, key, key)
}

func pathsOf(in []SessionMeta) []string {
	out := make([]string, len(in))
	for i, record := range in {
		out[i] = filepath.Base(record.Path)
	}
	return out
}

// Reverse validation: the trash listing must not stall on a held save-path
// lock for a bucket whose sidecar carries an unproven recovery preference —
// the pre-tl1474 read mode (agent.ListSessions with the proving resolver)
// LoadSessions the transcript for the proof and queues on the 30s budget.
func TestTL1474TrashListingListsPreferredBucketUnderSavePathLock(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	key := "20261101-080000.000000000-tl1474-preferred.jsonl"
	path := tl1474StageBucket(t, dir, key, []byte(`{"role":"user","content":"preferred turn"}`+"\n"), true)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		Recovered:               true,
		RecoveryDigest:          "01deadbeef",
		RecoveryPreferred:       true,
		RecoveryPreferredDigest: "01deadbeef",
	}); err != nil {
		t.Fatalf("save preferred sidecar: %v", err)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })

	unlock := agent.HoldSessionSavePathForTest(path)
	defer unlock()

	done := make(chan []SessionMeta, 1)
	go func() { done <- app.ListTrashedSessions() }()
	select {
	case listed := <-done:
		found := false
		for _, record := range listed {
			if filepath.Base(record.Path) == key {
				found = true
			}
		}
		if !found {
			t.Fatalf("preferred bucket missing from the listing under a held save-path lock: %v", pathsOf(listed))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("ListTrashedSessions stalled on a held save-path lock — the proving read mode got re-coupled into the trash listing")
	}
}
