//go:build windows

package main

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCurrentProcessTopLevelWindowReusesEnumCallback(t *testing.T) {
	if enumWindowsCallback == 0 {
		t.Fatal("EnumWindows callback was not registered")
	}

	// Go's Windows runtime currently supports a finite callback table. The old
	// implementation allocated one callback per poll and exhausted that table
	// during a normal long-running session.
	for range 2100 {
		_ = currentProcessTopLevelWindow()
	}
}

func TestClassifySendMessageTimeoutFailure(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantIn  []string
		wantNot []string
	}{
		{
			name:   "timeout",
			err:    windows.ERROR_TIMEOUT,
			wantIn: []string{"timed out after 2000ms"},
		},
		{
			name:   "invalid handle",
			err:    windows.ERROR_INVALID_HANDLE,
			wantIn: []string{"invalid window handle"},
		},
		{
			name:   "other errno",
			err:    syscall.Errno(5),
			wantIn: []string{"failed"},
		},
		{
			name:    "abortifhung without errno",
			err:     nil,
			wantIn:  []string{"SMTO_ABORTIFHUNG", "flagged hung"},
			wantNot: []string{"timed out"},
		},
	}
	for _, c := range cases {
		got := classifySendMessageTimeoutFailure(c.err, 2000)
		for _, want := range c.wantIn {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: classification %q missing %q", c.name, got, want)
			}
		}
		for _, no := range c.wantNot {
			if strings.Contains(got, no) {
				t.Fatalf("%s: classification %q must not contain %q", c.name, got, no)
			}
		}
	}
}

// TestProbeWindowsMainThreadWithoutWindow pins the inconclusive shape: the
// plain test binary owns no wailsWindow, so the live probe must answer
// "inconclusive — window absent" instead of claiming a hang.
func TestProbeWindowsMainThreadWithoutWindow(t *testing.T) {
	r := probeWindowsMainThread()
	if r.Status != mainThreadProbeInconclusive {
		t.Fatalf("status = %s, want inconclusive (detail: %s)", r.Status, r.Detail)
	}
	if r.Detail == "" {
		t.Fatal("inconclusive probe must carry a reason")
	}
	if r.Duration > time.Second {
		t.Fatalf("window-absent probe took %s, want near-zero", r.Duration)
	}
}

// TestWindowsProbeIsWired pins the init wiring: on Windows the watchdog must
// have a live probe, otherwise every stale heartbeat would report blindly.
func TestWindowsProbeIsWired(t *testing.T) {
	if probeMainThread == nil {
		t.Fatal("probeMainThread must be wired on windows")
	}
}
