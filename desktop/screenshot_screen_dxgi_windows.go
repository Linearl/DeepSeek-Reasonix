//go:build windows

package main

// DXGI Desktop Duplication capture for the desktop screenshot button
// (tl 1130 root fix). The previous path (GetDC(NULL) + BitBlt) is a probe-
// verified dead end on modern Windows: BitBlt returns a stale GDI-mirror
// snapshot that never picks up windows composited after the last mirror
// refresh — including the screenshot overlay's own black loading frame, which
// is exactly the all-black capture the user saw. Desktop duplication hands
// back the DWM-composed frame directly, so every visible window (WebView2
// included) is in the pixels.
//
// Pure syscall, no CGO — same style as screenshot_screen_windows.go. COM
// interface calls go through explicit vtable slots; every slot index below
// was taken from the Windows 10 SDK declaration blocks (shared/dxgi.h,
// shared/dxgi1_2.h, um/d3d11.h) and pinned by unit test, then verified live
// on a real session (live test + probe PNGs in the wt tmp dir).
//
// The duplication chain (D3D11 device + output duplication) is rebuilt per
// capture: screenshot captures are user-gesture frequency, and per-call
// ownership keeps no GPU resources alive across Wails bridge goroutines.

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	// D3D11 / DXGI constants used by the duplication path.
	dxgiFormatB8G8R8A8Unorm = 87
	d3d11UsageStaging       = 3
	d3d11CpuAccessRead      = 0x20000
	d3d11SdkVersion         = 7
	dxgiErrorWaitTimeout    = 0x887A0027
	coinitMultithreaded     = 0x0
	// AcquireNextFrame timeout (ms): the first frame after DuplicateOutput is
	// delivered immediately; a second frame only shows up on the next desktop
	// change, so a short wait + stale-first-frame semantics is fine for a
	// gesture-frequency capture.
	dxgiAcquireTimeoutMs = 200
)

// COM vtable slots (0-based, x64). IUnknown is identical on every interface;
// the rest follow the declaration order in the SDK headers. Two easy traps
// the tests pin: IDXGIFactory derives IDXGIObject directly (no GetDevice slot
// — EnumAdapters1 is 12, not 13; calling 13 hits IsCurrent, which returns
// BOOL 1 and looks like a bogus HRESULT), and IDXGIOutput has twelve own
// methods (GetDesc..GetFrameStatistics, 7..18) before IDXGIOutput1's
// DuplicateOutput (19).
const (
	comQueryInterface = 0
	comMethodRelease  = 2

	idxgiAdapterEnumOutputs    = 7  // IDXGIAdapter : IDXGIObject(3..6)
	idxgiAdapterGetDesc        = 8  //
	idxgiFactory1EnumAdapters1 = 12 // IDXGIFactory own methods 7..11, EnumAdapters1 first of Factory1

	idxgiOutputGetDesc = 7 // IDXGIOutput : IDXGIObject(3..6); Output1 starts at 19

	idxgiOutput1DuplicateOutput = 22 // Output1 own methods: GetDisplayModeList1(19)..GetDisplaySurfaceData1(21), DuplicateOutput last

	idxgiDuplAcquireNextFrame = 8  // IDXGIObject(3..6) + GetDesc(7)
	idxgiDuplReleaseFrame     = 14 // AcquireNextFrame(8)..UnMapDesktopSurface(13), ReleaseFrame last

	d3d11DeviceCreateTexture2D = 5  // after CreateBuffer(3), CreateTexture1D(4)
	d3d11ContextMap            = 14 // ID3D11DeviceChild(3..6) + 10 setters
	d3d11ContextUnmap          = 15
	d3d11ContextCopyResource   = 47 // mid-block resource copy, per d3d11.h order
)

