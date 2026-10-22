package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// draftRewriteAgent builds an Agent whose auxiliary channel is a scripted
// mock provider, exactly like the terminal fixtures do.
func draftRewriteAgent(t *testing.T, turns ...testutil.Turn) (*Agent, *testutil.MockProvider) {
	t.Helper()
	p := testutil.NewMock("rewrite-mock", turns...)
	a := New(p, tool.NewRegistry(), NewSession(""), Options{}, &recordSink{})
	return a, p
}

// 任务 1024 验收（辅助通道面）：
// ① 改写请求走辅助通道——不改写会话消息（Snapshot 前后一致）、不产生
//    transcript 消息；
// ② 请求带用户草稿正文与系统规则，返回辅助模型的改写文本；
// ③ 上下文尾巴超预算时按尾部裁剪（保留最新内容）；预算=档位参数
//    （任务 1052：0=默认 10KB）；
// ④ 空草稿/流失败/空输出一律 ("", err)，调用方可静默降级。
func TestRewriteDraftAuxiliaryOnlyAndReturnsText(t *testing.T) {
	a, p := draftRewriteAgent(t, testutil.Turn{Text: "改写后的草稿"})
	before := len(a.Session().Snapshot())

	out, err := a.RewriteDraft(context.Background(), "帮我看看那个报错", "用户上一条：CI 又红了", 0)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if out != "改写后的草稿" {
		t.Fatalf("rewritten = %q, want the scripted text", out)
	}
	if after := len(a.Session().Snapshot()); after != before {
		t.Fatalf("the auxiliary rewrite must not touch the session: %d -> %d", before, after)
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want exactly 1", len(reqs))
	}
	if !strings.Contains(reqs[0].Messages[0].Content, "帮我看看那个报错") {
		t.Fatalf("request must carry the draft: %q", reqs[0].Messages[0].Content)
	}
	if !strings.Contains(reqs[0].Messages[0].Content, "不新增需求") {
		t.Fatalf("request must carry the rewrite rules: %q", reqs[0].Messages[0].Content)
	}
	if reqs[0].MaxTokens <= 0 {
		t.Fatalf("rewrite must bound its output budget: %+v", reqs[0])
	}
}

func TestRewriteDraftTrimsContextTailAtTheFront(t *testing.T) {
	a, p := draftRewriteAgent(t, testutil.Turn{Text: "ok"})
	tail := strings.Repeat("a", DraftRewriteBudgetBytes-10) + "\n最近一句：用户在等 CI 结果"
	// budgetKB=0 → 默认 10KB（任务 1024 旧行为）。
	if _, err := a.RewriteDraft(context.Background(), "草稿", tail, 0); err != nil {
		t.Fatal(err)
	}
	sent := p.Requests()[0].Messages[0].Content
	if strings.Contains(sent, strings.Repeat("a", 2000)) {
		t.Fatalf("an over-budget context tail must be trimmed from the FRONT (oldest bytes dropped): %d bytes", len(sent))
	}
	if !strings.Contains(sent, "最近一句：用户在等 CI 结果") {
		t.Fatalf("the newest context must survive the trim: %q", sent)
	}
}

