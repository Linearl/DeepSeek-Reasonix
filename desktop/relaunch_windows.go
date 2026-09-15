package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// relaunchCreationFlags detaches the relaunch from this process's fate:
// CREATE_BREAKAWAY_FROM_JOB keeps the launcher alive when an installer-placed
// job object tears down the exiting desktop's process tree, and the
// process-group / console flags stop the child from inheriting ours.
const relaunchCreationFlags = windows.CREATE_BREAKAWAY_FROM_JOB |
	windows.CREATE_NEW_PROCESS_GROUP |
	windows.DETACHED_PROCESS

// configureDetachedRelaunch applies the breakaway flags. Jobs that forbid
// breakaway make CreateProcess fail outright, so the caller must retry with
// detachedRelaunchFallbackFlags rather than losing the relaunch.
const detachedRelaunchFallbackFlags = windows.CREATE_NEW_PROCESS_GROUP |
	windows.DETACHED_PROCESS

func configureDetachedRelaunch(cmd *exec.Cmd, creationFlags uint32) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: creationFlags,
		HideWindow:    true,
	}
}
