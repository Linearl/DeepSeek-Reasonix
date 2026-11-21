package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// tl1734 缺陷A profile：记忆面板加载链路的分相基准。
// 面板一次加载在后端跑的相（desktop/memory_app.go memoryForCtrl）：
//   1. Store.ListAll()        —— 全量事实读盘（store.go:505）
//   2. FindOverrides(all)     —— 覆盖冲突计算（auto_recall.go:89）
//   3. Store.ListArchived()   —— 归档读盘（store.go:679）
// 面板打开还伴随 ListTabs + 一次 memory.Load（controller 构建/写路径），
// 这里只量化面板直连的三相与 Load，供空面板/满面板的量级论证。

func benchEmptyStore(b *testing.B) (string, string) {
	b.Helper()
	root := b.TempDir()
	user := filepath.Join(root, "user")
	proj := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		b.Fatal(err)
	}
	return user, proj
}

func BenchmarkPanelListAllEmpty(b *testing.B) {
	user, proj := benchEmptyStore(b)
	store := StoreFor(user, proj)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.ListAll()
	}
}

func BenchmarkPanelListArchivedEmpty(b *testing.B) {
	user, proj := benchEmptyStore(b)
	store := StoreFor(user, proj)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.ListArchived()
	}
}

func BenchmarkPanelLoadEmpty(b *testing.B) {
	user, proj := benchEmptyStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Load(Options{CWD: proj, UserDir: user})
	}
}

func benchmarkPanelPhases(b *testing.B, n int) {
	user, proj := benchPoolStore(b, n)
	store := StoreFor(user, proj)
	b.Run("ListAll", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = store.ListAll()
		}
	})
	b.Run("ListArchived", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = store.ListArchived()
		}
	})
	b.Run("Load", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = Load(Options{CWD: proj, UserDir: user})
		}
	})
	b.Run("ViewAssembly", func(b *testing.B) {
		// memoryForCtrl 的主体（不含 LastMemoryRecall）：ListAll + FindOverrides
		// + ListArchived + 视图映射，等同面板一次全量刷新的后端成本。
		for i := 0; i < b.N; i++ {
			all := store.ListAll()
			_ = FindOverrides(all)
			_ = store.ListArchived()
		}
	})
}

func BenchmarkPanelPhases200(b *testing.B)  { benchmarkPanelPhases(b, 200) }
func BenchmarkPanelPhases1000(b *testing.B) { benchmarkPanelPhases(b, 1000) }

var _ = fmt.Sprintf // keep fmt imported for symmetry with load_bench_test.go
