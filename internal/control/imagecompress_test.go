package control

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"testing"
)

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestCompressForVisionDownscalesOversizedPNG(t *testing.T) {
	raw := makeTestPNG(t, 3000, 1500)
	out, mime := compressForVision(raw, "image/png")
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode out: %v", err)
	}
	// Pixel count is what governs vision token cost; assert the reduction there
	// (byte size isn't a robust invariant for synthetic, highly-compressible input).
	if cfg.Width != maxVisionDim || cfg.Height != 1500*maxVisionDim/3000 {
		t.Errorf("dims = %dx%d, want %dx%d", cfg.Width, cfg.Height, maxVisionDim, 1500*maxVisionDim/3000)
	}
	if cfg.Width*cfg.Height >= 3000*1500 {
		t.Errorf("pixel count %d not reduced from %d", cfg.Width*cfg.Height, 3000*1500)
	}
}

func TestCompressForVisionKeepsSmallImageVerbatim(t *testing.T) {
	raw := makeTestPNG(t, 100, 80)
	out, mime := compressForVision(raw, "image/png")
	if mime != "image/png" || !bytes.Equal(out, raw) {
		t.Errorf("an in-budget image must pass through unchanged (got %d bytes, mime %q)", len(out), mime)
	}
}

func TestCompressForVisionJPEGStaysJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2400, 1200)), nil); err != nil {
		t.Fatal(err)
	}
	out, mime := compressForVision(buf.Bytes(), "image/jpeg")
	if mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg", mime)
	}
	if cfg, _, _ := image.DecodeConfig(bytes.NewReader(out)); cfg.Width != maxVisionDim {
		t.Errorf("width = %d, want %d", cfg.Width, maxVisionDim)
	}
}

func TestCompressForVisionPassesThroughUndecodable(t *testing.T) {
	raw := []byte("<svg xmlns='...'></svg>")
	out, mime := compressForVision(raw, "image/svg+xml")
	if mime != "image/svg+xml" || !bytes.Equal(out, raw) {
		t.Error("an undecodable mime must pass through unchanged")
	}
}

// tl 1167: the desktop screenshot preview bridge serves os.MkdirTemp capture
// files whose format is only provable from the bytes (no trusted mime), so the
// exported entry point must detect, encode — and refuse anything else with an
// explicit error (a silent empty string is what painted the overlay black in
// the first place). Since tl 1178 the payload itself is served verbatim unless
// it exceeds the preview byte ceiling.
func TestPreviewImageDataURLServesRealImage(t *testing.T) {
	url, err := PreviewImageDataURL(makeTestPNG(t, 100, 80), "")
	if err != nil {
		t.Fatalf("PreviewImageDataURL: %v", err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") || len(url) <= len("data:image/png;base64,") {
		t.Fatalf("data URL malformed: %q", url[:min(40, len(url))])
	}
}

func TestPreviewImageDataURLRejectsNonImage(t *testing.T) {
	if _, err := PreviewImageDataURL([]byte("definitely not an image"), ""); err == nil {
		t.Fatal("text payload must be rejected, not served as an empty string")
	}
	if _, err := PreviewImageDataURL(nil, ""); err == nil {
		t.Fatal("empty payload must be rejected")
	}
}

func TestPreviewImageDataURLRejectsOversize(t *testing.T) {
	huge := make([]byte, maxImageAttachmentBytes+1)
	if _, err := PreviewImageDataURL(huge, ""); err == nil {
		t.Fatal("over-ceiling payload must be rejected")
	}
}

// makeNoisePNG builds a PNG whose pixels are deterministic high-entropy noise,
// so the encoded size tracks the raw pixel data (deflate cannot compress it) —
// the reliable way to get a payload past previewLosslessMaxBytes in a test.
func makeNoisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)),
				B: uint8(rng.Intn(256)), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// tl 1178: the preview is painted for human eyes on a local surface, so a wide
// capture must keep its full resolution — the model-vision rescale budget
// (maxVisionDim) does not apply to this channel while the payload fits under
// previewLosslessMaxBytes. 3000px wide is a ordinary desktop capture the old
// code blurred down to 1568.
func TestPreviewImageDataURLKeepsCaptureResolution(t *testing.T) {
	raw := makeTestPNG(t, 3000, 2000)
	url, err := PreviewImageDataURL(raw, "")
	if err != nil {
		t.Fatalf("PreviewImageDataURL: %v", err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("mime must stay image/png, got %q", url[:min(40, len(url))])
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if cfg.Width != 3000 || cfg.Height != 2000 {
		t.Fatalf("preview dims = %dx%d, want 3000x2000 (full capture resolution)", cfg.Width, cfg.Height)
	}
}

// tl 1178: degradation is byte-size-gated, not automatic. A capture under
// previewLosslessMaxBytes is served verbatim (byte-identical — the desktop
// bridge pins the same invariant end to end); one above it falls back to the
// vision pipeline so the data URL handed across the IPC bridge stays bounded.
func TestPreviewImageDataURLDegradesOnlyAboveThreshold(t *testing.T) {
	small := makeTestPNG(t, 100, 80)
	url, err := PreviewImageDataURL(small, "")
	if err != nil {
		t.Fatalf("PreviewImageDataURL: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	if !bytes.Equal(decoded, small) {
		t.Fatalf("an under-ceiling capture must be served verbatim (got %d bytes, want %d)", len(decoded), len(small))
	}

	raw := makeNoisePNG(t, 1700, 1700)
	if len(raw) <= previewLosslessMaxBytes {
		t.Fatalf("fixture is %d bytes, must exceed the %d byte threshold to exercise this path", len(raw), previewLosslessMaxBytes)
	}
	url, err = PreviewImageDataURL(raw, "")
	if err != nil {
		t.Fatalf("PreviewImageDataURL: %v", err)
	}
	decoded, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if cfg.Width != maxVisionDim || cfg.Height != maxVisionDim {
		t.Fatalf("over-ceiling capture dims = %dx%d, want the vision budget %dx%d", cfg.Width, cfg.Height, maxVisionDim, maxVisionDim)
	}
}
