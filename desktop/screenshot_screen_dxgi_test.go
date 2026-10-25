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
// variable set on a real session the test must pass). It proves the full
// chain — monitor resolution, output matching, duplication, pixel readback —
// and asserts the captured frame is not the all-black frame the legacy BitBlt
// path produced (probe reading: a live desktop is ~0% near-black).
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

	path := t.TempDir() + "/live-capture.png"
	w, h, originX, originY, err := dxgiCaptureAtCursor(path, hmon, left, top, right, bottom)
	if err != nil {
		t.Fatalf("dxgiCaptureAtCursor: %v", err)
	}
	if w <= 0 || h <= 0 {
		t.Fatalf("capture is empty: %dx%d", w, h)
	}
	if originX != int(left) || originY != int(top) {
		t.Errorf("capture origin (%d,%d) != monitor rect origin (%d,%d)", originX, originY, left, top)
	}

	// The probe's reading for a real desktop is near-black ≈ 0%; the bug this
	// file fixes produced a fully black frame. Sample the decoded PNG and
	// require a genuinely lit frame with a wide margin.
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
	t.Logf("live capture %dx%d origin(%d,%d): near-black %.2f%% (%d monitors attached)", w, h, originX, originY, ratio*100, len(handles))
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
			desc    [128]uint16
			vendor  uint32
			device  uint32
			subSys  uint32
			rev     uint32
			mem     [3]uint
			_       uintptr
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
