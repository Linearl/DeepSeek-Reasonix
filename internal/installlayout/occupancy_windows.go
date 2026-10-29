//go:build windows

package installlayout

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Occupancy probing on Windows (task 1259).
//
// Empirical baseline (Windows 10.0.26200, 2026-10-30): the file of a RUNNING
// image opens successfully even exclusively and even with DELETE access — the
// loader no longer holds an incompatible share — while actually deleting it
// (os.Remove) is still denied at the kernel level. Two consequences drove the
// design:
//
//   - File-open probes cannot detect a running image on modern Windows. The
//     only reliable occupancy signal is process enumeration with image-path
//     matching, so that is the primary (and decisive) face.
//   - Deletion is what the OS refuses, so PruneVersionTrees additionally
//     treats a failed RemoveAll as "occupied" (race or opaque protection) and
//     keeps the tree instead of aborting the pass.
//
// For processes whose image path resists the ordinary read (the elevated
// ghost instances of the 2026-10-29 incident), two fallbacks apply, in
// order: NtQuerySystemInformation(SystemProcessIdInformation) resolves image
// paths by pid without opening any handle, so it reads through protection
// that stops OpenProcess; and processes still unattributed (unreadable path)
// whose base image name matches the desktop binary become suspects that keep
// every plausibly-hosting tree occupied until the user resolves them.

// systemProcessIdInformation mirrors NT SYSTEM_PROCESS_ID_INFORMATION, used
// with NtQuerySystemInformation to resolve an image path by pid without a
// process handle.
type systemProcessIdInformation struct {
	ProcessId uintptr
	ImageName windows.NTUnicodeString
}

const processImageNameMaxBytes = 32 * 1024

// processImageName resolves the full image path of one PID. Tier 1 opens the
// process with the least-privileged query right; tier 2 asks the kernel by
// pid with no handle at all (works across integrity levels where OpenProcess
// itself is refused). Returns "" when both tiers fail.
func processImageName(pid uint32) string {
	if path := processImageNameByHandle(pid); path != "" {
		return path
	}
	return processImageNameByKernel(pid)
}

func processImageNameByHandle(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, windows.MAX_PATH)
	for {
		size := uint32(len(buf))
		if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err == nil {
			return windows.UTF16ToString(buf[:size])
		}
		if size <= uint32(len(buf)) {
			return "" // real failure (access, race): fall through to tier 2
		}
		buf = make([]uint16, size) // buffer too small: retry at the required size
	}
}

func processImageNameByKernel(pid uint32) string {
	name := make([]uint16, processImageNameMaxBytes/2)
	info := systemProcessIdInformation{
		ProcessId: uintptr(pid),
		ImageName: windows.NTUnicodeString{
			MaximumLength: processImageNameMaxBytes,
			Buffer:        &name[0],
		},
	}
	if err := windows.NtQuerySystemInformation(windows.SystemProcessIdInformation,
		unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return ""
	}
	return windows.UTF16ToString(name[:info.ImageName.Length/2])
}

// probeVersionTreeProcesses enumerates every process and attributes it to a
// version tree by its resolved image path. The calling process is never
// reported.
func probeVersionTreeProcesses(versionsDir, version string) []OccupiedProcess {
	attributed, _ := probeProcessAttribution(versionsDir)
	if version == "" {
		return attributed
	}
	out := make([]OccupiedProcess, 0, len(attributed))
	for _, p := range attributed {
		if strings.EqualFold(p.Version, version) {
			out = append(out, p)
		}
	}
	return out
}

// probeProcessAttribution walks the process table once. It returns the
// processes attributable to a version tree by path, plus the suspects:
// processes whose image path stayed unreadable after both resolver tiers but
// whose base image name equals the desktop binary — they run this very
// program from an unknown location, so any tree that actually contains a
// desktop binary is plausibly theirs.
func probeProcessAttribution(versionsDir string) (attributed []OccupiedProcess, suspectPids []int) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, nil
	}
	defer windows.CloseHandle(snapshot)

	self := os.Getpid()
	baseName := strings.ToLower(DesktopBinaryName())
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		pid := int(entry.ProcessID)
		if pid <= 0 || pid == self {
			continue
		}
		exePath := processImageName(uint32(pid))
		if exePath == "" {
			// Unattributable after both resolver tiers. A same-named image is
			// exactly the ghost shape we must not delete under; anything else
			// is irrelevant.
			if strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])) == baseName {
				suspectPids = append(suspectPids, pid)
			}
			continue
		}
		tree, ok := versionUnderTree(exePath, versionsDir)
		if !ok {
			continue
		}
		attributed = append(attributed, OccupiedProcess{PID: pid, Version: tree, ExePath: exePath})
	}
	return attributed, suspectPids
}

// versionUnderTree reports whether exePath lives directly inside
// <versionsDir>/<version>/ at any depth, returning that version name. The
// comparison is case-insensitive (Win32 paths) and separator-bounded so
// versions/v1/sibling cannot prefix-match versions/v1.
func versionUnderTree(exePath, versionsDir string) (string, bool) {
	if exePath == "" || versionsDir == "" {
		return "", false
	}
	root := strings.ToLower(filepath.Clean(versionsDir))
	p := strings.ToLower(filepath.Clean(exePath))
	sep := string(filepath.Separator)
	if !strings.HasPrefix(p, root+sep) {
		return "", false
	}
	rest := p[len(root)+1:]
	tree := rest
	if i := strings.Index(rest, sep); i >= 0 {
		tree = rest[:i]
	}
	if tree == "" || ValidateVersionName(tree) != nil {
		return "", false
	}
	return tree, true
}
