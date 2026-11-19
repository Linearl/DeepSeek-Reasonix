package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1714 压缩摘要空输出失败验收。
//
// 现场（20261119 21:45/22:07/22:34）：summarizer returned empty output
// 三次，out=41~1026 tokens 且 cache=warm——判定不是 out==0，而是流收场时
// 无正文（纯 reasoning 带 tool call / tool-call 占位 / 空白），失败后无
// 用户可见提示、静默靠下次压力窗口自然重试。
//
// 修复契约：判定挂 errSummaryEmptyOutput 哨兵并带形态读数；空输出做一次
// 同请求重放（1 次即止，20261119 判据防循环）；重放仍失败才向上失败，且
// 失败面新增用户可见 notice（canceled=用户打断，不发通知不打扰）。

// tl1714StubProvider：前 emptyCalls 次请求以「无正文流」收场（可带 reasoning /
// tool call / usage 计费），之后正常回复。记录请求数供重试次数断言。
type tl1714StubProvider struct {
	mu         sync.Mutex
	emptyCalls int
	reasoning  string // 空流形态携带的 reasoning（纯 reasoning + tool = 现场判空形态）
	toolCall   bool   // 空流形态打开一个 tool call（out tokens 落在参数上）
	billOut    int    // 空流形态的 CompletionTokens 计费读数
	reply      string
	requests   int
}

func (p *tl1714StubProvider) Name() string { return "tl1714-stub" }

func (p *tl1714StubProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowIndependent}
}

func (p *tl1714StubProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.requests++
	n := p.requests
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 4)
	if n <= p.emptyCalls {
		if p.toolCall {
			ch <- provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: "call-1", Name: "read_file", Arguments: `{"path":"x"}`}}
		}
		if p.reasoning != "" {
			ch <- provider.Chunk{Type: provider.ChunkReasoning, Text: p.reasoning}
		}
		if p.billOut > 0 {
			ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: p.billOut, TotalTokens: 100 + p.billOut}}
		}
		// 流以终端事件正常收场但零正文：runSummaryRequest 的空输出判定面
		// （20261119 现场形态——cache=warm、out=41~1026 仍判空）。缺终端事件
		// 的断流形态属 provider 层 StreamAuxiliary 的管辖（unexpected_eof），
		// 不在本件判定面内。
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: p.reply}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105}}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func (p *tl1714StubProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

// B1 判空复现钉：out>0（计费在 tool-call 参数上）却判空——现成形态构造，
// 错误挂 errSummaryEmptyOutput 哨兵且文案带形态读数（tool_calls / out_tokens）。
func TestTl1714EmptyOutputSentinelShape(t *testing.T) {
	prov := &tl1714StubProvider{emptyCalls: 3, toolCall: true, billOut: 1026, reply: "- goal: x"}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)

	_, usage, err := a.summarizeOnce(context.Background(), sess.Messages[1:], "")
	if !errors.Is(err, errSummaryEmptyOutput) {
		t.Fatalf("summarizeOnce err = %v, want errSummaryEmptyOutput", err)
	}
	if usage == nil || usage.CompletionTokens <= 0 {
		t.Fatalf("usage = %+v, want out tokens billed on the rejected shape", usage)
	}
	if !strings.Contains(err.Error(), "tool_calls=1") || !strings.Contains(err.Error(), "out_tokens=1026") {
		t.Fatalf("empty-output error lacks the shape readout: %v", err)
	}
	if n := prov.callCount(); n != 2 {
		t.Fatalf("summarizer requests = %d, want exactly one replay (2)", n)
	}
}

// B2 空输出重放 1 次成功：第一次空、第二次正文——重放命中即返回正文，
// usage 合并两次尝试（RequestCount=2）。
func TestTl1714EmptyOutputReplaysOnceAndSucceeds(t *testing.T) {
	prov := &tl1714StubProvider{emptyCalls: 1, reply: "- kept: alpha constraint"}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)

	summary, usage, err := a.summarizeOnce(context.Background(), sess.Messages[1:], "")
	if err != nil {
		t.Fatalf("summarizeOnce after replay = %v", err)
	}
	if !strings.Contains(summary, "alpha constraint") {
		t.Fatalf("summary = %q, want the replayed digest", summary)
	}
	if usage == nil || usage.RequestCount != 2 {
		t.Fatalf("usage = %+v, want RequestCount=2 (original + replay)", usage)
	}
	if n := prov.callCount(); n != 2 {
		t.Fatalf("summarizer requests = %d, want exactly one replay (2)", n)
	}
}

// B3 重放仍空则失败且不循环：恒空 stub 恰好两次请求（原发+重放），错误
// 保持哨兵形态。
func TestTl1714EmptyOutputReplayStillEmptyFailsOnce(t *testing.T) {
	prov := &tl1714StubProvider{emptyCalls: 99}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)

	_, _, err := a.summarizeOnce(context.Background(), sess.Messages[1:], "")
	if !errors.Is(err, errSummaryEmptyOutput) {
		t.Fatalf("summarizeOnce err = %v, want errSummaryEmptyOutput after one replay", err)
	}
	if n := prov.callCount(); n != 2 {
		t.Fatalf("summarizer requests = %d, want 2 (no retry loop)", n)
	}
}

// B3b 失败面用户可见 notice：压力道摘要空输出（重放后仍空）→ CompactionDone
// failed + Notice(compaction_failed) 双面在场，不再静默。
func TestTl1714FailedPassEmitsUserNotice(t *testing.T) {
	prov := &tl1714StubProvider{emptyCalls: 99}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)
	var failedDone, notice *event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		switch {
		case e.Kind == event.CompactionDone && e.Compaction.Outcome == "failed" && failedDone == nil:
			cp := e.Compaction
			failedDone = &event.Event{Kind: e.Kind, Compaction: cp}
		case e.Kind == event.Notice && e.Code == event.NoticeCodeCompactionFailed && notice == nil:
			ev := e
			notice = &ev
		}
	})

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v, want the turn to proceed with the failure recorded", err)
	}
	if failedDone == nil {
		t.Fatal("no failed CompactionDone on the empty-output failure")
	}
	if notice == nil {
		t.Fatal("no user-visible compaction_failed notice on the empty-output failure")
	}
	if !strings.Contains(notice.Text, "summarizer") {
		t.Fatalf("notice text = %q, want the summarizer failure surfaced", notice.Text)
	}
	if n := prov.callCount(); n != 2 {
		t.Fatalf("summarizer requests = %d, want 2 (single rung + one replay, no escalation loop)", n)
	}
}

// B4 canceled 定性钉：turn 打断（ctx canceled）是用户自己的动作——失败卡
// 照旧（既有 867 契约）但不发 notice，不把打断当故障打扰。
func TestTl1714CanceledSkipsUserNotice(t *testing.T) {
	prov := &fakeProvider{hang: true}
	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, prov, sess)
	var notice *event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCompactionFailed {
			ev := e
			notice = &ev
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := prepareContext(ctx, a, CompactionTriggerOverflow); err == nil {
		t.Fatal("cancelled prepare reported success")
	}
	if notice != nil {
		t.Fatalf("cancellation emitted a compaction_failed notice: %+v", notice)
	}
}
