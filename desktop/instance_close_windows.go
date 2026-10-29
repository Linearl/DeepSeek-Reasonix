//go:build windows

package main

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Graceful close of another instance (task 1259 layer 2): post WM_CLOSE to
// its top-level windows — the canonical close request the app already owns a
// decision for (quit, or hide-to-tray per the close-behavior setting) — and
// wait a bounded time for the process to exit. Never terminates the process:
// survivors are reported for manual handling by the caller.
//
// Note on tray-resident instances: with close-behavior "background" the app
// answers WM_CLOSE by hiding, which the bounded wait correctly reads as "did
// not close" — the instance lands in the manual-close report instead of
// being killed. That is the deliberate trade (never force kill) from the
// task ruling.

// instanceCloseGrace is the bounded wait for one graceful close. Generous
// enough for a Wails app to flush and exit, short enough that a hidden
// (tray) instance does not stall the switch for minutes. A test seam, not a
// constant, so the close loop can be exercised quickly.
var instanceCloseGrace = 10 * time.Second

const instanceClosePollInterval = 250 * time.Millisecond

var user32ProcedurePostMessage = windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW")

const wmCloseMsg = 0x0010 // WM_CLOSE

// closeTargetPID carries the pid through the Win32 callback (package-level,
// set right before each enumeration; the switch flow closes sequentially).
var (
	closeMu        sync.Mutex
	closeTargetPID atomic.Int64
	closeCallback  = windows.NewCallback(postCloseForWindow)
)

func postCloseForWindow(hwnd windows.HWND, lparam uintptr) uintptr {
	var pid uint32
	_, _ = windows.GetWindowThreadProcessId(hwnd, &pid)
	if pid != 0 && pid == uint32(closeTargetPID.Load()) {
		_, _, _ = user32ProcedurePostMessage.Call(uintptr(hwnd), uintptr(wmCloseMsg), 0, 0)
	}
	return 1 // keep enumerating
}

func gracefulCloseOtherInstance(pid int) bool {
	if pid <= 0 || pid == os.Getpid() {
		return false
	}
	postProcessClose(pid)
	deadline := time.Now().Add(instanceCloseGrace)
	for {
		if !desktopProcessAlive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(instanceClosePollInterval)
	}
}

// postProcessClose posts WM_CLOSE to every top-level window of the pid.
func postProcessClose(pid int) {
	closeMu.Lock()
	closeTargetPID.Store(int64(pid))
	closeMu.Unlock()
	_ = windows.EnumWindows(closeCallback, unsafe.Pointer(nil))
}
