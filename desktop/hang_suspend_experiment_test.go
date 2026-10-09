//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Task 696 acceptance a/b/d, end to end without touching the developer's
// Reasonix (issue #38): the parent test re-executes this test binary as a child
// that runs the REAL watchdog (heartbeat goroutine + ticker + live probe)
// against a REAL win32 message loop, then suspends (NtSuspendProcess) and
// resumes the whole child — the same shape Process Explorer's suspend produced
// in issue #38.
//
// The child scales the watchdog clock down ~10x so the whole experiment stays
// well under two minutes:
//
//	scaled: heartbeat 200ms  threshold 1.2s  check 300ms  sleep-skip 3s  probe 600ms
//	acceptance a: suspend 1.8s  (inside the old 12-30s false-positive band) -> no report
//	acceptance b: suspend 4.0s  (beyond the old sleep-skip band)           -> no report
//	acceptance d: message loop blocked 2.1s                                 -> report with stack
//
// Gated behind REASONIX_HANG_SUSPEND_TEST=1 because it spawns processes and
// sleeps for real; plain `go test` skips it.

const (
	hangSuspendChildScenarioEnv = "REASONIX_HANG_TEST_SCENARIO"
	hangSuspendChildDirEnv      = "REASONIX_HANG_TEST_DIR"
	hangSuspendGateEnv          = "REASONIX_HANG_SUSPEND_TEST"
)

func hangSuspendChildScenario() string {
	return os.Getenv(hangSuspendChildScenarioEnv)
}

func applyHangSuspendTestTiming() {
	mainThreadHeartbeatInterval = 200 * time.Millisecond
	mainThreadHangThreshold = 1200 * time.Millisecond
	mainThreadHangCheckInterval = 300 * time.Millisecond
	mainThreadSleepSkip = 3 * time.Second
	mainThreadProbeTimeout = 600 * time.Millisecond
}

var (
	hangProbeUser32           = windows.NewLazySystemDLL("user32.dll")
	hangProbeNtdll            = windows.NewLazySystemDLL("ntdll.dll")
	hangProbeRegisterClassExW = hangProbeUser32.NewProc("RegisterClassExW")
	hangProbeCreateWindowExW  = hangProbeUser32.NewProc("CreateWindowExW")
	hangProbeDefWindowProcW   = hangProbeUser32.NewProc("DefWindowProcW")
	hangProbePeekMessageW     = hangProbeUser32.NewProc("PeekMessageW")
	hangProbeTranslateMessage = hangProbeUser32.NewProc("TranslateMessage")
	hangProbeDispatchMessageW = hangProbeUser32.NewProc("DispatchMessageW")
	hangProbeNtSuspendProcess = hangProbeNtdll.NewProc("NtSuspendProcess")
	hangProbeNtResumeProcess  = hangProbeNtdll.NewProc("NtResumeProcess")
)

const (
	hangProbeWsOverlappedWindow   = 0x00CF0000
	hangProbePmRemove             = 0x0001
	hangProbeProcessSuspendResume = 0x0800
)

type hangProbeWindowClass struct {
	cbSize     uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSmall  windows.Handle
}

type hangProbeMessage struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	private uint32
}

