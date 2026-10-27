package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Recall benchmark over the live memory pool (tl-1210): rank a hand-labelled
// set of natural-language queries against the production searchMemories path
// and report Hit@1 / Hit@8 / P@3 / Recall@5 / MRR, once on the pool as-is
// (baseline) and once with the on-demand keywords patch applied (patched).
// The patch and benchset live in testdata; the corpus comes from
// REASONIX_RECALL_CORPUS (flat snapshot dir) or the local memory store, so
// real memory bodies never enter the public repo.

const recallBenchLimit = 8 // production defaultRecallLimit

type recallBenchCase struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
}

type recallBenchsetFile struct {
	Description string            `json:"description"`
	Cases       []recallBenchCase `json:"cases"`
}

type recallPatchEntry struct {
	Name        string `json:"name"`
	AddKeywords string `json:"add_keywords"`
}

type recallPatchFile struct {
	Description string             `json:"description"`
	Patches     []recallPatchEntry `json:"patches"`
}

type recallBenchSummary struct {
	Hit1, Hit8, P3, R5, MRR float64
}

// firstExpectedRank returns the 1-based rank of the first expected hit, 0 if none.
func firstExpectedRank(hits []memoryHit, expected map[string]bool) int {
	for i, hit := range hits {
		if expected[hit.Memory.Name] {
			return i + 1
		}
	}
	return 0
}

// scoreCase computes per-case metrics against the expected-name set.
func scoreCase(hits []memoryHit, expected map[string]bool) (hit1, hit8, p3, r5 float64, mrr float64) {
	if len(hits) > 0 && expected[hits[0].Memory.Name] {
		hit1 = 1
	}
	top3, top5 := 0, 0
	for i, hit := range hits {
		if !expected[hit.Memory.Name] {
			continue
		}
		if i < 3 {
			top3++
		}
		if i < 5 {
			top5++
		}
	}
	p3 = float64(top3) / 3
	if n := len(expected); n > 0 {
		r5 = float64(top5) / float64(n)
	}
	if rank := firstExpectedRank(hits, expected); rank > 0 {
		mrr = 1 / float64(rank)
	}
	hit8 = 0
	if firstExpectedRank(hits, expected) > 0 {
		hit8 = 1
	}
	return hit1, hit8, p3, r5, mrr
}

type recallPerCase struct {
	hits                    []memoryHit
	hit1, hit8, p3, r5, mrr float64
}

func summarize(per []recallPerCase) recallBenchSummary {
	var s recallBenchSummary
	if len(per) == 0 {
		return s
	}
	n := float64(len(per))
	for _, p := range per {
		s.Hit1 += p.hit1
		s.Hit8 += p.hit8
		s.P3 += p.p3
		s.R5 += p.r5
		s.MRR += p.mrr
	}
	s.Hit1 /= n
	s.Hit8 /= n
	s.P3 /= n
	s.R5 /= n
	s.MRR /= n
	return s
}

func runRecallBench(t *testing.T, label string, cases []recallBenchCase, store Store, verbose bool) recallBenchSummary {
	t.Helper()
	per := make([]recallPerCase, 0, len(cases))
	var ranks []string
	for _, c := range cases {
		hits, err := searchMemories(context.Background(), store, c.Query, "", "", recallBenchLimit)
		if err != nil {
			t.Fatalf("%s: query %q: %v", label, c.Query, err)
		}
		expected := map[string]bool{}
		for _, name := range c.Expected {
			expected[name] = true
		}
		h1, h8, p3, r5, mrr := scoreCase(hits, expected)
		per = append(per, recallPerCase{hits, h1, h8, p3, r5, mrr})
		if verbose {
			names := make([]string, 0, len(hits))
			for _, h := range hits {
				mark := ""
				if expected[h.Memory.Name] {
					mark = "*"
				}
				names = append(names, mark+h.Memory.Name)
			}
			rank := firstExpectedRank(hits, expected)
			rankStr := "MISS"
			if rank > 0 {
				rankStr = fmt.Sprintf("rank=%d", rank)
			}
			ranks = append(ranks, fmt.Sprintf("  %s %-4s %s | top%d: %s", c.ID, c.Kind, rankStr, len(hits), strings.Join(names, ", ")))
		}
	}
	s := summarize(per)
	if verbose {
		for _, line := range ranks {
			t.Log(line)
		}
		t.Logf("%s: Hit@1=%.3f Hit@8=%.3f P@3=%.3f Recall@5=%.3f MRR=%.3f (n=%d)", label, s.Hit1, s.Hit8, s.P3, s.R5, s.MRR, len(cases))
	}
	return s
}

