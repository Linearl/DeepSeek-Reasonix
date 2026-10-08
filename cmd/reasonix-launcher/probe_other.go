//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
)

// probeCrashEnv mirrors the Windows probe mode; off Windows it never fires.
const probeCrashEnv = "REASONIX_LAUNCHER_PROBE_CRASH"

func crashProbeRequested() bool {
	return false
}

func crashProbeNow() {
	fmt.Fprintln(os.Stderr, "localdumps verify is a windows-only verification")
	os.Exit(3)
}

func runCrashProbe(probeExe string) error {
	_ = probeExe
	return errors.New("localdumps verify is windows-only")
}
