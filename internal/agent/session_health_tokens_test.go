package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 1506：超窗判据三段口径（用户终裁 20261109 08:49）。三钉对应任务书
// 验收原文：
//  ① 含图会话（显示 265K 形态）打开不再误触发 Diet（修前红护栏钉死）；
//  ② 真超窗会话（usage 实账 ≥95% 窗）仍触发；
//  ③ 判据兜底估算与右栏读数同口径族（1025 分辨率计价，与 base64 长度无关）。

// writeHealthTranscript 落一个 schema-1 .jsonl 检查点供磁盘判据读取——
// 复用 writeJSONLCheckpointAtomic（自愈链路自己的规范写手），格式漂移时
// 测试与被测代码一起红，不产生假绿。
func writeHealthTranscript(t *testing.T, msgs []provider.Message) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "health-tl1506.jsonl")
	if err := writeJSONLCheckpointAtomic(path, msgs, make([]time.Time, len(msgs))); err != nil {
		t.Fatalf("write fixture transcript: %v", err)
	}
	return path
}

// legacyProjectionCaliber 复刻 tl1506 之前的判据口径
// （estimateMessagesTokens + 无封顶 base64 len/4），只用于钉住测试夹具的
// 「修前红」属性：夹具若退化到旧口径也不再过线，测试必须响亮地红，而不是
// 静默失去判别力。
func legacyProjectionCaliber(msgs []provider.Message) int {
	tokens := estimateMessagesTokens(msgs)
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		for _, img := range m.Images {
			tokens += len(img) / 4
		}
	}
	return tokens
}

// dietLine 是 projectionOverflowRatio 对应的触发线，测试里可读化。
func dietLine(window int) int { return int(float64(window) * projectionOverflowRatio) }

// 钉①：含图会话（右栏计价读数远低于窗）打开不再误触发 Diet。
// 现场形态：截图 payload 让旧判据（base64/4 无封顶）虚高过 95% 线
// （tl1506 现场：右栏 265.8K/1M，打开即 strip_images+fold）。
// 修后：无实账 → 计价口径估算 → 1080p 分辨率价 ≈2K/图，远离触发线。
func TestImageSessionNoLongerTriggersDietOnOpen(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system header"},
		{Role: provider.RoleUser, Content: "look at this"},
		// 噪声尾撑大 payload（模拟真实截图的熵），PNG 头部完好保持可嗅探：
		// 旧判据 len/4 ≈ 1M ≥ 950K（修前红）；新计价 = 分辨率价 ≈2121（修后绿）。
		{Role: provider.RoleUser, Content: "screenshot", Images: []string{pngDataURL(1920, 1080, noise(3<<20)...)}},
	}
	window := defaultHealthWindowTokens
	if old := legacyProjectionCaliber(msgs); old < dietLine(window) {
		t.Fatalf("fixture no longer a pre-fix false positive: legacy caliber %d < diet line %d", old, dietLine(window))
	}
	report := InspectSessionHealth(writeHealthTranscript(t, msgs), HealthInspectOptions{WindowTokens: window})
	if !report.Healthy {
		t.Fatalf("image session still flagged for diet: %+v", report.Issues)
	}
	if report.TokenSource != HealthTokenSourceProjectionEstimate {
		t.Fatalf("token source = %q, want %q", report.TokenSource, HealthTokenSourceProjectionEstimate)
	}
	if report.ProjectionTokens >= dietLine(window) {
		t.Fatalf("pricing-caliber estimate %d still crosses the diet line %d", report.ProjectionTokens, dietLine(window))
	}
}

