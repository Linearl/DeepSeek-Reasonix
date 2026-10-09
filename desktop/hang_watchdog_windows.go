//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmNull          = 0x0000
	smtoBlock       = 0x0001
	smtoAbortIfHung = 0x0002
)

var (
	user32DLL                  = windows.NewLazySystemDLL("user32.dll")
	enumWindowsProc            = user32DLL.NewProc("EnumWindows")
	getClassNameProc           = user32DLL.NewProc("GetClassNameW")
	getWindowThreadProcessProc = user32DLL.NewProc("GetWindowThreadProcessId")
	sendMessageTimeoutProc     = user32DLL.NewProc("SendMessageTimeoutW")
	windowsHeartbeatMu         sync.Mutex
	windowsHeartbeatStop       chan struct{}
	enumWindowsMu              sync.Mutex
	enumWindowsPID             uint32
	enumWindowsFound           uintptr
	enumWindowsCallback        = syscall.NewCallback(enumCurrentProcessTopLevelWindow)
)

func init() {
	// Windows has a live message-loop probe, so the watchdog re-checks the
	// window before judging a stale heartbeat (issue #38).
	probeMainThread = probeWindowsMainThread
}

func mainThreadWatchdogSupported() bool { return true }

func startNativeMainThreadHeartbeat(intervalMS uint64) {
	windowsHeartbeatMu.Lock()
	if windowsHeartbeatStop != nil {
		windowsHeartbeatMu.Unlock()
		return
	}
	stop := make(chan struct{})
	windowsHeartbeatStop = stop
	windowsHeartbeatMu.Unlock()

	go func() {
		interval := time.Duration(intervalMS) * time.Millisecond
		if interval <= 0 {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				hwnd := currentProcessTopLevelWindow()
				// Window creation can lag OnStartup. Treat absence as
				// inconclusive instead of manufacturing a startup hang.
				if hwnd == 0 {
					recordMainThreadProbeOutcome(mainThreadProbeResult{
						Status: mainThreadProbeInconclusive,
						Detail: "heartbeat: no current-process top-level window yet",
						At:     now,
					})
					recordMainThreadHeartbeat(now)
					continue
				}
				detail, responsive := windowMessageLoopResponsiveDetail(hwnd, interval)
				status := mainThreadProbeHung
				if responsive {
					status = mainThreadProbeResponsive
					recordMainThreadHeartbeat(now)
				}
				recordMainThreadProbeOutcome(mainThreadProbeResult{
					Status:   status,
					Detail:   detail,
					Duration: time.Since(now),
					At:       now,
				})
			}
		}
	}()
}

func stopNativeMainThreadHeartbeat() {
	windowsHeartbeatMu.Lock()
	stop := windowsHeartbeatStop
	windowsHeartbeatStop = nil
	windowsHeartbeatMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func currentProcessTopLevelWindow() uintptr {
	// Go's Windows callback table is process-lifetime state with a finite
	// capacity. Reuse one callback instead of consuming an entry on every
	// one-second heartbeat.
	enumWindowsMu.Lock()
	defer enumWindowsMu.Unlock()
	enumWindowsPID = uint32(os.Getpid())
	enumWindowsFound = 0
	enumWindowsProc.Call(enumWindowsCallback, 0)
	return enumWindowsFound
}

func enumCurrentProcessTopLevelWindow(hwnd uintptr, _ uintptr) uintptr {
	var windowPID uint32
	getWindowThreadProcessProc.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
	if windowPID == enumWindowsPID && windowClassName(hwnd) == "wailsWindow" {
		enumWindowsFound = hwnd
		return 0
	}
	return 1
}

func windowClassName(hwnd uintptr) string {
	var name [256]uint16
	n, _, _ := getClassNameProc.Call(
		hwnd,
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(len(name)),
	)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(name[:n])
}

// probeWindowsMainThread synchronously asks the current process's top-level
// wailsWindow whether its message loop still answers. Runs on the watchdog
// goroutine — it may wait up to mainThreadProbeTimeout without ever blocking
// the UI thread. This is the check that separates a suspend artifact (window
// answers the moment the process resumes) from a real hang (issue #38).
func probeWindowsMainThread() mainThreadProbeResult {
	start := time.Now()
	hwnd := currentProcessTopLevelWindow()
	if hwnd == 0 {
		return mainThreadProbeResult{
			Status:   mainThreadProbeInconclusive,
			Detail:   "no current-process top-level wailsWindow (window absent)",
			Duration: time.Since(start),
		}
	}
	detail, responsive := windowMessageLoopResponsiveDetail(hwnd, mainThreadProbeTimeout)
	status := mainThreadProbeHung
	if responsive {
		status = mainThreadProbeResponsive
	}
	return mainThreadProbeResult{
		Status:   status,
		Detail:   detail,
		Duration: time.Since(start),
	}
}

func windowMessageLoopResponsiveDetail(hwnd uintptr, timeout time.Duration) (string, bool) {
	timeoutMS := max(timeout.Milliseconds(), 250)
	var result uintptr
	ok, _, callErr := sendMessageTimeoutProc.Call(
		hwnd,
		wmNull,
		0,
		0,
		smtoBlock|smtoAbortIfHung,
		uintptr(timeoutMS),
		uintptr(unsafe.Pointer(&result)),
	)
	if ok != 0 {
		return fmt.Sprintf("SendMessageTimeoutW(WM_NULL) answered within %dms", timeoutMS), true
	}
	return classifySendMessageTimeoutFailure(callErr, timeoutMS), false
}

// classifySendMessageTimeoutFailure keeps the failure shapes distinguishable in
// the hang report (issue #38): a timeout means the message loop really stopped
// answering, an invalid handle means the window went away (inconclusive at
// best), and a failure without an error code is the SMTO_ABORTIFHUNG abort for
// a thread the OS already considers hung.
func classifySendMessageTimeoutFailure(callErr error, timeoutMS int64) string {
	var errno syscall.Errno
	if errors.As(callErr, &errno) {
		switch errno {
		case windows.ERROR_TIMEOUT:
			return fmt.Sprintf("SendMessageTimeoutW(WM_NULL) timed out after %dms", timeoutMS)
		case windows.ERROR_INVALID_HANDLE:
			return "SendMessageTimeoutW(WM_NULL) failed: invalid window handle"
		default:
			return fmt.Sprintf("SendMessageTimeoutW(WM_NULL) failed: %v", errno)
		}
	}
	return "SendMessageTimeoutW(WM_NULL) failed without an error code (SMTO_ABORTIFHUNG abort: window thread flagged hung)"
}
