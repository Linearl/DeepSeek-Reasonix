package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1395：压前反升根治——压缩的规划尺与计费尺统一。
//
// 断点：fold 的边界规划（planCompaction/tailStart）与经济判据（foldEconomics）
// 用 estimateMessagesTokens / msgChars×tokPerChar 的文本粗尺——图片、工具
// schema、tokenizer 漂移对它不可见；而窗口触发、摘要输入守卫、安装门
// （acceptCheckpointCandidate）与遥测投影全部用校准计费尺。两尺错位时：
// 规划尺说「尾区放得下」的带图消息滞留保护尾 → 压缩产物在校准尺上超窗 →
// 下一请求 400 → 救援/截断兜底 → 投影比压缩前更大（压前反升）。
//
// 本文件把「规划必须按计费尺计价」钉死在断点上。

// rulerSession builds a transcript whose recent half is screenshot-heavy:
// each image message prices ~2.1K tokens on the billing ruler but ~2 tokens
// on the coarse planning ruler (msgChars counts "screenshot" only).
func rulerSession(images int) *Session {
	big := strings.Repeat("word ", 400) // ~500 tokens billed, fold-worthy
	img := pngDataURL(1920, 1080)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "standing constraint: never change the public API"},
	}
	for range 4 {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: big},
			provider.Message{Role: provider.RoleUser, Content: "continue"},
		)
	}
	for range images {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
			Content: "screenshot", Images: []string{img}})
	}
	return &Session{Messages: msgs}
}

// TestPlanFoldRegionTailFitsBudgetOnBillingRuler pins the planner half of the
// unification: whatever tailStart leaves out of the fold must fit the recent
// tail budget on the SAME calibrated ruler the window and the install gate
// bill with — not on the char census that cannot see images.
func TestPlanFoldRegionTailFitsBudgetOnBillingRuler(t *testing.T) {
	const window = 40_000
	// 20 张 1080p ≈ 42K 计费 tokens ≈ 整个窗口；粗尺只看见 ~130。
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, rulerSession(20), window)
	msgs := a.sess.conversation.Snapshot()

	head, start, ok := a.planFoldRegion(msgs, false, true)
	if !ok {
		t.Fatal("fixture has no foldable region")
	}
	budget := a.recentTailBudget()
	tailTokens := a.estimatedPromptTokens(modelInputMessages(msgs[start:]))
	if tailTokens > budget {
		t.Fatalf("规划尺把 %d 计费 tokens 留在保护尾（预算 %d，窗口 %d）——粗尺看不见图片，压缩产物必超窗（tl1395 断点）",
			tailTokens, budget, window)
	}
	foldedImages := 0
	for i := head; i < start; i++ {
		foldedImages += len(msgs[i].Images)
	}
	if foldedImages == 0 {
		t.Fatal("超预算的图片全部滞留保护尾，fold 区没有可折的图片——规划与计费仍不同尺")
	}
}

// TestFoldEconomicsPricesOnBillingRuler pins the economics half: a region the
// coarse ruler calls "too small to fold" but the billing ruler prices at real
// window pressure must still fold — otherwise image pressure waits for the
// 400 (the 压前反升 trigger lane) instead of compacting at pressure time.
func TestFoldEconomicsPricesOnBillingRuler(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	img := pngDataURL(1920, 1080)
	region := []provider.Message{{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: "screenshot", Images: []string{img}}}
	const floorTokens = 400 // foldEconomics 的 minFoldTokens 地板值，判据不回归只统一尺
	if coarse := estimateMessagesTokens(region); coarse >= floorTokens {
		t.Fatalf("fixture 失真：粗尺已见 %d ≥ %d，测不出粗尺盲区", coarse, floorTokens)
	}
	if billed := a.estimatedPromptTokens(region); billed < floorTokens {
		t.Fatalf("fixture 失真：计费尺只报 %d < %d，图片没进计价", billed, floorTokens)
	}
	// tl1395 把判据搬到计费尺上（foldEconomics 变为 Agent 方法）；地板值不变。
	if !foldEconomics(region) {
		t.Fatalf("计费尺 %d 的图片区域被经济学判据拒绝（粗尺读数）——图片压力只能等 400 救援（tl1395）",
			a.estimatedPromptTokens(region))
	}
}

// TestCompactionLandsUnderWindowWithoutTruncationRescue is the end-to-end
// acceptance: the whole chain 400 → rescue/回退 must never start. One
// pressure round must land the projection under the trigger via the summary
// path alone (receipt action=summary, applied), with every provider request
// under the window and the visible projection holding a digest — not the
// truncation rescue dropping whole units (the 回退 shape).
func TestCompactionLandsUnderWindowWithoutTruncationRescue(t *testing.T) {
	const window = 40_000
	prov := &preFoldProvider{window: window}
	a := agentOverForceWindow(t, prov, rulerSession(20), window)
	before := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure maintenance: %v", err)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" || r.Action != "summary" {
		t.Fatalf("receipt = %+v, want applied summary（截断救援/回退即 tl1395 的 400→回退链）", r)
	}
	for i, req := range prov.reqs {
		if tokens := requestTokensOf(req); tokens > window {
			t.Fatalf("request %d reached the provider over the window: %d > %d", i, tokens, window)
		}
	}
	view := a.modelVisibleMessages()
	if !hasCompactionSummary(view) {
		t.Fatal("visible view carries no digest — full-history fallback shape")
	}
	after := a.estimatedVisibleRequestTokens(view)
	if after >= before {
		t.Fatalf("压缩后投影 %d ≥ 压缩前 %d——投影单调不升被破坏（tl1395 验收②）", after, before)
	}
	if trigger := a.compactTrigger(); after >= trigger {
		t.Fatalf("压缩后投影 %d 仍 ≥ 触发线 %d——压缩未落地", after, trigger)
	}
}

// TestProjectionTokensMonotonicAcrossCompactions pins acceptance ② over one
// session compacted repeatedly: every installed compaction must leave the
// projection no larger than it found it, round after round.
func TestProjectionTokensMonotonicAcrossCompactions(t *testing.T) {
	const window = 40_000
	prov := &preFoldProvider{window: window}
	sess := rulerSession(20)
	a := agentOverForceWindow(t, prov, sess, window)
	trigger := a.compactTrigger()

	prev := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	for round := 1; round <= 3; round++ {
		if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
			t.Fatalf("round %d: pressure maintenance: %v", round, err)
		}
		r := a.sess.compactionState.LastReceipt
		if r == nil || r.Status != "applied" || r.Action != "summary" {
			t.Fatalf("round %d receipt = %+v, want applied summary", round, r)
		}
		now := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
		if now > prev {
			t.Fatalf("round %d 压缩后投影 %d > 压缩前 %d——单调不升被破坏（tl1395 验收②）", round, now, prev)
		}
		if now >= trigger {
			t.Fatalf("round %d 投影 %d 仍 ≥ 触发线 %d——未落地", round, now, trigger)
		}
		// 再灌一轮新压力：新一轮折叠必须在同一会话上继续收敛，而不是回退。
		img := pngDataURL(1920, 1080)
		appended := []provider.Message{}
		for range 12 {
			appended = append(appended, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
				Content: "screenshot", Images: []string{img}})
		}
		a.sess.conversation.AddBatch(appended...)
		prev = now
	}
}
