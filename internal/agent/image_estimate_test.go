package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// 任务 1025：图片按分辨率折算 token。这些测试把嗅探器和折算公式钉在
// provider 侧的公开计价带上：可读图片不再随 base64 长度漂移。

func pngDataURL(w, h int, extra ...byte) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	raw := append(buf.Bytes(), extra...)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

// noise returns pseudo-random trailing bytes so two same-dimension images
// differ in encoded length without changing their pixels.
func noise(n int) []byte {
	out := make([]byte, n)
	seed := uint32(0x9E3779B9)
	for i := range out {
		seed = seed*1664525 + 1013904223
		out[i] = byte(seed >> 24)
	}
	return out
}

func jpegDataURL(w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func gifDataURL(w, h int) string {
	palette := color.Palette{color.Black, color.White}
	img := image.NewPaletted(image.Rect(0, 0, w, h), palette)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// webPDataURLForTest builds a minimal VP8X container with the canvas size —
// enough for the header sniffer without a webp encoder dependency.
func webPDataURLForTest(w, h int) string {
	var body bytes.Buffer
	body.WriteString("RIFF")
	body.Write([]byte{0, 0, 0, 0}) // file size placeholder
	body.WriteString("WEBP")
	body.WriteString("VP8X")
	body.Write([]byte{10, 0, 0, 0}) // chunk size
	body.WriteByte(0)               // flags
	body.Write([]byte{0, 0, 0})     // reserved
	body.Write([]byte{byte(w - 1), byte((w - 1) >> 8), byte((w - 1) >> 16)})
	body.Write([]byte{byte(h - 1), byte((h - 1) >> 8), byte((h - 1) >> 16)})
	raw := body.Bytes()
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(raw)-8))
	return "data:image/webp;base64," + base64.StdEncoding.EncodeToString(raw)
}

func splitDataURLPayload(dataURL string) (string, string, bool) {
	return provider.ParseImageDataURL(dataURL)
}

// providerRequestWithImage builds a two-message request: a text-only opener
// plus one user message carrying the generated screenshot.
func providerRequestWithImage(t *testing.T, w, h int) provider.Request {
	t.Helper()
	return provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "context line"},
		{Role: provider.RoleUser, Content: "look", Images: []string{pngDataURL(w, h)}},
	}}
}

func TestSniffEncodedImageDimensions(t *testing.T) {
	cases := []struct {
		name string
		url  string
		w, h int
	}{
		{"png", pngDataURL(640, 480), 640, 480},
		{"jpeg", jpegDataURL(800, 600), 800, 600},
		{"gif", gifDataURL(320, 240), 320, 240},
		{"webp-vp8x", webPDataURLForTest(1024, 768), 1024, 768},
		{"png-with-noise-tail", pngDataURL(100, 80, noise(1<<20)...), 100, 80},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h, ok := sniffDimensionsOfDataURL(t, tc.url)
			if !ok || w != tc.w || h != tc.h {
				t.Fatalf("sniff = %dx%d ok=%v, want %dx%d", w, h, ok, tc.w, tc.h)
			}
		})
	}
}

func sniffDimensionsOfDataURL(t *testing.T, dataURL string) (int, int, bool) {
	t.Helper()
	_, payload, ok := splitDataURLPayload(dataURL)
	if !ok {
		t.Fatal("not a base64 data URL")
	}
	return sniffEncodedImageDimensions(payload)
}

// TestSniffRejectsUnreadableHeaders pins the fallback trigger: anything the
// sniffer cannot read must report false so imageEstimateTokens prices it at
// the conservative flat cap instead of guessing.
func TestSniffRejectsUnreadableHeaders(t *testing.T) {
	for _, payload := range []string{
		base64.StdEncoding.EncodeToString([]byte("AAAAAAAAAAAAAAAA")),       // no magic
		base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'}),      // truncated PNG
		base64.StdEncoding.EncodeToString([]byte("RIFF0000WEBP")),           // no chunk
		base64.StdEncoding.EncodeToString([]byte("not an image at all!!!")), // plain text
	} {
		if w, h, ok := sniffEncodedImageDimensions(payload); ok {
			t.Fatalf("unreadable payload sniffed as %dx%d", w, h)
		}
	}
	// 非 data URL（HTTP URL / file id）走同一 fallback。
	if got := imageEstimateTokens("https://example.com/shot.png"); got != imageEstimateFallbackTokens {
		t.Fatalf("url image tokens = %d, want fallback %d", got, imageEstimateFallbackTokens)
	}
	if got := imageEstimateTokens("file-api-abc123"); got != imageEstimateFallbackTokens {
		t.Fatalf("file-id image tokens = %d, want fallback %d", got, imageEstimateFallbackTokens)
	}
}

