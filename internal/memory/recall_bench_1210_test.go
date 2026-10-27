package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 任务1210 召回基准集：用真实 project memory 库跑基线命中率（top1/top8）。
// 库目录取自 %APPDATA%\\reasonix\\projects\\<slug>\\memory——机器本地数据，
// CI/新环境无该库时显式 Skip（有意的环境依赖，不是构造失败的静默跳过）。
// 判据只消费 searchMemories（recall.go），不触碰 BM25Score/Tokens/floor（卡验收5）。

type recallBenchCase struct {
	ID      string   `json:"id"`
	Query   string   `json:"query"`
	Expects []string `json:"expects"`
	Note    string   `json:"note"`
}

type recallBenchFile struct {
	Version int               `json:"version"`
	Note    string            `json:"note"`
	Cases   []recallBenchCase `json:"cases"`
}

func TestRecallBench1210Baseline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "recall_bench_1210.json"))
	if err != nil {
		t.Fatalf("bench data: %v", err)
	}
	var bf recallBenchFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		t.Fatalf("parse bench data: %v", err)
	}
	if len(bf.Cases) < 20 {
		t.Fatalf("bench needs >=20 cases, got %d", len(bf.Cases))
	}

	appData := os.Getenv("APPDATA")
	store := Store{
		Dir:       filepath.Join(appData, "reasonix", "projects", "C--Users-yinji-AppData-Roaming-reasonix-global-workspace", "memory"),
		GlobalDir: filepath.Join(appData, "reasonix", "memory", "global"),
	}
	if _, statErr := os.Stat(store.Dir); statErr != nil {
		t.Skipf("real project memory dir absent (%v): this bench is machine-local by design", statErr)
	}

	top1, top8, missed := 0, 0, 0
	for _, c := range bf.Cases {
		hits, err := searchMemories(t.Context(), store, c.Query, "", "", 8)
		if err != nil {
			t.Errorf("%s search failed: %v", c.ID, err)
			continue
		}
		rank := 0
		for i, h := range hits {
			for _, prefix := range c.Expects {
				if strings.HasPrefix(h.Memory.Name, prefix) {
					rank = i + 1
					break
				}
			}
			if rank > 0 {
				break
			}
		}
		switch {
		case rank == 1:
			top1++
			top8++
		case rank > 1 && rank <= 8:
			top8++
		default:
			missed++
			t.Logf("%s MISS (rank>8/none) query=%q expects=%v", c.ID, c.Query, c.Expects)
		}
		if rank > 1 && rank <= 8 {
			t.Logf("%s weak (rank=%d) query=%q", c.ID, rank, c.Query)
		}
	}
	total := len(bf.Cases)
	t.Logf("1210 baseline over %d queries: top1=%d (%.1f%%), top8=%d (%.1f%%), missed=%d",
		total, top1, 100*float64(top1)/float64(total), top8, 100*float64(top8)/float64(total), missed)
}
