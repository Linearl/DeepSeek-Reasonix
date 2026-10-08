package main

// 任务 404（LocalDumps 验证面）：`reasonix-launcher localdumps` is the
// executable verification path for the crash-dump observation face. It exists
// because the 2026-10-08 machine-level verification proved the task-188 HKCU
// registration never collects dumps (four probe crashes — including one
// spawned by Task Scheduler outside any agent process tree — left zero dumps
// and zero WER events); the documented scope is HKLM, and writing HKLM is a
// system-state change that belongs to an explicit user action, not a silent
// desktop startup.
//
// Subcommands:
//
//	localdumps status           read-only: what is registered, in both scopes,
//	                            plus recent dumps in the dump folder.
//	localdumps ensure --machine write the HKLM registration for the desktop
//	                            (elevated; non-elevated runs print the exact
//	                            elevated one-liner instead).
//	localdumps verify           full acceptance: register a throwaway probe
//	                            key, crash a copy of this launcher under the
//	                            probe image name, and require a .dmp on disk.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/localdumps"
)

const (
	verifyDumpWaitTimeout = 90 * time.Second
	verifyDumpPollStep    = 2 * time.Second
)

func runLocaldumps(args []string) int {
	if len(args) == 0 {
		localdumpsUsage()
		return 2
	}
	switch args[0] {
	case "status":
		return localdumpsStatus()
	case "ensure":
		return localdumpsEnsure(args[1:])
	case "verify":
		return localdumpsVerify()
	case "help", "-h", "--help":
		localdumpsUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "localdumps: unknown subcommand %q\n", args[0])
		localdumpsUsage()
		return 2
	}
}

func localdumpsUsage() {
	fmt.Println("usage: reasonix-launcher localdumps <status|ensure|verify>")
	fmt.Println("  status         show WER LocalDumps registration (both scopes) and recent dumps")
	fmt.Println("  ensure         register reasonix-desktop.exe under HKLM (elevated; --machine is implicit)")
	fmt.Println("  verify         prove the crash-dump chain with a throwaway probe process")
}

func localdumpsStatus() int {
	folder, folderErr := localdumps.DefaultDumpFolder()
	fmt.Printf("WER LocalDumps status for %s\n", localdumps.DesktopImageName)
	printScope := func(label string, cfg localdumps.Config, err error) {
		switch {
		case err == nil:
			fmt.Printf("  %-15s registered: DumpFolder=%s DumpType=%d DumpCount=%d\n",
				label, cfg.DumpFolder, cfg.DumpType, cfg.DumpCount)
		case errors.Is(err, localdumps.ErrUnsupported):
			fmt.Printf("  %-15s unsupported on this platform\n", label)
		default:
			fmt.Printf("  %-15s not registered (%v)\n", label, err)
		}
	}
	cfg, err := localdumps.ReadMachine(localdumps.DesktopImageName)
	printScope("machine (HKLM):", cfg, err)
	cfg, err = localdumps.ReadUser(localdumps.DesktopImageName)
	printScope("user (HKCU):", cfg, err)
	fmt.Println("  note: HKCU registration is best-effort only; the 2026-10-08 task-404 verification")
	fmt.Println("  proved WER dump collection follows the machine (HKLM) scope.")

	if folderErr != nil {
		fmt.Printf("  dump folder: unresolved (%v)\n", folderErr)
		return 0
	}
	fmt.Printf("  dump folder: %s\n", folder)
	for _, entry := range recentDumps(folder, 5) {
		fmt.Printf("    %s (%d bytes, %s)\n", entry.name, entry.size, entry.mtime.Format(time.RFC3339))
	}
	return 0
}

func localdumpsEnsure(args []string) int {
	for _, arg := range args {
		switch arg {
		case "--machine":
			// The only scope Ensure writes; kept as a flag so the command
			// line states the system change it is about to make.
		default:
			fmt.Fprintf(os.Stderr, "localdumps ensure: unknown argument %q\n", arg)
			return 2
		}
	}
	folder, err := localdumps.DefaultDumpFolder()
	if err != nil {
		fmt.Fprintln(os.Stderr, "localdumps ensure:", err)
		return 1
	}
	if err := localdumps.EnsureMachine(localdumps.DesktopImageName, localdumps.DesktopConfig(folder)); err != nil {
		if localdumps.IsAccessDenied(err) {
			script := writeElevatedEnsureScript(localdumps.DesktopImageName)
			fmt.Println("not elevated: writing HKLM needs one administrator step.")
			fmt.Println("a ready-made script has been written to:")
			fmt.Printf("  %s\n", script)
			fmt.Println("run it elevated (accept the UAC prompt):")
			fmt.Printf("  Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','%s'\n", script)
			return 2
		}
		fmt.Fprintln(os.Stderr, "localdumps ensure:", err)
		return 1
	}
	fmt.Printf("registered %s under HKLM (dumps → %s)\n", localdumps.DesktopImageName, folder)
	return 0
}

