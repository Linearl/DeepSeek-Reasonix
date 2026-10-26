package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TL 1167: the screenshot preview bridge serves the os.MkdirTemp PNG a capture
// binding just produced. The generic AttachmentDataURL must keep rejecting that
// absolute path (cleanAttachmentPath's relative-only invariant is a security
// boundary), so this bridge carries its own guard — same directory-prefix
// criteria as ReleaseScreenshotCapture, plus temp-root containment and an
// image extension whitelist. Every case below pins one guard edge; every
// refusal must be an explicit error, never a silent empty string.

func makePNGFile(t *testing.T, path string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return buf.Bytes()
}

func TestScreenshotPreviewDataURLServesOwnTempCapture(t *testing.T) {
	a := &App{}
	dir := filepath.Join(t.TempDir(), "reasonix-screenshot-preview")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "screen-000001.png")
	raw := makePNGFile(t, path)
	got, err := a.ScreenshotPreviewDataURL(path)
	if err != nil {
		t.Fatalf("own capture temp file must be served: %v", err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("data URL malformed: %q", got[:min(60, len(got))])
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, prefix))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("payload bytes differ from the file (got %d bytes, want %d)", len(decoded), len(raw))
	}
}

// tl 1178: end to end, a wide capture (beyond the 1568px model-vision budget)
// must reach the overlay at full resolution — the preview is painted for human
// eyes, and the old code blurred it through compressForVision before encoding.
func TestScreenshotPreviewDataURLKeepsCaptureResolution(t *testing.T) {
	a := &App{}
	dir := filepath.Join(t.TempDir(), "reasonix-screenshot-preview")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for y := range 2000 {
		for x := range 3000 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x ^ y) % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := filepath.Join(dir, "screen-000003.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	got, err := a.ScreenshotPreviewDataURL(path)
	if err != nil {
		t.Fatalf("own capture temp file must be served: %v", err)
	}
	const marker = ";base64,"
	if !strings.HasPrefix(got, "data:image/png"+marker) {
		t.Fatalf("mime must stay image/png, got %q", got[:min(60, len(got))])
	}
	decoded, err := base64.StdEncoding.DecodeString(got[strings.Index(got, marker)+len(marker):])
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode preview payload: %v", err)
	}
	if cfg.Width != 3000 || cfg.Height != 2000 {
		t.Fatalf("preview dims = %dx%d, want 3000x2000 (full capture resolution)", cfg.Width, cfg.Height)
	}
}

func TestScreenshotPreviewDataURLRejectsForeignPaths(t *testing.T) {
	a := &App{}
	// Relative attachment-style paths stay AttachmentDataURL's business — and
	// they are exactly what the old (buggy) preview call was fed.
	for _, path := range []string{
		"", "   ",
		".reasonix/attachments/x.png",
		filepath.Join(".reasonix", "attachments", "x.png"),
	} {
		if _, err := a.ScreenshotPreviewDataURL(path); err == nil {
			t.Fatalf("relative/empty path %q must be rejected", path)
		} else if !strings.Contains(err.Error(), "screenshot preview") {
			t.Fatalf("rejection must name the bridge, got %v", err)
		}
	}

	// Under the temp root but not our capture directory: %TEMP%\other\a.png.
	tmp := t.TempDir()
	foreign := filepath.Join(tmp, "other")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	makePNGFile(t, filepath.Join(foreign, "a.png"))
	if _, err := a.ScreenshotPreviewDataURL(filepath.Join(foreign, "a.png")); err == nil {
		t.Fatal("a non-capture temp directory must be rejected")
	}

	// Absolute outside the temp root even with the capture directory name:
	// the temp-root containment must hold on its own. The temp-prefix check
	// runs before any stat, so the path need not exist — t.TempDir()'s own
	// parent still sits under the temp root, so pin the volume root instead.
	outsidePath := filepath.Join(filepath.VolumeName(tmp), "reasonix-screenshot-escape", "a.png")
	if _, err := a.ScreenshotPreviewDataURL(outsidePath); err == nil {
		t.Fatalf("a capture-named directory outside the temp root must be rejected: %q", outsidePath)
	}

	// A system directory that is not under temp at all (platform-shaped).
	systemPath := filepath.Join("C:\\Windows", "a.png")
	if runtime.GOOS != "windows" {
		systemPath = "/etc/a.png"
	}
	if _, err := a.ScreenshotPreviewDataURL(systemPath); err == nil {
		t.Fatalf("system path %q must be rejected", systemPath)
	}
}

func TestScreenshotPreviewDataURLRejectsNonImageFiles(t *testing.T) {
	a := &App{}
	dir := filepath.Join(t.TempDir(), "reasonix-screenshot-preview")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A non-image extension is refused before the file is even opened.
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ScreenshotPreviewDataURL(txt); err == nil {
		t.Fatal("non-image extension must be rejected")
	}
	// An image extension over non-image bytes is caught by content detection.
	fake := filepath.Join(dir, "screen-000002.png")
	if err := os.WriteFile(fake, []byte("definitely not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ScreenshotPreviewDataURL(fake); err == nil {
		t.Fatal("non-image bytes behind a .png extension must be rejected")
	}
	// A missing file is an explicit error too, not an empty string.
	if _, err := a.ScreenshotPreviewDataURL(filepath.Join(dir, "missing.png")); err == nil {
		t.Fatal("missing file must be rejected")
	}
}
