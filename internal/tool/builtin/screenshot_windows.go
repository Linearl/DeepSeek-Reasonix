//go:build windows

package builtin

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows capture for the screenshot tool (task 233 batch 1): PrintWindow
// with PW_RENDERFULLCONTENT into a top-down 32bpp DIB, then BGRA→RGBA and
// PNG-encode. No CGO; everything goes through syscall procs.

const (
	pwRenderFullContent = 0x2
	biRGB               = 0
	dibRGBColors        = 0
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows        = user32.NewProc("EnumWindows")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procGetWindowTextW     = user32.NewProc("GetWindowTextW")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procPrintWindow        = user32.NewProc("PrintWindow")
	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	gdi32                  = windows.NewLazySystemDLL("gdi32.dll")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
)

type windowRect struct {
	left, top, right, bottom int32
}

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

func captureWindowScreenshot(outputPath, titleSubstring string) (w, h int, title string, err error) {
	target, title, err := findTargetWindow(titleSubstring)
	if err != nil {
		return 0, 0, "", err
	}
	var rect windowRect
	if r, _, callErr := procGetWindowRect.Call(uintptr(target), uintptr(unsafe.Pointer(&rect))); r == 0 {
		return 0, 0, "", fmt.Errorf("GetWindowRect failed: %v", callErr)
	}
	w, h = int(rect.right-rect.left), int(rect.bottom-rect.top)
	if w <= 0 || h <= 0 {
		return 0, 0, "", fmt.Errorf("window %q has no visible size (is it minimized?); restore it and retry", title)
	}

	hdcWindow, _, _ := procGetDC.Call(uintptr(target))
	if hdcWindow == 0 {
		return 0, 0, "", fmt.Errorf("GetDC failed for window %q", title)
	}
	defer procReleaseDC.Call(uintptr(target), hdcWindow)
	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcWindow)
	if hdcMem == 0 {
		return 0, 0, "", fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(hdcMem)

	bmi := bitmapInfo{header: bitmapInfoHeader{
		size:     40,
		width:    int32(w),
		height:   -int32(h), // top-down
		planes:   1,
		bitCount: 32,
	}}
	// bits receives CreateDIBSection's lpBits out-param: a pointer into
	// GDI-allocated (non-Go) memory, so the GC never moves it and holding it
	// as unsafe.Pointer is sound. Task 749 verdict: go vet's unsafeptr check
	// has no way to prove out-param provenance (it only whitelists reflect
	// headers / reflect.Value.Pointer / pointer arithmetic), so the value is
	// kept in unsafe.Pointer form instead of round-tripping through uintptr,
	// which the checker cannot verify.
	var bits unsafe.Pointer
	hbmp, _, callErr := procCreateDIBSection.Call(
		hdcMem,
		uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbmp == 0 || bits == nil {
		return 0, 0, "", fmt.Errorf("CreateDIBSection failed: %v", callErr)
	}
	defer procDeleteObject.Call(hbmp)
	if prev, _, _ := procSelectObject.Call(hdcMem, hbmp); prev == 0 {
		return 0, 0, "", fmt.Errorf("SelectObject failed")
	}
	if r, _, callErr := procPrintWindow.Call(uintptr(target), hdcMem, pwRenderFullContent); r == 0 {
		return 0, 0, "", fmt.Errorf("PrintWindow failed: %v (restore the window if it is minimized)", callErr)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// w*h*4 is exact for a top-down 32bpp DIB: rows are always dword-aligned,
	// so there is no stride padding to account for. hbmp (owning the buffer)
	// stays alive until the deferred DeleteObject after the loop.
	pixels := unsafe.Slice((*uint8)(bits), w*h*4)
	for y := 0; y < h; y++ {
		src := pixels[y*w*4 : (y+1)*w*4]
		dst := img.Pix[y*img.Stride : (y+1)*img.Stride]
		for x := 0; x < w; x++ {
			dst[x*4+0] = src[x*4+2] // R
			dst[x*4+1] = src[x*4+1] // G
			dst[x*4+2] = src[x*4+0] // B
			dst[x*4+3] = 0xff       // PrintWindow leaves alpha unset
		}
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return 0, 0, "", fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return 0, 0, "", fmt.Errorf("encode %s: %w", outputPath, err)
	}
	return w, h, title, nil
}

// findTargetWindow picks the capture target: an explicit case-insensitive
// title substring, or the first visible window whose title contains
// "reasonix" when empty.
func findTargetWindow(titleSubstring string) (windows.HWND, string, error) {
	wanted := strings.ToLower(strings.TrimSpace(titleSubstring))
	if wanted == "" {
		wanted = "reasonix"
	}
	var found windows.HWND
	var foundTitle string
	cb := syscall.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		if found != 0 {
			return 1
		}
		if r, _, _ := procIsWindowVisible.Call(uintptr(h)); r == 0 {
			return 1
		}
		title := windowText(h)
		if strings.Contains(strings.ToLower(title), wanted) {
			found = h
			foundTitle = title
			return 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if found == 0 {
		return 0, "", fmt.Errorf("no visible window title matching %q; list candidate titles by capturing with a partial title you can see in the taskbar", wanted)
	}
	return found, foundTitle, nil
}

func windowText(h windows.HWND) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return strings.TrimSpace(syscall.UTF16ToString(buf[:n]))
}