// 任务 1052 验收（档位面）：
// ① 档位换算：0 → 默认 10KB；超 20KB 档 → 按 20KB 上界截断；
// ② 小档位（2KB）实际注入上下文 ≤ 2KB——更省成本与延时（用户拍板动机）；
// ③ 大档位（20KB）保留 10KB 默认档会裁掉的旧上下文。
func TestRewriteDraftBudgetTiers(t *testing.T) {
	if got := DraftRewriteContextBudget(0); got != DraftRewriteBudgetBytes {
		t.Fatalf("budgetKB=0 must fall back to the 10KB default: %d", got)
	}
	if got := DraftRewriteContextBudget(5); got != 5*1024 {
		t.Fatalf("budgetKB=5 must be 5120 bytes: %d", got)
	}
	if got := DraftRewriteContextBudget(99); got != DraftRewriteMaxBudgetBytes {
		t.Fatalf("an out-of-tier budget must clamp to the 20KB ceiling: %d", got)
	}

	// 2KB 档：尾巴按尾部裁剪、注入量 ≤ 2KB（以哨兵段长度近似断言）。
	a, p := draftRewriteAgent(t, testutil.Turn{Text: "ok"})
	head := strings.Repeat("旧", 40*1024) // 远超 2KB 档的旧上下文
	tail := "最新一句：用户在等 CI 结果"
	if _, err := a.RewriteDraft(context.Background(), "草稿", head+tail, 2); err != nil {
		t.Fatal(err)
	}
	sent := p.Requests()[0].Messages[0].Content
	if strings.Contains(sent, strings.Repeat("旧", 2000)) {
		t.Fatalf("the 2KB tier must drop the oldest context: %d bytes", len(sent))
	}
	if !strings.Contains(sent, tail) {
		t.Fatalf("the 2KB tier must keep the newest context: %q", sent)
	}

	// 20KB 档：10KB 默认档会裁掉的旧内容在此档保留。
	b, q := draftRewriteAgent(t, testutil.Turn{Text: "ok"})
	oldLine := strings.Repeat("旧", 8*1024) // ~8KB 旧内容，10KB 档裁掉、20KB 档保留
	newLine := "最新一句：用户在等 CI 结果"
	if _, err := b.RewriteDraft(context.Background(), "草稿", oldLine+newLine, 20); err != nil {
		t.Fatal(err)
	}
	if sent := q.Requests()[0].Messages[0].Content; !strings.Contains(sent, strings.Repeat("旧", 2000)) {
		t.Fatalf("the 20KB tier must keep context the 10KB default would trim: %d bytes", len(sent))
	}
}

func TestRewriteDraftDegradesToNoRewrite(t *testing.T) {
	// 空草稿：直接拒绝，不发请求。
	empty, _ := draftRewriteAgent(t)
	if _, err := empty.RewriteDraft(context.Background(), "   ", "", 0); err == nil {
		t.Fatal("an empty draft must be rejected without a model call")
	}
	if got := len(empty.svc.prov.(*testutil.MockProvider).Requests()); got != 0 {
		t.Fatalf("empty draft must not reach the provider: %d requests", got)
	}

	// 流失败：返回 err，调用方降级为「不改写」。
	failing, _ := draftRewriteAgent(t, testutil.Turn{StreamError: errors.New("boom")})
	if out, err := failing.RewriteDraft(context.Background(), "草稿", "", 0); err == nil || out != "" {
		t.Fatalf("a stream failure must degrade to (\"\", err): out=%q err=%v", out, err)
	}

	// 空输出：同样降级。
	blank, _ := draftRewriteAgent(t, testutil.Turn{Text: "  "})
	if out, err := blank.RewriteDraft(context.Background(), "草稿", "", 0); err == nil || out != "" {
		t.Fatalf("an empty rewrite must degrade to (\"\", err): out=%q err=%v", out, err)
	}
}

// 任务 1024 数据纪律：改写只允许携带传入的上下文尾巴，绝不把整个会话
// 历史塞进辅助请求——用会话里的一段私密文本做哨兵，断言它不出现在请求里。
func TestRewriteDraftNeverReadsSessionHistory(t *testing.T) {
	a, p := draftRewriteAgent(t, testutil.Turn{Text: "ok"})
	a.Session().Messages = append(a.Session().Messages, provider.Message{
		Role: provider.RoleAssistant, Content: "会话里的私密哨兵文本-SENTINEL-XYZ", ID: NewMessageID(),
	})
	if _, err := a.RewriteDraft(context.Background(), "草稿", "传入的尾巴", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Requests()[0].Messages[0].Content, "SENTINEL-XYZ") {
		t.Fatal("the rewrite request must carry only the caller-supplied tail, never session history")
	}
}
