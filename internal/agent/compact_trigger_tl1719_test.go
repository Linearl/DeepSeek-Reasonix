package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1719 压缩入口归一化钉：五个压缩入口收敛到同一折叠核心 + trigger 元数据
// 标签。手动家族四个标签（manual/ui/slash/serve）共享 manual 行为类语义；
// 本文件钉三层：①入口×标签矩阵（事件与 receipt 归因到具体入口）②行为一致
// （同状态四标签折叠消息数一致）③类行为纯函数（新标签与 manual 同值）。

// tl1719EntryLabels 是手动家族的入口标签全集（不含 manual 本身——它是
// guardian/SummarizeFrom 等非五入口手动道的既有标签）。
var tl1719EntryLabels = []string{CompactionTriggerManual, CompactionTriggerUI, CompactionTriggerSlash, CompactionTriggerServe}

func tl1719NewCompactableAgent(t *testing.T, sink event.Sink) *Agent {
	t.Helper()
	prov := &fakeProvider{reply: "tl1719 digest"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("more work\n", 800)},
		{Role: provider.RoleUser, Content: "tail"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}
	return New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 20_000, CompactRatio: 0.5, RecentKeep: 2,
	}, sink)
}

// TestTl1719EntryTriggerMatrix 钉：手动家族每个入口标签触发 CompactNowWithTrigger
// 后，CompactionStarted/CompactionDone 事件与维护 receipt 的 Trigger 字段都
// 精确等于入口标签，且投影真实推进（核心函数被调的证据）。
func TestTl1719EntryTriggerMatrix(t *testing.T) {
	for _, label := range tl1719EntryLabels {
		t.Run(label, func(t *testing.T) {
			var got []event.Event
			sink := event.FuncSink(func(e event.Event) { got = append(got, e) })
			a := tl1719NewCompactableAgent(t, sink)
			if err := a.CompactNowWithTrigger(context.Background(), "", label); err != nil {
				t.Fatalf("CompactNowWithTrigger(%q): %v", label, err)
			}
			started, done := -1, -1
			for i, e := range got {
				switch e.Kind {
				case event.CompactionStarted:
					started = i
					if e.Compaction.Trigger != label {
						t.Fatalf("started trigger = %q, want %q", e.Compaction.Trigger, label)
					}
				case event.CompactionDone:
					done = i
					if e.Compaction.Trigger != label {
						t.Fatalf("done trigger = %q, want %q", e.Compaction.Trigger, label)
					}
					if e.Compaction.Messages == 0 {
						t.Fatalf("done event carries no folded message count: %+v", e.Compaction)
					}
				}
			}
			if started < 0 || done < 0 {
				t.Fatalf("missing compaction events: started=%d done=%d (emitted %d events)", started, done, len(got))
			}
			if started > done {
				t.Fatalf("started event after done: %d > %d", started, done)
			}
			if ver := a.currentProjectionVersion(); ver != 1 {
				t.Fatalf("projection version = %d, want 1 (core did not install the fold)", ver)
			}
			snap := a.ContextMaintenanceSnapshot()
			if snap.LastReceipt == nil || snap.LastReceipt.Trigger != label {
				t.Fatalf("receipt trigger = %+v, want %q", snap.LastReceipt, label)
			}
		})
	}
}

// TestTl1719EntryBehaviorParity 钉：同一会话同状态下手动家族四入口折叠的
// 消息数量级一致（同一核心+同一行为类分支 ⇒ 完全一致；不一致即入口私有
// 逻辑回潮）。
func TestTl1719EntryBehaviorParity(t *testing.T) {
	folded := map[string]int{}
	for _, label := range tl1719EntryLabels {
		var got []event.Event
		sink := event.FuncSink(func(e event.Event) { got = append(got, e) })
		a := tl1719NewCompactableAgent(t, sink)
		if err := a.CompactNowWithTrigger(context.Background(), "", label); err != nil {
			t.Fatalf("CompactNowWithTrigger(%q): %v", label, err)
		}
		for _, e := range got {
			if e.Kind == event.CompactionDone {
				folded[label] = e.Compaction.Messages
			}
		}
	}
	base := folded[CompactionTriggerManual]
	if base == 0 {
		t.Fatalf("baseline manual fold folded nothing: %+v", folded)
	}
	for _, label := range []string{CompactionTriggerUI, CompactionTriggerSlash, CompactionTriggerServe} {
		if folded[label] != base {
			t.Fatalf("entry %q folded %d messages, manual folded %d — entry-private behavior divergence", label, folded[label], base)
		}
	}
}