// runHangProbeWindowThread owns a real message loop on a locked OS thread.
// The window class is "wailsWindow" so currentProcessTopLevelWindow finds it
// exactly like the app's real window. Closing pumpBlock makes the thread stop
// pumping for longer than the hang threshold + probe timeout — a genuine UI
// thread block for acceptance d.
func runHangProbeWindowThread(ready chan<- uintptr, pumpBlock <-chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var instance windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &instance); err != nil {
		ready <- 0
		return
	}
	className, _ := windows.UTF16PtrFromString("wailsWindow")
	windowName, _ := windows.UTF16PtrFromString("reasonix hang watchdog acceptance")
	windowClass := hangProbeWindowClass{
		cbSize: uint32(unsafe.Sizeof(hangProbeWindowClass{})),
		wndProc: windows.NewCallback(func(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
			result, _, _ := hangProbeDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
			return result
		}),
		instance: instance, className: className,
	}
	if registered, _, _ := hangProbeRegisterClassExW.Call(uintptr(unsafe.Pointer(&windowClass))); registered == 0 {
		ready <- 0
		return
	}
	hwnd, _, _ := hangProbeCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)),
		hangProbeWsOverlappedWindow, 0, 0, 200, 100, 0, 0, uintptr(instance), 0,
	)
	ready <- hwnd
	if hwnd == 0 {
		return
	}

	var msg hangProbeMessage
	blocked := false
	for {
		select {
		case <-pumpBlock:
			if !blocked {
				blocked = true
				time.Sleep(mainThreadHangThreshold + mainThreadProbeTimeout + 300*time.Millisecond)
			}
		default:
		}
		found, _, _ := hangProbePeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, hangProbePmRemove)
		if found != 0 {
			hangProbeTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			hangProbeDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hangChildPendingReports() []string {
	paths, err := os.ReadDir(pendingCrashDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range paths {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			out = append(out, filepath.Join(pendingCrashDir(), entry.Name()))
		}
	}
	return out
}

// runHangSuspendChildScenario executes inside the re-exec'd child (before any
// test machinery): create the window, start the real watchdog, then behave per
// scenario. Exit code is the child's verdict.
func runHangSuspendChildScenario(scenario string) int {
	applyHangSuspendTestTiming()
	dir := os.Getenv(hangSuspendChildDirEnv)
	if dir == "" {
		fmt.Fprintln(os.Stderr, "hang child:", hangSuspendChildDirEnv, "not set")
		return 2
	}

	ready := make(chan uintptr, 1)
	pumpBlock := make(chan struct{})
	go runHangProbeWindowThread(ready, pumpBlock)
	if hwnd := <-ready; hwnd == 0 {
		fmt.Fprintln(os.Stderr, "hang child: probe window creation failed")
		return 2
	}

	app := NewApp()
	app.startMainThreadWatchdog()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "hang child: write ready file:", err)
		return 2
	}

	switch scenario {
	case "pause-short", "pause-long":
		// The parent suspends the whole process for longer than the (scaled)
		// hang threshold while this sleep is frozen too. After resume the
		// watchdog must absorb the gap as a pause: no hang report, ever.
		time.Sleep(7 * time.Second)
		if leftovers := hangChildPendingReports(); len(leftovers) > 0 {
			fmt.Fprintln(os.Stderr, "hang child: suspend produced hang reports:", leftovers)
			return 1
		}
		return 0
	case "real-hang":
		// No suspend: the message loop itself stops answering past the
		// threshold. The watchdog must report, with stack and probe verdict.
		time.Sleep(time.Second)
		close(pumpBlock)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if leftovers := hangChildPendingReports(); len(leftovers) > 0 {
				return 0
			}
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, "hang child: blocked message loop produced no hang report")
		return 1
	default:
		fmt.Fprintf(os.Stderr, "hang child: unknown scenario %q\n", scenario)
		return 2
	}
}

// suspendHangChildProcess freezes/resumes every thread of the child at once,
// like Process Explorer's Suspend does (issue #38).
func suspendHangChildProcess(t *testing.T, pid uint32, suspend bool) {
	t.Helper()
	handle, err := windows.OpenProcess(hangProbeProcessSuspendResume, false, pid)
	if err != nil {
		t.Fatalf("open child process %d: %v", pid, err)
	}
	defer windows.CloseHandle(handle)
	proc := hangProbeNtSuspendProcess
	if !suspend {
		proc = hangProbeNtResumeProcess
	}
	if status, _, _ := proc.Call(uintptr(handle)); status != 0 {
		t.Fatalf("NtSuspendProcess/NtResumeProcess(suspend=%t): ntstatus 0x%x", suspend, status)
	}
}

func waitForHangChildReady(t *testing.T, dir string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	readyPath := filepath.Join(dir, "ready")
	for {
		if _, err := os.Stat(readyPath); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("hang child never became ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func hangChildReportsIn(t *testing.T, dir string) []crashReport {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "state", "crash-pending", "*.json"))
	if err != nil {
		t.Fatalf("glob pending reports: %v", err)
	}
	var reports []crashReport
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read pending report %s: %v", path, err)
		}
		var r crashReport
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("pending report %s not valid JSON: %v", path, err)
		}
		reports = append(reports, r)
	}
	return reports
}

