package agent

import (
	"testing"

	"reasonix/internal/provider"
)

// tl1395 增量：compress 工具的折叠门槛读数（visibleContextTokens）统一到计费尺。
//
// 断点：同一 explicit 压缩流程里，门槛判据（used < 0.5×触发线 → 拒折）曾用
// charsOfMessages×tokPerChar 的文本粗尺，而同流程的 SourceTokens 报告与
// projection≥source 装门用 estimatedVisibleRequestTokens 计费尺。粗尺看不见
// 图片：文本瘦、截图重的会话在门槛上被读成「太短不可折」，模型主动申请的
// 折叠被拒，图片压力退回等 400 救援（压前反升的入口通道之一）。统一后规划
// 判据与计费读数同源同单位（tl1395 主件同一原则的最后一处消费方）。
//
// fixture 取向（首跑钉③ sanity 失败的教训，20261110）：旧粗尺对 ASCII 文本
// 按 ~4 tokens/字节 高读，所以会话文本必须极瘦（几十字节级）才能让粗尺读数
// 落到门槛之下——图片盲区才测得出来；计费读数则全靠图片扛过门槛。
func imageLeanSession(images int) *Session {
	img := pngDataURL(1920, 1080)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "standing constraint: never change the public API"},
	}
	for range 2 {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: "noted"},
			provider.Message{Role: provider.RoleUser, Content: "continue"},
		)
	}
	for range images {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
			Content: "shot", Images: []string{img}})
	}
	return &Session{Messages: msgs}
}

// TestCompressFloorReadsTheBillingRuler pins the unification: the gate read
// equals the calibrated billing readout for the same view, on a fixture the
// old census demonstrably misjudged — nine screenshots push the billed size
// far above the fold floor while the text census reads "too short to fold".
func TestCompressFloorReadsTheBillingRuler(t *testing.T) {
	const window = 40_000
	// 9 张 1080p ≈ 19K 计费 tokens；文本 ~60 字节，粗尺读数只有几百。
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, imageLeanSession(9), window)
	snap := a.snapshotExplicitCompression()

	used := a.visibleContextTokens(snap)
	if want := a.estimatedVisibleRequestTokens(snap.visible); used != want {
		t.Fatalf("compress 门槛读数 %d ≠ 计费尺读数 %d——同一流程两把尺（tl1395 增量面）", used, want)
	}

	// fixture sanity：旧粗尺确实把同一视图读进「太短不可折」档，测得出盲区。
	coarse := 0
	if chars := charsOfMessages(snap.visible); chars > 0 {
		if perChar := a.tokPerChar(); perChar > 0 {
			coarse = int(float64(chars) / perChar)
		} else {
			coarse = chars
		}
	}
	floor := int(float64(a.compactTrigger()) * explicitCompressFloorRatio)
	if coarse >= floor {
		t.Fatalf("fixture 失真：旧粗尺读数 %d 也过了门槛 %d——测不出粗尺图片盲区", coarse, floor)
	}
	if used < floor {
		t.Fatalf("计费尺读数 %d < 门槛 %d——图片没进门槛读数，统一失效（tl1395）", used, floor)
	}
}

// imageLeanSession 的文本量守恒钉：fixture 文本一旦变胖，粗尺的 ~4
// tokens/字节高读会把它抬过门槛，钉③就测不出图片盲区了（首跑教训固化为
// 断言，防 fixture 回潮）。
func TestImageLeanSessionStaysLean(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	sess := imageLeanSession(9)
	textBytes := 0
	for _, m := range sess.Messages {
		textBytes += msgChars(m)
	}
	if coarse := int(float64(textBytes) / fallbackTokPerChar); coarse >= 8_000 {
		t.Fatalf("imageLeanSession 文本量失控：%d 字节在旧粗尺下读 %d，逼近窗口档门槛——fixture 需重新瘦身", textBytes, coarse)
	}
	if billed := a.estimatedPromptTokens(provider.ModelMessages(sess.Messages)); billed < 15_000 {
		t.Fatalf("fixture 失真：9 张 1080p 的计费读数只有 %d（需 ≥15000 扛过各档门槛）", billed)
	}
}
