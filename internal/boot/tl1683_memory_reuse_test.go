package boot

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/memory"
	"reasonix/internal/plugin"
)

// ── tl1683 ①/④：363A 复用分支必须覆盖 memory 三段与 shell 解析 ─────────────
//
// 归因（docs/reasonix/report/tl1683-boot热路径三期恶化归因-20261118.md
// §4.4 / §7.2）：复用分支只覆盖 skills/commands/hooks，memory 三段
// （MigrateV2 / MaterializeLegacyPinned / memory.Load）与 shell 解析在
// boot.go 里无条件执行——于是每次「热路径」boot 都重跑完整 prompt 组装，
// memory_migrate 一段独占 P95 增量的 98.9%（21～29s）。
//
// 修复后：复用 build 复用上一次的 memory 快照与 shell 解析（memory 快照还
// 要通过 store 指纹校验，store 变了就退回新鲜读），并在 boot: assembly
// reuse 行里自报——这条自报同时是 bench 热路径判定的直接证据（tl1683 ⑤）。

// tl1683FactFile 是一条已迁移（id + revision 齐备）的事实文件内容。
const tl1683FactFile = "---\nid: fact-1\nrevision: 1\nname: fact\n" +
	"description: a stable fact\nmetadata:\n  type: user\n  scope: global\n---\n\nbody\n"

// TestAssemblyReuseReusesMemorySnapshot1683：三轮 boot 的复用行为断言。
//
//	第一轮（冷）：产出 assembly（带 memory 快照 + 指纹 + shell）。
//	第二轮（复用，store 未变）：必须复用 memory 快照（自报 memory=true）。
//	第三轮（复用，store 已变）：指纹失配 ⇒ 必须退回新鲜读（memory=false）。
//
// 第三轮是安全边界的钉：复用不能拿过期快照冒充最新记忆。
func TestAssemblyReuseReusesMemorySnapshot1683(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("tl1683-reuse", testutil.Turn{Text: "ok"}))
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig)

	userDir := config.MemoryUserDir()
	if userDir == "" {
		t.Fatal("config.MemoryUserDir() 为空：复用指纹无从比对（用例前提不成立）")
	}
	globalDir := filepath.Join(userDir, "memory", "global")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global memory dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "fact.md"), []byte(tl1683FactFile), 0o644); err != nil {
		t.Fatalf("write fact: %v", err)
	}

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	baseOpts := func() Options {
		return Options{Sink: event.Discard, MCPHostProfile: plugin.HostProfileInteractive}
	}

	res1, err := BuildRuntime(context.Background(), baseOpts())
	if err != nil {
		t.Fatalf("round1 BuildRuntime: %v", err)
	}
	t.Cleanup(res1.Controller.Close)
	if res1.Assembly == nil {
		t.Fatal("round1 assembly 为 nil")
	}
	if res1.Assembly.Memory == nil {
		t.Fatal("round1 assembly 未携带 memory 快照：复用无从复用")
	}
	if res1.Assembly.MemoryFingerprint == "" {
		t.Fatal("round1 assembly 未携带 memory 指纹：复用无从判等")
	}
	if res1.Assembly.Shell.Path == "" {
		t.Fatal("round1 assembly 未携带 shell 解析结果：复用无从复用")
	}

	// 第二轮：store 变了（新增一条 legacy 事实）⇒ 指纹失配 ⇒ 退回新鲜读。
	// 用 legacy 文件（无前置块）当判据：新鲜路径的 MigrateV2 必须把它迁掉，
	// 于是「迁移是否真的发生」成为可观察的事实，而不依赖分段毫秒数。
	legacy := filepath.Join(globalDir, "legacy.md")
	if err := os.WriteFile(legacy, []byte("a plain legacy fact\n"), 0o644); err != nil {
		t.Fatalf("write legacy fact: %v", err)
	}
	logBuf.Reset()
	opts2 := baseOpts()
	opts2.RuntimeReload = RuntimeReload{ReuseAssembly: res1.Assembly, PreviousPlan: FullReusePlan()}
	res2, err := BuildRuntime(context.Background(), opts2)
	if err != nil {
		t.Fatalf("round2 BuildRuntime: %v", err)
	}
	t.Cleanup(res2.Controller.Close)
	logs2 := logBuf.String()
	if !strings.Contains(logs2, "boot: assembly reuse") {
		t.Fatalf("round2 缺少 boot: assembly reuse 自报行：\n%s", logs2)
	}
	if !strings.Contains(logs2, "memory=false") {
		t.Fatalf("round2 store 已变却复用了过期 memory 快照（期望 memory=false）：\n%s", logs2)
	}
	migrated, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("read legacy fact: %v", err)
	}
	if !strings.Contains(string(migrated), "id:") || !strings.Contains(string(migrated), "revision:") {
		t.Fatalf("round2 的新鲜路径必须迁移 legacy 事实（未见 id/revision）：\n%s", string(migrated))
	}

	// 第三轮：store 自 round2 之后未变 ⇒ 指纹命中 ⇒ 复用 memory 快照。
	logBuf.Reset()
	opts3 := baseOpts()
	opts3.RuntimeReload = RuntimeReload{ReuseAssembly: res2.Assembly, PreviousPlan: FullReusePlan()}
	res3, err := BuildRuntime(context.Background(), opts3)
	if err != nil {
		t.Fatalf("round3 BuildRuntime: %v", err)
	}
	t.Cleanup(res3.Controller.Close)
	logs3 := logBuf.String()
	if !strings.Contains(logs3, "boot: assembly reuse") {
		t.Fatalf("round3 缺少 boot: assembly reuse 自报行：\n%s", logs3)
	}
	if !strings.Contains(logs3, "memory=true") {
		t.Fatalf("round3 store 未变却没复用 memory 快照（期望 memory=true）：\n%s", logs3)
	}
	if !strings.Contains(logs3, "prompt=true") {
		t.Fatalf("round3 未复用 prompt（期望 prompt=true）：\n%s", logs3)
	}
	if !strings.Contains(logs3, "shell=true") {
		t.Fatalf("round3 未复用 shell 解析（期望 shell=true）：\n%s", logs3)
	}
	// 复用轮的 memory 三段必须是空转：分段毫秒数在个位以内（真机上是
	// 21～29s vs 0ms 级的差）。
	for _, seg := range []string{"memory_migrate", "memory_pinned", "memory_load"} {
		ms, ok := promptSegmentMS(logs3, seg)
		if !ok {
			t.Fatalf("round3 的 prompt 分段缺 %s：\n%s", seg, logs3)
		}
		if ms > 5 {
			t.Fatalf("round3 的 %s 分段 = %dms（复用分支应为空转，≤5ms）：\n%s", seg, ms, logs3)
		}
	}
}

