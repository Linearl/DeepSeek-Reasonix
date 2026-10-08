package main

import (
	"os"
	"path/filepath"
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
	instruction := buildCrashAnalysisInstruction(`C:\src\reasonix`, payload, false)
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

// Task 642: a mock payload's analysis run must announce the simulation so the
// submitted issue is labeled mock/test instead of triaged as a real failure.
func TestBuildCrashAnalysisInstructionMarksMockPayload(t *testing.T) {
	instruction := buildCrashAnalysisInstruction(`C:\src\reasonix`, `{"kind":"crash"}`, true)
	if !strings.Contains(instruction, "测试/mock 报告") || !strings.Contains(instruction, "mock/test") {
		t.Fatalf("mock instruction missing the simulation notice:\n%s", instruction)
	}
	if !strings.HasPrefix(instruction, "请分析") {
		t.Fatalf("mock instruction should still open with the analysis ask:\n%s", instruction)
	}
	plain := buildCrashAnalysisInstruction(`C:\src\reasonix`, `{"kind":"crash"}`, false)
	if strings.Contains(plain, "测试/mock 报告") {
		t.Fatalf("non-mock instruction must not carry the simulation notice:\n%s", plain)
	}
}
