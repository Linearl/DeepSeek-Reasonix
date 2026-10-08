//go:build windows

package localdumps

import "golang.org/x/sys/windows/registry"

// RootUser is the hive behind the task-188 best-effort face. Exported so the
// value-logic tests exercise the exact Ensure/Read/Delete path without admin
// rights.
var RootUser = registry.CURRENT_USER

// EnsureMachine writes the registration under HKLM — the documented
// LocalDumps location and the only scope the 2026-10-08 machine-level
// verification (task 404) could prove collects dumps. Needs elevation; a
// non-elevated caller gets an access-denied error (IsAccessDenied).
func EnsureMachine(imageName string, cfg Config) error {
	return Ensure(registry.LOCAL_MACHINE, imageName, cfg)
}

// ReadMachine reads the registration back from HKLM.
func ReadMachine(imageName string) (Config, error) {
	return Read(registry.LOCAL_MACHINE, imageName)
}

// ReadUser reads the task-188 best-effort registration from HKCU.
func ReadUser(imageName string) (Config, error) {
	return Read(registry.CURRENT_USER, imageName)
}

// DeleteMachine removes a machine-level per-application key (elevated).
func DeleteMachine(imageName string) error {
	return Delete(registry.LOCAL_MACHINE, imageName)
}
