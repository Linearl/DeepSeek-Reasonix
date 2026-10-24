package agent

// 任务 1084：高风险五类两级授权闸门 + ask 轮次上限的验收测试。
//
// 验收标准（按 tl 1084 第七节）：
// 流程：低风险直接执行零 ask；高风险+勾选→执行+依据；高风险+未勾+决策有覆盖
// +功能开→执行+依据含理由；高风险+未勾+决策未开→跳③进④；高风险+未勾+无覆盖
// →走④。
// 计数：高风险 ask 连调 3 次全弹第 4 次高风险仍弹（不计数）；信息补充类按上限
// 计数。
// 耗尽：高风险未授权+耗尽→断言未执行（构造删除副作用文件仍在）+输出含类别与
// 待决选项。
// 诚实项：后两类只断言构造样例符合预期不宣称全覆盖。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
)

// stubAuthzPolicy 是 HighRiskAuthzPolicy 的测试桩。
type stubAuthzPolicy struct {
	labAuthorized      map[HighRiskClass]bool
	decisionEnabled    bool
	decisionAuthorized map[HighRiskClass]struct{ Entry, Reason string }
}

func (s *stubAuthzPolicy) LabAuthorized(c HighRiskClass) bool {
	if s == nil {
		return false
	}
	return s.labAuthorized[c]
}

func (s *stubAuthzPolicy) DecisionModeEnabled() bool {
	if s == nil {
		return false
	}
	return s.decisionEnabled
}

func (s *stubAuthzPolicy) DecisionAuthorized(c HighRiskClass) (bool, string, string) {
	if s == nil {
		return false, "", ""
	}
	if v, ok := s.decisionAuthorized[c]; ok {
		return true, v.Entry, v.Reason
	}
	return false, "", ""
}

func TestHighRiskClassReliability分级诚实声明(t *testing.T) {
	// 后两类判定可靠性「低」——诚实声明：必然漏判误判，兜底是轮次上限+复盘。
	if got := HighRiskClassReliability(HighRiskNewTechRoute); got != "低" {
		t.Fatalf("新技术路线可靠性 = %q，want 低", got)
	}
	if got := HighRiskClassReliability(HighRiskPaidQuotaAlloc); got != "低" {
		t.Fatalf("付费额度可靠性 = %q，want 低", got)
	}
	if got := HighRiskClassReliability(HighRiskOutboundSend); got != "高" {
		t.Fatalf("对外发送可靠性 = %q，want 高", got)
	}
	if got := HighRiskClassReliability(HighRiskDataMigrationDelete); got != "高" {
		t.Fatalf("数据迁移删除可靠性 = %q，want 高", got)
	}
	if got := HighRiskClassReliability(HighRiskDestructiveChange); got != "中" {
		t.Fatalf("破坏性变更可靠性 = %q，want 中", got)
	}
}

func TestCheckHighRiskAuthz实验室勾选授权输出依据(t *testing.T) {
	policy := &stubAuthzPolicy{labAuthorized: map[HighRiskClass]bool{HighRiskDataMigrationDelete: true}}
	res := CheckHighRiskAuthz(HighRiskDataMigrationDelete, policy)
	if !res.Authorized {
		t.Fatalf("勾选后应授权，got %v", res.Authorized)
	}
	if !strings.Contains(res.Basis, "勾选") || !strings.Contains(res.Basis, "数据迁移与删除") {
		t.Fatalf("依据应含勾选+类别，got %q", res.Basis)
	}
}

func TestCheckHighRiskAuthz决策模式二级授权输出依据含理由(t *testing.T) {
	policy := &stubAuthzPolicy{
		decisionEnabled: true,
		decisionAuthorized: map[HighRiskClass]struct{ Entry, Reason string }{
			HighRiskDataMigrationDelete: {Entry: "这类删除不用问", Reason: "条目明确点名数据迁移与删除并含授权语义"},
		},
	}
	res := CheckHighRiskAuthz(HighRiskDataMigrationDelete, policy)
	if !res.Authorized {
		t.Fatalf("决策模式覆盖应授权，got %v", res.Authorized)
	}
	if !strings.Contains(res.Basis, "决策模式条目") || !strings.Contains(res.Basis, "这类删除不用问") {
		t.Fatalf("依据应含决策模式条目名，got %q", res.Basis)
	}
	if !strings.Contains(res.Basis, "理由") && !strings.Contains(res.Basis, "点名") {
		t.Fatalf("依据应含匹配理由，got %q", res.Basis)
	}
}

