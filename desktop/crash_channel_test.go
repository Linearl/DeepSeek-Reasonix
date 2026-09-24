package main

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/safego"
)

// TestRecoveredPanicLandsInLogChannel pins task 188's log half of the
// acceptance: a panic inside a guarded goroutine/callback is recovered and its
// stack reaches the standard log — the stream installDesktopLogging redirects
// to the rolling desktop.log — instead of dying on the fd=2 console the GUI
// subsystem does not have.
func TestRecoveredPanicLandsInLogChannel(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	safego.Guard("task188.crash.channel", func() {
		panic("deliberate guarded panic (task 188)")
	})
	// Guard is synchronous and Recover logs before it returns; a short poll
	// covers the log package's own locking without racing the writer swap.
	deadline := time.Now().Add(2 * time.Second)
	for {
		out := buf.String()
		if strings.Contains(out, "[safego] task188.crash.channel: recovered panic:") &&
			strings.Contains(out, "deliberate guarded panic (task 188)") &&
			strings.Contains(out, "goroutine") {
			if !strings.Contains(out, "\tat ") && !strings.Contains(out, ".go:") {
				t.Fatalf("stack missing from recovered panic log:\n%s", out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovered panic never reached the log channel:\n%s", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDeliberateCrashLeavesCrashFile pins task 188's dump half of the
// acceptance end to end: re-executing this same test binary with
// REASONIX_CRASH_TEST=panic must die with an unrecovered panic that
// debug.SetCrashOutput mirrored to the pinned file — the artifact a real
// production crash would leave behind for post-mortem triage.
func TestDeliberateCrashLeavesCrashFile(t *testing.T) {
	crashFile := filepath.Join(t.TempDir(), "deliberate-crash.out")
	cmd := exec.Command(os.Args[0], "-test.run", "TestDeliberateCrashLeavesCrashFile")
	cmd.Env = append(os.Environ(),
		crashTestModeEnv+"=panic",
		crashTestFileEnv+"="+crashFile,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the deliberately crashing child must not exit cleanly; output:\n%s", out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(crashFile)
		if readErr == nil && len(data) > 0 {
			text := string(data)
			if !strings.Contains(text, "reasonix crash test (task 188)") {
				t.Fatalf("crash file missing the panic value:\n%s", text)
			}
			if !strings.Contains(text, "goroutine") {
				t.Fatalf("crash file missing the goroutine stack:\n%s", text)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("crash file never materialized at %s (child output:\n%s)", crashFile, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestUnknownCrashTestModeIsIgnored: a typo in the env must never take the
// process down (runCrashTest returns, the caller continues).
func TestUnknownCrashTestModeIsIgnored(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	runCrashTest("not-a-mode")
	if !strings.Contains(buf.String(), "unknown mode") {
		t.Fatalf("expected the unknown-mode log line, got: %q", buf.String())
	}
}
