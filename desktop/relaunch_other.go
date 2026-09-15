//go:build !windows

package main

import "os/exec"

// relaunchCreationFlags is unused off Windows; see relaunch_windows.go.
const relaunchCreationFlags = 0

// detachedRelaunchFallbackFlags is unused off Windows.
const detachedRelaunchFallbackFlags = 0

func configureDetachedRelaunch(cmd *exec.Cmd, creationFlags uint32) {
	_ = cmd
	_ = creationFlags
}
