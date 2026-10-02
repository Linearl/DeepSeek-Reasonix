package baseproc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// baseHelperEnv arms the child branch of TestSubprocessStderrLandsInTheLogFile
// when it is re-executed as the stand-in subprocess.
const baseHelperEnv = "BASEPROC_STDERR_HELPER"

// baseMarkerEnv is an explicitly-supplied environment variable: its arrival
// in the child is the F1 assertion (spawn inherits env deliberately, not
// through cwd propagation).
const baseMarkerEnv = "BASEPROC_MARKER"

const baseStderrMarker = "baseproc stderr marker"

func TestOpenBaseLogCreatesParentDirectoryAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "base", "base.log")
	f, err := openBaseLog(path, defaultBaseLogMaxBytes)
	if err != nil {
		t.Fatalf("openBaseLog: %v", err)
	}
	if _, err := fmt.Fprintln(f, "first line"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	f, err = openBaseLog(path, defaultBaseLogMaxBytes)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := fmt.Fprintln(f, "second line"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Count(string(got), "line") != 2 {
		t.Fatalf("log = %q, want both lines appended (no truncation)", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("rotated file exists for an under-budget log: %v", err)
	}
}

func TestOpenBaseLogRotatesAnOversizedGeneration(t *testing.T) {
	// F2: one deep history, cheap rotation at open — a runaway subprocess must
	// not grow a single unbounded file, and the previous run must survive.
	dir := t.TempDir()
	path := filepath.Join(dir, "base.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 64)), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f, err := openBaseLog(path, 32) // smaller than the seed → rotate
	if err != nil {
		t.Fatalf("openBaseLog: %v", err)
	}
	_ = f.Close()

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("rotated generation missing: %v", err)
	}
	if len(rotated) != 64 {
		t.Fatalf("rotated size = %d, want the 64-byte seed", len(rotated))
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("new generation missing: %v", err)
	}
	if st.Size() != 0 {
		t.Fatalf("new generation size = %d, want 0 (fresh file after rotation)", st.Size())
	}
}

func TestResolveStderrNeverFailsASpawn(t *testing.T) {
	// R1 shape: an unopenable log must degrade to the inherited stderr rather
	// than take the spawn down with it.
	dir := t.TempDir() // open() on a directory fails on every platform
	got := resolveStderr(Options{LogFile: dir})
	if got.w != os.Stderr {
		t.Fatalf("stderr sink = %T, want os.Stderr fallback", got.w)
	}
	if got.cleanup == nil {
		t.Fatal("cleanup is nil: a failed resolve must still be safe to release")
	}
	got.cleanup() // must not panic
}

// TestSubprocessStderrLandsInTheLogFile is both the F2 assertion and, when
// re-executed as the child (baseHelperEnv), the stand-in subprocess: the
// child writes its markers to fd 2 and exits before the test framework can
// touch stdout — stdout is the protocol channel and must stay pure.
func TestSubprocessStderrLandsInTheLogFile(t *testing.T) {
	if os.Getenv(baseHelperEnv) == "1" {
		fmt.Fprintln(os.Stderr, baseStderrMarker)
		fmt.Fprintf(os.Stderr, "%s=%s\n", baseLogEnv, os.Getenv(baseLogEnv))
		fmt.Fprintf(os.Stderr, "%s=%s\n", baseMarkerEnv, os.Getenv(baseMarkerEnv))
		os.Exit(0)
	}

	path := filepath.Join(t.TempDir(), "base", "base.log")
	marker := "s1c-marker-" + fmt.Sprint(time.Now().UnixNano())
	client := Start(context.Background(), Options{
		Enabled:       true,
		ServerVersion: "v-parent",
		LogFile:       path,
		Env:           append(os.Environ(), baseHelperEnv+"=1", baseMarkerEnv+"="+marker),
		Command:       []string{os.Args[0], "-test.run=^TestSubprocessStderrLandsInTheLogFile$"},
		// The child exits instead of serving, so the handshake cannot
		// succeed: R1 sends the view inline and that is the expected shape.
		HandshakeTimeout: 5 * time.Second,
	})
	defer func() { _ = client.Close() }()

	waitFor(t, 5*time.Second, "the helper subprocess to be reaped", func() bool {
		_, err := os.ReadFile(path)
		return err == nil
	})
	// Give the reaper a beat so the file handle is released before reading on
	// Windows (the writer is still holding it until teardown completes).
	deadline := time.Now().Add(5 * time.Second)
	var content string
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), baseStderrMarker) {
			content = string(raw)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if content == "" {
		raw, _ := os.ReadFile(path)
		t.Fatalf("log = %q, want the child's stderr marker (F2: no more silent subprocess)", raw)
	}
	if !strings.Contains(content, baseLogEnv+"="+path) {
		t.Fatalf("log = %q, want %s=%s passed to the child (F1)", content, baseLogEnv, path)
	}
	if !strings.Contains(content, baseMarkerEnv+"="+marker) {
		t.Fatalf("log = %q, want the explicit spawn env marker %s (F1)", content, marker)
	}
	if got := client.Mode(); got != ModeInline {
		t.Fatalf("mode = %q after a child that never served, want inline (R1)", got)
	}
}