var (
	dxgiDll           = syscall.NewLazyDLL("dxgi.dll")
	d3d11Dll          = syscall.NewLazyDLL("d3d11.dll")
	ole32Dll          = syscall.NewLazyDLL("ole32.dll")
	procCreateFactory = dxgiDll.NewProc("CreateDXGIFactory1")
	procD3D11Create   = d3d11Dll.NewProc("D3D11CreateDevice")
	procCoInitialize  = ole32Dll.NewProc("CoInitializeEx")
)

// GUIDs (DEFINE_GUID order: d0 d1 d2 d3 as the 8-byte tail little-endian).
type dxgiGUID struct {
	d0 uint32
	d1 uint16
	d2 uint16
	d3 [8]byte
}

var (
	iidIDXGIFactory1   = dxgiGUID{0x770aae78, 0xf26f, 0x4dba, [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	iidIDXGIOutput1    = dxgiGUID{0x00cddea8, 0x939b, 0x4b83, [8]byte{0xa3, 0x40, 0xa6, 0x85, 0x22, 0x66, 0x66, 0xcc}}
	iidID3D11Texture2D = dxgiGUID{0x6f15aaf2, 0xd208, 0x4e89, [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
)

// vcall invokes method `index` on a COM interface through its vtable. obj is
// the interface pointer; slot 0..2 are IUnknown's QueryInterface/AddRef/
// Release in every interface, so Release = index 2 everywhere below. Go 1.26's
// syscall.Syscall* take (trap, nargs, args…): nargs counts what lands in the
// callee's argument slots, and a COM method's `this` IS its first argument —
// so nargs = len(args)+1. Missing that made Release run without a this (wild
// call) and EnumOutputs lose its out-param (every call "failed").
func vcall(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	this := uintptr(obj)
	n := uintptr(len(args) + 1) // +1: this
	arg := func(i int) uintptr {
		if i < len(args) {
			return args[i]
		}
		return 0
	}
	switch len(args) {
	case 0:
		r, _, _ := syscall.Syscall(fn, n, this, 0, 0)
		return r
	case 1:
		r, _, _ := syscall.Syscall(fn, n, this, args[0], 0)
		return r
	case 2:
		r, _, _ := syscall.Syscall6(fn, n, this, args[0], args[1], 0, 0, 0)
		return r
	case 3:
		r, _, _ := syscall.Syscall6(fn, n, this, args[0], args[1], args[2], 0, 0)
		return r
	case 4:
		r, _, _ := syscall.Syscall9(fn, n, this, args[0], args[1], args[2], args[3], 0, 0, 0, 0)
		return r
	case 5:
		r, _, _ := syscall.Syscall9(fn, n, this, args[0], args[1], args[2], args[3], args[4], 0, 0, 0)
		return r
	case 6:
		r, _, _ := syscall.Syscall9(fn, n, this, arg(0), arg(1), arg(2), arg(3), arg(4), arg(5), 0, 0)
		return r
	default:
		return 0
	}
}

func comRelease(obj unsafe.Pointer) {
	if obj != nil {
		vcall(obj, comMethodRelease)
	}
}

func hrOK(r uintptr) bool { return int32(r) >= 0 }

// dxgiOutputDesc mirrors DXGI_OUTPUT_DESC (DeviceName[32], DesktopCoordinates
// RECT, Monitor HMONITOR, Rotation, BitsPerColor, ColorSpace) — the tail
// fields after Monitor are padding-only for our use.
type dxgiOutputDesc struct {
	deviceName        [32]uint16
	desktopCoordinates struct{ left, top, right, bottom int32 }
	monitor           uintptr
	rotation          uint32
	bitsPerColor      uint32
	colorSpace        uint32
}

type d3d11TextureDesc struct {
	width          uint32
	height         uint32
	mipLevels      uint32
	arraySize      uint32
	format         uint32
	sampleCount    uint32
	sampleQuality  uint32
	usage          uint32
	bindFlags      uint32
	cpuAccessFlags uint32
	miscFlags      uint32
}

type d3d11MappedSubresource struct {
	data       unsafe.Pointer
	rowPitch   uint32
	depthPitch uint32
}

// dxgiSession bundles the duplication chain for one capture call: a device
// created on the output's own adapter plus the duplication interface. Every
// field is released by close().
type dxgiSession struct {
	device      unsafe.Pointer
	context     unsafe.Pointer
	duplication unsafe.Pointer
	width       int
	height      int
	originX     int
	originY     int
}

func (s *dxgiSession) close() {
	comRelease(s.duplication)
	comRelease(s.context)
	comRelease(s.device)
}

// dxgiAdapterOutput is one (adapter, output) pair from the factory walk. The
// duplication device must be created on the output's own adapter, so the pair
// stays together.
type dxgiAdapterOutput struct {
	adapter unsafe.Pointer
	output  unsafe.Pointer
	desc    dxgiOutputDesc
}

func (o *dxgiAdapterOutput) release() {
	comRelease(o.output)
	comRelease(o.adapter)
}

// collectDXGIOutputs walks every adapter/output on the machine through
// CreateDXGIFactory1 and returns the capturable ones (attached, non-empty
// desktop rectangle). The caller releases each entry.
func collectDXGIOutputs() ([]dxgiAdapterOutput, error) {
	// CoInitializeEx(NULL, COINIT_MULTITHREADED); S_FALSE/RPC_E_CHANGED_MODE
	// mean COM is already up on this thread and are fine to proceed.
	if r, _, _ := procCoInitialize.Call(0, coinitMultithreaded); int32(r) < 0 {
		return nil, fmt.Errorf("CoInitializeEx failed: 0x%x", r)
	}
	var factory unsafe.Pointer
	if r, _, _ := procCreateFactory.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	); !hrOK(r) || factory == nil {
		return nil, fmt.Errorf("CreateDXGIFactory1 failed: 0x%x", r)
	}
	defer comRelease(factory)

	var found []dxgiAdapterOutput
	for adapterIdx := 0; ; adapterIdx++ {
		var adapter unsafe.Pointer
		// EnumAdapters1 takes the adapter index BY VALUE (UINT) and the out
		// pointer by reference.
		if r := vcall(factory, idxgiFactory1EnumAdapters1, uintptr(adapterIdx), uintptr(unsafe.Pointer(&adapter))); !hrOK(r) || adapter == nil {
			break // DXGI_ERROR_NOT_FOUND: no more adapters
		}
		for outputIdx := 0; ; outputIdx++ {
			var output unsafe.Pointer
			if r := vcall(adapter, idxgiAdapterEnumOutputs, uintptr(outputIdx), uintptr(unsafe.Pointer(&output))); !hrOK(r) || output == nil {
				break // DXGI_ERROR_NOT_FOUND: no more outputs on this adapter
			}
			var desc dxgiOutputDesc
			if r := vcall(output, idxgiOutputGetDesc, uintptr(unsafe.Pointer(&desc))); !hrOK(r) {
				comRelease(output)
				continue
			}
			if desc.monitor == 0 || desc.desktopCoordinates.right-desc.desktopCoordinates.left <= 0 || desc.desktopCoordinates.bottom-desc.desktopCoordinates.top <= 0 {
				comRelease(output)
				continue
			}
			// Several outputs share one adapter pointer: take one extra
			// reference per collected entry so the caller's per-entry release
			// stays balanced.
			vcall(adapter, 1)
			found = append(found, dxgiAdapterOutput{adapter: adapter, output: output, desc: desc})
		}
		// Balance the single reference EnumAdapters1 handed us — collected
		// entries hold their own references now.
		comRelease(adapter)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no capturable DXGI output on any adapter")
	}
	return found, nil
}

// duplicateOnOutput opens an IDXGIOutputDuplication on one collected output.
// The D3D11 device is created on the output's own adapter (adapter-bound
// creation with D3D_DRIVER_TYPE_UNKNOWN — the documented pairing).
func duplicateOnOutput(o *dxgiAdapterOutput) (*dxgiSession, error) {
	var output1 unsafe.Pointer
	if r := vcall(o.output, comQueryInterface, uintptr(unsafe.Pointer(&iidIDXGIOutput1)), uintptr(unsafe.Pointer(&output1))); !hrOK(r) || output1 == nil {
		return nil, fmt.Errorf("IDXGIOutput::QueryInterface(IDXGIOutput1) failed: 0x%x", r)
	}
	defer comRelease(output1)

	var device, context unsafe.Pointer
	if r, _, callErr := procD3D11Create.Call(
		uintptr(o.adapter),
		0, // D3D_DRIVER_TYPE_UNKNOWN (required when pAdapter != NULL)
		0, 0,
		0, 0, // default feature levels
		d3d11SdkVersion,
		uintptr(unsafe.Pointer(&device)),
		0, // feature level out (unused)
		uintptr(unsafe.Pointer(&context)),
	); !hrOK(r) || device == nil || context == nil {
		return nil, fmt.Errorf("D3D11CreateDevice failed: 0x%x (%v)", r, callErr)
	}

	var duplication unsafe.Pointer
	if r := vcall(output1, idxgiOutput1DuplicateOutput, uintptr(device), uintptr(unsafe.Pointer(&duplication))); !hrOK(r) || duplication == nil {
		comRelease(context)
		comRelease(device)
		return nil, fmt.Errorf("DuplicateOutput failed: 0x%x", r)
	}
	d := &dxgiSession{device: device, context: context, duplication: duplication}
	d.originX = int(o.desc.desktopCoordinates.left)
	d.originY = int(o.desc.desktopCoordinates.top)
	d.width = int(o.desc.desktopCoordinates.right - o.desc.desktopCoordinates.left)
	d.height = int(o.desc.desktopCoordinates.bottom - o.desc.desktopCoordinates.top)
	return d, nil
}

// acquireFrame grabs the current desktop frame into a staging texture and
// returns its BGRA bytes (top-down rows, tightly packed) plus the frame's
// LastPresentTime (0 = a driver placeholder frame, not a real desktop
// update). The caller must not hold the returned slice past the session's
// close() — the mapping lives in the staging texture.
func (s *dxgiSession) acquireFrame() ([]uint8, int64, error) {
	var frameInfo [48]byte // DXGI_OUTDUPL_FRAME_INFO
	var resource unsafe.Pointer
	r := vcall(s.duplication, idxgiDuplAcquireNextFrame, dxgiAcquireTimeoutMs, uintptr(unsafe.Pointer(&frameInfo[0])), uintptr(unsafe.Pointer(&resource)))
	if int32(r) < 0 {
		if uint32(r) == dxgiErrorWaitTimeout {
			return nil, 0, fmt.Errorf("AcquireNextFrame timed out")
		}
		return nil, 0, fmt.Errorf("AcquireNextFrame failed: 0x%x", r)
	}
	lastPresent := int64(uint64(frameInfo[0]) | uint64(frameInfo[1])<<8 | uint64(frameInfo[2])<<16 | uint64(frameInfo[3])<<24 |
		uint64(frameInfo[4])<<32 | uint64(frameInfo[5])<<40 | uint64(frameInfo[6])<<48 | uint64(frameInfo[7])<<56)
	defer comRelease(resource)

	var texture unsafe.Pointer
	if r := vcall(resource, comQueryInterface, uintptr(unsafe.Pointer(&iidID3D11Texture2D)), uintptr(unsafe.Pointer(&texture))); !hrOK(r) || texture == nil {
		return nil, 0, fmt.Errorf("IDXGIResource::QueryInterface(ID3D11Texture2D) failed: 0x%x", r)
	}
	defer comRelease(texture)

	stagingDesc := d3d11TextureDesc{
		width:          uint32(s.width),
		height:         uint32(s.height),
		mipLevels:      1,
		arraySize:      1,
		format:         dxgiFormatB8G8R8A8Unorm,
		sampleCount:    1,
		usage:          d3d11UsageStaging,
		cpuAccessFlags: d3d11CpuAccessRead,
	}
	var staging unsafe.Pointer
	if r := vcall(s.device, d3d11DeviceCreateTexture2D, uintptr(unsafe.Pointer(&stagingDesc)), 0, uintptr(unsafe.Pointer(&staging))); !hrOK(r) || staging == nil {
		return nil, 0, fmt.Errorf("CreateTexture2D(staging) failed: 0x%x", r)
	}
	defer comRelease(staging)

	// ID3D11DeviceContext::CopyResource (dst, src) copies the desktop texture
	// into the CPU-readable staging texture.
	vcall(s.context, d3d11ContextCopyResource, uintptr(staging), uintptr(texture))

	var mapped d3d11MappedSubresource
	// D3D11_MAP_READ = 1 (an enum value, not a flag — 0 is invalid and fails
	// the call with E_INVALIDARG).
	if r := vcall(s.context, d3d11ContextMap, uintptr(staging), 0, 1, 0, uintptr(unsafe.Pointer(&mapped))); int32(r) < 0 || mapped.data == nil {
		return nil, 0, fmt.Errorf("Map(staging) failed: 0x%x", r)
	}
	defer vcall(s.context, d3d11ContextUnmap, uintptr(staging), 0)

	rowBytes := int(mapped.rowPitch)
	out := make([]uint8, s.width*s.height*4)
	src := unsafe.Slice((*uint8)(mapped.data), rowBytes*s.height)
	for y := 0; y < s.height; y++ {
		copy(out[y*s.width*4:(y+1)*s.width*4], src[y*rowBytes:y*rowBytes+s.width*4])
	}
	return out, lastPresent, nil
}

// acquireLiveFrame takes the first REAL desktop frame. The driver hands back
// a black placeholder for the first AcquireNextFrame after DuplicateOutput
// (LastPresentTime == 0 — the probe measured 100% near-black on it), so the
// placeholder is dropped and the next composited frame is taken; a timeout
// with an already-held frame is not fatal (a static desktop produces no new
// frames).
func (s *dxgiSession) acquireLiveFrame() ([]uint8, error) {
	var best []uint8
	for attempt := 0; attempt < 4; attempt++ {
		pix, present, err := s.acquireFrame()
		if err != nil {
			if best != nil {
				return best, nil
			}
			return nil, err
		}
		best = pix
		s.releaseFrame()
		if present != 0 {
			return pix, nil
		}
		time.Sleep(33 * time.Millisecond) // wait out one DWM frame
	}
	return best, nil
}

// releaseFrame closes the duplication frame so the OS can keep compositing.
func (s *dxgiSession) releaseFrame() {
	vcall(s.duplication, idxgiDuplReleaseFrame)
}

// bgraToRGBAImage converts a top-down 32bpp BGRA buffer to an RGBA image.
func bgraToRGBAImage(pix []uint8, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := pix[y*w*4 : (y+1)*w*4]
		dst := img.Pix[y*img.Stride : (y+1)*img.Stride]
		for x := 0; x < w; x++ {
			dst[x*4+0] = src[x*4+2] // R
			dst[x*4+1] = src[x*4+1] // G
			dst[x*4+2] = src[x*4+0] // B
			dst[x*4+3] = 0xff       // desktop duplication leaves alpha unset
		}
	}
	return img
}

func encodePNG(outputPath string, img image.Image) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encode %s: %w", outputPath, err)
	}
	return nil
}

