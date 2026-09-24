//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const werLocalDumpsKey = `SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps\reasonix-desktop.exe`

// ensureWERLocalDumps registers reasonix-desktop.exe with the Windows Error
// Reporting LocalDumps facility (task 188, direction 1): a minidump then lands
// under %LOCALAPPDATA%\CrashDumps for any process-level crash — including the
// non-Go deaths Go's own debug.SetCrashOutput cannot see (native faults, OOM
// kills the kernel delivers to the process, WebView2-mediated exits).
//
// HKCU keeps it user-level (no elevation); the call is idempotent and
// best-effort — a failed registration logs one line and startup continues,
// because diagnostics must never break the app. Returns the dump folder for
// tests and the startup log line.
func ensureWERLocalDumps() (string, error) {
	dumpFolder := ""
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		dumpFolder = filepath.Join(localAppData, "CrashDumps")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("wer localdumps: no LOCALAPPDATA and no home: %w", err)
		}
		dumpFolder = filepath.Join(home, "AppData", "Local", "CrashDumps")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, werLocalDumpsKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("wer localdumps: create key: %w", err)
	}
	defer key.Close()
	// DumpType 2 = mini dump (task 188: enough for stacks/symbols, small enough
	// that a crash loop cannot fill the disk); DumpCount caps the rolling set.
	if err := key.SetStringValue("DumpFolder", dumpFolder); err != nil {
		return "", fmt.Errorf("wer localdumps: DumpFolder: %w", err)
	}
	if err := key.SetDWordValue("DumpType", 2); err != nil {
		return "", fmt.Errorf("wer localdumps: DumpType: %w", err)
	}
	if err := key.SetDWordValue("DumpCount", 10); err != nil {
		return "", fmt.Errorf("wer localdumps: DumpCount: %w", err)
	}
	return dumpFolder, nil
}

// installWERLocalDumps is the startup wiring: log-and-continue, never fatal.
func installWERLocalDumps() {
	folder, err := ensureWERLocalDumps()
	if err != nil {
		log.Printf("[crash] WER LocalDumps registration failed: %v", err)
		return
	}
	log.Printf("[crash] WER LocalDumps registered for reasonix-desktop.exe (dumps → %s)", folder)
}

// readWERLocalDumpsFolder reads back the registered DumpFolder (task 188 tests).
func readWERLocalDumpsFolder() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, werLocalDumpsKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer key.Close()
	folder, _, err := key.GetStringValue("DumpFolder")
	if err != nil {
		return "", err
	}
	return folder, nil
}
