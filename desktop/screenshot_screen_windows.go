//go:build windows

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Full-screen capture for the desktop screenshot button (tl 1087, plan A):
// the monitor under the mouse cursor is blitted into a top-down 32bpp DIB via
// BitBlt, then BGRA→RGBA and PNG-encode. No CGO — same pure-syscall style as
// internal/tool/builtin/screenshot_windows.go (task 233), kept local to the
// desktop module because the desktop shell is its own nested module.

const (
	monitorDefaultToNearest = 2
	smXVirtualScreen        = 76
	smYVirtualScreen        = 77
	smCxVirtualScreen       = 78
	smCyVirtualScreen       = 79
	srccopy                 = 0x00CC0020
	// monitorInfoHeaderSize is the DWORD size field of MONITORINFOEXW: the
	// struct carries the extra szDevice member beyond plain MONITORINFO.
	monitorInfoHeaderSize = 104
)

var (
	screenshotUser32            = windows.NewLazySystemDLL("user32.dll")
	screenshotProcGetCursorPos  = screenshotUser32.NewProc("GetCursorPos")
	screenshotProcMonitorFromPt = screenshotUser32.NewProc("MonitorFromPoint")
	screenshotProcGetMonitorInf = screenshotUser32.NewProc("GetMonitorInfoW")
	screenshotProcGetSysMetrics = screenshotUser32.NewProc("GetSystemMetrics")
	screenshotProcGetDC         = screenshotUser32.NewProc("GetDC")
	screenshotProcReleaseDC     = screenshotUser32.NewProc("ReleaseDC")
	screenshotGdi32             = windows.NewLazySystemDLL("gdi32.dll")
	screenshotProcCreateCompDC  = screenshotGdi32.NewProc("CreateCompatibleDC")
	screenshotProcDeleteDC      = screenshotGdi32.NewProc("DeleteDC")
	screenshotProcCreateDIBSec  = screenshotGdi32.NewProc("CreateDIBSection")
	screenshotProcSelectObject  = screenshotGdi32.NewProc("SelectObject")
	screenshotProcDeleteObject  = screenshotGdi32.NewProc("DeleteObject")
	screenshotProcBitBlt        = screenshotGdi32.NewProc("BitBlt")
)

type screenshotPoint struct {
	x, y int32
}

// bitmapInfoHeader / bitmapInfo mirror the task-233 window-capture structs
// (internal/tool/builtin/screenshot_windows.go) — the desktop module cannot
// import the CLI module's internals, so the same layout is declared locally.
type bitmapInfoHeader struct {
	size          int32
	width         int32
	height        int32
	planes        int16
	bitCount      int16
	compression   int32
	sizeImage     int32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       int32
	clrImportant  int32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}

type screenshotMonitorInfo struct {
	cbSize    uint32
	rcMonitor struct{ left, top, right, bottom int32 }
	rcWork    struct{ left, top, right, bottom int32 }
	flags     uint32
	szDevice  [32]uint16
}

// ScreenMetrics reports a screen rectangle in virtual-desktop coordinates.
// The desktop screenshot overlay (tl 1087) maps its client coordinates onto
// these pixels; the origin is nonzero on multi-monitor layouts.
type ScreenMetrics struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x"`
	Y      int `json:"y"`
}

// cursorMonitorRect resolves the monitor that currently holds the mouse
// cursor. Plan A (tl 1087): the screenshot always targets that one display;
// MonitorFromPoint with MONITOR_DEFAULTTONEAREST keeps a cursor parked on a
// dead coordinate from failing the call.
func cursorMonitorRect() (left, top, right, bottom int32, err error) {
	var cur screenshotPoint
	if r, _, callErr := screenshotProcGetCursorPos.Call(uintptr(unsafe.Pointer(&cur))); r == 0 {
		return 0, 0, 0, 0, fmt.Errorf("GetCursorPos failed: %v", callErr)
	}
	// The point must stay referenced until the syscall returns: LazyProc.Call
	// keeps no liveness for its arguments, so a collected stack object would
	// hand the API freed memory (same hazard class as the unsafe.Pointer
	// verdict in the task-233 window capture).
	runtime.KeepAlive(cur)
	// Pack the POINT as a single uintptr (x low word, y high word) exactly as
	// the win32 by-value signature expects.
	packed := uintptr(uint32(uint16(cur.x)) | (uint32(uint16(cur.y)) << 16))
	hmon, _, _ := screenshotProcMonitorFromPt.Call(packed, monitorDefaultToNearest)
	if hmon == 0 {
		return 0, 0, 0, 0, fmt.Errorf("MonitorFromPoint failed for cursor at (%d,%d)", cur.x, cur.y)
	}
	info := screenshotMonitorInfo{cbSize: monitorInfoHeaderSize}
	if r, _, callErr := screenshotProcGetMonitorInf.Call(hmon, uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0, 0, 0, fmt.Errorf("GetMonitorInfo failed: %v", callErr)
	}
	runtime.KeepAlive(info)
	m := info.rcMonitor
	if m.right-m.left <= 0 || m.bottom-m.top <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("monitor at cursor has no visible size (%dx%d)", m.right-m.left, m.bottom-m.top)
	}
	return m.left, m.top, m.right, m.bottom, nil
}

