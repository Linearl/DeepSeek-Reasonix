package servepool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/installlayout"
)

// fakeLayout builds a versioned install root with an active version that
// ships the CLI, returning the install root path.
func fakeLayout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	versionDir := filepath.Join(root, "versions", "v1.0.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(versionDir, installlayout.CLIBinaryName())
	if err := os.WriteFile(cli, []byte("cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	desktop := filepath.Join(versionDir, installlayout.DesktopBinaryName())
	if err := os.WriteFile(desktop, []byte("desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installlayout.WriteCurrent(root, installlayout.CurrentPointer{
		SchemaVersion: installlayout.CurrentSchemaVersion,
		ActiveVersion: "v1.0.0",
		ActiveDir:     "versions/v1.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveServeBinaryPrefersSiblingCLI(t *testing.T) {
	root := t.TempDir()
	self := filepath.Join(root, installlayout.DesktopBinaryName())
	if err := os.WriteFile(self, []byte("desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, installlayout.CLIBinaryName())
	if err := os.WriteFile(cli, []byte("cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveServeBinary(self); got != cli {
		t.Fatalf("resolveServeBinary = %q, want sibling CLI %q", got, cli)
	}
}

// The task 248 shape: the running desktop lives in versions/<ver>.replaced-<n>/
// without a CLI beside it, while the active version directory has one. The
// fallback must find the active CLI through current.json instead of spawning
// the desktop binary (which does not understand "serve").
func TestResolveServeBinaryFallsBackToActiveCLI(t *testing.T) {
	root := fakeLayout(t)
	replacedDir := filepath.Join(root, "versions", "v1.0.0.replaced-1700000000")
	if err := os.MkdirAll(replacedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(replacedDir, installlayout.DesktopBinaryName())
	if err := os.WriteFile(self, []byte("desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "versions", "v1.0.0", installlayout.CLIBinaryName())
	if got := resolveServeBinary(self); got != want {
		t.Fatalf("resolveServeBinary = %q, want active CLI %q", got, want)
	}
}

func TestResolveServeBinaryFallsBackToSelf(t *testing.T) {
	root := t.TempDir()
	// A plain directory with no sibling CLI and no current.json: nothing to
	// upgrade to, so the running executable itself is the answer.
	self := filepath.Join(root, installlayout.DesktopBinaryName())
	if err := os.WriteFile(self, []byte("desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveServeBinary(self); got != self {
		t.Fatalf("resolveServeBinary = %q, want self %q", got, self)
	}
}

func TestTailBufferKeepsTail(t *testing.T) {
	tb := &tailBuffer{max: 8}
	if n, err := tb.Write([]byte("abcdefghij")); err != nil || n != 10 {
		t.Fatalf("Write = %d/%v, want 10/nil", n, err)
	}
	if got := tb.String(); got != "cdefghij" {
		t.Fatalf("String = %q, want %q (tail kept)", got, "cdefghij")
	}
	// A single write larger than the cap keeps only its own tail.
	if _, err := tb.Write([]byte("1234567890")); err != nil {
		t.Fatal(err)
	}
	if got := tb.String(); got != "34567890" {
		t.Fatalf("String = %q, want %q", got, "34567890")
	}
	if tb.Len() != 8 {
		t.Fatalf("Len = %d, want 8", tb.Len())
	}
}

func TestTailBufferEmpty(t *testing.T) {
	tb := &tailBuffer{max: 8}
	if tb.Len() != 0 || tb.String() != "" {
		t.Fatalf("empty buffer Len/String = %d/%q, want 0/\"\"", tb.Len(), tb.String())
	}
	if _, err := tb.Write([]byte(strings.Repeat("x\n", 4))); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(tb.String(), "\n") {
		t.Fatalf("String = %q, want trailing newline trimmed", tb.String())
	}
}

// The spawn timeout path reads the buffer while a straggler exec pipe-copier
// may still be writing (Process.Wait does not wait for copiers), so the
// buffer must be safe for concurrent Write/Len/String. Run with -race to
// make any regression visible.
func TestTailBufferConcurrentReadWrite(t *testing.T) {
	tb := &tailBuffer{max: 256}
	const writers = 8
	const perWriter = 200
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < perWriter; j++ {
				if _, err := tb.Write([]byte("0123456789")); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-start
		for j := 0; j < writers*perWriter; j++ {
			_ = tb.Len()
			_ = tb.String()
		}
	}()
	close(start)
	wg.Wait()
	<-done
	if got := tb.Len(); got != 256 {
		t.Fatalf("Len = %d, want capped at max 256", got)
	}
}
