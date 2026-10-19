package runtimepolicy

// tl 982：todo/criteria 契约门禁（ContractPreconditionGuard）开关化默认 off
// （用户 2026-10-18 拍板：约束力≈零 + 893 替代就位 + 976 死锁实证）。本文件
// 钉死四条验收：off 态默认列表零拦截；on 态回退路径等价（既有两个拦截测试
// 已改显式 on）；setter 往返无粘滞；ConstraintGuard 外发门禁不受拆 gate 影响。

import (
	"encoding/json"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/taskcontract"
)

// 验收①：off 态（NewEngine 默认列表）todo/criteria 契约门禁零拦截——
// auth 面、二次生产写 multi-file 面、pathless 面三类原 deny 输入全部放行，
// 且决策里不残留 first_writer 理由。
func TestContractGateDefaultsOffZeroInterception(t *testing.T) {
	e := NewEngine(Constraints{})
	e.CommitReceipt(writeResult("internal/agent/agent.go"))
	cases := []struct {
		name string
		ctx  CallContext
	}{
		{"auth write without todo", authWriteCall()},
		{"second production target", writeCall("internal/agent/run_loop.go")},
		{"pathless writer after production write", CallContext{Profile: evidence.EffectProfile{Known: true, WorkspaceWrite: true}}},
	}
	for _, tc := range cases {
		got := e.BeforeTool(tc.ctx)
		if got.Action == GuardDeny {
			t.Fatalf("%s: default-off engine must not intercept: %+v", tc.name, got)
		}
		for _, reason := range got.Reasons {
			if reason == taskcontract.ReasonFirstWriter {
				t.Fatalf("%s: first_writer reason must be gone when off: %v", tc.name, got.Reasons)
			}
		}
	}
}

// 验收③：default-off + setter 往返钉住——默认零拦截；on 恢复门禁前 deny；
// off 再零拦截；再 on 再 deny。往返无粘滞，回退路径可信。
func TestContractGateSetterRoundTrip(t *testing.T) {
	e := NewEngine(Constraints{})
	auth := authWriteCall()
	if e.BeforeTool(auth).Action == GuardDeny {
		t.Fatal("factory default must leave the contract gate off")
	}
	e.SetContractPreconditionGuardEnabled(true)
	got := e.BeforeTool(auth)
	if got.Action != GuardDeny {
		t.Fatalf("on must restore the pre-982 deny: %+v", got)
	}
	e.SetContractPreconditionGuardEnabled(false)
	if e.BeforeTool(auth).Action == GuardDeny {
		t.Fatal("off after one round trip must not intercept")
	}
	e.SetContractPreconditionGuardEnabled(true)
	if got = e.BeforeTool(auth); got.Action != GuardDeny {
		t.Fatalf("on after a second round trip must deny again: %+v", got)
	}
}

// 验收②（ConstraintGuard 行为不变）：契约门禁 off 的同一默认列表里，
// push 外发命令识别照常拦截，且开关 on/off 两个态下表现一致——拆的是
// write gate 契约门禁，不是外发命令门禁。
func TestConstraintGuardStaysActiveWhenContractGateOff(t *testing.T) {
	e := NewEngine(Constraints{ForbidExternal: true})
	push := CallContext{
		ToolName: "bash",
		Args:     json.RawMessage(`{"command":"git push origin main"}`),
		Profile:  evidence.EffectProfile{Known: true},
	}
	if got := e.BeforeTool(push); got.Action != GuardDeny {
		t.Fatalf("external push must still deny with the contract gate off: %+v", got)
	}
	e.SetContractPreconditionGuardEnabled(true)
	if got := e.BeforeTool(push); got.Action != GuardDeny {
		t.Fatalf("external push must still deny with the contract gate on: %+v", got)
	}
}