// 钉②：真超窗会话（usage 实账 ≥95% 窗）仍触发 Diet；阈值语义不变（低于
// 95% 不触发）；有实账时按实账判而非取 max（低实账压得过虚高估算）。
func TestTrustedActualUsageStillTriggersDiet(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system header"},
		{Role: provider.RoleUser, Content: "hello"},
	}
	window := defaultHealthWindowTokens
	path := writeHealthTranscript(t, msgs)

	report := InspectSessionHealth(path, HealthInspectOptions{
		WindowTokens:        window,
		ActualContextTokens: 960_000, // 96% ≥ 95%
	})
	if report.Healthy {
		t.Fatal("actual-usage overflow no longer triggers the diet criterion")
	}
	if report.TokenSource != HealthTokenSourceUsageActual {
		t.Fatalf("token source = %q, want %q", report.TokenSource, HealthTokenSourceUsageActual)
	}
	if report.ProjectionTokens != 960_000 {
		t.Fatalf("projection tokens = %d, want the injected actual 960000", report.ProjectionTokens)
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == HealthProjectionOverflow {
			found = true
			if !strings.Contains(issue.Detail, "tokens_source=usage_actual") {
				t.Fatalf("issue detail missing caliber tag: %q", issue.Detail)
			}
		}
	}
	if !found {
		t.Fatal("no projection_overflow issue emitted")
	}

	// 负对照：实账 90% < 95% 线 ⇒ 不触发（判据阈值未被本件改动）。
	below := InspectSessionHealth(path, HealthInspectOptions{
		WindowTokens:        window,
		ActualContextTokens: 900_000,
	})
	if !below.Healthy {
		t.Fatalf("below-threshold actual flagged for diet: %+v", below.Issues)
	}

	// 判据选择器语义：有实账按实账判（不是 max(实账,估算)）——修复后判定
	// 不得被估算器的残差反向拉回误触发。
	imgMsgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system header"},
		{Role: provider.RoleUser, Content: "screenshot", Images: []string{pngDataURL(1920, 1080, noise(3<<20)...)}},
	}
	if legacyProjectionCaliber(imgMsgs) < dietLine(window) {
		t.Fatal("fixture decayed: estimate-side fixture no longer crosses the diet line")
	}
	override := InspectSessionHealth(writeHealthTranscript(t, imgMsgs), HealthInspectOptions{
		WindowTokens:        window,
		ActualContextTokens: 100_000,
	})
	if !override.Healthy {
		t.Fatalf("low actual must override the estimate, still flagged: %+v", override.Issues)
	}
	if override.TokenSource != HealthTokenSourceUsageActual {
		t.Fatalf("override token source = %q, want %q", override.TokenSource, HealthTokenSourceUsageActual)
	}
}

// 钉③：判据兜底估算与右栏读数同口径族——图片走 1025 分辨率计价
// （imageEstimateTokens，与显示族共用同一函数），与 base64 编码长度无关。
// 对照组必须与图片组**同形**（同消息数、仅剥 Images）：estimateMessagesTokens
// 按消息计框架 token，消息数不同的对照会把框架差混进图片增量（首版红因）。
func TestHealthEstimateMatchesPricingCaliber(t *testing.T) {
	img := pngDataURL(1920, 1080)
	withImg := []provider.Message{
		{Role: provider.RoleSystem, Content: "system header"},
		{Role: provider.RoleUser, Content: "look"},
		{Role: provider.RoleUser, Content: "screenshot", Images: []string{img}},
	}
	// 同形控制组：同 3 条消息，仅剥 Images——增量即纯图片段。
	control := make([]provider.Message, len(withImg))
	copy(control, withImg)
	for i := range control {
		control[i].Images = nil
	}
	est := estimateProjectionTokens(withImg)
	base := estimateProjectionTokens(control)
	if want := base + imageEstimateTokens(img); est != want {
		t.Fatalf("estimate %d != text %d + priced image %d", est, base, imageEstimateTokens(img))
	}
	// 同像素不同编码长度同价（1025 契约在判据兜底段同样成立；同形对照，
	// 噪声尾只改编码不改分辨率）。
	padded := append([]provider.Message{}, withImg...)
	padded[2].Images = []string{pngDataURL(1920, 1080, noise(2<<20)...)}
	if got := estimateProjectionTokens(padded); got != est {
		t.Fatalf("criterion estimate depends on encoding length: %d vs %d", got, est)
	}
	// 图片计价可见性（1010 契约不回退）：图片段价格 ≥ floor，不为零。
	if imagePart := est - base; imagePart < imageEstimateTokenFloor {
		t.Fatalf("image contributes %d tokens, want >= floor %d", imagePart, imageEstimateTokenFloor)
	}
}