// captureScreenRectToPNG blits the given virtual-desktop rectangle into a
// top-down 32bpp DIB and writes it as a PNG. GetDC(NULL) hands back the whole
// screen DC, so BitBlt accepts monitor-relative virtual coordinates directly.
func captureScreenRectToPNG(outputPath string, left, top, right, bottom int32) (w, h int, err error) {
	w, h = int(right-left), int(bottom-top)
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("capture rect is empty (%dx%d)", w, h)
	}
	hdcScreen, _, _ := screenshotProcGetDC.Call(0)
	if hdcScreen == 0 {
		return 0, 0, fmt.Errorf("GetDC(NULL) failed for the screen")
	}
	defer screenshotProcReleaseDC.Call(0, hdcScreen)
	hdcMem, _, _ := screenshotProcCreateCompDC.Call(hdcScreen)
	if hdcMem == 0 {
		return 0, 0, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer screenshotProcDeleteDC.Call(hdcMem)

	bmi := bitmapInfo{header: bitmapInfoHeader{
		size:     40,
		width:    int32(w),
		height:   -int32(h), // top-down
		planes:   1,
		bitCount: 32,
	}}
	// bits is GDI-owned (non-Go) memory handed back through an out-param; it
	// stays in unsafe.Pointer form (not round-tripped through uintptr) for the
	// same go vet unsafeptr provenance reason as task 749's verdict.
	var bits unsafe.Pointer
	hbmp, _, callErr := screenshotProcCreateDIBSec.Call(
		hdcMem,
		uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbmp == 0 || bits == nil {
		return 0, 0, fmt.Errorf("CreateDIBSection failed: %v", callErr)
	}
	defer screenshotProcDeleteObject.Call(hbmp)
	if prev, _, _ := screenshotProcSelectObject.Call(hdcMem, hbmp); prev == 0 {
		return 0, 0, fmt.Errorf("SelectObject failed")
	}
	// The uint32→uintptr wraps below keep negative virtual-desktop origins out
	// of the argument values: BitBlt takes the source point as unsigned ints.
	if r, _, callErr := screenshotProcBitBlt.Call(
		hdcMem,
		0,
		0,
		uintptr(uint32(w)),
		uintptr(uint32(h)),
		hdcScreen,
		uintptr(uint32(left)),
		uintptr(uint32(top)),
		srccopy,
	); r == 0 {
		return 0, 0, fmt.Errorf("BitBlt failed: %v", callErr)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// w*h*4 is exact for a top-down 32bpp DIB (dword-aligned rows, no stride
	// padding). hbmp owns the buffer and is deleted by the deferred
	// DeleteObject after the loop finishes reading it.
	pixels := unsafe.Slice((*uint8)(bits), w*h*4)
	for y := 0; y < h; y++ {
		src := pixels[y*w*4 : (y+1)*w*4]
		dst := img.Pix[y*img.Stride : (y+1)*img.Stride]
		for x := 0; x < w; x++ {
			dst[x*4+0] = src[x*4+2] // R
			dst[x*4+1] = src[x*4+1] // G
			dst[x*4+2] = src[x*4+0] // B
			dst[x*4+3] = 0xff       // BitBlt leaves alpha unset
		}
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return 0, 0, fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return 0, 0, fmt.Errorf("encode %s: %w", outputPath, err)
	}
	return w, h, nil
}

// captureScreenAtCursor captures the full screen of the monitor holding the
// mouse cursor (tl 1087 plan A) into outputPath, returning the pixel size and
// virtual-desktop origin of the captured display so the overlay can map its
// client coordinates onto screenshot pixels.
func captureScreenAtCursor(outputPath string) (w, h, originX, originY int, err error) {
	left, top, right, bottom, err := cursorMonitorRect()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	w, h, err = captureScreenRectToPNG(outputPath, left, top, right, bottom)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return w, h, int(left), int(top), nil
}

// screenMetricsAtCursor reports the bounds of the monitor holding the cursor
// (same resolution path as captureScreenAtCursor, no capture side effect).
func screenMetricsAtCursor() (ScreenMetrics, error) {
	left, top, right, bottom, err := cursorMonitorRect()
	if err != nil {
		return ScreenMetrics{}, err
	}
	return ScreenMetrics{
		Width:  int(right - left),
		Height: int(bottom - top),
		X:      int(left),
		Y:      int(top),
	}, nil
}

// virtualScreenMetrics reports the whole virtual-desktop bounds (union of all
// monitors) — tl 1088 cross-monitor selection needs the origin too, which the
// per-monitor path above cannot supply. Errors out when metrics come back
// empty (no displays attached).
func virtualScreenMetrics() (ScreenMetrics, error) {
	get := func(index int) int {
		r, _, _ := screenshotProcGetSysMetrics.Call(uintptr(index))
		return int(int32(r))
	}
	m := ScreenMetrics{
		X:      get(smXVirtualScreen),
		Y:      get(smYVirtualScreen),
		Width:  get(smCxVirtualScreen),
		Height: get(smCyVirtualScreen),
	}
	if m.Width <= 0 || m.Height <= 0 {
		return ScreenMetrics{}, fmt.Errorf("virtual screen metrics are empty (%dx%d)", m.Width, m.Height)
	}
	return m, nil
}

// captureVirtualScreenPNG captures the entire virtual desktop (all monitors)
// — tl 1088's cross-monitor capture primitive.
func captureVirtualScreenPNG(outputPath string) (ScreenMetrics, error) {
	m, err := virtualScreenMetrics()
	if err != nil {
		return ScreenMetrics{}, err
	}
	w, h, err := captureScreenRectToPNG(outputPath, int32(m.X), int32(m.Y), int32(m.X+m.Width), int32(m.Y+m.Height))
	if err != nil {
		return ScreenMetrics{}, err
	}
	m.Width, m.Height = w, h
	return m, nil
}

var _ = syscall.SyscallN // anchors the syscall import: NewCallback-capable build files share this set
