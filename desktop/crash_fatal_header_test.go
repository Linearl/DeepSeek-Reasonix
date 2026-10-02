package main

import (
	"os"
	"strings"
	"testing"
)

// Task 377: the fatal-crash sink carries a provenance header, so a header-only
// file (process died without runtime crash output) is the same no-evidence case
// a 0-byte file used to be — removed without a report, never read as a crash.
func TestCaptureFatalCrashHeaderOnlyFileRemovedWithoutReport(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := fatalCrashDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pid := 987654
	path := fatalCrashPathForPID(pid)
	header := fatalCrashLogHeaderPrefix + " pid=987654 started=2026-10-02T00:00:00Z\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	origAlive := fatalCrashProcessAlive
	fatalCrashProcessAlive = func(int) bool { return false }
	t.Cleanup(func() { fatalCrashProcessAlive = origAlive })

	pendingBefore := pendingReportCount(t)
	capturePreviousFatalCrash()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("header-only fatal log not removed")
	}
	if got := pendingReportCount(t); got != pendingBefore {
		t.Fatalf("header-only file produced pending reports: %d -> %d", pendingBefore, got)
	}
}

func TestCaptureFatalCrashHeaderPlusDumpReports(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := fatalCrashDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pid := 987655
	path := fatalCrashPathForPID(pid)
	body := fatalCrashLogHeaderPrefix + " pid=987655 started=2026-10-02T00:00:00Z\n" +
		"fatal error: unexpected signal\ngoroutine 1 [running]:\nmain.main()\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	origAlive := fatalCrashProcessAlive
	fatalCrashProcessAlive = func(int) bool { return false }
	t.Cleanup(func() { fatalCrashProcessAlive = origAlive })

	pendingBefore := pendingReportCount(t)
	capturePreviousFatalCrash()
	if got := pendingReportCount(t); got != pendingBefore+1 {
		t.Fatalf("real dump produced %d new reports, want 1", got-pendingBefore)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("consumed fatal log not removed")
	}
}

func TestStripFatalCrashHeader(t *testing.T) {
	header := fatalCrashLogHeaderPrefix + " pid=1 started=x\n"
	if got := stripFatalCrashHeader(header + "stack"); got != "stack" {
		t.Fatalf("strip = %q", got)
	}
	if got := stripFatalCrashHeader(header); got != "" {
		t.Fatalf("header-only strip = %q", got)
	}
	if got := stripFatalCrashHeader("panic: raw legacy content\n"); !strings.HasPrefix(got, "panic:") {
		t.Fatalf("legacy content altered: %q", got)
	}
}

// pendingReportCount counts the structured crash-pending reports on disk.
func pendingReportCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(pendingCrashDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			n++
		}
	}
	return n
}
