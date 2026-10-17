package agent

// 任务 971（工具优化聚合页）实施核对点：压缩时刻工具重建须原子——registry
// 重建（SetProviderVisibleTools）+ schema 组装（providerToolSchemas，喂给
// req.Tools 的同一函数）+ 请求发出三者一致。
//
// 断言三面：
//  ① 请求面一致：providerToolSchemas() 返回的正是 registry 可见面
//     （Schemas()）——schema 组装与 registry 状态同源，不存在第二份拷贝。
//  ② 压缩不撕：一次 compaction pass（CompactNow）不重建也不撕裂工具面——
//     pass 前后可见名集合与 schemaRev 逐位一致（红线① tools 会话内不可变，
//     压缩边界之外不许中途换面）。
//  ③ 换面原子：SetProviderVisibleTools 是全有或全无——并发 Schemas() 读者
//     在换面风暴中只能观测到「完整旧面」或「完整新面」，永不半新半旧。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// surfaceTool is a minimal tool.Tool for surface-shape tests only.
type surfaceTool struct{ name string }

func (s surfaceTool) Name() string            { return s.name }
func (s surfaceTool) Description() string     { return "" }
func (s surfaceTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s surfaceTool) ReadOnly() bool          { return true }
func (s surfaceTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}

func surfaceNames(schemas []provider.ToolSchema) []string {
	names := make([]string, 0, len(schemas))
	for _, s := range schemas {
		names = append(names, s.Name)
	}
	return names
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newSurfaceAgent wires an agent whose registry carries the named tools and is
// fully visible (nil restriction). The provider is a fakeProvider that accepts
// tools (no ToolSupportProvider ⇒ SupportsTools true).
func newSurfaceAgent(t *testing.T, names ...string) (*Agent, *tool.Registry) {
	t.Helper()
	reg := tool.NewRegistry()
	for _, n := range names {
		reg.Add(surfaceTool{name: n})
	}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
	}}
	a := New(&fakeProvider{reply: "digest"}, reg, sess, Options{
		ContextWindow: 20_000, CompactRatio: 0.5, RecentKeep: 1,
		SessionPath: t.TempDir() + "/session.jsonl", WorkspaceID: "ws", ModelRef: "p/m",
	}, event.Discard)
	return a, reg
}

// ① 请求面与 registry 可见面同源一致。
func TestToolSurfaceRequestMatchesRegistry(t *testing.T) {
	a, reg := newSurfaceAgent(t, "read_file", "multi_edit", "restart_update")
	reg.SetProviderVisibleTools([]string{"read_file", "multi_edit"})
	got := surfaceNames(a.providerToolSchemas())
	want := surfaceNames(reg.Schemas())
	if !sameNames(got, want) {
		t.Fatalf("providerToolSchemas = %v, registry Schemas = %v (must be identical)", got, want)
	}
	if !sameNames(got, []string{"multi_edit", "read_file"}) {
		t.Fatalf("visible surface = %v, want sorted [multi_edit read_file]", got)
	}
}

// ② 压缩 pass 不重建也不撕裂工具面：pass 前后可见名集合与 schemaRev 逐位一致。
func TestCompactionDoesNotTearToolSurface(t *testing.T) {
	a, reg := newSurfaceAgent(t, "read_file", "multi_edit", "restart_update")
	reg.SetProviderVisibleTools([]string{"read_file", "multi_edit"})
	before := surfaceNames(a.providerToolSchemas())
	revBefore := reg.SchemaRevision()
	if err := a.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	after := surfaceNames(a.providerToolSchemas())
	revAfter := reg.SchemaRevision()
	if !sameNames(before, after) {
		t.Fatalf("tool surface changed across compaction: before=%v after=%v", before, after)
	}
	if revBefore != revAfter {
		t.Fatalf("schemaRev moved across compaction without a rebuild: %d → %d", revBefore, revAfter)
	}
}

// ③ 换面原子：并发读者在 SetProviderVisibleTools 风暴中只观测完整旧面或
// 完整新面，永不半新半旧；每次换面恰好 bump schemaRev 一次。
func TestToolSurfaceSwapIsAtomicUnderConcurrentReaders(t *testing.T) {
	_, reg := newSurfaceAgent(t, "a1", "a2", "a3", "b1", "b2", "b3")
	faceA := []string{"a1", "a2", "a3"}
	faceB := []string{"b1", "b2", "b3"}
	reg.SetProviderVisibleTools(faceA)

	var stop atomic.Bool
	var torn atomic.Int64
	var wg sync.WaitGroup
	// 读者：反复 Schemas()，每次必须是完整 faceA 或完整 faceB。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				names := surfaceNames(reg.Schemas())
				if !sameNames(names, faceA) && !sameNames(names, faceB) {
					torn.Add(1)
				}
			}
		}()
	}
	// 写者：交替换面若干轮。
	revBefore := reg.SchemaRevision()
	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			reg.SetProviderVisibleTools(faceB)
		} else {
			reg.SetProviderVisibleTools(faceA)
		}
	}
	stop.Store(true)
	wg.Wait()
	if got := torn.Load(); got != 0 {
		t.Fatalf("observed %d torn (partial) surface reads; swap must be atomic", got)
	}
	// 200 次换面 ⇒ schemaRev 恰好 +200（每次全有或全无，无多余 bump）。
	if got := reg.SchemaRevision(); got != revBefore+200 {
		t.Fatalf("schemaRev = %d, want %d (exactly one bump per swap)", got, revBefore+200)
	}
}
