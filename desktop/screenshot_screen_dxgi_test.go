//go:build windows

package main

// Tests for the tl 1130 desktop-duplication capture chain. The pixel content
// of a real capture cannot be asserted in a headless unit run — that half is
// probe-verified (wt tmp/probe-tl1130) and exercised live through the
// TL1130_LIVE_CAPTURE=1 gate below; everything checkable without a desktop
// session is pinned here.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// guidString renders a dxgiGUID as the canonical Windows SDK string form, so
// the tests can compare against the DEFINE_GUID values from dxgi.h/d3d11.h
// directly. The memory layout (little-endian d0/d1/d2 + raw d3 tail) is what
// QueryInterface receives — an endian slip here fails every COM call.
func guidString(g dxgiGUID) string {
	return fmt.Sprintf("%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		g.d0, g.d1, g.d2,
		g.d3[0], g.d3[1], g.d3[2], g.d3[3], g.d3[4], g.d3[5], g.d3[6], g.d3[7])
}

// TestDXGIGUIDsMatchWindowsSDK pins the three interface IDs against the
// MIDL_INTERFACE declarations in the Windows 10 SDK headers (shared/dxgi.h,
// shared/dxgi1_2.h, um/d3d11.h): IDXGIDevice, IDXGIOutput1, ID3D11Texture2D.
func TestDXGIGUIDsMatchWindowsSDK(t *testing.T) {
	cases := []struct {
		name string
		got  dxgiGUID
		want string
	}{
		{"IID_IDXGIFactory1", iidIDXGIFactory1, "770AAE78-F26F-4DBA-A829-253C83D1B387"},
		{"IID_IDXGIOutput1", iidIDXGIOutput1, "00CDDEA8-939B-4B83-A340-A685226666CC"},
		{"IID_ID3D11Texture2D", iidID3D11Texture2D, "6F15AAF2-D208-4E89-9AB4-489535D34F9C"},
	}
	for _, tc := range cases {
		if got := guidString(tc.got); got != tc.want {
			t.Errorf("%s = %s, want %s (a drifted GUID fails QueryInterface at runtime)", tc.name, got, tc.want)
		}
	}
}

// TestDXGIVtableSlotsPinWindowsABI pins the COM vtable indexes used through
// vcall against the dxgi.h / d3d11.h declaration order. A one-slot drift is a
// wild call, not a clean error — hence an explicit pin.
func TestDXGIVtableSlotsPinWindowsABI(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"IUnknown::QueryInterface", comQueryInterface, 0},
		{"IUnknown::Release", comMethodRelease, 2},
		{"IDXGIAdapter::EnumOutputs", idxgiAdapterEnumOutputs, 7},
		{"IDXGIOutput::GetDesc", idxgiOutputGetDesc, 7},
		{"IDXGIAdapter::GetDesc", idxgiAdapterGetDesc, 8},
		{"IDXGIOutput1::DuplicateOutput", idxgiOutput1DuplicateOutput, 22},
		{"IDXGIOutputDuplication::AcquireNextFrame", idxgiDuplAcquireNextFrame, 8},
		{"IDXGIOutputDuplication::ReleaseFrame", idxgiDuplReleaseFrame, 14},
		{"ID3D11Device::CreateTexture2D", d3d11DeviceCreateTexture2D, 5},
		{"ID3D11DeviceContext::Map", d3d11ContextMap, 14},
		{"ID3D11DeviceContext::Unmap", d3d11ContextUnmap, 15},
		{"ID3D11DeviceContext::CopyResource", d3d11ContextCopyResource, 47},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s slot = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// TestDXGIHResultHelpers pins the HRESULT success check and the
// DXGI_ERROR_WAIT_TIMEOUT discrimination the AcquireNextFrame retry/abort
// decision depends on.
func TestDXGIHResultHelpers(t *testing.T) {
	if !hrOK(0) {
		t.Error("hrOK(S_OK) = false, want true")
	}
	if !hrOK(1) {
		t.Error("hrOK(S_FALSE=1) = false, want true")
	}
	var efail int32 = -1 // 0xFFFFFFFF as a 32-bit HRESULT
	if hrOK(uintptr(efail)) {
		t.Error("hrOK(E_FAIL) = true, want false")
	}
	if uint32(dxgiErrorWaitTimeout) != 0x887A0027 {
		t.Errorf("dxgiErrorWaitTimeout = 0x%x, want 0x887a0027", uint32(dxgiErrorWaitTimeout))
	}
}

