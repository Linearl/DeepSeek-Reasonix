//go:build !windows

package installlayout

// POSIX systems unlink open executables legally, so deleting a version tree
// under a running process leaves no ghost: the process keeps its mapped image
// and the files are gone by design, the same way a package manager replaces
// them. The ghost mechanism task 1259 fixes is Windows-only, so the probes
// deliberately report nothing there and every consumer keeps its pre-1259
// behavior on other platforms.
//
// A future port can implement the attribution probe (process enumeration via
// /proc/<pid>/exe on Linux); the shared surfaces in occupancy.go already
// consume it shape-first.

func probeProcessAttribution(versionsDir string) (attributed []OccupiedProcess, suspectPids []int) {
	return nil, nil
}

// probeVersionTreeProcesses is the per-tree view over the attribution probe.
func probeVersionTreeProcesses(versionsDir, version string) []OccupiedProcess {
	return nil
}
