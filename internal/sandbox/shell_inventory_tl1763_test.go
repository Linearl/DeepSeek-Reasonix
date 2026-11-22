package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tl1763 regression coverage: Git for Windows bash discovery and the pwsh
// Store-edition alias. Forensic background (2026-11-22, affected machine):
// all three bash routes produced correct candidate paths, but probeBash's
// 3s budget lost to msys cold-start (2.4-3.7s measured across both cmd- and
// Git-Bash-carrier hosts), so the capability report said not-installed; the
// pwsh alias under %LOCALAPPDATA%\Microsoft\WindowsApps was invisible to
// LookPath (directory absent from the process PATH) though os.Stat sees the
// AppExecLink reparse point and CreateProcess executes it.

// TestTL1763GitDerivedCapabilityTwoStates pins the git-derived route end to
// end: with the probe winning, the capability is available and attributes the
// git-derived source; with the probe losing (what the 3s budget did under
// load), the candidate survives but the report degrades to not-installed.
func TestTL1763GitDerivedCapabilityTwoStates(t *testing.T) {
	const (
		binBash = `C:\Program Files\Git\bin\bash.exe`
		usrBash = `C:\Program Files\Git\usr\bin\bash.exe`
	)
	newSnap := func(probeOK bool) *shellSnapshot {
		return &shellSnapshot{
			lookPath: fakeLookPath(map[string]string{}), // no bash on PATH
			exists: func(p string) bool {
				return strings.EqualFold(p, binBash) || strings.EqualFold(p, usrBash)
			},
			isWSL:      func(string) bool { return false },
			probeFunc:  func(string) bool { return probeOK },
			probeCache: map[string]bool{},
		}
	}
	snap := newSnap(true)
	snap.bashCands, snap.sources = windowsBashCandidateSources("auto", "", []string{}, fakeLookPath(map[string]string{
		"git.exe": `C:\Program Files\Git\cmd\git.exe`,
	}), snap.exists)
	snap.goos = "windows"

	caps := windowsShellCapabilities(snap)
	if len(caps) == 0 {
		t.Fatal("no capabilities reported")
	}
	gitBash := caps[0]
	if !gitBash.Available || !strings.EqualFold(gitBash.Path, binBash) || gitBash.Source != ShellSourceGitDerived {
		t.Fatalf("git-bash capability with a passing probe = %+v, want available at %s via %s", gitBash, binBash, ShellSourceGitDerived)
	}

	snap = newSnap(false)
	snap.bashCands, snap.sources = windowsBashCandidateSources("auto", "", []string{}, fakeLookPath(map[string]string{
		"git.exe": `C:\Program Files\Git\cmd\git.exe`,
	}), snap.exists)
	snap.goos = "windows"
	caps = windowsShellCapabilities(snap)
	gitBash = caps[0]
	if gitBash.Available || gitBash.Reason != "not-installed" {
		t.Fatalf("git-bash capability with a failing probe = %+v, want unavailable with reason not-installed", gitBash)
	}
}

// TestTL1763RegistryRouteCandidates injects fake registry InstallPath roots
// against a real temporary Git tree, so the registry route (step 4 of the
// discovery order) is covered without touching the machine registry.
func TestTL1763RegistryRouteCandidates(t *testing.T) {
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "Git"),
		filepath.Join("bin", "bash.exe"),
		filepath.Join("usr", "bin", "bash.exe"),
	)

	got, sources := windowsBashCandidateSources("auto", "", []string{filepath.Join(root, "Git")}, fakeLookPath(map[string]string{}), fileExists)
	wantBin := filepath.Join(root, "Git", "bin", "bash.exe")
	found := false
	for _, p := range got {
		if strings.EqualFold(p, wantBin) {
			found = true
			if sources[strings.ToLower(p)] != ShellSourceRegistry {
				t.Errorf("candidate %q source = %q, want %q", p, sources[strings.ToLower(p)], ShellSourceRegistry)
			}
			if !fileExists(p) {
				t.Errorf("registry candidate %q does not exist on disk", p)
			}
		}
	}
	if !found {
		t.Fatalf("registry route candidate %q missing from %v", wantBin, got)
	}
}

// TestTL1763StandardRootRouteCandidates points %ProgramFiles% at a temporary
// tree with a real Git\bin\bash.exe, so the standard-roots route (step 5)
// yields an existing candidate with no git.exe on PATH and no registry entry.
func TestTL1763StandardRootRouteCandidates(t *testing.T) {
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "Git"),
		filepath.Join("bin", "bash.exe"),
		filepath.Join("usr", "bin", "bash.exe"),
	)
	t.Setenv("ProgramFiles", root)
	t.Setenv("ProgramW6432", root)
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "x86"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	t.Setenv("ProgramData", filepath.Join(root, "pd"))
	t.Setenv("USERPROFILE", filepath.Join(root, "user"))

	got, sources := windowsBashCandidateSources("auto", "", nil, fakeLookPath(map[string]string{}), fileExists)
	wantBin := filepath.Join(root, "Git", "bin", "bash.exe")
	found := false
	for _, p := range got {
		if strings.EqualFold(p, wantBin) {
			found = true
			if sources[strings.ToLower(p)] != ShellSourceStandard {
				t.Errorf("candidate %q source = %q, want %q", p, sources[strings.ToLower(p)], ShellSourceStandard)
			}
			if !fileExists(p) {
				t.Errorf("standard-route candidate %q does not exist on disk", p)
			}
		}
	}
	if !found {
		t.Fatalf("standard route candidate %q missing from %v", wantBin, got)
	}
}

