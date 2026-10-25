//go:build windows

// tl-1130 抓屏探针：实证 BitBlt 黑屏根因。
//
// 阶段 1（干净桌面）：GetDC(NULL)+BitBlt 抓主屏全幅，统计近黑像素占比。
//   预期（BitBlt 本身可用）：近黑占比低 → 方向 1（抓不到 DWM 合成层）被证伪。
// 阶段 2（模拟 overlay loading 帧）：弹一个全屏纯黑 topmost 窗口（等价于
//   ScreenshotOverlay 的 --shadow-color:#000 loading 背景），Sleep 等两帧合成，
//   再抓同一主屏。
//   预期：接近全黑 → 证明「overlay 先上屏、抓屏抓到自己的黑遮罩」= 方向 2。
//
// 输出：每阶段 PNG 落盘 + 近黑像素占比/平均亮度读数。近黑判定：R,G,B 均 < 8。
// 仅作探针证据，不进产品代码。

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	gdi32          = syscall.NewLazyDLL("gdi32.dll")
	procSetCtxDPI  = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetSysMet  = user32.NewProc("GetSystemMetrics")
	procGetDC      = user32.NewProc("GetDC")
	procReleaseDC  = user32.NewProc("ReleaseDC")
	procRegisterCl = user32.NewProc("RegisterClassW")
	procCreateWnd  = user32.NewProc("CreateWindowExW")
	procShowWnd    = user32.NewProc("ShowWindow")
	procUpdateWnd  = user32.NewProc("UpdateWindow")
	procDestroyWnd = user32.NewProc("DestroyWindow")
	procSetWndPos  = user32.NewProc("SetWindowPos")
	procIsVisible  = user32.NewProc("IsWindowVisible")
	procGetWndRect = user32.NewProc("GetWindowRect")
	procGetWndDC   = user32.NewProc("GetDC")
	procFillRect   = user32.NewProc("FillRect")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procDefWndProc = user32.NewProc("DefWindowProcW")
	procCreateComp = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC   = gdi32.NewProc("DeleteDC")
	procCreateDIB  = gdi32.NewProc("CreateDIBSection")
	procSelectObj  = gdi32.NewProc("SelectObject")
	procDeleteObj  = gdi32.NewProc("DeleteObject")
	procBitBlt     = gdi32.NewProc("BitBlt")
)

const (
	smCXScreen  = 0
	smCYScreen  = 1
	srccopy     = 0x00CC0020
	swShow      = 5
	wsPopUp     = 0x80000000
	wsVisible   = 0x10000000
	hwndTopmost = ^uintptr(0) // -1
	swpNoSize   = 0x0001
	swpShow     = 0x0040
	colorWindow = 5 // COLOR_WINDOW
)

// wndClass mirrors WNDCLASSW exactly. Go's natural alignment already pads
// style(4B)→proc(8B) and lands hInstance at offset 24 (8-aligned right after
// cbWndExtra) — no manual padding fields, those double-pad and shift the
// tail by 8 bytes (access violation inside CreateWindowExW). Both trailing
// string pointers must exist: omitting lpszMenuName shifts lpszClassName and
// RegisterClassW fails with "parameter incorrect".
type rect struct{ left, top, right, bottom int32 }

type wndClass struct {
	style      uint32
	lpfnWndPtr uintptr
	cbClsExtra int32
	cbWndExtra int32
	hInstance  uintptr
	hIcon      uintptr
	hCursor    uintptr
	hbrBg      uintptr
	lpszMenu   uintptr
	lpszName   *uint16
}

