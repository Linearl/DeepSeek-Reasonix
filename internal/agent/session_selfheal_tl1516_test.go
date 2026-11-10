package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1516 现场（20261109 用户截图立项）：『问题咨询』会话顶部折叠占位
// 「[本地折叠] 已省略 419 条早期消息……原文在修复备份目录与改写前日志段中」
// 只说原文在哪（location），没给怎么恢复/查看的入口（route）。本件把占位文案
// 对齐 tl1504 自愈通知引导族（recovery.noticeSelfHealed，9b4133b70）：补
// 「设置 → 存储与路径 → 工作阶段版本与副本」面板路径（可复制的恢复入口）。
// 钉两条：
//
//	① 占位必须携带存储面板路径——文案回退成「只报位置不给路径」即红；
//	② 占位仍保留备份位置事实（pre-repair 目录 + 改写前日志段）——
//	   面板路径不能挤掉「原文在哪」的答案本身。
//
// 追溯（本件不修）：该折叠是 1506 Diet 误判同族（旧 base64÷4 判据虚高），
// 判据修复 5a452dbcc 已滚主线，随包装机即止。
func TestTL1516FoldPlaceholderCarriesRecoveryRoute(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "You are Reasonix, a coding agent."},
		{Role: provider.RoleUser, Content: strings.Repeat("early history line ", 400)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("old answer line ", 400)},
		{Role: provider.RoleUser, Content: "latest user question"},
	}
	folded, count := foldProjectionTail(msgs, 500)
	if count <= 0 {
		t.Fatalf("fold must act on an over-budget history (folded=%d)", count)
	}
	if len(folded) < 2 || folded[1].Role != provider.RoleUser {
		t.Fatalf("placeholder must sit right after the system header: len=%d", len(folded))
	}
	placeholder := folded[1].Content
	if !strings.Contains(placeholder, "[本地折叠]") {
		t.Fatalf("placeholder lost its fold marker: %q", placeholder)
	}
	// 钉①：恢复路径（tl1504 同族面板路径）——location 之外必须有 route。
	const route = "设置 → 存储与路径 → 工作阶段版本与副本"
	if !strings.Contains(placeholder, route) {
		t.Fatalf("fold placeholder must guide to the storage panel route %q for recovery; got %q", route, placeholder)
	}
	// 钉②：备份位置事实不丢——pre-repair 目录与改写前日志段仍要点名。
	if !strings.Contains(placeholder, "pre-repair") {
		t.Fatalf("fold placeholder must still name the sibling pre-repair backup directory; got %q", placeholder)
	}
	if !strings.Contains(placeholder, "日志段") {
		t.Fatalf("fold placeholder must still name the pre-rewrite log segments; got %q", placeholder)
	}
	if !strings.Contains(placeholder, "原文未删") {
		t.Fatalf("fold placeholder must state the originals are kept (原文未删); got %q", placeholder)
	}
}