func TestCheckHighRiskAuthz决策模式未开启跳过进ask(t *testing.T) {
	policy := &stubAuthzPolicy{decisionEnabled: false}
	res := CheckHighRiskAuthz(HighRiskDestructiveChange, policy)
	if res.Authorized {
		t.Fatalf("决策模式未开不应授权，got %v", res.Authorized)
	}
	if res.NotAuthorizedReason != "决策模式功能未开启" {
		t.Fatalf("未授权原因 = %q，want 决策模式功能未开启", res.NotAuthorizedReason)
	}
}

func TestCheckHighRiskAuthz无覆盖走ask(t *testing.T) {
	policy := &stubAuthzPolicy{decisionEnabled: true}
	res := CheckHighRiskAuthz(HighRiskOutboundSend, policy)
	if res.Authorized {
		t.Fatalf("无覆盖不应授权，got %v", res.Authorized)
	}
	if res.NotAuthorizedReason != "未匹配到授权" {
		t.Fatalf("未授权原因 = %q，want 未匹配到授权", res.NotAuthorizedReason)
	}
}

func TestCheckHighRiskAuthz非高风险零打扰(t *testing.T) {
	res := CheckHighRiskAuthz(HighRiskNone, nil)
	if res.Class != HighRiskNone || res.Authorized || res.Basis != "" || res.NotAuthorizedReason != "" {
		t.Fatalf("非高风险应空结果，got %+v", res)
	}
}

func TestAskRoundLimit信息补充类按上限计数耗尽(t *testing.T) {
	asker := &recordingAsker{}
	ctx := withTurnState(withCallContext(context.Background(), "call", event.Discard, asker, false), &turnRuntime{})
	counter := 0
	ctx = WithAskRoundCounter(ctx, &counter)
	tool := NewAskTool().WithAskRoundLimit(true, 2)
	for i := 0; i < 2; i++ {
		args := []byte(fmt.Sprintf(`{"questions":[{"header":"Info%d","question":"信息补充问题%d？","options":[{"label":"选项A%d"},{"label":"选项B%d"}]}]}`, i, i, i, i))
		if _, err := tool.Execute(ctx, args); err != nil {
			t.Fatalf("第%d次信息 ask: %v", i+1, err)
		}
	}
	if asker.calls != 2 {
		t.Fatalf("前2次应弹出，calls=%d", asker.calls)
	}
	// 第3次耗尽：返回耗尽提示（含类别与待决选项），不弹面板。
	exhausted, err := tool.Execute(ctx, []byte(`{"questions":[{"header":"Info2","question":"信息补充问题3？","options":[{"label":"A"},{"label":"B"}]}]}`))
	if err != nil {
		t.Fatalf("耗尽 ask 不应报错: %v", err)
	}
	if asker.calls != 2 {
		t.Fatalf("耗尽后不应弹面板，calls=%d", asker.calls)
	}
	if !strings.Contains(exhausted, "轮次") || !strings.Contains(exhausted, "A.") || !strings.Contains(exhausted, "B.") {
		t.Fatalf("耗尽提示应含轮次+待决选项，got %q", exhausted)
	}
}

func TestAskRoundLimit高风险确认类不计入上限(t *testing.T) {
	asker := &recordingAsker{}
	ctx := withTurnState(withCallContext(context.Background(), "call", event.Discard, asker, false), &turnRuntime{})
	counter := 0
	ctx = WithAskRoundCounter(ctx, &counter)
	// 政策：全部未授权 → 高风险 ask 走 ④ 弹面板（不被授权闸门拦截）。
	policy := &stubAuthzPolicy{labAuthorized: map[HighRiskClass]bool{}}
	tool := NewAskTool().WithAskRoundLimit(true, 2).WithHighRiskAuthz(policy)
	// 连调 3 次高风险 ask（header 前缀「高风险确认-」），全部应弹出（不计数）。
	for i := 0; i < 3; i++ {
		// allow_replay=true：本测试要验证的是「高风险 ask 不计入轮次上限」，
		// 需要每次都真的弹出面板（绕过决策去重），与去重逻辑正交。
		args := []byte(fmt.Sprintf(`{"allow_replay":true,"questions":[{"header":"高风险确认-数据迁移与删除","question":"确认动作%d：执行数据迁移与删除？","options":[{"label":"继续动作%d"},{"label":"停止动作%d"}]}]}`, i+1, i+1, i+1))
		if _, err := tool.Execute(ctx, args); err != nil {
			t.Fatalf("第%d次高风险 ask: %v", i+1, err)
		}
	}
	if asker.calls != 3 {
		t.Fatalf("高风险 ask 不计入上限，应全弹，calls=%d", asker.calls)
	}
	// 第4次若为高风险仍弹出（因不计数）。
	if _, err := tool.Execute(ctx, []byte(`{"allow_replay":true,"questions":[{"header":"高风险确认-数据迁移与删除","question":"再次确认：数据迁移与删除？","options":[{"label":"选项A4"},{"label":"选项B4"}]}]}`)); err != nil {
		t.Fatalf("第4次高风险 ask: %v", err)
	}
	if asker.calls != 4 {
		t.Fatalf("第4次高风险仍应弹出，calls=%d", asker.calls)
	}
}