// TestTl1719ManualClassPureFunctions 钉：核心全部 manual 类分支经
// IsManualClassTrigger 判定后，ui/slash/serve 与 manual 在剪枝门、摘要预算、
// 落地判据上返回完全一致。
func TestTl1719ManualClassPureFunctions(t *testing.T) {
	for _, label := range tl1719EntryLabels {
		if !IsManualClassTrigger(label) {
			t.Fatalf("%q should be manual-class", label)
		}
		if got, want := shouldPruneBeforeFold(label, true), shouldPruneBeforeFold(CompactionTriggerManual, true); got != want {
			t.Fatalf("shouldPruneBeforeFold(%q, over=true) = %v, manual = %v", label, got, want)
		}
		if got, want := shouldPruneBeforeFold(label, false), shouldPruneBeforeFold(CompactionTriggerManual, false); got != want {
			t.Fatalf("shouldPruneBeforeFold(%q, over=false) = %v, manual = %v", label, got, want)
		}
		policy := ContextPreparePolicy{Trigger: label}
		if got, want := maxSummariesFor(policy, true), maxSummariesFor(ContextPreparePolicy{Trigger: CompactionTriggerManual}, true); got != want {
			t.Fatalf("maxSummariesFor(%q, over=true) = %d, manual = %d", label, got, want)
		}
		if got, want := maxSummariesFor(policy, false), maxSummariesFor(ContextPreparePolicy{Trigger: CompactionTriggerManual}, false); got != want {
			t.Fatalf("maxSummariesFor(%q, over=false) = %d, manual = %d", label, got, want)
		}
		if got, want := foldLanded(policy, 100, 200, 300), foldLanded(ContextPreparePolicy{Trigger: CompactionTriggerManual}, 100, 200, 300); got != want {
			t.Fatalf("foldLanded(%q) = %v, manual = %v", label, got, want)
		}
	}
	for _, other := range []string{CompactionTriggerPressure, CompactionTriggerOverflow, CompactionTriggerTool, CompactionTriggerModelRanges, CompactionTriggerSnip, "auto", ""} {
		if IsManualClassTrigger(other) {
			t.Fatalf("%q must not be manual-class", other)
		}
	}
}

// TestTl1719NonManualLabelNormalizes 钉：手动面入口（CompactNowWithTrigger）
// 对非手动家族标签防御性归一化为 manual，而非按误标执行一条 pressure/tool
// 语义的 pass。
func TestTl1719NonManualLabelNormalizes(t *testing.T) {
	var got []event.Event
	sink := event.FuncSink(func(e event.Event) { got = append(got, e) })
	a := tl1719NewCompactableAgent(t, sink)
	if err := a.CompactNowWithTrigger(context.Background(), "", CompactionTriggerTool); err != nil {
		t.Fatalf("CompactNowWithTrigger(tool): %v", err)
	}
	for _, e := range got {
		if e.Kind == event.CompactionStarted && e.Compaction.Trigger != CompactionTriggerManual {
			t.Fatalf("normalized trigger = %q, want %q", e.Compaction.Trigger, CompactionTriggerManual)
		}
	}
}

// tl1719CompressStub 是 capability list 枚举钉用的 compress 占位工具——
// agent 包不 import builtin，只要求名字一致（过滤逻辑只看 ToolName）。
type tl1719CompressStub struct{ name string }

func (s tl1719CompressStub) Name() string         { return s.name }
func (s tl1719CompressStub) Description() string  { return "compress stub" }
func (tl1719CompressStub) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (tl1719CompressStub) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (tl1719CompressStub) ReadOnly() bool { return true }

// TestTl1719CapabilityListEnumeratesCompress 钉（工具发现性缺口 86→87）：
// compress 在 provider-visible 表面上时也必须出现在 use_capability list
// 枚举中（发现性例外），而其余 provider-visible 核心工具维持过滤不重复列出。
func TestTl1719CapabilityListEnumeratesCompress(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(tl1719CompressStub{name: tool.CompressToolName})
	reg.Add(tl1719CompressStub{name: "bash"})
	reg.Add(tl1719CompressStub{name: "subagent_spawn_hidden"})
	reg.SetProviderVisibleTools([]string{tool.CompressToolName, "bash"})

	frontend := newTL1719ListFrontend(t, reg)
	out, err := frontend.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Capabilities []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode list result: %v\n%s", err, out)
	}
	var sawCompress, sawBash bool
	for _, c := range payload.Capabilities {
		switch c.ID {
		case "tool:compress":
			sawCompress = true
		case "tool:bash":
			sawBash = true
		}
	}
	if !sawCompress {
		t.Fatalf("capability list does not enumerate compress (discoverability gap):\n%s", out)
	}
	if sawBash {
		t.Fatalf("provider-visible core tool bash should stay filtered from list:\n%s", out)
	}
}

// newTL1719ListFrontend 用与既有 use_capability list 测试相同的运行时装配
// 构造一个只挂本地 catalog 的 frontend。
func newTL1719ListFrontend(t *testing.T, reg *tool.Registry) tool.Tool {
	t.Helper()
	t.Setenv("REASONIX_CACHE_HOME", t.TempDir())
	catalogFn := func() capability.Catalog {
		return capability.BuildCatalog(capability.CatalogOptions{
			Tools: reg.AllContractEntries(),
		})
	}
	host := plugin.NewHost()
	defer host.Close()
	runtime := NewMCPCapabilityRuntime(context.Background(), host, nil, reg, catalogFn)
	return runtime.NewFrontend(nil, nil)
}
