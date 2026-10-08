package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// 任务 426 契约守卫：builtinSafety（safety.go）是内置工具安全属性的单一事实
// 源，本文件钉死三件事——
//
//  1. 清单守卫：每个注册的内置工具必须在表里有一行登记（新增工具漏登记即本
//     测试红，防「写死清单过时」）。
//  2. 无漂移：登记的 ReadOnly 必须与 Tool.ReadOnly() 一致——同一事实不许出现
//     第二个会漂移的副本。
//  3. 等价性：FileMutation / ConcurrentSafe=false / Destructive 三个登记集合
//     与收敛前的三份散落清单逐一等价（permission.IsFileMutationTool 的七个
//     名字、parallelisableCall 的五个 barrier 名单、kill/restart 危险面）。
//     改任何一行登记，先改这里的期望并在交付说明里注明行为面。
func TestBuiltinSafetyMetadataContract(t *testing.T) {
	builtins := Builtins()
	if len(builtins) == 0 {
		t.Fatal("no built-in tools registered; registration wiring is broken")
	}
	for _, bt := range builtins {
		name := bt.Name()
		spec, ok := builtinSafety[name]
		if !ok {
			t.Errorf("built-in %q has no safety metadata row in safety.go; register it so the single source covers it (invent nothing: copy the closest existing class and say why in the commit)", name)
			continue
		}
		// 无漂移：ReadOnly 登记必须与接口一致。
		if spec.ReadOnly != bt.ReadOnly() {
			t.Errorf("built-in %q safety row ReadOnly=%v disagrees with ReadOnly()=%v; the table must mirror the interface, never replace it", name, spec.ReadOnly, bt.ReadOnly())
		}
		// 只读工具不允许登记破坏性。
		if spec.ReadOnly && spec.Destructive {
			t.Errorf("built-in %q is registered ReadOnly+Destructive; that combination is a classification bug", name)
		}
		// 登记为可并发的工具必须只读。
		if spec.ConcurrentSafe && !spec.ReadOnly {
			t.Errorf("built-in %q is registered ConcurrentSafe but not ReadOnly; parallel fan-out is read-only only", name)
		}
	}
	// 运行期注册（boot reg.Add、不在编译期名册里）的工具也必须有登记行。
	for _, name := range []string{"ui_interact", "restart_update"} {
		if _, ok := builtinSafety[name]; !ok {
			t.Errorf("runtime-registered tool %q has no safety metadata row in safety.go", name)
		}
	}
}

// 等价性快照：三个登记集合与收敛前散落清单逐字面等价。刻意写成名字列表，
// 让任何一侧改动都在 diff 里可读。
func TestSafetyMetadataEquivalenceWithLegacyLists(t *testing.T) {
	legacyFileMutation := []string{"write_file", "edit_file", "multi_edit", "move_file", "notebook_edit", "delete_range", "delete_symbol"}
	for _, name := range legacyFileMutation {
		if !SafetyOfName(name).FileMutation {
			t.Errorf("%q was file-mutating before the convergence (permission.IsFileMutationTool) but its metadata says otherwise", name)
		}
	}
	legacyBarriers := []string{"complete_step", "todo_write", "wait", "bash_output", "compress"}
	for _, name := range legacyBarriers {
		spec := SafetyOfName(name)
		if !spec.ReadOnly || spec.ConcurrentSafe {
			t.Errorf("%q was a serial barrier before the convergence (parallelisableCall) but its metadata marks it parallel-safe", name)
		}
	}
	legacyDestructive := []string{"bash", "kill_shell", "restart_update", "restart_and_update"}
	for _, name := range legacyDestructive {
		if !SafetyOfName(name).Destructive {
			t.Errorf("%q was in the destructive class before the convergence but its metadata says otherwise", name)
		}
	}
	// 反向：非文件变更工具不得领 FileMutation（抽样已知只读/宿主面）。
	for _, name := range []string{"bash", "read_file", "web_fetch", "complete_step", "screenshot", "update_goal"} {
		if SafetyOfName(name).FileMutation {
			t.Errorf("%q must not claim FileMutation", name)
		}
	}
	// 未知名：零值（最保守：串行、不认领文件变更）。
	if got := SafetyOfName("no_such_tool"); got != (SafetySpec{}) {
		t.Errorf("unknown tool must yield the zero spec, got %+v", got)
	}
}

// failClosedTool 是不在登记表里的动态工具（MCP/能力代理的替身）。
type failClosedTool struct {
	name     string
	readOnly bool
}

func (f failClosedTool) Name() string            { return f.name }
func (f failClosedTool) Schema() json.RawMessage { return nil }
func (f failClosedTool) Description() string     { return "" }
func (f failClosedTool) ReadOnly() bool          { return f.readOnly }
func (f failClosedTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return "", nil
}

// SafetyOf 推导必须与收敛前兜底行为一致：只读未知工具视为可并发（原
// partitionToolCalls 的 return target.ReadOnly()），非只读一律串行；表内
// 工具以登记为准（含与 ReadOnly() 的一致性，由契约钉死）。
func TestSafetyOfFallbackDerivation(t *testing.T) {
	reader := SafetyOf(failClosedTool{name: "mcp__srv__probe_read", readOnly: true})
	if !reader.ReadOnly || !reader.ConcurrentSafe || reader.RiskLevel != RiskLow || reader.FileMutation || reader.Destructive {
		t.Errorf("read-only fallback drifted: %+v", reader)
	}
	writer := SafetyOf(failClosedTool{name: "mcp__srv__probe_write", readOnly: false})
	if writer.ReadOnly || writer.ConcurrentSafe || writer.RiskLevel != RiskMedium || writer.FileMutation || writer.Destructive {
		t.Errorf("writer fallback drifted: %+v", writer)
	}
	// 表内工具：实例读取走登记（barrier 的 ConcurrentSafe=false 必须生效，
	// 否则证据台账会被并行扇出）。
	if spec := SafetyOf(failClosedTool{name: "complete_step", readOnly: true}); spec.ConcurrentSafe {
		t.Errorf("complete_step must stay serial via its registered row, got %+v", spec)
	}
	if spec := SafetyOf(failClosedTool{name: "read_file", readOnly: true}); !spec.ConcurrentSafe || spec.RiskLevel != RiskLow {
		t.Errorf("read_file registration not honored through the instance path: %+v", spec)
	}
}