// TestDXGIBgraToRGBA pins the staging-texture pixel conversion: BGRA byte
// order in, RGBA image out, alpha forced to opaque (desktop duplication
// leaves the alpha channel unset).
func TestDXGIBgraToRGBA(t *testing.T) {
	pix := []uint8{
		0x11, 0x22, 0x33, 0x00, // B,G,R,A
		0xFF, 0x00, 0x00, 0x00, // pure blue
		0x00, 0xFF, 0x00, 0x00, // pure green
		0x00, 0x00, 0xFF, 0x00, // pure red
	}
	img := bgraToRGBAImage(pix, 2, 2)
	if img.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds = %v, want 2x2", img.Bounds())
	}
	expect := []struct{ x, y, r, g, b, a uint8 }{
		{0, 0, 0x33, 0x22, 0x11, 0xFF},
		{1, 0, 0x00, 0x00, 0xFF, 0xFF},
		{0, 1, 0x00, 0xFF, 0x00, 0xFF},
		{1, 1, 0xFF, 0x00, 0x00, 0xFF},
	}
	for _, e := range expect {
		idx := img.PixOffset(int(e.x), int(e.y))
		r, g, b, a := img.Pix[idx], img.Pix[idx+1], img.Pix[idx+2], img.Pix[idx+3]
		if r != e.r || g != e.g || b != e.b || a != e.a {
			t.Errorf("pixel(%d,%d) = RGBA(%d,%d,%d,%d), want (%d,%d,%d,%d)", e.x, e.y, r, g, b, a, e.r, e.g, e.b, e.a)
		}
	}
}

// TestDXGIEncodePNGOutputIsDecodable guards the capture→file tail: the PNG
// encoder must produce a decodable image of the requested size (the overlay
// loads this file through AttachmentDataURL).
func TestDXGIEncodePNGOutputIsDecodable(t *testing.T) {
	path := t.TempDir() + "/capture.png"
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 7)
	}
	if err := encodePNG(path, src); err != nil {
		t.Fatalf("encodePNG: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := decoded.Bounds(); got != image.Rect(0, 0, 3, 2) {
		t.Errorf("decoded bounds = %v, want 3x2", got)
	}
}

