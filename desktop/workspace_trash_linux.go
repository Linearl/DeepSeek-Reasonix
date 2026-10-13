//go:build !darwin && !windows

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// trashPlatformPaths moves entries to the freedesktop trash via `gio trash`
// (GVFS). When gio is absent the operation fails closed — no rm fallback,
// the recycle-bin semantic must not degrade into an unrecoverable delete.
func trashPlatformPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	gio, err := exec.LookPath("gio")
	if err != nil {
		return fmt.Errorf("gio is required to move entries to the trash: %w", err)
	}
	var failures []string
	for _, p := range paths {
		if out, cmdErr := exec.Command(gio, "trash", "--", p).CombinedOutput(); cmdErr != nil {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = cmdErr.Error()
			}
			failures = append(failures, fmt.Sprintf("%s: %s", p, detail))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
