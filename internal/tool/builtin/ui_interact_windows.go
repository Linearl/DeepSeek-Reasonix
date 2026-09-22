//go:build windows

package builtin

import (
	"fmt"
	"time"
	"unsafe"
)

// Windows driver for the ui_interact tool (task 233 batch 2): SetForeground
// + SendInput. Click coordinates are window-relative (same origin as the
// screenshot output) and translated to absolute desktop coordinates per call.
//
// The INPUT union is reproduced exactly (type + pad + MOUSEINPUT, 40 bytes on
// amd64) because SendInput validates cbSize; keyboard events overlay
// KEYBDINPUT onto the union head (KEYBDINPUT is smaller than MOUSEINPUT).

const (
	uiVKShift    = 0x10
	uiVKControl  = 0x11
	uiVKMenu     = 0x12
	uiMouseMove  = 0x0001
	uiMouseLeft  = 0x0002
	uiMouseLeftU = 0x0004
	uiMouseRight = 0x0008
	uiMouseRgtU  = 0x0010
	uiMouseAbs   = 0x8000
	uiKeyEvtUp   = 0x0002
	uiKeyEvtUni  = 0x0004

	uiInputMouse    = 0
	uiInputKeyboard = 1

	smCXScreen = 0
	smCYScreen = 1
)

var (
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSendInput           = user32.NewProc("SendInput")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
)

type uiWindowRect struct {
	left, top, right, bottom int32
}

type uiMouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type uiKeyboardInput struct {
	wVK         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// win32Input mirrors win32's INPUT on amd64: SendInput rejects a cbSize that
// differs from sizeof(INPUT), so the union's largest member and the resulting
// padding must match byte for byte. Keyboard events overlay KEYBDINPUT onto
// the union head.
type win32Input struct {
	inputType uint32
	_         uint32
	mouse     uiMouseInput
}

func (w *win32Input) setKeyboard(k uiKeyboardInput) {
	*(*uiKeyboardInput)(unsafe.Pointer(&w.mouse)) = k
}

func sendWin32Input(in *win32Input) {
	procSendInput.Call(1, uintptr(unsafe.Pointer(in)), uintptr(unsafe.Sizeof(*in)))
}

func uiDriverActivate(titleSubstring string) (string, error) {
	hwnd, title, err := findTargetWindow(titleSubstring)
	if err != nil {
		return "", err
	}
	if r, _, callErr := procSetForegroundWindow.Call(uintptr(hwnd)); r == 0 {
		return "", fmt.Errorf("SetForegroundWindow failed for %q: %v", title, callErr)
	}
	time.Sleep(120 * time.Millisecond) // let the focus switch settle
	return title, nil
}

func uiTargetRect(titleSubstring string) (uiWindowRect, string, error) {
	title, err := uiDriverActivate(titleSubstring)
	if err != nil {
		return uiWindowRect{}, "", err
	}
	hwnd, _, herr := findTargetWindow(titleSubstring)
	if herr != nil {
		return uiWindowRect{}, "", herr
	}
	var rect uiWindowRect
	if r, _, callErr := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect))); r == 0 {
		return uiWindowRect{}, "", fmt.Errorf("GetWindowRect failed for %q: %v", title, callErr)
	}
	return rect, title, nil
}

func uiDriverClick(titleSubstring string, x, y int, double, right bool) (string, error) {
	rect, title, err := uiTargetRect(titleSubstring)
	if err != nil {
		return "", err
	}
	uiSendMouseMotion(int(rect.left)+x, int(rect.top)+y)
	uiSendMouseButton(right, false)
	uiSendMouseButton(right, true)
	if double {
		uiSendMouseButton(right, false)
		uiSendMouseButton(right, true)
	}
	return title, nil
}

func uiSendMouseMotion(screenX, screenY int) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	in := win32Input{inputType: uiInputMouse, mouse: uiMouseInput{
		dx:      int32((screenX*65536 + int(w) - 1) / int(w)),
		dy:      int32((screenY*65536 + int(h) - 1) / int(h)),
		dwFlags: uiMouseMove | uiMouseAbs,
	}}
	sendWin32Input(&in)
}

func uiSendMouseButton(right, up bool) {
	var flags uint32
	switch {
	case right && up:
		flags = uiMouseRgtU
	case right:
		flags = uiMouseRight
	case up:
		flags = uiMouseLeftU
	default:
		flags = uiMouseLeft
	}
	in := win32Input{inputType: uiInputMouse, mouse: uiMouseInput{dwFlags: flags}}
	sendWin32Input(&in)
}

func uiDriverType(titleSubstring, text string) (string, error) {
	title, err := uiDriverActivate(titleSubstring)
	if err != nil {
		return "", err
	}
	for _, r := range text {
		uiSendUnicodeKey(uint16(r))
	}
	return title, nil
}

func uiSendUnicodeKey(r uint16) {
	var in win32Input
	in.inputType = uiInputKeyboard
	in.setKeyboard(uiKeyboardInput{wScan: r, dwFlags: uiKeyEvtUni})
	sendWin32Input(&in)
	in.setKeyboard(uiKeyboardInput{wScan: r, dwFlags: uiKeyEvtUni | uiKeyEvtUp})
	sendWin32Input(&in)
}

func uiDriverKey(titleSubstring string, vk uint16, modifiers []uint16) (string, error) {
	title, err := uiDriverActivate(titleSubstring)
	if err != nil {
		return "", err
	}
	uiSendKeyWithModifiers(vk, modifiers)
	return title, nil
}

func uiSendKeyWithModifiers(vk uint16, modifiers []uint16) {
	var in win32Input
	in.inputType = uiInputKeyboard
	for _, m := range modifiers {
		in.setKeyboard(uiKeyboardInput{wVK: m})
		sendWin32Input(&in)
	}
	in.setKeyboard(uiKeyboardInput{wVK: vk})
	sendWin32Input(&in)
	in.setKeyboard(uiKeyboardInput{wVK: vk, dwFlags: uiKeyEvtUp})
	sendWin32Input(&in)
	for i := len(modifiers) - 1; i >= 0; i-- {
		in.setKeyboard(uiKeyboardInput{wVK: modifiers[i], dwFlags: uiKeyEvtUp})
		sendWin32Input(&in)
	}
}