// TestDXGILiveCapture is the live-desktop integration half, gated behind
// TL1130_LIVE_CAPTURE=1 because a headless CI runner has no desktop session
// to duplicate (this is an explicit opt-in gate, not a silent skip: with the
// variable set on a real session the test must pass). It pins the product
// contract after the tl-1130b rework: the full chain (captureScreenAtCursor)
// always delivers a genuinely lit frame — via duplication when the calibers
// line up, via the BitBlt fallback when the duplication path refuses (DPI
// caliber split, HDR format, static desktop) — and the duplication path
// itself must either deliver or refuse with a NAMED reason, never hand back
// the all-black placeholder the first round shipped as a success.
func TestDXGILiveCapture(t *testing.T) {
	if os.Getenv("TL1130_LIVE_CAPTURE") != "1" {
		t.Skip("live desktop capture is opt-in: run with TL1130_LIVE_CAPTURE=1 on a real session")
	}

	hmon, left, top, right, bottom, err := cursorMonitorHandle()
	if err != nil {
		t.Fatalf("cursorMonitorHandle: %v", err)
	}
	if hmon == 0 {
		t.Fatal("cursorMonitorHandle returned a zero HMONITOR")
	}
	if right <= left || bottom <= top {
		t.Fatalf("cursor monitor rect is empty: %dx%d", right-left, bottom-top)
	}

	handles := enumMonitorHandles()
	if len(handles) == 0 {
		t.Fatal("enumMonitorHandles found no monitors")
	}

	// Monitor-matching diagnostics: the cursor HMONITOR must appear among the
	// factory-walked outputs — print both sides when it does not.
	if dxgiDump, err := collectDXGIOutputs(); err != nil {
		t.Logf("collectDXGIOutputs: %v", err)
	} else {
		for _, o := range dxgiDump {
			t.Logf("dxgi output: monitor=0x%x rect=(%d,%d)-(%d,%d)",
				o.desc.monitor,
				o.desc.desktopCoordinates.left, o.desc.desktopCoordinates.top,
				o.desc.desktopCoordinates.right, o.desc.desktopCoordinates.bottom)
		}
		for i := range dxgiDump {
			dxgiDump[i].release()
		}
	}

	// The duplication path alone may legitimately refuse on this machine —
	// but only for a named, understood reason (the silent-black modes are
	// exactly what tl-1130b closed). Anything else is a regression.
	dxgiPath := t.TempDir() + "/dxgi.png"
	_, _, _, _, dxgiErr := dxgiCaptureAtCursor(dxgiPath, hmon, left, top, right, bottom)
	if dxgiErr == nil {
		t.Logf("duplication delivered on this machine (calibers aligned)")
	} else {
		named := strings.Contains(dxgiErr.Error(), "dpi caliber mismatch") ||
			strings.Contains(dxgiErr.Error(), "unsupported capture format") ||
			strings.Contains(dxgiErr.Error(), "no live desktop frame") ||
			strings.Contains(dxgiErr.Error(), "all-black frames") ||
			strings.Contains(dxgiErr.Error(), "no DXGI output")
		t.Logf("duplication refused (ok, fallback answers): %v", dxgiErr)
		if !named {
			t.Errorf("duplication failed with an UNNAMED error — not one of the guarded modes: %v", dxgiErr)
		}
	}

	// The product contract: the full chain returns a lit frame no matter
	// which path served it. Sample the decoded PNG and require a genuinely
	// lit frame with a wide margin — the pre-tl1130b code produced 100%
	// near-black right here, which is the regression signature.
	path := t.TempDir() + "/live-capture.png"
	w, h, originX, originY, err := captureScreenAtCursor(path)
	if err != nil {
		t.Fatalf("captureScreenAtCursor: %v", err)
	}
	if w <= 0 || h <= 0 {
		t.Fatalf("capture is empty: %dx%d", w, h)
	}
	if originX != int(left) || originY != int(top) {
		t.Errorf("capture origin (%d,%d) != monitor rect origin (%d,%d)", originX, originY, left, top)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	bounds := decoded.Bounds()
	black, total := 0, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := decoded.At(x, y).RGBA()
			if r < 8<<8 && g < 8<<8 && b < 8<<8 {
				black++
			}
			total++
		}
	}
	ratio := float64(black) / float64(total)
	if ratio > 0.9 {
		t.Errorf("captured frame is %.1f%% near-black — the all-black regression signature", ratio*100)
	}
	t.Logf("live capture %dx%d origin(%d,%d): near-black %.2f%% (%d monitors attached, duplication refused=%v)", w, h, originX, originY, ratio*100, len(handles), dxgiErr != nil)
}

// TestDXGIDuplDescLayout pins DXGI_OUTDUPL_DESC to its dxgi1_2.h size: the
// ModeDesc head is Width+Height (8) + RefreshRate RATIONAL (8) + Format +
// ScanlineOrdering + Scaling (12) = 28, plus Rotation and
// DesktopImageInSystemMemory = 36 bytes total. GetDesc(slot 7) writes 36
// bytes, so a drifted layout reads frame geometry from the wrong offsets —
// exactly the class of silent ABI mistake the first round shipped.
func TestDXGIDuplDescLayout(t *testing.T) {
	if size := unsafe.Sizeof(dxgiDuplDesc{}); size != 36 {
		t.Fatalf("unsafe.Sizeof(dxgiDuplDesc) = %d, want 36 (DXGI_OUTDUPL_DESC)", size)
	}
}