// TestImageEstimateMatchesProviderBand pins 1080p and 4K screenshots to the
// published vision-pricing bands (Anthropic area/750, Qwen tile grids land in
// the same order of magnitude). This is the "真实计费" column of the 三口径
// 对照表 expressed as an executable contract.
func TestImageEstimateMatchesProviderBand(t *testing.T) {
	cases := []struct {
		name     string
		w, h     int
		min, max int
	}{
		{"icon 64x64", 64, 64, imageEstimateTokenFloor, imageEstimateTokenFloor},
		{"thumb 400x300", 400, 300, imageEstimateTokenFloor, 600},
		{"720p 1280x720", 1280, 720, 1300, 2000},
		{"1080p 1920x1080", 1920, 1080, 1600, 2400},
		// 4K 长边压到 1568：1568×882 ÷750≈1844 → +15% ≈2121 —— 与 1080p 同价，
		// 正是 provider 侧缩边计价的行为。
		{"4K 3840x2160 (clamped to 1568 long edge)", 3840, 2160, 1800, 2400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imageEstimateTokens(pngDataURL(tc.w, tc.h))
			if got < tc.min || got > tc.max {
				t.Fatalf("%s tokens = %d, want [%d,%d]", tc.name, got, tc.min, tc.max)
			}
		})
	}
	// 长边收缩契约：超过 1568 的边按 1568 计。
	clampedW, clampedH := imageEstimateScaledDims(3840, 2160)
	if clampedW != 1568 {
		t.Fatalf("4K width after clamp = %d, want 1568", clampedW)
	}
	if clampedH >= clampedW {
		t.Fatalf("clamped height %d must stay below width %d for landscape", clampedH, clampedW)
	}
}

// TestImageEstimateIndependentOfEncodingLength: same pixels, wildly different
// base64 lengths (noise tail / re-encode) price identically — the caliber is
// resolution-based, not byte-based (this is what stops a 2MB screenshot from
// estimating at ~500K tokens while a tiny icon no longer over-pays 2K).
func TestImageEstimateIndependentOfEncodingLength(t *testing.T) {
	plain := imageEstimateTokens(pngDataURL(640, 480))
	padded := imageEstimateTokens(pngDataURL(640, 480, noise(2<<20)...))
	if plain != padded {
		t.Fatalf("pricing depends on encoding length: %d vs %d", plain, padded)
	}
	// 2MB 不可读假图（1010 现场形态）仍是封顶价，不是 500K。
	unreadable := imageEstimateTokens("data:image/png;base64," + string(bytes.Repeat([]byte("A"), 2<<20)))
	if unreadable != imageEstimateFallbackTokens {
		t.Fatalf("unreadable 2MB image = %d, want flat cap %d", unreadable, imageEstimateFallbackTokens)
	}
}

// TestEstimatedShapeTokensAddsImages pins the composition contract: text
// prices through the calibration ratio (or cold fallback) and images add on
// top — the single shape feeding admission, UI gauge and telemetry proj.
func TestEstimatedShapeTokensAddsImages(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	req := providerRequestWithImage(t, 1920, 1080)
	shape := a.requestCalibrationShape(req)
	total := a.estimatedShapeTokens(shape)
	textOnly := shape
	textOnly.imageTokens = 0
	if total <= 0 || textOnly.requestChars <= 0 {
		t.Fatal("fixture produced an empty shape")
	}
	if diff := total - a.estimatedShapeTokens(textOnly); diff != shape.imageTokens {
		t.Fatalf("image increment = %d, want imageTokens %d", diff, shape.imageTokens)
	}
	// 与纯文本请求（无图消息）同源：图只贡献 imageTokens。
	plain := requestCalibrationShapeOf(provider.Request{Messages: req.Messages[:1]})
	if got := a.estimatedShapeTokens(plain); got > total-shape.imageTokens+1 {
		t.Fatalf("plain text estimate %d exceeds image-free total %d", got, total-shape.imageTokens)
	}
}

// TestCalibrationSubtractsImageTokens: provider usage bills images; the
// calibrated text ratio must not absorb them (任务 1025), or an image session
// would over-price every later text-only turn.
func TestCalibrationSubtractsImageTokens(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	req := providerRequestWithImage(t, 1920, 1080)
	shape := a.requestCalibrationShape(req)
	imageTokens := shape.imageTokens
	if imageTokens <= 0 {
		t.Fatal("fixture produced no image tokens")
	}
	// 模拟 provider 回执：真实 prompt = 文本 + 图片计价（这里假设与本地一致）。
	textTruth := int(float64(shape.requestChars) * fallbackTokPerChar)
	a.setPromptTokenCalibration(textTruth+imageTokens, shape)

	// 校准比率必须回到纯文本比率：任何纯文本形状按 ~0.25/char 计，
	// 不被图片抬高。这里用一个更长的纯文本轮做验证。
	longText := provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("word ", 800)},
	}}
	longShape := requestCalibrationShapeOf(longText)
	cal, ok := a.calibratedPromptTokens(longShape)
	if !ok {
		t.Fatal("calibration not accepted")
	}
	if want := int(float64(longShape.requestChars) * fallbackTokPerChar); absInt(cal-want) > want/8 {
		t.Fatalf("calibrated text tokens = %d, want ~%d (image tokens leaked into the ratio)", cal, want)
	}
	// tokPerChar（fold 经济学）同样不吸图片：比率 = (promptTokens−imageTokens)
	// / compactChars。未扣图时 2129/16≈133 会超出 [0.05,2] 界而跌回冷值，
	// 扣图后才命中校准路径——这个断言正是扣图行为的判别器。
	if want := float64(textTruth) / float64(shape.compactChars); want > 0.05 && want < 2 {
		if r := a.tokPerChar(); absFloat(r-want) > want*0.01 {
			t.Fatalf("tokPerChar = %v, want %v (image tokens not subtracted)", r, want)
		}
	} else {
		t.Fatalf("fixture ratio %v out of calibrated range; make content dominate framing", want)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
