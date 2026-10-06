package memory

import (
	"fmt"
	"path/filepath"
	"testing"
)

// benchUserDir 在临时目录里摆出 StoreFor 期望的布局并写入 n 条事实
//（中英混合正文，近似真实记忆池），返回 (userDir, cwd) 供 memory.Load 直接发现。
func benchUserDir(b *testing.B, n int) (string, string) {
	b.Helper()
	root := b.TempDir()
	userDir := filepath.Join(root, "user")
	cwd := filepath.Join(root, "proj")
	store := StoreFor(userDir, cwd)
	for i := 0; i < n; i++ {
		fact := Memory{
			Name:        fmt.Sprintf("bench-fact-%03d", i),
			Title:       fmt.Sprintf("基准事实 %03d", i),
			Description: fmt.Sprintf("事实 %03d 的一句话摘要，供索引与召回使用", i),
			Keywords:    "bench keyword branch worktree",
			Body: fmt.Sprintf("事实 %03d：在 worktree 里先确认 toplevel，再跑包级测试，显式路径提交。"+
				"英文对照: verify the worktree toplevel before every command, commit in slices with explicit paths.", i),
			Scope: FactScopeProject,
			Type:  TypeProject,
		}
		if _, err := store.Save(fact); err != nil {
			b.Fatal(err)
		}
	}
	return userDir, cwd
}

// BenchmarkBuildRecallIndex 度量一次全量索引构建的分配。懒构建改造后，这笔
// 开销从 boot 与每次记忆写入路径移到「首次召回」，量级本身不变。
func BenchmarkBuildRecallIndex(b *testing.B) {
	userDir, cwd := benchUserDir(b, 200)
	store := StoreFor(userDir, cwd)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if index := BuildRecallIndex(store); index == nil {
			b.Fatal("expected non-nil index")
		}
	}
}

// BenchmarkLoadMemorySet 度量一次完整 memory.Load。改造前含索引构建
//（≈ BenchmarkBuildRecallIndex 的量），改造后只剩发现——boot/压缩重建/每次
// 记忆写入路径的分配以此为凭下降。
func BenchmarkLoadMemorySet(b *testing.B) {
	userDir, cwd := benchUserDir(b, 200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if set := Load(Options{CWD: cwd, UserDir: userDir}); set == nil {
			b.Fatal("nil set")
		}
	}
}

// BenchmarkSetAutoRecallCold 与 Warm 拆出懒构建的两半：Cold 含首次构建，
// Warm 命中缓存——Warm 应显著低于 Cold 且≈纯打分成本。
func BenchmarkSetAutoRecallCold(b *testing.B) {
	userDir, cwd := benchUserDir(b, 200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		set := Load(Options{CWD: cwd, UserDir: userDir})
		if r := set.AutoRecall("worktree toplevel 分支提交", RecallOptions{}); r.Suppressed != "" && len(r.Hits) == 0 {
			b.Fatalf("expected hits, got %+v", r)
		}
	}
}

func BenchmarkSetAutoRecallWarm(b *testing.B) {
	userDir, cwd := benchUserDir(b, 200)
	set := Load(Options{CWD: cwd, UserDir: userDir})
	set.AutoRecall("warm up the index", RecallOptions{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := set.AutoRecall("worktree toplevel 分支提交", RecallOptions{}); len(r.Hits) == 0 {
			b.Fatalf("expected hits, got %+v", r)
		}
	}
}
