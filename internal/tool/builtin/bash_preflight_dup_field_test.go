package builtin

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// tl-1213 A1 构造实证：生成层把 bash 的 command 字段拆成两个同名 parameter
// （第二个为空串）时，encoding/json 取后值 ⇒ preflight 校验拒绝，且执行元
// 数据必须是 state=not_run / failurePhase=preflight（命令根本没跑）——控制
// 器看门（internal/control/turn_preflight_stall_test.go）按这个事件形态置标
// 记并自动 abort 卡死的 turn。
func TestBashDuplicateCommandFieldFailsPreflightNotRun(t *testing.T) {
	b := deleteBash(bashShell(), t.TempDir(), &deleteGateTerminal{})
	// 两份同名 command 键：第二份空串正是 2026-10-28 事故的坏参数形态。
	args := []byte(`{"command":"echo hi","command":""}`)
	res, err := b.ExecuteDetailed(context.Background(), args)
	if err == nil {
		t.Fatal("duplicate command fields (last empty) must fail the preflight")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("err = %v, want the preflight command-required refusal", err)
	}
	if res.Execution == nil {
		t.Fatal("preflight refusal must carry execution metadata")
	}
	if res.Execution.State != tool.ShellStateNotRun || res.Execution.FailurePhase != tool.ShellPhasePreflight {
		t.Fatalf("execution = state %q phase %q, want not_run/preflight (nothing ran)",
			res.Execution.State, res.Execution.FailurePhase)
	}
	// 事件面镜像一致性：controller 看门消费的是 event.ShellExecution 的同名字段值。
	if tool.ShellStateNotRun != "not_run" || tool.ShellPhasePreflight != "preflight" {
		t.Fatalf("shell constant drift: %q/%q no longer match the event wire values", tool.ShellStateNotRun, tool.ShellPhasePreflight)
	}
}
