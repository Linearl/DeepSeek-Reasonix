package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// 任务 1040：含图会话窗口占用失常。三处同族缺口——截断账本在单条计价上
// 早退（带图消息计 0 价）、elide 置空 Images 但账面记账、尾区折算（tailStart）
// 不数图片。这些测试把「图片必须出现在每把尺上」钉死。
//
// 修复前现象（可证伪）：dropOldestUnits 的 remaining 用 messageTokens 逐条
// 减，单条带图消息 messageTokens==0，remaining 不减 → 带图消息在截断里
// 从不消耗预算，视图只增不减。

func TestMessageTokensPricesImages(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	img := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: "look", Images: []string{pngDataURL(1920, 1080)}}
	text := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "look"}
	imgTokens := a.messageTokens(img)
	textTokens := a.messageTokens(text)
	if imgTokens <= textTokens {
		t.Fatalf("带图消息计价 %d 未超过同文本无图消息 %d —— 图片在单条计价里是 0（1040 缺口）", imgTokens, textTokens)
	}
	// 图片增量应与分辨率折算一致（1080p ≈ 2121，带内）。
	if delta := imgTokens - textTokens; delta < imageEstimateTokenFloor {
		t.Fatalf("图片增量 %d 低于下限 %d", delta, imageEstimateTokenFloor)
	}
}

// 截断账本必须把带图消息的图片价格计入：带图消息被 drop 后，剩余估计
// 必须下降至少一张图的价格。修复前 remaining 对带图消息减 0，账本虚高，
// 截断要么过早停止、要么成功判定失真。
func TestDropOldestUnitsAccountingCarriesImages(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	imgURL := pngDataURL(1280, 720)
	oneImage := imageEstimateTokens(imgURL)
	if oneImage <= 0 {
		t.Fatal("fixture image priced at 0")
	}
	// 5 个独立 user unit 各带一张图 + 尾部一条无图 user 消息（落在保护尾）。
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
			Content: "screenshot", Images: []string{imgURL}})
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "tail question"})
	before := a.estimatedVisibleRequestTokens(msgs)
	// target 低于总估计：必须通过 drop 图片 unit 拿回预算。
	target := before - 2*oneImage
	head, protect := 1, len(msgs)-1
	out, dropped := a.dropOldestUnits(msgs, head, protect, target)
	if dropped == 0 {
		t.Fatalf("drop 未删任何 unit：target=%d before=%d", target, before)
	}
	after := a.estimatedVisibleRequestTokens(out)
	if after >= before {
		t.Fatalf("drop 后估计 %d 未低于 drop 前 %d —— 带图 unit 的价格没进账本（1040 缺口）", after, before)
	}
	// drop 到位：删除的每个带图 unit 应带来至少一张图的价格下降。
	if saved := before - after; saved < dropped*oneImage-imageEstimateTokenFloor {
		t.Fatalf("drop %d 个 unit 只省下 %d tokens，低于 %d 张图的合计价 %d", dropped, saved, dropped, dropped*oneImage)
	}
}

// elideToolResult 置空 Images 是刻意行为（tool 结果省略）；但它必须与
// 账面同步——若 elide 后仍用「不带图的 estimate」判成功，图片字节其实还在
// 视图里，UI 与真实发送量就背离。这条断言钉住：含图视图走 elide 分级后，
// 剩余估计若判低于 target，图片也必须真的离开视图（或整体走 drop）。
func TestTruncateViewDoesNotEludeImageBytesWhileClaimingSuccess(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	imgURL := pngDataURL(1280, 720)
	// tool 结果 + 用户带图消息：tool 结果可 elide，图不可 elide（无 Content 门槛）。
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "task"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "c1", Name: "read_file", Content: strings.Repeat("result body ", 5000)},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "screenshot", Images: []string{imgURL}},
	}
	total := a.estimatedVisibleRequestTokens(msgs)
	oneImage := imageEstimateTokens(imgURL)
	if oneImage <= 0 || total <= oneImage {
		t.Fatalf("fixture 失真：total=%d image=%d", total, oneImage)
	}
	target := total - oneImage - 1 // 只够删掉图片级预算
	projected, affected := a.truncateView(msgs, target)
	if affected == 0 || projected == nil {
		// 视图已能放下或无从下手——只要不出现「声称成功但图片仍在」即可。
		return
	}
	after := a.estimatedVisibleRequestTokens(projected)
	if after >= target {
		// 没达成目标是允许的（尽力而为），但不能假装达成。
		return
	}
	// 达成目标 = 账面低于 target；此时若图片还在视图里，就是「白送」缺口。
	stillImages := 0
	for _, m := range projected {
		stillImages += len(m.Images)
	}
	if stillImages > 0 && after+oneImage > target {
		// 图片贡献的预算没有从账本里真正兑现：删掉图片才能达成 target。
		t.Fatalf("视图仍携带 %d 张图片但账面已判达标（after=%d target=%d）——图片白送视图（1040 缺口）", stillImages, after, target)
	}
}