func localdumpsVerify() int {
	folder, err := localdumps.DefaultDumpFolder()
	if err != nil {
		fmt.Fprintln(os.Stderr, "localdumps verify:", err)
		return 1
	}
	// The probe never collides with a shipped executable: its key is written
	// under the throwaway probe image name and removed at the end.
	fmt.Printf("registering probe key for %s under HKLM...\n", localdumps.VerifyImageName)
	if err := localdumps.EnsureMachine(localdumps.VerifyImageName, localdumps.DesktopConfig(folder)); err != nil {
		if localdumps.IsAccessDenied(err) {
			script := writeElevatedEnsureScript(localdumps.VerifyImageName)
			fmt.Println("not elevated: the probe key needs the same one administrator step.")
			fmt.Println("run this elevated script, then re-run `localdumps verify`:")
			fmt.Printf("  Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','%s'\n", script)
			return 2
		}
		fmt.Fprintln(os.Stderr, "localdumps verify:", err)
		return 1
	}
	cleanupProbe := func() {
		if delErr := localdumps.DeleteMachine(localdumps.VerifyImageName); localdumps.IsAccessDenied(delErr) {
			fmt.Println("cleanup note: removing the probe key needs elevation:")
			fmt.Printf("  Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile','-Command','Remove-Item -Path ''HKLM:\\SOFTWARE\\Microsoft\\Windows\\Windows Error Reporting\\LocalDumps\\%s'''\n", localdumps.VerifyImageName)
		}
	}

	probeExe, err := probeExecutableCopy()
	if err != nil {
		fmt.Fprintln(os.Stderr, "localdumps verify:", err)
		cleanupProbe()
		return 1
	}
	defer os.Remove(probeExe)

	fmt.Printf("crashing probe process %s (native access violation)...\n", filepath.Base(probeExe))
	if err := runCrashProbe(probeExe); err != nil {
		fmt.Fprintf(os.Stderr, "localdumps verify: probe run failed: %v\n", err)
		cleanupProbe()
		return 1
	}
	// A crashed child is an expected "error" from Wait; reaching here means it
	// ran and died. WER dump collection lands asynchronously — poll for it.
	fmt.Println("waiting for the dump to land (WER collection is asynchronous)...")
	dumpPath := waitForDump(folder, localdumps.VerifyImageName, verifyDumpWaitTimeout)
	cleanupProbe()
	if dumpPath == "" {
		fmt.Printf("VERIFY FAIL: the probe died but no dump appeared in %s within %s.\n", folder, verifyDumpWaitTimeout)
		fmt.Println("WER dump collection is not working for machine-level keys on this machine —")
		fmt.Println("check that the WerSvc service can start and that no policy disables WER.")
		return 1
	}
	info, err := os.Stat(dumpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "localdumps verify: stat dump: %v\n", err)
		return 1
	}
	fmt.Printf("VERIFY PASS: dump landed at %s (%d bytes).\n", dumpPath, info.Size())
	fmt.Printf("the same machine-level mechanism now covers %s on every native crash.\n", localdumps.DesktopImageName)
	fmt.Println("the dump file is kept as evidence; the probe key and its binary copy were removed.")
	return 0
}

// writeElevatedEnsureScript drops a ready-to-run elevated PowerShell script
// for one image name. It returns the script path; the caller prints the exact
// Start-Process line. Keeping the registry writes in a script file sidesteps
// nested-quote bugs in printed one-liners.
func writeElevatedEnsureScript(imageName string) string {
	folder, err := localdumps.DefaultDumpFolder()
	if err != nil {
		folder = `%LOCALAPPDATA%\CrashDumps`
	}
	keyPath := `HKLM:\` + localdumps.KeyBase + `\` + imageName
	body := fmt.Sprintf(`# reasonix-launcher localdumps: register WER LocalDumps for %s
# (task 404; reversible: delete the key below to undo)
New-Item -Force -Path '%s' | Out-Null
Set-ItemProperty -Path '%s' -Name DumpFolder -Value '%s' -Type ExpandString
Set-ItemProperty -Path '%s' -Name DumpType -Value 2 -Type DWord
Set-ItemProperty -Path '%s' -Name DumpCount -Value 10 -Type DWord
Write-Host 'LocalDumps registered for %s'
`, imageName, keyPath, keyPath, folder, keyPath, keyPath, imageName)
	path := filepath.Join(os.TempDir(), "reasonix-localdumps-ensure-"+imageName+".ps1")
	if writeErr := os.WriteFile(path, []byte(body), 0o600); writeErr != nil {
		fmt.Fprintf(os.Stderr, "localdumps: write elevated script: %v\n", writeErr)
		return path
	}
	return path
}

// probeExecutableCopy copies this launcher binary under the probe image name;
// WER keys LocalDumps by image name, so the copy crashes as its own "app"
// without touching any shipped executable.
func probeExecutableCopy() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve own executable: %w", err)
	}
	probePath := filepath.Join(os.TempDir(), localdumps.VerifyImageName)
	body, err := os.ReadFile(self)
	if err != nil {
		return "", fmt.Errorf("read launcher binary: %w", err)
	}
	if err := os.WriteFile(probePath, body, 0o755); err != nil {
		return "", fmt.Errorf("write probe copy: %w", err)
	}
	return probePath, nil
}

type dumpEntry struct {
	name  string
	size  int64
	mtime time.Time
}

func recentDumps(folder string, limit int) []dumpEntry {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil
	}
	var dumps []dumpEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".dmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dumps = append(dumps, dumpEntry{name: entry.Name(), size: info.Size(), mtime: info.ModTime()})
	}
	// newest last, so the printed list reads newest-first after slicing
	for i := 1; i < len(dumps); i++ {
		for j := i; j > 0 && dumps[j].mtime.After(dumps[j-1].mtime); j-- {
			dumps[j], dumps[j-1] = dumps[j-1], dumps[j]
		}
	}
	if len(dumps) > limit {
		dumps = dumps[:limit]
	}
	return dumps
}

// waitForDump polls the dump folder for a fresh dump of the given image.
func waitForDump(folder, imageName string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	prefix := imageName + "."
	for time.Now().Before(deadline) {
		if entries := recentDumps(folder, 10); len(entries) > 0 {
			for _, entry := range entries {
				if strings.HasPrefix(entry.name, prefix) && time.Since(entry.mtime) < timeout {
					return filepath.Join(folder, entry.name)
				}
			}
		}
		time.Sleep(verifyDumpPollStep)
	}
	return ""
}