func TestHangSuspendChildNoop(t *testing.T) {
	// Never runs as a test: TestMain intercepts the child env before m.Run.
	t.Skip("hang suspend experiment child entry; see TestHangSuspendAcceptance")
}

// TestHangSuspendAcceptance is issue #38's acceptance a/b/d on a real process:
// a) a suspend inside the old false-positive band must not report,
// b) a suspend beyond the old sleep-skip band must stay silent (regression),
// d) a genuinely blocked message loop must still report, with stack and probe
// verdict — the fix must not mute real hangs.
func TestHangSuspendAcceptance(t *testing.T) {
	if os.Getenv(hangSuspendGateEnv) != "1" {
		t.Skip("end-to-end suspend experiment; run with " + hangSuspendGateEnv + "=1 go test -run TestHangSuspendAcceptance")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	startChild := func(t *testing.T, scenario string) (*exec.Cmd, string) {
		t.Helper()
		dir := t.TempDir()
		cmd := exec.Command(exe, "-test.run", "^TestHangSuspendChildNoop$")
		cmd.Env = append(os.Environ(),
			hangSuspendChildScenarioEnv+"="+scenario,
			hangSuspendChildDirEnv+"="+dir,
			"REASONIX_STATE_HOME="+filepath.Join(dir, "state"),
		)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child (%s): %v", scenario, err)
		}
		t.Cleanup(func() {
			if cmd.Process != nil && cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
			}
		})
		return cmd, dir
	}

	t.Run("AcceptanceA_SuspendInsideBandNoReport", func(t *testing.T) {
		cmd, dir := startChild(t, "pause-short")
		waitForHangChildReady(t, dir, 15*time.Second)
		suspendHangChildProcess(t, uint32(cmd.Process.Pid), true)
		time.Sleep(1800 * time.Millisecond) // inside the scaled 1.2s-3s band
		suspendHangChildProcess(t, uint32(cmd.Process.Pid), false)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child verdict: %v", err)
		}
		if reports := hangChildReportsIn(t, dir); len(reports) != 0 {
			t.Fatalf("13s-band suspend must not report, got %d reports: %+v", len(reports), reports)
		}
	})

	t.Run("AcceptanceB_SuspendBeyondBandNoReport", func(t *testing.T) {
		cmd, dir := startChild(t, "pause-long")
		waitForHangChildReady(t, dir, 15*time.Second)
		suspendHangChildProcess(t, uint32(cmd.Process.Pid), true)
		time.Sleep(4 * time.Second) // beyond the scaled 3s sleep-skip band
		suspendHangChildProcess(t, uint32(cmd.Process.Pid), false)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child verdict: %v", err)
		}
		if reports := hangChildReportsIn(t, dir); len(reports) != 0 {
			t.Fatalf("35s-band suspend must stay exempt, got %d reports: %+v", len(reports), reports)
		}
	})

	t.Run("AcceptanceD_BlockedMessageLoopReports", func(t *testing.T) {
		cmd, dir := startChild(t, "real-hang")
		waitForHangChildReady(t, dir, 15*time.Second)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child verdict: %v", err)
		}
		reports := hangChildReportsIn(t, dir)
		if len(reports) == 0 {
			t.Fatal("blocked message loop must produce a hang report")
		}
		r := reports[0]
		if r.Kind != "performance" || r.Source != "native.watchdog" {
			t.Fatalf("unexpected report identity: %+v", r)
		}
		if len(r.Stack) == 0 {
			t.Fatal("report must carry a goroutine stack")
		}
		for _, want := range []string{"status: hung", "SendMessageTimeoutW(WM_NULL) timed out"} {
			if !strings.Contains(r.Message, want) {
				t.Fatalf("report message missing %q:\n%s", want, r.Message)
			}
		}
		if got := len(reports); got != 1 {
			t.Fatalf("exactly one report expected, got %d", got)
		}
	})
}
