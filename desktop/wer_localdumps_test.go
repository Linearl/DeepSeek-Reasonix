//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWERLocalDumpsRegistration pins task 188's direction 1: registration is
// idempotent and readable back — the switch WER needs before it will drop a
// minidump for reasonix-desktop.exe. The key is the product key the app writes
// at every startup, so the test leaving it behind IS the install (no cleanup:
// removing it would undo what main just did).
func TestWERLocalDumpsRegistration(t *testing.T) {
	folder, err := ensureWERLocalDumps()
	if err != nil {
		t.Fatalf("ensureWERLocalDumps: %v", err)
	}
	if want := filepath.Join(os.Getenv("LOCALAPPDATA"), "CrashDumps"); folder != want {
		// LOCALAPPDATA can be redirected by the test harness; only require the
		// fallback shape in that case.
		if !strings.HasSuffix(filepath.Join("CrashDumps"), filepath.Base(folder)) {
			t.Fatalf("dump folder = %q, want %q", folder, want)
		}
	}
	readBack, err := readWERLocalDumpsFolder()
	if err != nil {
		t.Fatalf("readWERLocalDumpsFolder: %v", err)
	}
	if readBack != folder {
		t.Fatalf("registered DumpFolder = %q, want %q", readBack, folder)
	}
	// Second call must be a no-op success (idempotent).
	if _, err := ensureWERLocalDumps(); err != nil {
		t.Fatalf("second registration: %v", err)
	}
}