// dxgiMonitorMatches decides whether a DXGI output is the cursor monitor:
// HMONITOR equality when the driver reports real handles, else exact
// desktop-rectangle equality (both sides in the process's DPI caliber).
func dxgiMonitorMatches(desc dxgiOutputDesc, hmon uintptr, left, top, right, bottom int32) bool {
	d := desc.desktopCoordinates
	if d.left == left && d.top == top && d.right == right && d.bottom == bottom {
		return true
	}
	return desc.monitor == hmon
}

// dxgiCaptureAtCursor captures the cursor monitor's full frame through
// desktop duplication into outputPath. The monitor is matched primarily by
// its virtual-desktop rect: on at least one hybrid-GPU machine the driver
// fills DXGI_OUTPUT_DESC.Monitor with a synthetic handle (identical across
// outputs) that never equals the user32 HMONITOR, while the desktop
// rectangle matches GetMonitorInfo's exactly (same DPI caliber in the PMv2
// process). Returns pixel size + virtual-desktop origin so the overlay
// coordinate mapping is unchanged from the BitBlt era.
func dxgiCaptureAtCursor(outputPath string, targetHmon uintptr, left, top, right, bottom int32) (w, h, originX, originY int, err error) {
	outputs, err := collectDXGIOutputs()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer func() {
		for i := range outputs {
			outputs[i].release()
		}
	}()
	for i := range outputs {
		o := &outputs[i]
		if !dxgiMonitorMatches(o.desc, targetHmon, left, top, right, bottom) {
			continue
		}
		session, err := duplicateOnOutput(o)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		pix, err := session.acquireLiveFrame()
		if err != nil {
			session.close()
			return 0, 0, 0, 0, err
		}
		encErr := encodePNG(outputPath, bgraToRGBAImage(pix, session.width, session.height))
		session.close()
		if encErr != nil {
			return 0, 0, 0, 0, encErr
		}
		return session.width, session.height, session.originX, session.originY, nil
	}
	return 0, 0, 0, 0, fmt.Errorf("no DXGI output for the cursor monitor (hmon 0x%x)", targetHmon)
}

