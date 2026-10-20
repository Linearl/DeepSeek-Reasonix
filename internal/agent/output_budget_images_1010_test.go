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

func TestRequestShapeCountsCappedImageBytes(t *testing.T) {
	big := strings.Repeat("A", 2<<20) // 2MB base64，超过单图封顶
	req := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "hi", Images: []string{"data:image/png;base64," + big}},
	}}
	// Agent 级 wire 形状计图片（封顶）；裸文本形状不分（分块面自己计全量）。
	chars := requestCalibrationShapeOf(req).requestChars
	want := int64(len("user")) + int64(len("hi")) + requestImageEstimateByteCap + 4
	if chars != want {
		t.Fatalf("image shape chars = %d, want capped %d (not the raw %d)", chars, want, len(big))
	}
	textChars, _, _ := requestCalibrationTextShape(req, provider.SharedWindowInputPolicy{})
	if textChars != want-requestImageEstimateByteCap {
		t.Fatalf("bare text shape must stay image-free: %d", textChars)
	}
	small := strings.Repeat("A", 100)
	req.Messages[0].Images = []string{"data:image/png;base64," + small}
	chars = requestCalibrationShapeOf(req).requestChars
	want = int64(len("user")) + int64(len("hi")) + int64(len("data:image/png;base64,")+len(small)) + 4
	if chars != want {
		t.Fatalf("small image shape chars = %d, want full %d", chars, want)
	}
}

func TestRequestShapeWithoutImagesUnchanged(t *testing.T) {
	// 任务 1010 验收 5：无图会话逐字节等价——图片计数不得碰纯文本形状。
	req := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "hello world"},
		{Role: provider.RoleAssistant, Content: "answer", ToolCalls: []provider.ToolCall{{ID: "t1", Name: "read", Arguments: "{\"path\":\"x\"}"}}, ToolCallID: "t1"},
	}}
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
	// 摘要请求正是这样漏过 1M 窗的。单图封顶 8KiB≈2K tokens，所以这里用
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
			t.Fatalf("prefix [1:%d) still fits (est %d <= %d): the cap stopped early", end+1, est, limit)
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
