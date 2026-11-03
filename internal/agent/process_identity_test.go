package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl1367 line 1: the holder identity primitives behind the lease-refusal
// wording. ProcessImageName is the test seam; the Windows/other builds only
// get smoke coverage here (the real syscall path was verified by hand against
// QueryFullProcessImageName flag semantics: 0 → drive-letter Win32 form).

func withProcessImageNameForTest(t *testing.T, name func(int) (string, string)) {
	t.Helper()
	orig := ProcessImageName
	ProcessImageName = name
	t.Cleanup(func() { ProcessImageName = orig })
}

func TestProcessLooksLikeSelfMatchesOwnImage(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("executable path unavailable: %v", err)
	}
	withProcessImageNameForTest(t, func(int) (string, string) {
		return filepath.Base(self), self
	})
	if !ProcessLooksLikeSelf(4123) {
		t.Fatal("the holder's image path equal to our own executable must read as self-family")
	}
	// A reasonix-named binary at another install path matches by name.
	withProcessImageNameForTest(t, func(int) (string, string) {
		return "reasonix-desktop.exe", filepath.Join(filepath.Dir(self), "elsewhere", "reasonix-desktop.exe")
	})
	if !ProcessLooksLikeSelf(4123) {
		t.Fatal("a reasonix-named binary at another path must still read as self-family")
	}
}

func TestProcessLooksLikeSelfMatchesReasonixName(t *testing.T) {
	withProcessImageNameForTest(t, func(int) (string, string) {
		return "reasonix-desktop.exe", `C:\Program Files\Reasonix\versions\v1\reasonix-desktop.exe`
	})
	if !ProcessLooksLikeSelf(4123) {
		t.Fatal("a reasonix-named holder binary must read as self-family regardless of path")
	}
	withProcessImageNameForTest(t, func(int) (string, string) {
		return "Reasonix-Launcher.EXE", `D:\tools\Reasonix-Launcher.EXE`
	})
	if !ProcessLooksLikeSelf(4124) {
		t.Fatal("the match is case-insensitive over the image base name")
	}
}

func TestProcessLooksLikeSelfRejectsUnrelatedProgram(t *testing.T) {
	withProcessImageNameForTest(t, func(int) (string, string) {
		return "chrome.exe", `C:\Program Files\Chrome\Application\chrome.exe`
	})
	if ProcessLooksLikeSelf(4125) {
		t.Fatal("an unrelated holder binary must read as not-self (pid-reuse family)")
	}
}

func TestProcessLooksLikeSelfStaysConservativeWhenIdentityUnresolvable(t *testing.T) {
	withProcessImageNameForTest(t, func(int) (string, string) { return "", "" })
	if !ProcessLooksLikeSelf(4126) {
		t.Fatal("an unresolvable holder identity must keep the pre-tl1367 live-family wording, not flip to reused")
	}
	if ProcessLooksLikeSelf(0) || ProcessLooksLikeSelf(-1) {
		t.Fatal("non-positive pids are never plausible holders")
	}
}

func TestReasonixProcessLabelDegradesGracefully(t *testing.T) {
	withProcessImageNameForTest(t, func(int) (string, string) {
		return "Reasonix.exe", `C:\Apps\Reasonix.exe`
	})
	if got := ReasonixProcessLabel(7); got != "Reasonix.exe (C:\\Apps\\Reasonix.exe)" {
		t.Fatalf("label = %q, want name (path)", got)
	}
	withProcessImageNameForTest(t, func(int) (string, string) { return "only-name.exe", "" })
	if got := ReasonixProcessLabel(7); got != "only-name.exe" {
		t.Fatalf("label = %q, want bare name when the path is unresolvable", got)
	}
	withProcessImageNameForTest(t, func(int) (string, string) { return "", "" })
	if got := ReasonixProcessLabel(7); got != "" {
		t.Fatalf("label = %q, want empty when nothing resolves", got)
	}
	if !strings.Contains(ReasonixProcessLabel(0), "") {
		t.Fatal("pid 0 must not panic")
	}
}