// TestTL1763StorePwshAliasCandidatesAndCapability covers the pwsh Store
// edition: %LOCALAPPDATA%\Microsoft\WindowsApps\pwsh.exe (modeled as a 0-byte
// file — the AppExecLink reparse point reports size 0 through os.Stat) must
// enter the candidate list between the MSI pwsh and the inbox powershell.exe,
// make the pwsh capability available through exists alone, and stay isolated
// from the powershell capability (which must not claim the alias).
func TestTL1763StorePwshAliasCandidatesAndCapability(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "Microsoft", "WindowsApps", "pwsh.exe")
	writeTree(t, root,
		filepath.Join("PowerShell", "7", "pwsh.exe"),
		filepath.Join("Microsoft", "WindowsApps", "pwsh.exe"),
	)
	// Model the AppExecLink reparse point's reported size of 0.
	if err := os.Truncate(alias, 0); err != nil {
		t.Fatalf("truncate alias: %v", err)
	}
	t.Setenv("ProgramFiles", root)
	t.Setenv("ProgramW6432", root)
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "x86"))
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("SystemRoot", filepath.Join(root, "windows"))

	cands := windowsPowerShellCandidates()
	wantMSI := filepath.Join(root, "PowerShell", "7", "pwsh.exe")
	wantInbox := filepath.Join(root, "windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	msiIdx, aliasIdx, inboxIdx := -1, -1, -1
	for i, p := range cands {
		switch {
		case strings.EqualFold(p, wantMSI):
			msiIdx = i
		case strings.EqualFold(p, alias):
			aliasIdx = i
		case strings.EqualFold(p, wantInbox):
			inboxIdx = i
		}
	}
	if msiIdx < 0 || aliasIdx < 0 || inboxIdx < 0 {
		t.Fatalf("candidates = %v, want MSI %q, Store alias %q, inbox %q", cands, wantMSI, alias, wantInbox)
	}
	if !(msiIdx < aliasIdx && aliasIdx < inboxIdx) {
		t.Fatalf("candidate order wrong: MSI@%d alias@%d inbox@%d (want MSI < Store < inbox)", msiIdx, aliasIdx, inboxIdx)
	}

	snap := &shellSnapshot{
		psCands:    cands,
		lookPath:   fakeLookPath(map[string]string{}),
		exists:     fileExists,
		probeCache: map[string]bool{},
	}
	// With the MSI install present it wins over the Store alias.
	pwshCap := windowsPowerShellCapability(snap, ShellCapabilityPwsh, []string{"pwsh", "pwsh.exe"}, "pwsh")
	if !pwshCap.Available || !strings.EqualFold(pwshCap.Path, wantMSI) || pwshCap.Source != ShellSourceStandard {
		t.Fatalf("pwsh capability with MSI present = %+v, want available at %q via %s", pwshCap, wantMSI, ShellSourceStandard)
	}
	// Without the MSI install the Store alias carries the capability: exists
	// alone (os.Stat over the AppExecLink reparse point) is the detection.
	snap.exists = func(p string) bool { return strings.EqualFold(p, alias) }
	pwshCap = windowsPowerShellCapability(snap, ShellCapabilityPwsh, []string{"pwsh", "pwsh.exe"}, "pwsh")
	if !pwshCap.Available || !strings.EqualFold(pwshCap.Path, alias) || pwshCap.Source != ShellSourceStandard {
		t.Fatalf("pwsh capability without MSI = %+v, want available at the Store alias %q via %s", pwshCap, alias, ShellSourceStandard)
	}
	inboxCap := windowsPowerShellCapability(snap, ShellCapabilityPowerShell, []string{"powershell", "powershell.exe"}, "powershell")
	if inboxCap.Available {
		t.Fatalf("powershell capability = %+v, want unavailable on this synthetic tree (must not claim the pwsh alias)", inboxCap)
	}
}

// TestTL1763StorePwshAliasWinsInResolveShellFallback pins the resolveShell
// decision table: with no bash anywhere and both the Store pwsh alias and the
// inbox powershell.exe present, auto mode binds the alias (PowerShell 7+
// understands &&; inbox 5.1 does not) and reports the PowerShell kind.
func TestTL1763StorePwshAliasWinsInResolveShellFallback(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "Microsoft", "WindowsApps", "pwsh.exe")
	inbox := filepath.Join(root, "windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	writeTree(t, root,
		filepath.Join("Microsoft", "WindowsApps", "pwsh.exe"),
		filepath.Join("windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
	)

	sh := resolveShell("auto", "", nil, "windows",
		fakeLookPath(map[string]string{}),
		fileExists,
		nil, // no bash candidates
		[]string{alias, inbox},
		func(string) bool { return true },
		func(string) bool { return false },
	)
	if sh.Kind != ShellPowerShell || !strings.EqualFold(sh.Path, alias) {
		t.Fatalf("auto resolve = %+v, want PowerShell kind bound to the Store alias %q", sh, alias)
	}
	if !sh.SupportsChaining() {
		t.Errorf("Store pwsh path %q must report && support", sh.Path)
	}
}

// TestTL1763ProbeBudgetGuard keeps the bash probe budget above the measured
// msys cold-start range (2.4-3.7s, tl1763 forensics): a regression back to the
// old 3s budget would resurrect the all-routes-not-installed defect.
func TestTL1763ProbeBudgetGuard(t *testing.T) {
	if shellProbeBashTimeout < 10*time.Second {
		t.Fatalf("shellProbeBashTimeout = %v, want >= 10s (measured msys cold-start peaks at 3.7s)", shellProbeBashTimeout)
	}
}

// writeTree creates directories and empty files under root, failing the test
// on any error.
func writeTree(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", full, err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}
