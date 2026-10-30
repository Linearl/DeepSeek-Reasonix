//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

// reasonixProcesses are the binaries that, if alive, may be writing session
// logs while we rewrite them: the desktop shell and the headless CLI/serve.
var reasonixProcesses = []string{"reasonix-desktop.exe", "reasonix.exe"}

// reasonixRunning reports the first live Reasonix image name. It shells out to
// tasklist because Go cannot enumerate foreign processes without extra deps,
// and it fails soft: an unavailable tasklist just means "cannot tell", which
// the caller treats as not running (the per-session lease check still applies).
func reasonixRunning() (string, bool) {
	for _, name := range reasonixProcesses {
		cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq "+name, "/NH", "/FO", "CSV")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(out)), strings.ToLower(name)) {
			return name, true
		}
	}
	return "", false
}
