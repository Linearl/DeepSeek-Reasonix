package agent

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/permission"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务 426 验收断言：元数据单一真源的三个消费者（并发分组、权限判定、UI
// 展示）行为与同一份 SafetySpec 同步。若任何一处被改回局部硬编码（或读了
// 别的来源），元数据表变化时三处就会失步——本测试对每个注册内置工具逐名
// 断言三处行为都等于其登记值，失步即红。

// TestSafetyConsumersStayInSyncWithMetadata walks every registered built-in
// and asserts all three consumers read the same spec:
//
//  1. 并发分组：partition 的 parallelisableCall 结果 == ReadOnly&&ConcurrentSafe
//     （bash 走 BatchClassifier 按参数分级，另用只读命令验证分级路径仍在位）。
//  2. 权限判定：IsFileMutationTool 的规则面（"Edit" 规则能否按路径匹配）==
//     FileMutation。
//  3. UI 展示：applySafetyMeta 填充的事件字段 == RiskLevel/Destructive。
func TestSafetyConsumersStayInSyncWithMetadata(t *testing.T) {
	reg := tool.NewRegistry()
	for _, bt := range tool.Builtins() {
		reg.Add(bt)
	}
	for _, bt := range tool.Builtins() {
		name := bt.Name()
		spec := tool.SafetyOf(bt)

		// 1. 并发分组消费。带 BatchClassifier 的工具按参数分级优先、不吃静态
		// 元数据，单独在 TestClassifierToolsKeepPriorityOverStaticMetadata
		// 里验证分级路径。
		if _, classified := bt.(tool.BatchClassifier); !classified {
			got := parallelisableCall(reg, provider.ToolCall{Name: name, Arguments: "{}"})
			want := spec.ReadOnly && spec.ConcurrentSafe
			if got != want {
				t.Errorf("%q: concurrency grouping drifted from metadata (partition=%v, spec.ReadOnly=%v ConcurrentSafe=%v)", name, got, spec.ReadOnly, spec.ConcurrentSafe)
			}
		}

		// 2. 权限判定消费："Edit" 规则按路径匹配 = 文件变更面。
		ruled := permission.RuleMatchesString("Edit", name, "/tmp/sync-probe")
		if ruled != spec.FileMutation {
			t.Errorf("%q: permission rule surface drifted from metadata (Edit-rule match=%v, spec.FileMutation=%v)", name, ruled, spec.FileMutation)
		}

		// 3. UI 展示消费。
		var ev event.Tool
		applySafetyMeta(&ev, name)
		if ev.RiskLevel != string(spec.RiskLevel) || ev.Destructive != spec.Destructive {
			t.Errorf("%q: UI event fields drifted from metadata (riskLevel=%q destructive=%v, spec=%q/%v)", name, ev.RiskLevel, ev.Destructive, spec.RiskLevel, spec.Destructive)
		}
	}
}

// classifiedTool 是带按参数分级的动态工具替身（能力代理同款接口面）：分级
// 结果必须优先于静态元数据——这是收敛时保持的判定顺序。
type classifiedTool struct {
	name       string
	parallelOK bool
}

func (c classifiedTool) Name() string            { return c.name }
func (c classifiedTool) Schema() json.RawMessage { return nil }
func (c classifiedTool) Description() string     { return "" }
func (c classifiedTool) ReadOnly() bool          { return false }
func (c classifiedTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}
func (c classifiedTool) ClassifyCall(raw json.RawMessage) tool.CallClass {
	return tool.CallClass{Known: true, ReadOnly: c.parallelOK, ParallelSafe: c.parallelOK}
}

// 带 BatchClassifier 的工具按参数分级优先，静态元数据不得抢先拦截分级——
// 分级说可并行即并行，分级说不可即串行，与收敛前的判定顺序一致。裸 bash
// 不实现分类器：基线行为就是串行（ReadOnly=false），收敛不得改变。
func TestClassifierToolsKeepPriorityOverStaticMetadata(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(classifiedTool{name: "classified_ok", parallelOK: true})
	reg.Add(classifiedTool{name: "classified_no", parallelOK: false})
	if !parallelisableCall(reg, provider.ToolCall{Name: "classified_ok", Arguments: `{}`}) {
		t.Error("classifier verdict parallel-safe must stay parallelisable (metadata must not preempt the per-call class)")
	}
	if parallelisableCall(reg, provider.ToolCall{Name: "classified_no", Arguments: `{}`}) {
		t.Error("classifier verdict not-parallel-safe must stay serial")
	}
	if bashTool, ok := tool.LookupBuiltin("bash"); ok {
		reg.Add(bashTool)
		if parallelisableCall(reg, provider.ToolCall{Name: "bash", Arguments: `{"command":"git status"}`}) {
			t.Error("plain bash calls were serial before the convergence and must stay serial")
		}
	}
}