type bmiHeader struct {
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

type bmi struct {
	header bmiHeader
	colors [1]uint32
}

func mustSM(idx int) uintptr {
	r, _, _ := procGetSysMet.Call(uintptr(idx))
	return r
}

func analyze(name, path string, w, h int, pix []uint8) {
	black, total := 0, w*h
	var sum uint64
	for i := 0; i < total; i++ {
		r, g, b := pix[i*4], pix[i*4+1], pix[i*4+2]
		if r < 8 && g < 8 && b < 8 {
			black++
		}
		sum += uint64(r) + uint64(g) + uint64(b)
	}
	pct := float64(black) * 100 / float64(total)
	avg := float64(sum) / float64(total) / 3.0
	fmt.Printf("[%s] size=%dx%d near-black=%.2f%% avgBrightness=%.1f png=%s\n", name, w, h, pct, avg, path)

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := pix[y*w*4 : (y+1)*w*4]
		dst := img.Pix[y*img.Stride : (y+1)*img.Stride]
		for x := 0; x < w; x++ {
			dst[x*4+0] = src[x*4+2]
			dst[x*4+1] = src[x*4+1]
			dst[x*4+2] = src[x*4+0]
			dst[x*4+3] = 0xff
		}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Println("create png:", err)
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}

func bltScreen(tag string) {
	w := int(int32(mustSM(smCXScreen)))
	h := int(int32(mustSM(smCYScreen)))
	if w <= 0 || h <= 0 {
		fmt.Printf("[%s] bad metrics %dx%d\n", tag, w, h)
		return
	}
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		fmt.Printf("[%s] GetDC failed\n", tag)
		return
	}
	defer procReleaseDC.Call(0, hdc)
	mdc, _, _ := procCreateComp.Call(hdc)
	defer procDeleteDC.Call(mdc)
	info := bmi{header: bmiHeader{size: 40, width: int32(w), height: -int32(h), planes: 1, bitCount: 32}}
	var bits unsafe.Pointer
	hbmp, _, err := procCreateDIB.Call(mdc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 || bits == nil {
		fmt.Printf("[%s] CreateDIBSection failed: %v\n", tag, err)
		return
	}
	defer procDeleteObj.Call(hbmp)
	procSelectObj.Call(mdc, hbmp)
	if r, _, _ := procBitBlt.Call(mdc, 0, 0, uintptr(uint32(w)), uintptr(uint32(h)), hdc, 0, 0, srccopy); r == 0 {
		fmt.Printf("[%s] BitBlt failed\n", tag)
		return
	}
	pix := unsafe.Slice((*uint8)(bits), w*h*4)
	snapshot := make([]uint8, len(pix))
	copy(snapshot, pix)
	analyze(tag, fmt.Sprintf("probe-%s.png", tag), w, h, snapshot)
}

func wndProc(hwnd syscall.Handle, msg uint32, wp, lp uintptr) uintptr {
	r, _, _ := procDefWndProc.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return r
}

// blackCover pops a topmost pure-black window (80% of the screen, NOT
// fullscreen — a true fullscreen WS_POPUP triggers DWM fullscreen
// optimization which freezes the GDI mirror, see stage2 notes), actively
// fills it black, sleeps past two DWM frames, captures, then tears down.
// It mirrors the overlay's `--shadow-color: #000` loading frame occupying
// the screen when the Go BitBlt runs.
func blackCover(tag string) {
	cx, cy := mustSM(smCXScreen), mustSM(smCYScreen)
	className, _ := syscall.UTF16PtrFromString("probeTl1130Black")
	brush, _, _ := procCreateSolidBrush.Call(0x000000) // pure black
	var wc wndClass
	wc.lpfnWndPtr = syscall.NewCallback(wndProc)
	wc.hbrBg = brush
	wc.lpszName = className
	r, _, err := procRegisterCl.Call(uintptr(unsafe.Pointer(&wc)))
	if r == 0 {
		fmt.Println("RegisterClass:", err)
		return
	}
	title, _ := syscall.UTF16PtrFromString("probe-cover")
	hwnd, _, err := procCreateWnd.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsPopUp, 0, 0, cx*8/10, cy*8/10, 0, 0, 0, 0,
	)
	if hwnd == 0 {
		fmt.Println("CreateWindow failed:", err)
		return
	}
	// Topmost + show; pump nothing — DWM composites the frame on its own.
	procSetWndPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoSize|swpShow)
	procShowWnd.Call(hwnd, swShow)
	procUpdateWnd.Call(hwnd)
	// Actively paint the client area pure black through the window's own DC —
	// do not rely on the class background brush / WM_ERASEBKGND path.
	var wr rect
	procGetWndRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	vis, _, _ := procIsVisible.Call(hwnd)
	fmt.Printf("[cover] visible=%v rect=%d,%d-%d,%d\n", vis != 0, wr.left, wr.top, wr.right, wr.bottom)
	hwndDC, _, _ := procGetWndDC.Call(hwnd)
	if hwndDC != 0 {
		cli := rect{left: 0, top: 0, right: wr.right - wr.left, bottom: wr.bottom - wr.top}
		procFillRect.Call(hwndDC, uintptr(unsafe.Pointer(&cli)), brush)
	}
	time.Sleep(400 * time.Millisecond)
	bltScreen(tag)
	procDestroyWnd.Call(hwnd)
}

func main() {
	fmt.Println("== tl-1130 BitBlt probe ==")
	// stage4/5 run with PerMonitorV2 awareness — the Wails product process is
	// PMv2 (built-in manifest dpiAwareness=permonitorv2,permonitor), so the
	// GDI-mirror behavior under PMv2 is the product-relevant configuration.
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -1*4.
	if r, _, err := procSetCtxDPI.Call(^uintptr(3), uintptr(0)); r == 0 {
		fmt.Println("SetProcessDpiAwarenessContext(PMv2) failed:", err)
	}
	fmt.Printf("primary screen (after PMv2): %dx%d\n", int(int32(mustSM(smCXScreen))), int(int32(mustSM(smCYScreen))))
	bltScreen("stage4-pmv2-clean")
	blackCover("stage5-pmv2-cover")
	fmt.Println("== done ==")
}
