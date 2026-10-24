package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/control"
)

// Screenshot capture bindings for the desktop screenshot button (tl 1087,
// plan A: capture the monitor under the mouse cursor, annotate in the
// overlay, then save into the workspace attachments). The clipboard write
// (tl 1088) rides the same temp-PNG → Go-side clipboard path.

// ScreenShotCapture is one full-screen capture: the PNG path is a temp file
// the frontend loads once into the overlay (so the overlay itself is never
// in the captured frame) and deletes through ReleaseScreenshotCapture.
type ScreenShotCapture struct {
	Path string `json:"path"`
	// Width/Height are the captured monitor's pixel dimensions.
	Width  int `json:"width"`
	Height int `json:"height"`
	// X/Y are the monitor's virtual-desktop origin; nonzero on multi-monitor
	// layouts so the overlay can map cursor coordinates to screenshot pixels.
	X int `json:"x"`
	Y int `json:"y"`
}

var screenshotCaptureSeq uint64

// captureScreenshotTempDir creates the per-capture temp directory both
// capture entry points share.
func captureScreenshotTempDir() (string, string, error) {
	dir, err := os.MkdirTemp("", "reasonix-screenshot-*")
	if err != nil {
		return "", "", fmt.Errorf("create screenshot temp dir: %w", err)
	}
	screenshotCaptureSeq++
	return dir, filepath.Join(dir, fmt.Sprintf("screen-%06d.png", screenshotCaptureSeq)), nil
}

// CaptureScreenshotAtCursor captures the full screen of the monitor that
// currently holds the mouse cursor and returns the temp PNG location plus
// geometry. The caller owns the temp file and must call
// ReleaseScreenshotCapture when the overlay closes.
func (a *App) CaptureScreenshotAtCursor() (ScreenShotCapture, error) {
	dir, path, err := captureScreenshotTempDir()
	if err != nil {
		return ScreenShotCapture{}, err
	}
	w, h, originX, originY, err := captureScreenAtCursor(path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return ScreenShotCapture{}, err
	}
	return ScreenShotCapture{Path: path, Width: w, Height: h, X: originX, Y: originY}, nil
}

// CaptureScreenshotVirtualDesktop captures the whole virtual desktop (all
// monitors) so a selection can span displays (tl 1088 cross-monitor capture).
// Geometry comes back in virtual-desktop coordinates; the overlay maps its
// client coordinates onto these pixels exactly as it does for the single
// monitor path.
func (a *App) CaptureScreenshotVirtualDesktop() (ScreenShotCapture, error) {
	dir, path, err := captureScreenshotTempDir()
	if err != nil {
		return ScreenShotCapture{}, err
	}
	metrics, err := captureVirtualScreenPNG(path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return ScreenShotCapture{}, err
	}
	return ScreenShotCapture{Path: path, Width: metrics.Width, Height: metrics.Height, X: metrics.X, Y: metrics.Y}, nil
}

// ReleaseScreenshotCapture removes a capture's temp directory. A missing or
// empty path is a no-op so a failed capture never leaves the frontend
// holding an error the user cannot clear.
func (a *App) ReleaseScreenshotCapture(path string) error {
	dir := filepath.Dir(strings.TrimSpace(path))
	if dir == "" || dir == "." {
		return nil
	}
	base := filepath.Base(dir)
	if !strings.HasPrefix(base, "reasonix-screenshot-") {
		return nil // refuse to remove anything that is not our temp dir
	}
	return os.RemoveAll(dir)
}

// SaveScreenshotAttachment stores the overlay's final PNG (annotations
// baked in) under the active tab's workspace attachments and returns the
// relative @-reference path — the same link the paste/drop image chain uses.
func (a *App) SaveScreenshotAttachment(dataURL string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.SaveImageDataURLIn(a.activeWorkspaceRoot(), dataURL)
	})
}

// SaveScreenshotToPath writes the overlay's final PNG to a path the user
// picked through PickExportFile (tl 1087: save-before-confirm). The path is
// absolute (the dialog hands one back); an empty path is a cancelled export.
func (a *App) SaveScreenshotToPath(path, dataURL string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		path += ".png"
	}
	const marker = ";base64,"
	if !strings.HasPrefix(dataURL, "data:") || !strings.Contains(dataURL, marker) {
		return fmt.Errorf("unsupported screenshot payload")
	}
	return a.SaveExportFile(path, dataURL[strings.Index(dataURL, marker)+len(marker):], true)
}

// PickScreenshotExportFile opens the native save dialog defaulting to a
// timestamped screenshot name. Returns "" when the user cancels.
func (a *App) PickScreenshotExportFile() (string, error) {
	return a.PickExportFile(
		"screenshot-"+time.Now().Format("20060102-150405")+".png",
		"image/png",
	)
}

// CopyScreenshotToClipboard puts the final PNG on the OS clipboard as a real
// image (tl 1088) so external apps can paste it. Windows-only below; other
// platforms keep the frontend's navigator.clipboard fallback.
func (a *App) CopyScreenshotToClipboard(dataURL string) error {
	return writeClipboardImageDataURL(dataURL)
}