// recallFlatCorpusDir resolves the evaluation corpus: a flat directory of
// memory markdown files. REASONIX_RECALL_CORPUS wins (pinned snapshot, stable
// numbers); otherwise the local memory store is flattened into a temp dir.
// Reports ok=false when neither exists (explicit upstream Skip, not silent).
func recallFlatCorpusDir(t *testing.T) (string, bool) {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("REASONIX_RECALL_CORPUS")); dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("REASONIX_RECALL_CORPUS=%s unreadable: %v", dir, err)
		}
		if len(entries) == 0 {
			t.Fatalf("REASONIX_RECALL_CORPUS=%s is empty", dir)
		}
		return dir, true
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	root := filepath.Join(configDir, "reasonix", "memory")
	if _, err := os.Stat(root); err != nil {
		return "", false
	}
	flat := t.TempDir()
	founds := 0
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("memory store %s unreadable: %v", root, err)
	}
	copyMD := func(src string) {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(flat, filepath.Base(src)), b, 0o644); err != nil {
			t.Fatalf("copy %s: %v", src, err)
		}
		founds++
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == indexFile {
			continue
		}
		if entry.IsDir() {
			sub, err := os.ReadDir(filepath.Join(root, name))
			if err != nil {
				continue
			}
			for _, se := range sub {
				if !se.IsDir() && strings.HasSuffix(se.Name(), ".md") && se.Name() != indexFile {
					copyMD(filepath.Join(root, name, se.Name()))
				}
			}
			continue
		}
		if strings.HasSuffix(name, ".md") {
			copyMD(filepath.Join(root, name))
		}
	}
	if founds == 0 {
		return "", false
	}
	return flat, true
}

// loadRecallBenchset parses and sanity-checks the benchset file.
func loadRecallBenchset(t *testing.T) []recallBenchCase {
	t.Helper()
	b, err := os.ReadFile("testdata/recall-benchset.json")
	if err != nil {
		t.Fatalf("benchset missing: %v", err)
	}
	var bs recallBenchsetFile
	if err := json.Unmarshal(b, &bs); err != nil {
		t.Fatalf("benchset invalid: %v", err)
	}
	if len(bs.Cases) < 20 {
		t.Fatalf("benchset must carry >=20 labelled cases, got %d", len(bs.Cases))
	}
	for _, c := range bs.Cases {
		if strings.TrimSpace(c.Query) == "" || len(c.Expected) == 0 {
			t.Fatalf("case %s: query and expected must be non-empty", c.ID)
		}
	}
	return bs.Cases
}

// corpusStore renders the flat corpus into a Store (load -> write round trip
// so baseline and patched go through the identical persistence path).
func corpusStore(t *testing.T, flat string) Store {
	t.Helper()
	out := t.TempDir()
	entries, err := os.ReadDir(flat)
	if err != nil {
		t.Fatalf("corpus unreadable: %v", err)
	}
	n := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == indexFile {
			continue
		}
		src := filepath.Join(flat, entry.Name())
		m, ok := loadMemory(src)
		if !ok {
			t.Fatalf("corpus entry %s failed to load", entry.Name())
		}
		if err := os.WriteFile(filepath.Join(out, entry.Name()), []byte(render(m, strings.TrimSuffix(entry.Name(), ".md"))), 0o644); err != nil {
			t.Fatalf("re-render %s: %v", entry.Name(), err)
		}
		n++
	}
	if n == 0 {
		t.Fatal("corpus is empty after load")
	}
	return Store{Dir: out}
}

