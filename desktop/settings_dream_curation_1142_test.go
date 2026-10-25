package main

import (
	"strings"
	"testing"
)

// tl 1142（用户 2026-10-24 反馈）：Dream 预设任务旧 prompt 只覆盖「写入」，
// 缺记忆整理原则，导致从预设新建的任务只会新增不会查重归并。这里钉住预设
// prompt 必须包含协调线 2026-10-24 七条整理原则的等价语义（措辞可压缩，
// 语义不可缺）。运行实例（heartbeat-tasks.json）已由协调线手工带上七条，
// 仓库源（本常量）是用户新建入口的权威模板，两者必须同源。
func TestDreamCurationPromptContainsSevenCurationPrinciples(t *testing.T) {
	required := []struct {
		label string
		need  []string // 每组关键词任一命中即算覆盖
	}{
		{"① 查重优先/合并真重复", []string{"查重优先", "真重复"}},
		{"② 过时更新不新建", []string{"更新不新建", "原条目上"}},
		{"③ 修正回写原条目", []string{"回写原条目", "已被"}},
		{"④ 战役状态归并压缩", []string{"归并压缩", "最新状态"}},
		{"⑤ 偏好/feedback 不删", []string{"feedback 类不删", "长期资产"}},
		{"⑥ 判据/坑位同族关联", []string{"同族", "关联"}},
		{"⑦ 合并组汇报格式", []string{"合并组：A+B→C"}},
	}
	for _, item := range required {
		found := false
		for _, kw := range item.need {
			if strings.Contains(dreamCurationTaskPrompt, kw) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("dream preset prompt missing principle %s (keywords %v)", item.label, item.need)
		}
	}
	// 处置通道与安全边界必须点名 memory 工具（禁止直接改文件）。
	for _, kw := range []string{"memory:remember", "memory:forget", "禁止直接改"} {
		if !strings.Contains(dreamCurationTaskPrompt, kw) {
			t.Fatalf("dream preset prompt missing action/safety keyword %q", kw)
		}
	}
}

// tl 1142：新建 heartbeat 任务的 approvalMode 跟随全局默认，但 auto 降级为
// yolo——无人值守场景 auto 的计划询问无人在场等于任务卡死；ask 原样保留
// （安全档）；未知/空值随 Normalize 归一为 ask，与设置页显示的默认一致。
func TestHeartbeatApprovalFromGlobalDefault(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ask", "ask"},
		{"auto", "yolo"},
		{"yolo", "yolo"},
		{"", "ask"},
		{"garbage", "ask"},
		{"AUTO", "yolo"},
		{" Ask ", "ask"},
	}
	for _, tc := range cases {
		if got := heartbeatApprovalFromGlobalDefault(tc.in); got != tc.want {
			t.Fatalf("heartbeatApprovalFromGlobalDefault(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
