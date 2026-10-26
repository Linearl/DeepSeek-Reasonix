package control

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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
// exported entry point must detect, compress, and encode — and must refuse
// anything else with an explicit error (a silent empty string is what painted
// the overlay black in the first place).
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
