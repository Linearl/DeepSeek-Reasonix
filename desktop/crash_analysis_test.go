package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsReasonixSourceDirAcceptsRealCheckoutShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "desktop"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module reasonix\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop", "crash_app.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isReasonixSourceDir(dir) {
		t.Fatal("a checkout with module reasonix + desktop source must be accepted")
	}
}

func TestIsReasonixSourceDirRejectsWrongModuleAndMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isReasonixSourceDir(dir) {
		t.Fatal("a different module must be rejected")
	}

	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module reasonix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isReasonixSourceDir(dir2) {
		t.Fatal("module reasonix without the desktop source must be rejected")
	}
}

func TestFilterEnvDropsOnlyNamedVars(t *testing.T) {
	environ := []string{"HOME=/home/x", "GITHUB_TOKEN=secret", "GH_TOKEN=also-secret", "PATH=/bin"}
	kept := filterEnv(environ, "GITHUB_TOKEN", "GH_TOKEN")
	joined := strings.Join(kept, "\n")
	if strings.Contains(joined, "GITHUB_TOKEN") || strings.Contains(joined, "GH_TOKEN") {
		t.Fatalf("token env vars survived filtering: %q", joined)
	}
	for _, want := range []string{"HOME=/home/x", "PATH=/bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("filter dropped unrelated var, missing %q in %q", want, joined)
		}
	}
}

func TestBuildCrashAnalysisInstructionCarriesPayloadAndContract(t *testing.T) {
	payload := `{
  "kind": "crash",
  "errorType": "panic",
  "topFrame": "desktop/crash_app.go:99"
}`
	instruction := buildCrashAnalysisInstruction(`C:\src\reasonix`, payload)
	for _, want := range []string{
		"gh-issue-submit",
		crashAnalysisRepo,
		`C:\src\reasonix`,
		"file:line",
		`"kind": "crash"`,
		"desktop/crash_app.go:99",
		"issue 链接",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q:\n%s", want, instruction)
		}
	}
	if !strings.HasPrefix(instruction, "请分析") {
		t.Fatalf("instruction should open with the analysis ask:\n%s", instruction)
	}
}

// ── task 643: gh discovery must survive a stale process PATH ────────────────

func stubGhFallbackDirs(t *testing.T, dirs ...string) {
	t.Helper()
	orig := ghFallbackLocationDirs
	ghFallbackLocationDirs = func() []string { return dirs }
	t.Cleanup(func() { ghFallbackLocationDirs = orig })
}

func clearPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
}

// Every production fallback location must be absolute: a half-set env var would
// otherwise yield a relative path that could stat against the process cwd.
func TestGhFallbackLocationsAreAllAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("ProgramFiles", "")
		t.Setenv("ProgramFiles(x86)", "")
		t.Setenv("LocalAppData", "")
		t.Setenv("USERPROFILE", "")
	}
	locations := ghFallbackLocations()
	if runtime.GOOS == "windows" {
		if len(locations) != 0 {
			t.Fatalf("all install env vars unset must yield no fallback locations, got %q", locations)
		}
		return
	}
	if len(locations) == 0 {
		t.Fatal("unix fallback must keep its fixed install locations")
	}
	for _, loc := range locations {
		if !filepath.IsAbs(loc) {
			t.Fatalf("fallback locations must be absolute, got %q", loc)
		}
	}
}

// The task 643 scenario: gh is installed at a known location but the process
// PATH predates it — resolution must still find the executable.
func TestResolveGhExecutableFallsBackWhenPathMisses(t *testing.T) {
	clearPath(t)
	dir := t.TempDir()
	candidate := filepath.Join(dir, ghExecutableName())
	if err := os.WriteFile(candidate, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubGhFallbackDirs(t, dir)

	gh, onPath, found := resolveGhExecutable()
	if !found {
		t.Fatalf("gh at a known install location must be found even when PATH misses (candidate %q)", candidate)
	}
	if onPath {
		t.Fatalf("a fallback hit must not be reported as a PATH hit: %q", gh)
	}
	if gh != candidate {
		t.Fatalf("resolved %q, want %q", gh, candidate)
	}
}

func TestResolveGhExecutableNotFoundWhenPathAndFallbackMiss(t *testing.T) {
	clearPath(t)
	stubGhFallbackDirs(t, t.TempDir()) // exists but holds no gh
	gh, onPath, found := resolveGhExecutable()
	if found || gh != "" || onPath {
		t.Fatalf("with no gh on PATH or in fallbacks, resolution must fail cleanly, got (%q, %v, %v)", gh, onPath, found)
	}
}

func TestGhAuthenticatedNotFoundDetailIsDistinct(t *testing.T) {
	clearPath(t)
	stubGhFallbackDirs(t, t.TempDir())
	ok, detail := ghAuthenticated()
	if ok {
		t.Fatal("ghAuthenticated must fail when gh is nowhere to be found")
	}
	// The task 643 false alarm: this state must be distinguishable from an auth
	// failure, not collapsed into the same generic message.
	if !strings.Contains(detail, "not found") {
		t.Fatalf("not-found detail must say so explicitly, got %q", detail)
	}
}

func TestGhSuccessDetailExplainsFallbackDiscovery(t *testing.T) {
	if detail := ghSuccessDetail(`C:\Program Files\GitHub CLI\gh.exe`, true); detail != "" {
		t.Fatalf("a PATH hit needs no success detail, got %q", detail)
	}
	detail := ghSuccessDetail(`C:\Program Files\GitHub CLI\gh.exe`, false)
	for _, want := range []string{"outside PATH", `C:\Program Files\GitHub CLI\gh.exe`, "auth OK"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("fallback success detail missing %q, got %q", want, detail)
		}
	}
}