// promptSegmentMS 从「boot: prompt segments」行里取某个分段的毫秒数。
func promptSegmentMS(logs, segment string) (int64, bool) {
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "boot: prompt segments") {
			continue
		}
		idx := strings.Index(line, segment+"=")
		if idx < 0 {
			continue
		}
		rest := line[idx+len(segment)+1:]
		end := strings.Index(rest, "ms")
		if end <= 0 {
			continue
		}
		n, err := strconv.ParseInt(rest[:end], 10, 64)
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}

// TestAssemblyMemoryFingerprintDetectsStoreChange1683：指纹是复用的安全边界，
// 必须对「新增事实」「改写事实」「删除事实」三种变化都敏感。
func TestAssemblyMemoryFingerprintDetectsStoreChange1683(t *testing.T) {
	dir := t.TempDir()
	store := memory.StoreFor(dir, dir)
	if store.Fingerprint() == "" {
		t.Fatal("非空 store 的指纹不应为空串")
	}
	if err := os.MkdirAll(store.GlobalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := store.Fingerprint()

	fact := filepath.Join(store.GlobalDir, "f.md")
	if err := os.WriteFile(fact, []byte("---\nid: f\nrevision: 1\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	added := store.Fingerprint()
	if added == base {
		t.Fatal("新增事实后指纹未变")
	}
	// 改写：内容长度变化 ⇒ mtime/size 变化 ⇒ 指纹变化。
	if err := os.WriteFile(fact, []byte("---\nid: f\nrevision: 2\n---\n\nbody body body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := store.Fingerprint()
	if changed == added {
		t.Fatal("改写事实后指纹未变")
	}
	// 删除：指纹回到基线。
	if err := os.Remove(fact); err != nil {
		t.Fatal(err)
	}
	if store.Fingerprint() != base {
		t.Fatalf("删除事实后指纹未回到基线：%q != %q", store.Fingerprint(), base)
	}
}
