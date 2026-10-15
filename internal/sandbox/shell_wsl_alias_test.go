package sandbox

import (
	"strings"
	"testing"
)

// TestIsWindowsWSLBashPath pins the WSL launcher/alias exclusion from upstream
// issue 9597 / PR 10617: the %SystemRoot% launcher and the 0-byte App
// Execution Alias under %LOCALAPPDATA%\Microsoft\WindowsApps both run commands
// inside the WSL distro (the alias even passes `bash -c true` when WSL is
// installed), so neither may be picked as a native Windows workspace shell.
// The pure helper compares Windows paths on any host.
func TestIsWindowsWSLBashPath(t *testing.T) {
	const win = `C:\Windows`
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"system32 launcher", `C:\Windows\System32\bash.exe`, true},
		{"case-insensitive system root", `c:\windows\system32\BASH.EXE`, true},
		{"forward slashes", `C:/Windows/System32/bash.exe`, true},
		{"extended-length prefix", `\\?\C:\Windows\System32\bash.exe`, true},
		{"windowsapps alias", `C:\Users\ACER\AppData\Local\Microsoft\WindowsApps\bash.exe`, true},
		{"windowsapps alias forward slashes", `C:/Users/ACER/AppData/Local/Microsoft/WindowsApps/bash.exe`, true},
		{"windowsapps alias lowercase", `c:\users\acer\appdata\local\microsoft\windowsapps\bash.exe`, true},
		{"non-standard git bash", `D:\app\Git\Git\bin\bash.exe`, false},
		{"standard git bash", `C:\Program Files\Git\bin\bash.exe`, false},
		{"usr bin git bash", `D:\app\Git\Git\usr\bin\bash.exe`, false},
		{"windowsapps non-bash exe", `C:\Users\ACER\AppData\Local\Microsoft\WindowsApps\wt.exe`, false},
		{"windows-rooted git bash stays", `C:\WindowsTools\Git\bin\bash.exe`, false},
		{"empty path", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWindowsWSLBashPath(tc.path, win); got != tc.want {
				t.Fatalf("isWindowsWSLBashPath(%q, %q) = %v, want %v", tc.path, win, got, tc.want)
			}
		})
	}
	// An unset Windows root must not disable the alias check: the alias lives
	// under the user profile, not under %SystemRoot%.
	if !isWindowsWSLBashPath(`C:\Users\ACER\AppData\Local\Microsoft\WindowsApps\bash.exe`, "") {
		t.Fatal("WindowsApps alias must be excluded even without a Windows root")
	}
}

// TestWindowsAppsAliasExcludedFromNonStandardDiscovery reproduces the issue
// 9597 machine shape: PATH lists the WindowsApps WSL alias first, Git for
// Windows sits in a non-standard directory that is not on PATH, and the alias
// passes the health probe because WSL is installed. The alias must lose (both
// at PATH resolution and in the candidate list), and the non-standard Git Bash
// must be discovered instead. When nothing usable remains the resolution falls
// back with a warning — never silently.
func TestWindowsAppsAliasExcludedFromNonStandardDiscovery(t *testing.T) {
	const (
		alias     = `C:\Users\ACER\AppData\Local\Microsoft\WindowsApps\bash.exe`
		nonStd    = `D:\app\Git\Git\bin\bash.exe`
		pwshPath  = `C:\Program Files\PowerShell\7\pwsh.exe`
		winRoot   = `C:\Windows`
		realProbe = true // the alias launches a working WSL distro
	)
	isWSL := func(p string) bool { return isWindowsWSLBashPath(p, winRoot) }
	lookPath := fakeLookPath(map[string]string{
		"bash": alias,
		"pwsh": pwshPath,
	})
	exists := func(p string) bool { return strings.EqualFold(p, nonStd) }
	cands := []string{nonStd}

	// The alias is excluded even though it probes successfully: discovery falls
	// through to the non-standard Git Bash candidate instead of routing the
	// workspace into the Linux VM.
	got := resolveShell("bash", "", nil, "windows", lookPath, exists, cands, nil,
		func(string) bool { return realProbe }, isWSL)
	if got.Kind != ShellBash || !strings.EqualFold(got.Path, nonStd) {
		t.Fatalf("alias-preferred PATH must fall through to non-standard Git Bash, got {%s %q}", got.Kind, got.Path)
	}

	// The candidate builder applies the same exclusion, so an alias-shaped
	// entry never reaches consumers of the inventory.
	filtered, _ := windowsBashCandidateSources("auto", "", lookPath, func(p string) bool {
		return strings.EqualFold(p, alias) || strings.EqualFold(p, nonStd)
	})
	for _, p := range filtered {
		if isWSL(p) {
			t.Fatalf("candidate list kept a WSL alias: %v", filtered)
		}
	}

	// Nothing usable anywhere: the fallback is loud, not silent (warn names
	// the missing preference) and lands on PowerShell, the auto fallback.
	var warn strings.Builder
	pwshOnly := fakeLookPath(map[string]string{"pwsh": pwshPath})
	got = resolveShell("bash", "", &warn, "windows", pwshOnly, func(string) bool { return false },
		nil, []string{pwshPath}, func(string) bool { return false }, isWSL)
	if got.Kind != ShellPowerShell {
		t.Fatalf("fully missing bash should fall back to PowerShell, got {%s %q}", got.Kind, got.Path)
	}
	if !strings.Contains(warn.String(), "not found") || !strings.Contains(warn.String(), "bash") {
		t.Fatalf("missing bash must warn by name, got %q", warn.String())
	}
}
