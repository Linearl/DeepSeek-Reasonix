package agent

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务 1010 需求 1：请求形状估计必须看见图片，超窗时给出可操作错误。
// 现场（20261019 ai-passport）：图片 base64 是投影膨胀主力，而形状估计完全
// 不计图片，折前缀上限、准入、截断救援全都相信「2.1M 能装进 1M 窗」。
// 任务 1025 修正计价方式：可读图片按分辨率折算（imageEstimateTokens），
// 不可读的（本文件的假 base64 就是）沿用 1010 单图封顶口径。

func TestRequestShapePricesImagesByResolution(t *testing.T) {
	// 不可读 header 的假 base64 → 沿用 1010 封顶口径（8KiB/4 = 2048 tokens）。
	unreadable := strings.Repeat("A", 2<<20) // 2MB base64，超过单图封顶
	req := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "hi", Images: []string{"data:image/png;base64," + unreadable}},
	}}
	shape := requestCalibrationShapeOf(req)
	// 文本口径保持图片无关（1010 契约，分块面自己计全量字节）。
	textChars, _, _ := requestCalibrationTextShape(req, provider.SharedWindowInputPolicy{})
	if shape.requestChars != textChars {
		t.Fatalf("image must not inflate text chars: shape=%d text=%d", shape.requestChars, textChars)
	}
	if want := imageEstimateFallbackTokens; shape.imageTokens != want {
		t.Fatalf("unreadable image tokens = %d, want flat cap %d (not the raw %d)", shape.imageTokens, want, len(unreadable))
	}

	// 可读图片：同分辨率不同 base64 长度折同一 token 数（分辨率口径，非字节口径）。
	small := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "hi", Images: []string{pngDataURL(100, 50)}},
	}}
	large := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "hi", Images: []string{pngDataURL(100, 50, noise(300<<10)...)}},
	}}
	smallTokens := requestCalibrationShapeOf(small).imageTokens
	largeTokens := requestCalibrationShapeOf(large).imageTokens
	if smallTokens != largeTokens {
		t.Fatalf("same-resolution images priced differently: %d vs %d", smallTokens, largeTokens)
	}
	// 100×50 面积远小于 floor 起步档 → 拿 floor。
	if smallTokens != imageEstimateTokenFloor {
		t.Fatalf("tiny image tokens = %d, want floor %d", smallTokens, imageEstimateTokenFloor)
	}

	// 1080p 量级截图：对齐 provider 侧 ~1.6K-2.2K token 带。
	hd := requestCalibrationShapeOf(provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "shot", Images: []string{pngDataURL(1920, 1080)}},
	}}).imageTokens
	// 长边压到 1568：1568×882=1,382,976px ÷750≈1844 → +15% ≈2121。
	if hd < 1600 || hd > 2400 {
		t.Fatalf("1080p tokens = %d, want the ~1.6K-2.4K provider band", hd)
	}
}

func TestRequestShapeWithoutImagesUnchanged(t *testing.T) {
	// 任务 1010 验收 5 + 任务 1025：无图会话逐字节等价——图片计数不得碰纯文本形状。
	req := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "hello world"},
		{Role: provider.RoleAssistant, Content: "answer", ToolCalls: []provider.ToolCall{{ID: "t1", Name: "read", Arguments: "{\"path\":\"x\"}"}}, ToolCallID: "t1"},
	}}
	shape := requestCalibrationShapeOf(req)
	if shape.imageTokens != 0 {
		t.Fatalf("text-only request image tokens = %d", shape.imageTokens)
	}
	chars, cjkRunes, cjkBytes := requestCalibrationTextShape(req, provider.SharedWindowInputPolicy{})
	if chars == 0 || cjkRunes != 0 || cjkBytes != 0 {
		t.Fatalf("plain shape = %d/%d/%d", chars, cjkRunes, cjkBytes)
	}
}

