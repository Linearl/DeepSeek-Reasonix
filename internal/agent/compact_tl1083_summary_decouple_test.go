package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1083 §5.2-P0②：压缩摘要输入解耦的两组回归钉。
//
// 现场（实例①，3.7M/1M 心跳会话）：pressure 触发的摘要在封顶单次摘要失败后
// 直接落在 blocked 收据上，分块道（chunked/tree-reduce）只对 manual/overflow
// 开放——「越大越压不动」恶性循环的断根点；以及 armed 压缩模型 401 时每次
// pass 都重走 30s/2m/5m 退墙梯然后 prune-only，fold 永远装不上（§6.2 验收：
// 401 ⇒ 换道落地 fold，而非 prune-only 循环）。
//
// 禁墙钟：全部用 stub 序号（failFirst / streamErr）与调用计数断言，唯一
// sleep 是 shrinkSummaryBackoff 的毫秒级退墙梯（与 912 测试同款）。

// ① pressure 的分块道救回：封顶单次摘要 + 加倍预算重试都截断失败后，分块
// 道必须接手并装上真摘要（failFirst=2：call1=摘要、call2=加倍重试，call3+
// =分块碎片与合并）。若分块道未开，prepare 会以错误收场且装不上任何投影。
func TestTl1083PressureFoldRescuesThroughChunkedPath(t *testing.T) {
	prov := &extractStubProvider{failFirst: 2, reply: "digest"}
	a := agentOverForce(t, prov, foldableSessionOverForce(6))
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure prepare = %v, want the chunked lane to install a real digest", err)
	}
	if degradedFold(a) {
		t.Fatal("pressure rescue must not degrade to a mechanical digest")
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied fold", r)
	}
	if got := latestDigest(a.sess.compactionState.Projection.Messages); got == "" {
		t.Fatal("the chunked rescue must install the summarized digest")
	}
	if prov.calls < 3 {
		t.Fatalf("provider calls = %d, want the summary + doubled retry + chunked fragments (>=3)", prov.calls)
	}
}

// ② happy path 零变化：pressure 一次成功摘要照旧恰一次请求——分块道只在
// 尺寸失败之后才开（任务822 契约的保留部分），普通压缩不得多花一次请求。
func TestTl1083PressureHappyPathStaysSingleCall(t *testing.T) {
	prov := &extractStubProvider{reply: "digest"}
	a := agentOverForce(t, prov, foldableSessionOverForce(6))
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure prepare = %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 (single summary, no chunked on success)", prov.calls)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied fold", r)
	}
}

// ③ §6.2 验收主腿：armed 压缩模型 401 ⇒ 摘要换道到对话目的地并落地 fold，
// 而非退墙梯烧完 prune-only。经济模型恰好一次调用（那次 401），对话模型
// 接手后续 pass；blocked 标志锁死死钥匙，summaryDestinationRef 跟随。
func TestTl1083CompactModelAuthExpiredSwitchesSummaryLane(t *testing.T) {
	home := &destCountProvider{Provider: &fakeProvider{reply: "home digest", promptTokens: 42}}
	econ := &destCountProvider{Provider: &fakeProvider{
		streamErr: &provider.AuthError{Provider: "econ", Status: 401},
	}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"econ/m3": econ,
	}}
	sess := foldableSessionOverForce(6)
	a := New(home, nil, sess, Options{
		ModelRef:      "fake/a",
		ModelResolver: resolver,
		CompactModel:  "econ/m3",
		ContextWindow: 5000,
		CompactRatio:  0.5, CompactForceRatio: 0.5, RecentKeep: 2,
		ArchiveDir: t.TempDir(),
	}, event.Discard)

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure prepare = %v, want the 401 lane to land the fold on the conversation destination", err)
	}
	if got := econ.calls.Load(); got != 1 {
		t.Fatalf("compact model served %d requests, want exactly 1 (the 401 attempt)", got)
	}
	if got := home.calls.Load(); got < 1 {
		t.Fatalf("conversation destination served %d requests, want >=1 (the lane switch)", got)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied fold (not the prune-only exit)", r)
	}
	if !strings.Contains(latestDigest(a.sess.compactionState.Projection.Messages), "home digest") {
		t.Fatalf("digest = %q, want the conversation destination's reply", latestDigest(a.sess.compactionState.Projection.Messages))
	}
	if !a.compactModelAuthBlocked.Load() {
		t.Fatal("the dead compact key must be blocked for later passes")
	}
	if ref := a.summaryDestinationRef(); ref != "fake/a" {
		t.Fatalf("summaryDestinationRef = %q, want the conversation ref after the block", ref)
	}
	// 后续 pass 不再重走退墙梯：直接一次 summarizeFold 也由对话目的地应答。
	res, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err != nil {
		t.Fatalf("post-block summarizeFold = %v, want the conversation destination to serve", err)
	}
	if !strings.Contains(res.Text, "home digest") {
		t.Fatalf("post-block summary = %q, want the conversation reply", res.Text)
	}
	if got := econ.calls.Load(); got != 1 {
		t.Fatalf("blocked compact model served %d requests, want still 1", got)
	}
}

// ④ 反向钉：401 打在对话目的地（无 armed 压缩模型）时没有可换的道——不得
// 误锁（本无锁可上），行为与 tl1083 之前逐字节同形：退墙梯走满后失败可见。
func TestTl1083ConversationAuthExpiredHasNoLaneToSwitch(t *testing.T) {
	captureSlogFor912(t)
	shrinkSummaryBackoff(t)

	home := &destCountProvider{Provider: &fakeProvider{
		streamErr: &provider.AuthError{Provider: "home", Status: 401},
	}}
	sess := foldableSessionOverForce(3)
	a := New(home, nil, sess, Options{
		ModelRef:      "fake/a",
		ContextWindow: 5000,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)

	_, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err == nil {
		t.Fatal("summarizeFold = nil, want the exhausted-ladder failure to stay visible")
	}
	if got := home.calls.Load(); got != 4 {
		t.Fatalf("provider calls = %d, want 4 (first attempt + 3 backoff steps)", got)
	}
	if a.compactModelAuthBlocked.Load() {
		t.Fatal("without an armed compact model the auth block must stay untouched")
	}
	if _, armed := a.compactModelResolvedForTest(); armed {
		t.Fatal("no compact model is armed; the resolution cache must stay empty")
	}
}

// ⑤ 纯函数钉：分块道资格判定不受 trigger 接线影响——尺寸失败（截断）在
// CachePrefix 输入形态下就合格，provider overflow 仍要求先走完 slim 阶梯
// （防 ② 的 trigger 改动漂移这条边界）。
func TestTl1083ChunkedEligibilityUnchangedByTriggerWiring(t *testing.T) {
	trunc := fmt.Errorf("%w: provider reached the output token limit", errSummaryOutputTruncated)
	if !chunkedFallbackApplies(trunc, SummaryInputCachePrefix) {
		t.Fatal("output truncation must qualify from the replay form without a slim detour")
	}
	limit := &provider.ContextLimitError{
		APIError:        &provider.APIError{Provider: "stub", Status: 400, Body: "prompt too long"},
		WindowTokens:    128_000,
		RequestedTokens: 300_000,
	}
	if chunkedFallbackApplies(limit, SummaryInputCachePrefix) {
		t.Fatal("a provider overflow from the replay form must still walk the slim rung first")
	}
	if !chunkedFallbackApplies(limit, SummaryInputSlim) {
		t.Fatal("a provider overflow after the slim rung must qualify")
	}
}
