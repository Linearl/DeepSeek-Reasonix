package agent

import "testing"

// tl1395 增量：compress 工具的折叠门槛读数（visibleContextTokens）统一到计费尺。
//
// 断点：同一 explicit 压缩流程里，门槛判据（used < 0.5×触发线 → 拒折）曾用
// charsOfMessages×tokPerChar 的文本粗尺，而同流程的 SourceTokens 报告与
// projection≥source 装门用 estimatedVisibleRequestTokens 计费尺。粗尺看不见
// 图片：截图重的会话在门槛上被读成「太短不可折」，模型主动申请的折叠被拒，
// 图片压力退回等 400 救援（压前反升的入口通道之一）。统一后规划判据与计费
// 读数同源同单位（tl1395 主件同一原则的最后一处消费方）。

// TestCompressFloorReadsTheBillingRuler pins the unification: the gate read
// equals the calibrated billing readout for the same view, on a fixture the
// old census demonstrably misjudged — screenshots push the billed size far
// above the fold floor while the text census reads "too short to fold".
func TestCompressFloorReadsTheBillingRuler(t *testing.T) {
	const window = 40_000
	// 10 张 1080p ≈ 21K 计费 tokens；粗尺只看见 ~2K 文本 tokens。
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, rulerSession(10), window)
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
