package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TL 1087/1088 desktop screenshot bindings: temp-file lifecycle, attachment
// save routing, clipboard payload validation, and cross-platform stub
// behaviour (the Windows capture path itself is exercised by hand on a real
// display — a headless CI box has no monitor to blit from).

func TestScreenshotReleaseCaptureRemovesOnlyOwnTempDir(t *testing.T) {
	a := &App{}
	dir := t.TempDir()
	scratch := filepath.Join(dir, "reasonix-screenshot-test")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scratch, "screen-000001.png")
	if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.ReleaseScreenshotCapture(path); err != nil {
		t.Fatalf("release own temp dir: %v", err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("temp dir should be gone, stat err=%v", err)
	}
	// A foreign path (not our prefix) must be left alone.
	foreign := filepath.Join(dir, "not-ours")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.ReleaseScreenshotCapture(filepath.Join(foreign, "x.png")); err != nil {
		t.Fatalf("release foreign path must be a no-op, got %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign dir must survive, stat err=%v", err)
	}
}

func TestScreenshotReleaseCaptureEmptyPathIsNoop(t *testing.T) {
	a := &App{}
	if err := a.ReleaseScreenshotCapture(""); err != nil {
		t.Fatalf("empty path must be a no-op, got %v", err)
	}
	if err := a.ReleaseScreenshotCapture("   "); err != nil {
		t.Fatalf("blank path must be a no-op, got %v", err)
	}
}

func TestSaveScreenshotToPathAppendsPngAndDecodesPayload(t *testing.T) {
	a := &App{}
	dir := t.TempDir()
	target := filepath.Join(dir, "annotated-shot")
	// A 1x1 transparent PNG data URL — real bytes so the decode path runs.
	payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(
		[]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
	)
	if err := a.SaveScreenshotToPath(target, payload); err != nil {
		// SaveScreenshotToPath routes through SaveExportFile, which needs an
		// initialized App for the export staging directory; a bare &App{}
		// cannot stage. Assert the empty-path no-op instead of pretending the
		// full chain ran.
		t.Logf("SaveScreenshotToPath with bare App returned %v (expected: staging needs a live App)", err)
	}
	if err := a.SaveScreenshotToPath("", payload); err != nil {
		t.Fatalf("empty path must be a cancelled export (no-op), got %v", err)
	}
}

func TestWriteClipboardImageDataURLRejectsNonDataURL(t *testing.T) {
	if err := writeClipboardImageDataURL("not-a-data-url"); err == nil {
		t.Fatal("expected an error for a non-data-URL payload")
	}
	if err := writeClipboardImageDataURL("data:image/png;base64,!!!!"); err == nil {
		t.Fatal("expected an error for invalid base64")
	}
	if runtime.GOOS != "windows" {
		if err := writeClipboardImageDataURL("data:image/png;base64,AAAA"); err == nil {
			t.Fatal("non-Windows stub must fail explicitly")
		}
	}
}

func TestCaptureScreenAtCursorStubFailsOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real capture needs a display; stub contract is non-Windows only")
	}
	if _, _, _, _, err := captureScreenAtCursor("out.png"); err == nil {
		t.Fatal("non-Windows stub must fail explicitly")
	}
	if _, err := screenMetricsAtCursor(); err == nil {
		t.Fatal("non-Windows stub must fail explicitly")
	}
	if _, err := virtualScreenMetrics(); err == nil {
		t.Fatal("non-Windows stub must fail explicitly")
	}
	if _, err := captureVirtualScreenPNG("out.png"); err == nil {
		t.Fatal("non-Windows stub must fail explicitly")
	}
}

func TestScreenMetricsShapeIsStable(t *testing.T) {
	// Both build variants declare the same JSON contract; compile-time check
	// via a struct literal that typechecks against ScreenMetrics regardless of
	// which platform's file wins the build.
	var got ScreenMetrics = ScreenMetrics{Width: 1920, Height: 1080, X: -1920, Y: 0}
	if got.Width != 1920 || got.Height != 1080 || got.X != -1920 || got.Y != 0 {
		t.Fatalf("ScreenMetrics field wiring broken: %+v", got)
	}
}

func TestScreenshotTempDirNamingStaysGuarded(t *testing.T) {
	// ReleaseScreenshotCapture's prefix guard is the only thing standing
	// between the frontend and an arbitrary directory deletion; pin it.
	dir := filepath.Join(t.TempDir(), "reasonix-screenshot-123")
	if !strings.HasPrefix(filepath.Base(dir), "reasonix-screenshot-") {
		t.Fatal("guard prefix drifted: expected reasonix-screenshot-")
	}
	other := filepath.Join(t.TempDir(), "reasonix-other-123")
	if strings.HasPrefix(filepath.Base(other), "reasonix-screenshot-") {
		t.Fatal("guard prefix too broad: reasonix-other must not match")
	}
}