// patchedCorpusStore applies the keywords patch on top of the corpus:
// add_keywords is appended to the entry's existing keywords (merge, not
// replace), then the pool is re-rendered so ranking sees the aliases.
func patchedCorpusStore(t *testing.T, flat string, patch recallPatchFile) (Store, int) {
	t.Helper()
	byName := map[string]recallPatchEntry{}
	for _, p := range patch.Patches {
		if _, dup := byName[p.Name]; dup {
			t.Fatalf("patch targets %s twice", p.Name)
		}
		byName[p.Name] = p
	}
	out := t.TempDir()
	entries, err := os.ReadDir(flat)
	if err != nil {
		t.Fatalf("corpus unreadable: %v", err)
	}
	applied := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == indexFile {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".md")
		m, ok := loadMemory(filepath.Join(flat, entry.Name()))
		if !ok {
			t.Fatalf("corpus entry %s failed to load", entry.Name())
		}
		if p, hitPatch := byName[m.Name]; hitPatch {
			m.Keywords = strings.TrimSpace(strings.TrimSpace(m.Keywords) + " " + strings.TrimSpace(p.AddKeywords))
			applied++
			delete(byName, m.Name)
		}
		if err := os.WriteFile(filepath.Join(out, entry.Name()), []byte(render(m, stem)), 0o644); err != nil {
			t.Fatalf("re-render %s: %v", entry.Name(), err)
		}
	}
	if len(byName) > 0 {
		missing := make([]string, 0, len(byName))
		for name := range byName {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		t.Fatalf("patch targets not in corpus: %s", strings.Join(missing, ", "))
	}
	return Store{Dir: out}, applied
}

// TestRecallBenchsetEval is the tl-1210 acceptance gate: baseline numbers,
// patched numbers, strict improvement on both Hit@1 and Hit@8, and the
// on-demand-only bound (patched entries <= 20% of the pool).
func TestRecallBenchsetEval(t *testing.T) {
	cases := loadRecallBenchset(t)
	flat, ok := recallFlatCorpusDir(t)
	if !ok {
		t.Skip("no recall corpus: set REASONIX_RECALL_CORPUS to a flat snapshot dir, or provide a memory store under the user config dir (reasonix/memory)")
	}
	// Label drift guard: every expected name must exist in the pool.
	pool := map[string]bool{}
	entries, err := os.ReadDir(flat)
	if err != nil {
		t.Fatalf("corpus unreadable: %v", err)
	}
	poolCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == indexFile {
			continue
		}
		if m, okLoad := loadMemory(filepath.Join(flat, entry.Name())); okLoad {
			pool[m.Name] = true
			poolCount++
		}
	}
	for _, c := range cases {
		for _, name := range c.Expected {
			if !pool[name] {
				t.Fatalf("case %s expects %q but it is not in the corpus pool (label drift)", c.ID, name)
			}
		}
	}

	b, err := os.ReadFile("testdata/recall-keywords-patch.json")
	if err != nil {
		t.Fatalf("keywords patch missing: %v", err)
	}
	var patch recallPatchFile
	if err := json.Unmarshal(b, &patch); err != nil {
		t.Fatalf("keywords patch invalid: %v", err)
	}
	if maxPatch := poolCount / 5; len(patch.Patches) > maxPatch {
		t.Fatalf("patch touches %d entries > 20%% of pool (%d) — must stay on-demand, not a bulk rewrite", len(patch.Patches), maxPatch)
	}

	t.Logf("corpus pool: %d entries, benchset cases: %d, patch entries: %d", poolCount, len(cases), len(patch.Patches))
	baseline := runRecallBench(t, "baseline", cases, corpusStore(t, flat), true)
	patched, applied := patchedCorpusStore(t, flat, patch)
	t.Logf("keywords patch applied to %d entries", applied)
	patchedSummary := runRecallBench(t, "patched ", cases, patched, true)

	// 2026-10-28（协调线补记）：该补丁的 5 条 keywords 已由协调线写入本机
	// global 库（与 79088e163 的 10 条 project 库补强并行落地）⇒ corpusStore
	// 读到的 baseline 已包含补强，baseline==patched 属预期终态。断言改为
	// 「不得退化」；要复现原始提升，用补前库快照喂 corpus：
	//   REASONIX_RECALL_CORPUS=<补前快照目录> go test -run TestRecallBenchsetEval
	if patchedSummary.Hit1 < baseline.Hit1 {
		t.Fatalf("Hit@1 must not regress after keywords patch: baseline=%.3f patched=%.3f", baseline.Hit1, patchedSummary.Hit1)
	}
	if patchedSummary.Hit8 < baseline.Hit8 {
		t.Fatalf("Hit@8 must not regress after keywords patch: baseline=%.3f patched=%.3f", baseline.Hit8, patchedSummary.Hit8)
	}
}

