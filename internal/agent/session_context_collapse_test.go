package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
)

// 任务 1025 ③：压缩后的载体链收口。969 的载体集保留「每个节的最新快照」，
// 记忆中途保存后就是「旧全量（含旧 memory 块）+ 新 delta（含新 memory 块）」
// 两条都活——同一份 memory 计两遍，正是 631KB 压缩后视图的大头（988 现场）。

func TestCollapseSessionContextChainCollapsesStaleFullPlusDelta(t *testing.T) {
	memoryV1 := strings.Repeat("old memory line\n", 8000) // ~128KB，模拟大 memory 块
	memoryV2 := strings.Repeat("new memory line\n", 9000)
	full := sessioncontext.Build(deltaTestSections(memoryV1))
	delta := sessioncontext.BuildDelta(deltaTestSections(memoryV2), deltaTestSections(memoryV1))
	if delta.Content == "" {
		t.Fatal("expected a memory delta fixture")
	}
	projection := []provider.Message{
		{Role: provider.RoleSystem, Content: "stable"},
		HostGeneratedUserMessage(full.Content),
		formatSummaryMessage("conversation summary"),
		HostGeneratedUserMessage(delta.Content),
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "recent"},
	}
	collapsed := collapseSessionContextChain(projection, projection)

	contexts := 0
	var fresh sessioncontext.Snapshot
	for _, msg := range collapsed {
		if isSessionContextMessage(msg) {
			contexts++
			snapshot, ok := sessioncontext.Parse(msg.Content)
			if !ok {
				t.Fatalf("collapsed carrier is not a valid envelope: %q", msg.Content[:80])
			}
			fresh = snapshot
		}
	}
	if contexts != 1 {
		t.Fatalf("context carriers after collapse = %d, want exactly 1: %+v", contexts, collapsed)
	}
	if fresh.Delta {
		t.Fatal("collapsed carrier must be a full snapshot")
	}
	if fresh.Sections.BackgroundMemory != strings.TrimSpace(memoryV2) {
		t.Fatalf("collapsed memory = %d bytes, want the NEW value (%d bytes)", len(fresh.Sections.BackgroundMemory), len(memoryV2))
	}
	// 合并态不丢节。
	if fresh.Sections.Environment != "go/linux" || fresh.Sections.SkillsCatalog != "skills" ||
		fresh.Sections.CapabilityCatalog != "catalog" || fresh.Sections.Workspace != "ws" {
		t.Fatalf("collapsed sections lost a part: %+v", fresh.Sections)
	}
	// 陈旧 memory 不得再出现：总字节应只含一份新 memory。
	totalBytes := 0
	for _, msg := range collapsed {
		totalBytes += len(msg.Content)
	}
	if totalBytes >= len(full.Content)+len(delta.Content) {
		t.Fatalf("collapse saved nothing: %d bytes vs chain %d", totalBytes, len(full.Content)+len(delta.Content))
	}
	if strings.Contains(strings.Join(contentsOf(collapsed), ""), strings.TrimRight(memoryV1, "\n")) {
		t.Fatal("stale memory v1 still rides the projection")
	}
	// 语义等价：合并态 = 压缩前视图的合并态。
	mergedBefore, _ := mergedTurnContextSections(projection)
	if mergedBefore != fresh.Sections {
		t.Fatalf("merged state changed: before %+v after %+v", mergedBefore, fresh.Sections)
	}
}

func contentsOf(msgs []provider.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestCollapseSessionContextChainKeepsSingleCurrentCarrier(t *testing.T) {
	full := sessioncontext.Build(deltaTestSections("memory v1"))
	projection := []provider.Message{
		{Role: provider.RoleSystem, Content: "stable"},
		HostGeneratedUserMessage(full.Content),
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "recent"},
	}
	// 单一载体已在当前态：逐字节不动（Build∘Parse 往返恒等，前缀缓存不破）。
	if got := collapseSessionContextChain(projection, projection); len(got) != len(projection) {
		t.Fatalf("single current carrier was rewritten: %+v", got)
	} else if got[1].Content != full.Content {
		t.Fatal("carrier bytes changed")
	}
	// 无上下文的投影原样返回。
	plain := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	if got := collapseSessionContextChain(plain, plain); len(got) != 1 {
		t.Fatalf("plain projection changed: %+v", got)
	}
}

func TestCollapseSessionContextChainUsesViewMergedState(t *testing.T) {
	// 投影里只有旧全量；canonical 尾部（view）有更新的 delta。收口必须用
	// view 的合并态，否则把尾部的最新 memory 又打回旧值。
	memoryV1 := strings.Repeat("old memory line\n", 4000)
	memoryV2 := strings.Repeat("new memory line\n", 4000)
	full := sessioncontext.Build(deltaTestSections(memoryV1))
	delta := sessioncontext.BuildDelta(deltaTestSections(memoryV2), deltaTestSections(memoryV1))
	projection := []provider.Message{
		{Role: provider.RoleSystem, Content: "stable"},
		HostGeneratedUserMessage(full.Content),
		formatSummaryMessage("summary"),
	}
	view := append(append([]provider.Message(nil), projection...), HostGeneratedUserMessage(delta.Content))
	collapsed := collapseSessionContextChain(projection, view)
	if len(collapsed) != len(projection) {
		t.Fatalf("projection tail contexts are not ours to touch: %+v", collapsed)
	}
	snapshot, ok := sessioncontext.Parse(collapsed[1].Content)
	if !ok || snapshot.Sections.BackgroundMemory != strings.TrimSpace(memoryV2) {
		t.Fatalf("collapsed carrier did not take the view's newest memory: ok=%v", ok)
	}
}