// tailStart 必须把图片折进尾区预算：带图消息不能因 msgChars 不数图片而
// 整批吞进保护尾。修复前：10 张 1080p 截图 msgChars 仅 "screenshot" 若干
// 字节，尾区预算轻松全部吞下，fold 无图可扔。
func TestTailStartAccountsImages(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}, compactionState: CompactionState{}}}
	// 构造 200K 窗口的 agent：tail 预算 = min(16%×200K, cap) = 32K。
	a.sess.output.learned.Store(&learnedContextBudget{windowTokens: 200_000})
	imgURL := pngDataURL(1920, 1080)
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
			Content: "screenshot", Images: []string{imgURL}})
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "tail"})
	head := a.pinnedPrefixLen(msgs)
	budget := a.recentTailBudget()
	start := tailStart(msgs, head, budget, a.tokPerChar(), minRecentKeep)
	// 10 张图 ≈ 21K tokens > 32K 的一半：至少有一张图必须落在 fold 区。
	foldedImages := 0
	for i := head; i < start; i++ {
		foldedImages += len(msgs[i].Images)
	}
	if foldedImages == 0 {
		t.Fatalf("tailStart=%d 起全部留在尾区，图片预算 %d 未被计入尾区账本（budget=%d）——1040 缺口", start, 10*imageEstimateTokens(imgURL), budget)
	}
}

// 1040 探针：直接看单条/多条计价的数字，确认图片是否真的进了账。
func TestDebug1040Probe(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	imgURL := pngDataURL(1280, 720)
	m := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "screenshot", Images: []string{imgURL}}
	single := a.requestCalibrationShape(provider.Request{Messages: []provider.Message{m}})
	total := a.messageTokens(m)
	oneImage := imageEstimateTokens(imgURL)
	t.Logf("single: requestChars=%d imageTokens=%d ; messageTokens=%d ; oneImage=%d ; est-of-single=%d",
		single.requestChars, single.imageTokens, total, oneImage, a.estimatedShapeTokens(single))
}

// 任务 1040 残留修复（方案 B）：带图消息不得走「内容级省略置空 Images」那级
// rescue——图片字节不在 Content 里，置空 Images 会让账面下降但发送量不降。
// elideOversizedContents 必须跳过带图消息；其预算由 drop/整体 elide 覆盖。
func TestElideOversizedContentsKeepsImageMessagesIntact(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	imgURL := pngDataURL(1280, 720)
	img := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: "screenshot context " + strings.Repeat("pad ", 3000), Images: []string{imgURL}}
	plain := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: strings.Repeat("history ", 3000)}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		img, plain,
	}
	// target 小到两条都过 contentElideOversizedDiv 门槛。
	target := a.estimatedVisibleRequestTokens(msgs) / 4
	out, affected := a.elideOversizedContents(msgs, a.pinnedPrefixLen(msgs), target)
	if affected == 0 {
		t.Skip("fixture 未触发 elide 门槛")
	}
	// 带图消息必须保持原样（Images 仍在、Content 未被截）。
	if len(out[1].Images) != 1 {
		t.Fatalf("带图消息的 Images 被省略级清空（剩 %d 张）——发送量没降但账面降了（1040 残留）", len(out[1].Images))
	}
	if out[1].Content != img.Content {
		t.Fatal("带图消息 Content 被内容级省略截断")
	}
	if out[2].Content == plain.Content {
		t.Fatal("无图消息未被 elide（fixture 未生效）")
	}
}

// 任务1040 分段条切片：gauge popup 的分段桶必须为图片单开一项——此前图片
// 只按 Content 字节计（"screenshot" 9 字节 → 归入消息桶 2 token），headline
// 却按分辨率计 ~2K/图，两个读数无法对账。
func TestContextCompositionBucketsImages(t *testing.T) {
	imgURL := pngDataURL(1920, 1080)
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "screenshot", Images: []string{imgURL}},
	}
	comp := computeContextComposition(messages, nil)
	oneImage := imageEstimateTokens(imgURL)
	if comp.ImageTokens < oneImage || comp.ImageTokens > oneImage*len(messages) {
		t.Fatalf("ImageTokens=%d，want 一张图 %d 的整数倍", comp.ImageTokens, oneImage)
	}
	if comp.MessageTokens <= 0 {
		t.Fatalf("MessageTokens=%d，文本桶不应为空", comp.MessageTokens)
	}
	if want := comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens + comp.ImageTokens; comp.TotalTokens != want {
		t.Fatalf("TotalTokens=%d != 分段和 %d", comp.TotalTokens, want)
	}
	// LocalOnly 消息不进任何桶（与 headline 的 LocalOnly 过滤一致）。
	local := computeContextComposition([]provider.Message{
		{Role: provider.RoleTool, LocalOnly: true, Content: strings.Repeat("x", 100)},
	}, nil)
	if local.TotalTokens != 0 {
		t.Fatalf("LocalOnly 消息进入了分段桶：total=%d", local.TotalTokens)
	}
	// 无图会话零变化：只带文本的消息不产生任何 ImageTokens。
	plain := computeContextComposition([]provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "hello"},
	}, nil)
	if plain.ImageTokens != 0 {
		t.Fatalf("无图会话 ImageTokens=%d，want 0", plain.ImageTokens)
	}
}