// dxgiCaptureVirtualDesktop captures every attached output and stitches the
// frames into one virtual-desktop PNG. Each output contributes its own
// duplication (desktop duplication is per-output by design); outputs that
// fail individually (e.g. detached mid-call) are skipped unless all fail.
// Outputs beyond duplication's reach (protected mode, RDP quirks) are covered
// by the BitBlt fallback in captureVirtualScreenPNG.
func dxgiCaptureVirtualDesktop(outputPath string, m ScreenMetrics) error {
	outputs, err := collectDXGIOutputs()
	if err != nil {
		return err
	}
	defer func() {
		for i := range outputs {
			outputs[i].release()
		}
	}()
	canvas := image.NewRGBA(image.Rect(0, 0, m.Width, m.Height))
	captured := 0
	for i := range outputs {
		o := &outputs[i]
		session, err := duplicateOnOutput(o)
		if err != nil {
			continue
		}
		pix, err := session.acquireLiveFrame()
		if err != nil {
			session.close()
			continue
		}
		// Paste the frame at its virtual-desktop origin. The canvas is in
		// pixels and both sides are physical coordinates (PMv2), so the copy
		// is byte-for-byte.
		rowBytes := session.width * 4
		for y := 0; y < session.height; y++ {
			canvasY := y + session.originY - m.Y
			if canvasY < 0 || canvasY >= m.Height {
				continue
			}
			src := pix[y*rowBytes : y*rowBytes+rowBytes]
			dstX := session.originX - m.X
			dst := canvas.Pix[canvasY*canvas.Stride:]
			for x := 0; x < session.width; x++ {
				px := dstX + x
				if px < 0 || px >= m.Width {
					continue
				}
				d := px * 4
				s := x * 4
				dst[d+0] = src[s+2] // R
				dst[d+1] = src[s+1] // G
				dst[d+2] = src[s+0] // B
				dst[d+3] = 0xff
			}
		}
		session.close()
		captured++
	}
	if captured == 0 {
		return fmt.Errorf("desktop duplication failed on every output")
	}
	return encodePNG(outputPath, canvas)
}