func TestCollapseSessionContextChainLeavesOlderFullsAlone(t *testing.T) {
	// 前缀里更早的全量快照（被新全量整体取代、但位于缓存稳定前缀中）保持
	// 逐字节不动；只收口尾链。
	older := sessioncontext.Build(sessioncontext.Sections{Workspace: "older-ws"})
	newer := sessioncontext.Build(deltaTestSections("memory v1"))
	projection := []provider.Message{
		HostGeneratedUserMessage(older.Content),
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "mid"},
		HostGeneratedUserMessage(newer.Content),
		formatSummaryMessage("summary"),
	}
	collapsed := collapseSessionContextChain(projection, projection)
	if len(collapsed) != len(projection) {
		t.Fatalf("older full was dropped or chain not collapsed: %+v", collapsed)
	}
	if collapsed[0].Content != older.Content {
		t.Fatal("older full in the stable prefix changed bytes")
	}
}

// 任务 988 对账切片：telemetry 一行里 src/fold/proj 必须是同一把尺的三个位置。
// 修复前 fold 位用静态 estimateMessagesTokens，中文会话下与校准尺差 2-4 倍，
// 现场 proj=400846 与 fold/src 无法对账。
func TestCompactionTelemetryUsesOneRuler(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	// 构造中文含量高的 fold：静态尺（rune 计数）与校准尺（2×chars，模拟 provider
	// 对 CJK 的低膨胀）会明显背离。
	cjk := strings.Repeat("压缩后视图体积对账验证", 500)
	fold := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: cjk},
		{Role: provider.RoleAssistant, Content: cjk},
	}
	// 校准：provider 报 1.5 tokens/char（校准界内、且与静态尺明显背离）。
	shape := requestCalibrationShapeOf(provider.Request{Messages: fold})
	a.setPromptTokenCalibration(int(shape.requestChars)*3/2, shape)

	guarded := a.guardedSummaryInputTokens(fold)
	static := estimateMessagesTokens(fold)
	if guarded == static {
		t.Fatalf("fixture did not separate the rulers (both %d) — calibration not applied?", guarded)
	}
	// foldToSummaryMode 产出的 FoldTokens 必须走校准尺。
	res, err := a.foldToSummaryMode(t.Context(), fold, "", SummaryInputCachePrefix)
	if err != nil {
		// 无 provider 时摘要请求会失败，但 FoldTokens 在失败前已填。
		if res.FoldTokens == 0 {
			t.Fatalf("FoldTokens unset on failure: %v", err)
		}
	}
	if res.FoldTokens != guarded {
		t.Fatalf("FoldTokens = %d, want calibrated %d (static was %d)", res.FoldTokens, guarded, static)
	}
	// src/proj 位（estimatedVisibleRequestTokens）与 fold 位同源。
	if src := a.estimatedVisibleRequestTokens(fold); src != guarded {
		t.Fatalf("src ruler %d != fold ruler %d", src, guarded)
	}
}

// 任务 1025 验收：压缩触发时机回归。修正后图片会话的 estimate 不得更高于
// 修正前的封顶口径触发误压缩——可读 1080p 截图修正前后同带，未读图完全同值。
func TestImageSessionEstimateDoesNotTriggerEarlier(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	// 现场形态：10 张 1080p 截图 + 文本。
	msgs := make([]provider.Message, 0, 12)
	msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: "sys"})
	for i := 0; i < 10; i++ {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "screenshot", Images: []string{pngDataURL(1920, 1080)}})
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: strings.Repeat("analysis ", 200)})
	est := a.estimatedPromptTokens(msgs)
	// 修正前口径：文本字符/4 + 每图封顶 2048。修正后不得高于它 5% 以上
	//（1080p 计价 ~2121 vs 封顶 2048，允许带内漂移；大幅超出即为矫枉过正）。
	textOnly := requestCalibrationTextShapeOf(msgs)
	before := int(float64(textOnly)*fallbackTokPerChar) + 10*imageEstimateFallbackTokens
	if est > before+before/20 {
		t.Fatalf("image session estimate %d exceeds pre-1025 caliber %d by more than 5%%", est, before)
	}
	// 修正后必须仍看得见图片（1010 契约不回退）。
	if est <= int(float64(textOnly)*fallbackTokPerChar)+10*imageEstimateTokenFloor {
		t.Fatalf("estimate %d went blind to images again", est)
	}
}

// requestCalibrationTextShapeOf returns the bare text char count of messages.
func requestCalibrationTextShapeOf(msgs []provider.Message) int64 {
	chars, _, _ := requestCalibrationTextShape(provider.Request{Messages: msgs}, provider.SharedWindowInputPolicy{})
	return chars
}