func TestAdmissionImageOverflowNamesImages(t *testing.T) {
	prov := &sharedWindowTestProvider{shared: true}
	a := &Agent{
		agentConfig: agentConfig{contextWindow: 100_000},
		svc:         agentServices{prov: prov},
		sess:        sessionRuntime{output: outputBudgetState{}},
	}
	huge := strings.Repeat("A", 3<<20)
	withImages := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("x", 400_000), Images: []string{"data:image/png;base64," + huge}},
	}, MaxTokens: 8192}
	_, err := a.admitOutputBudget(withImages)
	if !errors.Is(err, ErrCompactionRequired) {
		t.Fatalf("image overflow admission err = %v, want ErrCompactionRequired", err)
	}
	if !strings.Contains(err.Error(), "image") || !strings.Contains(err.Error(), "repair") {
		t.Fatalf("image overflow err must name the images and the repair: %v", err)
	}
	plain := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("x", 400_000)},
	}, MaxTokens: 8192}
	_, err = a.admitOutputBudget(plain)
	if !errors.Is(err, ErrCompactionRequired) {
		t.Fatalf("plain overflow admission err = %v, want ErrCompactionRequired", err)
	}
	if strings.Contains(err.Error(), "image") {
		t.Fatalf("plain overflow err must not mention images: %v", err)
	}
}

func TestSafeSummaryPrefixCapSeesImages(t *testing.T) {
	// 任务 1010 需求 2：折前缀上限按带图的真实形状收缩——现场里 2.1M 的
	// 摘要请求正是这样漏过 1M 窗的。不可读 base64 走 1010 封顶（2K tokens/图），
	// 多图累积（现场正是数百张截图累积成灾）越过窗口。
	prov := &sharedWindowTestProvider{shared: true}
	sess := &Session{Messages: []provider.Message{{Role: provider.RoleUser, Content: "q"}}}
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 20_000}, event.Discard)
	huge := strings.Repeat("A", 1<<20)
	imageMsgs := make([]provider.Message, 0, 10)
	imageMsgs = append(imageMsgs, provider.Message{Role: provider.RoleSystem, Content: "sys"})
	for i := 0; i < 8; i++ {
		imageMsgs = append(imageMsgs, provider.Message{Role: provider.RoleUser, Content: "screenshot", Images: []string{"data:image/png;base64," + huge}})
	}
	imageMsgs = append(imageMsgs, provider.Message{Role: provider.RoleUser, Content: "tail"})
	limit, enforce := a.safeSummaryPromptTokenLimit()
	if !enforce {
		t.Fatal("safe summary limit must be enforced for a shared-window provider")
	}
	end := a.maximumSafeSummaryPrefixEnd(imageMsgs, 1, len(imageMsgs), "")
	// 回归断言：图片盲的旧估计会原样放行整个折段（end==len），现场里 2.1M
	// 的摘要请求正是这样漏过 1M 窗的。
	if end == len(imageMsgs) {
		t.Fatalf("safe prefix end = %d, the image bytes were invisible to the cap", end)
	}
	// 契约断言：end 是「不超限的最大前缀」——前缀本身装得下，多装一条就超。
	if est := a.estimatedRequestTokens(a.summaryRequest(imageMsgs[1:end], "")); est > limit {
		t.Fatalf("prefix [1:%d) estimates %d > limit %d", end, est, limit)
	}
	if end+1 <= len(imageMsgs) {
		if est := a.estimatedRequestTokens(a.summaryRequest(imageMsgs[1:end+1], "")); est <= limit {
			t.Fatalf("prefix [1:%d) still fits (est %d <= limit %d): the cap stopped early", end+1, est, limit)
		}
	}
	textOnly := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: strings.Repeat("word ", 100)},
		{Role: provider.RoleUser, Content: "tail"},
	}
	if end := a.maximumSafeSummaryPrefixEnd(textOnly, 1, 3, ""); end != 3 {
		t.Fatalf("text-only prefix end = %d, want 3 (fits)", end)
	}
}
