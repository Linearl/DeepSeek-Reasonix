package memory

import (
	"fmt"
	"path/filepath"
	"testing"
)

// tl1229 回归：BuildRecallIndex 的打点不改变构建产物本身——stats 只旁观，
// 索引内容与打点前同一函数同一输入逐字段一致；空池也留一条零值 stats。

func recallStatsFixture(t *testing.T) Store {
	t.Helper()
	root := t.TempDir()
	store := StoreFor(filepath.Join(root, "user"), filepath.Join(root, "proj"))
	for i := 0; i < 5; i++ {
		if _, err := store.Save(Memory{
			Name:        fmt.Sprintf("stats-fact-%d", i),
			Title:       "打点回归事实",
			Description: "用于钉死打点不改产物",
			Keywords:    "stats probe regression",
			Body:        "事实正文：打点必须只旁观，索引内容不变。",
			Scope:       FactScopeProject,
			Type:        TypeProject,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestBuildRecallIndexStatsRecorded(t *testing.T) {
	store := recallStatsFixture(t)
	index := BuildRecallIndex(store)
	if index == nil || len(index.docs) == 0 || len(index.fielded) != 5 {
		t.Fatalf("index = %+v, want 5 pool entries", index)
	}
	stats := LastRecallIndexBuildStats()
	if stats == nil {
		t.Fatal("build stats must be recorded")
	}
	if stats.Docs != 5 || stats.Terms <= 0 || stats.Duration < 0 || stats.At.IsZero() {
		t.Fatalf("stats = %+v, want docs=5 terms>0 duration>=0 at set", stats)
	}
}

func TestBuildRecallIndexStatsEmptyPool(t *testing.T) {
	root := t.TempDir()
	store := StoreFor(filepath.Join(root, "user"), filepath.Join(root, "proj"))
	if index := BuildRecallIndex(store); index != nil {
		t.Fatalf("empty pool must build nil index, got %+v", index)
	}
	stats := LastRecallIndexBuildStats()
	if stats == nil || stats.Docs != 0 || stats.Terms != 0 {
		t.Fatalf("empty-pool stats = %+v, want zero-value record", stats)
	}
}