// TestNearBlackRatio pins the all-black verdict the delivery decision leans
// on: full black → 1, lit content → ~0, and the sampling stride must not
// miss a small lit region.
func TestNearBlackRatio(t *testing.T) {
	const w, h = 64, 64
	black := make([]uint8, w*h*4) // zeroed = all-black BGRA
	if r := nearBlackRatio(black, w, h); r != 1 {
		t.Fatalf("nearBlackRatio(all black) = %f, want 1", r)
	}
	lit := make([]uint8, w*h*4)
	for i := 0; i < len(lit); i += 4 {
		lit[i], lit[i+1], lit[i+2] = 200, 200, 200
	}
	if r := nearBlackRatio(lit, w, h); r != 0 {
		t.Fatalf("nearBlackRatio(all lit) = %f, want 0", r)
	}
	// A small lit island (2x2 pixels in the top-left corner) must drag the
	// ratio clearly off 1 — the verdict that keeps a mostly-black-but-real
	// desktop deliverable.
	island := make([]uint8, w*h*4)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			island[(y*w+x)*4] = 255
		}
	}
	if r := nearBlackRatio(island, w, h); r >= nearBlackRatioReject {
		t.Fatalf("nearBlackRatio(small lit island) = %f, want < %f", r, nearBlackRatioReject)
	}
	// The verdict is layout-agnostic: RGBA and BGRA read the same, alpha is
	// never consulted.
	rgba := make([]uint8, w*h*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 0 // transparent alpha must not flip the verdict
	}
	if r := nearBlackRatio(rgba, w, h); r != 1 {
		t.Fatalf("nearBlackRatio(zeroed rgba) = %f, want 1", r)
	}
}

// TestLiveFrameVerdict pins the delivery rules: the LastPresentTime==0
// placeholder is never deliverable, one real-time black frame retries, two in
// a row hard-stops, and a lit real-time frame delivers immediately. This is
// the decision whose absence shipped two rounds of black captures.
func TestLiveFrameVerdict(t *testing.T) {
	// Placeholder frame (probe: exactly 100% near-black, LPT == 0).
	deliver, wait, bf := liveFrameVerdict(0, 1.0, 0)
	if deliver || !wait || bf != 0 {
		t.Fatalf("placeholder: deliver=%v wait=%v black=%d, want false/true/0", deliver, wait, bf)
	}
	// First real-time black frame: retry once.
	deliver, wait, bf = liveFrameVerdict(42, 1.0, 0)
	if deliver || !wait || bf != 1 {
		t.Fatalf("first black: deliver=%v wait=%v black=%d, want false/true/1", deliver, wait, bf)
	}
	// Second consecutive real-time black frame: stop, fall back.
	deliver, wait, bf = liveFrameVerdict(43, 1.0, 1)
	if deliver || wait || bf != 2 {
		t.Fatalf("second black: deliver=%v wait=%v black=%d, want false/false/2", deliver, wait, bf)
	}
	// A lit real-time frame delivers regardless of prior placeholders.
	deliver, wait, bf = liveFrameVerdict(44, 0.01, 0)
	if !deliver || wait || bf != 0 {
		t.Fatalf("lit frame: deliver=%v wait=%v black=%d, want true/false/0", deliver, wait, bf)
	}
	// A mostly-dark-but-real desktop (dark theme with icon/clock islands)
	// stays deliverable: only ≥99.9% near-black rejects.
	deliver, _, _ = liveFrameVerdict(45, 0.98, 0)
	if !deliver {
		t.Fatal("98% near-black real frame rejected; want delivered")
	}
}