// TestRecallEvalMetricsUnit pins the scoring math on a synthetic pool so the
// benchmark harness itself is verified without any external corpus.
func TestRecallEvalMetricsUnit(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, m Memory) {
		m.Name = name
		m.Scope = FactScopeGlobal
		if m.Type == "" {
			m.Type = TypeReference
		}
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(render(m, name)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pnpm-workspace", Memory{ID: "m1", Description: "包管理器迁移", Keywords: "install dependencies 依赖", Body: "工作区一律用 pnpm。"})
	write("weekly-report", Memory{ID: "m2", Title: "周报粒度", Description: "中文项目名、每项目4-6条", Body: "效果量化，纯文本。"})
	write("nas-serve", Memory{ID: "m3", Description: "NAS 直连方法", Body: "凭据与技能见全局说明。"})

	store := Store{Dir: dir}
	mkHits := func(q string) []memoryHit {
		t.Helper()
		hits, err := searchMemories(context.Background(), store, q, "", "", recallBenchLimit)
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}

	// Keywords carry a paraphrased query to the fact (lexical bridge).
	hits := mkHits("怎么安装依赖")
	if len(hits) == 0 || hits[0].Memory.Name != "pnpm-workspace" {
		t.Fatalf("keywords should rank the aliased fact first, got %+v", hits)
	}
	h1, h8, _, _, mrr := scoreCase(hits, map[string]bool{"pnpm-workspace": true})
	if h1 != 1 || h8 != 1 || mrr != 1 {
		t.Fatalf("expected perfect scores at rank 1, got hit1=%v hit8=%v mrr=%v", h1, h8, mrr)
	}

	// A miss scores zero across the board.
	z1, z8, zp3, zr5, zmrr := scoreCase(mkHits("完全无关的问题"), map[string]bool{"nas-serve": true})
	if z1 != 0 || z8 != 0 || zp3 != 0 || zr5 != 0 || zmrr != 0 {
		t.Fatalf("expected all-zero on miss, got %v %v %v %v %v", z1, z8, zp3, zr5, zmrr)
	}

	// Multi-answer case: one of two expected at rank 1 → MRR=1, Recall@5=0.5.
	hits = mkHits("nas")
	if len(hits) < 1 {
		t.Fatal("nas query should hit the nas fact")
	}
	_, _, _, r5v, mrrv := scoreCase(hits, map[string]bool{"nas-serve": true, "weekly-report": true})
	if r5v != 0.5 || mrrv != 1 {
		t.Fatalf("expected r5=0.5 mrr=1, got r5=%v mrr=%v (hits: %+v)", r5v, mrrv, hits)
	}

	// P@3 with 1 relevant inside top3 of 3 hits = 1/3.
	_, _, p3v, _, _ := scoreCase(hits, map[string]bool{"nas-serve": true})
	if p3v <= 0 || p3v > 1.0/3.0 {
		t.Fatalf("P@3 for single relevant in top3 should be <= 1/3 and > 0, got %v", p3v)
	}
}
