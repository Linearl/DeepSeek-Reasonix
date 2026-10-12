package boot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/memory"
)

// 任务 801 Phase 2：固化数据源适配器测试。验收：只暴露 decision- 前缀条目
// （提取链产物）、时间增量过滤正确、按更新时间升序、不删除/不改写源记忆。
// 注意：memory.Save 落盘时统一盖 updated_at（store_v2 的写路径语义），条目的
// 真实时刻以保存后的 store.ListAll() 读回为准，不预设传入值。
func TestDecisionSolidifySourceFiltersAndOrders(t *testing.T) {
	memDir := t.TempDir()
	store := memory.Store{Dir: filepath.Join(memDir, "project"), GlobalDir: filepath.Join(memDir, "global")}
	// decision- 前缀两条 + 非 decision 一条 + global 一条，顺序即真实 updated_at 顺序。
	if _, err := store.Save(memory.Memory{Name: "decision-early-1", Title: "early", Body: "b1", Type: memory.TypeFeedback}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(memory.Memory{Name: "decision-late-2", Title: "late", Body: "b2", Type: memory.TypeFeedback}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(memory.Memory{Name: "unrelated-pref", Title: "other", Body: "b3", Type: memory.TypeUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(memory.Memory{Name: "decision-global-3", Title: "glob", Body: "b4", Type: memory.TypeFeedback, Scope: memory.FactScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	times := map[string]time.Time{}
	for _, m := range store.ListAll() {
		times[m.Name] = m.UpdatedAt
	}
	src := decisionSolidifySource{store: store}
	items := src.DecisionMemoriesSince(time.Time{})
	if len(items) != 3 {
		t.Fatalf("expected 3 decision items (2 project + 1 global), got %d", len(items))
	}
	if items[0].Name != "decision-early-1" || items[1].Name != "decision-late-2" || items[2].Name != "decision-global-3" {
		t.Fatalf("items must be ordered by UpdatedAt ascending: %v", items)
	}
	// 增量：严格晚于 decision-late-2 落盘时刻的只剩 global-3（After 为严格
	// 大于，边界条目自身被排除）。
	items = src.DecisionMemoriesSince(times["decision-late-2"])
	if len(items) != 1 || items[0].Name != "decision-global-3" {
		t.Fatalf("incremental filter wrong: %v", items)
	}
	// 源记忆保留：文件仍在盘上（禁区：不删源记忆）。
	if _, err := os.Stat(filepath.Join(store.Dir, "decision-early-1.md")); err != nil {
		t.Fatalf("source memory must stay untouched: %v", err)
	}
	// 空 store 零值安全。
	empty := decisionSolidifySource{}
	if got := empty.DecisionMemoriesSince(time.Time{}); len(got) != 0 {
		t.Fatalf("empty store must yield no items, got %d", len(got))
	}
}