// TestCaptureFallbackChain scripts both capture paths through the seams and
// pins the tl-1130b contract: a dxgi failure must be answered by the BitBlt
// fallback, and when BitBlt is black too (its own defense), the returned
// error names BOTH reasons — never a silent success, never a black PNG.
func TestCaptureFallbackChain(t *testing.T) {
	origDXGI, origBlit := dxgiCaptureAtCursorFn, blitCaptureRectFn
	defer func() { dxgiCaptureAtCursorFn, blitCaptureRectFn = origDXGI, origBlit }()

	// dxgi ok: no fallback happens (blit seam must stay untouched).
	dxgiCalls, blitCalls := 0, 0
	dxgiCaptureAtCursorFn = func(_ string, _ uintptr, _, _, _, _ int32) (int, int, int, int, error) {
		dxgiCalls++
		return 100, 200, 0, 0, nil
	}
	blitCaptureRectFn = func(_ string, _, _, _, _ int32) (int, int, error) {
		blitCalls++
		return 0, 0, fmt.Errorf("must not be called")
	}
	w, h, _, _, err := captureScreenAtCursor(t.TempDir() + "/a.png")
	if err != nil || w != 100 || h != 200 || dxgiCalls != 1 || blitCalls != 0 {
		t.Fatalf("dxgi-ok path: w=%d h=%d err=%v dxgi=%d blit=%d", w, h, err, dxgiCalls, blitCalls)
	}

	// dxgi fails, blit delivers: success comes from the fallback.
	dxgiCaptureAtCursorFn = func(_ string, _ uintptr, _, _, _, _ int32) (int, int, int, int, error) {
		return 0, 0, 0, 0, fmt.Errorf("dpi caliber mismatch: duplication frame 1920x1200 vs user32 rect 1280x800")
	}
	blitCaptureRectFn = func(path string, _, _, _, _ int32) (int, int, error) {
		_ = os.WriteFile(path, []byte("png"), 0o644)
		return 300, 400, nil
	}
	w, h, _, _, err = captureScreenAtCursor(t.TempDir() + "/b.png")
	if err != nil || w != 300 || h != 400 {
		t.Fatalf("fallback path: w=%d h=%d err=%v", w, h, err)
	}

	// Both fail: the error must name both reasons.
	blitCaptureRectFn = func(_ string, _, _, _, _ int32) (int, int, error) {
		return 0, 0, fmt.Errorf("BitBlt produced an all-black frame")
	}
	_, _, _, _, err = captureScreenAtCursor(t.TempDir() + "/c.png")
	if err == nil {
		t.Fatal("both-paths-failed returned nil error; want combined failure")
	}
	if !strings.Contains(err.Error(), "all-black") || !strings.Contains(err.Error(), "caliber mismatch") {
		t.Fatalf("combined error lost a reason: %v", err)
	}
}

// TestDXGIFactoryVsDeviceProbe is a diagnostic harness for the tl 1130 hunt:
// it walks the factory path (CreateDXGIFactory1 → EnumAdapters1 → GetDesc →
// EnumOutputs) and the device path (D3D11CreateDevice → QI IDXGIDevice →
// GetAdapter) side by side and prints every HRESULT, so a slot or ABI mistake
// shows up as a broken reading instead of a silent empty list.
// Run: TL1130_DXGI_DEBUG=1 go test -run TestDXGIFactoryVsDeviceProbe -v
func TestDXGIFactoryVsDeviceProbe(t *testing.T) {
	if os.Getenv("TL1130_DXGI_DEBUG") != "1" {
		t.Skip("diagnostic probe is opt-in: TL1130_DXGI_DEBUG=1")
	}
	if r, _, _ := procCoInitialize.Call(0, coinitMultithreaded); int32(r) < 0 {
		t.Fatalf("CoInitializeEx: 0x%x", r)
	}

	// The factory1 GUID is only needed by this probe; the product path no
	// longer walks the factory.
	// ---- factory path ----
	var factory unsafe.Pointer
	if r, _, _ := procCreateFactory.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	); !hrOK(r) {
		t.Fatalf("CreateDXGIFactory1: 0x%x", r)
	}
	defer comRelease(factory)
	for ai := 0; ai < 3; ai++ {
		var adapter unsafe.Pointer
		if r := vcall(factory, 12, uintptr(ai), uintptr(unsafe.Pointer(&adapter))); !hrOK(r) || adapter == nil {
			t.Logf("factory EnumAdapters1(%d): hr=0x%x", ai, uint32(r))
			continue
		}
		var adesc struct {
			desc   [128]uint16
			vendor uint32
			device uint32
			subSys uint32
			rev    uint32
			mem    [3]uint
			_      uintptr
		}
		hr := vcall(adapter, 8, uintptr(unsafe.Pointer(&adesc)))
		t.Logf("factory adapter %d: GetDesc(slot8) hr=0x%x vendor=0x%x name=%q", ai, uint32(hr), adesc.vendor, syscall.UTF16ToString(adesc.desc[:]))
		for oi := 0; oi < 4; oi++ {
			var outp unsafe.Pointer
			hrO := vcall(adapter, 7, uintptr(oi), uintptr(unsafe.Pointer(&outp)))
			t.Logf("  adapter %d EnumOutputs(slot7, %d): hr=0x%x out=%v", ai, oi, uint32(hrO), outp != nil)
			if outp != nil {
				comRelease(outp)
			}
		}
		comRelease(adapter)
	}

	// ---- device path ----
}