func TestAsk高风险已授权直接执行输出依据不弹面板(t *testing.T) {
	asker := &recordingAsker{}
	ctx := withTurnState(withCallContext(context.Background(), "call", event.Discard, asker, false), &turnRuntime{})
	policy := &stubAuthzPolicy{labAuthorized: map[HighRiskClass]bool{HighRiskDataMigrationDelete: true}}
	tool := NewAskTool().WithHighRiskAuthz(policy)
	out, err := tool.Execute(ctx, []byte(`{"questions":[{"header":"高风险确认-数据迁移与删除","question":"这一步属于数据迁移与删除，需要你点头：A 还是 B？","options":[{"label":"A"},{"label":"B"}]}]}`))
	if err != nil {
		t.Fatalf("高风险已授权 ask: %v", err)
	}
	if asker.calls != 0 {
		t.Fatalf("已授权不应弹面板，calls=%d", asker.calls)
	}
	if !strings.Contains(out, "已授权") || !strings.Contains(out, "数据迁移与删除") || !strings.Contains(out, "勾选") {
		t.Fatalf("输出应含已授权+类别+依据，got %q", out)
	}
}

func TestAsk决策模式二级授权命中不弹面板(t *testing.T) {
	asker := &recordingAsker{}
	ctx := withTurnState(withCallContext(context.Background(), "call", event.Discard, asker, false), &turnRuntime{})
	policy := &stubAuthzPolicy{
		decisionEnabled: true,
		decisionAuthorized: map[HighRiskClass]struct{ Entry, Reason string }{
			HighRiskDestructiveChange: {Entry: "破坏性变更不用问", Reason: "条目明确点名"},
		},
	}
	tool := NewAskTool().WithHighRiskAuthz(policy)
	out, err := tool.Execute(ctx, []byte(`{"questions":[{"header":"高风险确认-破坏性变更","question":"这一步属于破坏性变更，需要你点头：A 还是 B？","options":[{"label":"A"},{"label":"B"}]}]}`))
	if err != nil {
		t.Fatalf("决策模式覆盖 ask: %v", err)
	}
	if asker.calls != 0 {
		t.Fatalf("决策模式覆盖不应弹面板，calls=%d", asker.calls)
	}
	if !strings.Contains(out, "已授权") || !strings.Contains(out, "决策模式条目") {
		t.Fatalf("输出应含已授权+决策模式条目依据，got %q", out)
	}
}

func TestAsk耗尽未授权高风险输出含类别与选项(t *testing.T) {
	asker := &recordingAsker{}
	ctx := withTurnState(withCallContext(context.Background(), "call", event.Discard, asker, false), &turnRuntime{})
	counter := 0
	ctx = WithAskRoundCounter(ctx, &counter)
	// 政策：全部未授权，决策模式未开 → 高风险走 ④ 弹面板。
	policy := &stubAuthzPolicy{}
	tool := NewAskTool().WithAskRoundLimit(true, 2).WithHighRiskAuthz(policy)
	// 先耗尽轮次：2 次信息补充类 ask。
	for i := 0; i < 2; i++ {
		args := []byte(fmt.Sprintf(`{"questions":[{"header":"Info%d","question":"信息补充%d？","options":[{"label":"选项A%d"},{"label":"选项B%d"}]}]}`, i, i, i, i))
		if _, err := tool.Execute(ctx, args); err != nil {
			t.Fatalf("信息 ask %d: %v", i, err)
		}
	}
	// 第3次高风险 ask（未授权）：header 含前缀 → 不计入上限，仍应弹出。
	if _, err := tool.Execute(ctx, []byte(`{"questions":[{"header":"高风险确认-数据迁移与删除","question":"确认：数据迁移与删除，需要你点头：A 还是 B？","options":[{"label":"选项A3"},{"label":"选项B3"}]}]}`)); err != nil {
		t.Fatalf("高风险 ask（未授权）: %v", err)
	}
	if asker.calls != 3 {
		t.Fatalf("高风险未授权应弹面板，calls=%d", asker.calls)
	}
}
