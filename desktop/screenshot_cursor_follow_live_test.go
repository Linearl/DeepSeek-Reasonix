package main

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tl 1370 cursor-follow live evidence: on a real multi-monitor session this
// walks the mouse cursor across the display boundary with SetCursorPos and
// pins the whole chain the overlay's follow loop rides — the identity query
// follows the cursor's display, and CaptureScreenshotAtCursor re-targets the
// same display. The cursor is moved for real, so the test is opt-in
// (TL1370_LIVE_FOLLOW=1) exactly like TestDXGILiveCapture; it always restores
// the original cursor position.
//
// Skip semantics: default `go test` skips (moving the user's mouse uninvited
// is not acceptable), a single-display box skips (no boundary to cross), and
// every setup failure is a t.Fatal — never a silent pass.

var screenshotProcSetCursorPos = screenshotUser32.NewProc("SetCursorPos")

type liveMonitorRect struct {
	device string
	rect   struct{ left, top, right, bottom int32 }
}

func setCursorPos(x, y int32) error {
	r, _, _ := screenshotProcSetCursorPos.Call(uintptr(uint32(uint16(x))), uintptr(uint32(uint16(y))))
	if r == 0 {
		return fmt.Errorf("SetCursorPos(%d,%d) failed", x, y)
	}
	return nil
}

func liveMonitorRects(t *testing.T) []liveMonitorRect {
	t.Helper()
	var monitors []liveMonitorRect
	for _, hmon := range enumMonitorHandles() {
		info := screenshotMonitorInfo{cbSize: monitorInfoHeaderSize}
		if r, _, callErr := screenshotProcGetMonitorInf.Call(hmon, uintptr(unsafe.Pointer(&info))); r == 0 {
			t.Fatalf("GetMonitorInfo failed during enumeration: %v", callErr)
		}
		monitors = append(monitors, liveMonitorRect{
			device: windows.UTF16ToString(info.szDevice[:]),
			rect:   info.rcMonitor,
		})
	}
	return monitors
}

func centerOf(r struct{ left, top, right, bottom int32 }) (int32, int32) {
	return (r.left + r.right) / 2, (r.top + r.bottom) / 2
}

func TestScreenshotCursorFollowLiveDualMonitor(t *testing.T) {
	if os.Getenv("TL1370_LIVE_FOLLOW") != "1" {
		t.Skip("live cursor-follow walks the real mouse across displays; opt in with TL1370_LIVE_FOLLOW=1")
	}
	if runtime.GOOS != "windows" {
		t.Skip("live cursor-follow is Windows-only (the query itself fails explicitly off-Windows)")
	}

	var origin screenshotPoint
	if r, _, _ := screenshotProcGetCursorPos.Call(uintptr(unsafe.Pointer(&origin))); r == 0 {
		t.Fatal("GetCursorPos failed; live follow needs a real desktop session")
	}
	t.Cleanup(func() {
		if err := setCursorPos(origin.x, origin.y); err != nil {
			t.Logf("restoring the cursor position failed: %v", err)
		}
	})

	monitors := liveMonitorRects(t)
	if len(monitors) < 2 {
		t.Skipf("cursor-follow needs at least two displays, found %d", len(monitors))
	}
	displayA, displayB := monitors[0], monitors[1]
	t.Logf("displays: %s rect=(%d,%d)-(%d,%d) and %s rect=(%d,%d)-(%d,%d)",
		displayA.device, displayA.rect.left, displayA.rect.top, displayA.rect.right, displayA.rect.bottom,
		displayB.device, displayB.rect.left, displayB.rect.top, displayB.rect.right, displayB.rect.bottom)

	app := &App{}
	moveTo := func(target liveMonitorRect, label string) {
		t.Helper()
		x, y := centerOf(target.rect)
		if err := setCursorPos(x, y); err != nil {
			t.Fatalf("SetCursorPos to %s center failed: %v", label, err)
		}
		// Give the OS a beat so the cursor position is settled everywhere.
		time.Sleep(80 * time.Millisecond)
	}

	// Crossing to display A: the identity query and the capture both re-target.
	moveTo(displayA, "displayA")
	mon, err := app.ScreenshotCursorMonitor()
	if err != nil {
		t.Fatalf("ScreenshotCursorMonitor on displayA: %v", err)
	}
	if mon.Device != displayA.device {
		t.Fatalf("identity query did not follow the cursor onto %s (got %s)", displayA.device, mon.Device)
	}
	if mon.X != int(displayA.rect.left) || mon.Y != int(displayA.rect.top) || mon.Width != int(displayA.rect.right-displayA.rect.left) {
		t.Fatalf("geometry did not follow onto %s: %+v", displayA.device, mon)
	}
	capture, err := app.CaptureScreenshotAtCursor()
	if err != nil {
		t.Fatalf("CaptureScreenshotAtCursor on displayA: %v", err)
	}
	if err := app.ReleaseScreenshotCapture(capture.Path); err != nil {
		t.Fatalf("release displayA capture: %v", err)
	}
	if capture.X != mon.X || capture.Y != mon.Y || capture.Width != mon.Width || capture.Height != mon.Height {
		t.Fatalf("capture did not re-target the cursor's display: capture=(%d,%d %dx%d) query=(%d,%d %dx%d)",
			capture.X, capture.Y, capture.Width, capture.Height, mon.X, mon.Y, mon.Width, mon.Height)
	}
	t.Logf("displayA follow ok: device=%s capture=(%d,%d %dx%d)", mon.Device, capture.X, capture.Y, capture.Width, capture.Height)

	// Crossing to display B: the same chain must re-target again.
	moveTo(displayB, "displayB")
	mon, err = app.ScreenshotCursorMonitor()
	if err != nil {
		t.Fatalf("ScreenshotCursorMonitor on displayB: %v", err)
	}
	if mon.Device != displayB.device {
		t.Fatalf("identity query did not follow the cursor onto %s (got %s)", displayB.device, mon.Device)
	}
	capture, err = app.CaptureScreenshotAtCursor()
	if err != nil {
		t.Fatalf("CaptureScreenshotAtCursor on displayB: %v", err)
	}
	if err := app.ReleaseScreenshotCapture(capture.Path); err != nil {
		t.Fatalf("release displayB capture: %v", err)
	}
	if capture.X != mon.X || capture.Y != mon.Y || capture.Width != mon.Width || capture.Height != mon.Height {
		t.Fatalf("capture did not re-target the cursor's display on B: capture=(%d,%d %dx%d) query=(%d,%d %dx%d)",
			capture.X, capture.Y, capture.Width, capture.Height, mon.X, mon.Y, mon.Width, mon.Height)
	}
	t.Logf("displayB follow ok: device=%s capture=(%d,%d %dx%d)", mon.Device, capture.X, capture.Y, capture.Width, capture.Height)

	// Back-and-forth crossings stay stable: every crossing re-resolves the
	// right display, with no errors accumulating between them.
	for i, target := range []liveMonitorRect{displayA, displayB, displayA, displayB} {
		moveTo(target, "back-and-forth")
		mon, err := app.ScreenshotCursorMonitor()
		if err != nil {
			t.Fatalf("crossing %d: identity query failed: %v", i+1, err)
		}
		if mon.Device != target.device {
			t.Fatalf("crossing %d: identity did not follow to %s (got %s)", i+1, target.device, mon.Device)
		}
	}
	t.Log("back-and-forth crossings all re-resolved the correct display")
}
