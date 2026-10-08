//go:build windows

package main

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// probeCrashEnv asks the process to die with a deliberate native crash
// (task 404 `localdumps verify`). Only the launcher's own probe copy — a
// renamed copy of this binary run by `localdumps verify` — ever sets it; in
// production the variable is unset and the mode is unreachable.
const probeCrashEnv = "REASONIX_LAUNCHER_PROBE_CRASH"

func crashProbeRequested() bool {
	return os.Getenv(probeCrashEnv) == "1"
}

// crashProbeNow dies with a native access violation raised OUTSIDE any Go
// runtime SEH frame: a fresh thread whose start address is not executable
// faults before Go's handler can convert the fault into a panic, so the
// unhandled exception goes down the Windows Error Reporting path — exactly
// the channel the LocalDumps registration must prove. A plain Go panic is the
// wrong probe shape here: the Go runtime exits it cleanly with code 2 and WER
// never engages (verified 2026-10-08, task 404).
func crashProbeNow() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	handle, _, _ := kernel32.NewProc("CreateThread").Call(0, 0, 1, 0, 0, 0)
	if handle != 0 {
		kernel32.NewProc("WaitForSingleObject").Call(handle, 0xFFFFFFFF)
	}
	// Unreached when the thread faults as expected; a definite exit keeps the
	// degenerate case from turning the probe into a stray process.
	os.Exit(3)
}

// runCrashProbe runs the probe copy in probe mode and waits for its death —
// a non-zero exit IS the expected outcome, so Wait's error is discarded.
func runCrashProbe(probeExe string) error {
	cmd := exec.Command(probeExe)
	cmd.Env = append(os.Environ(), probeCrashEnv+"=1")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Wait()
	return nil
}
