package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// filesystemRootForTest returns this platform's canonical filesystem root
// without assuming a specific drive letter on Windows.
func filesystemRootForTest(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "/"
	}
	vol := filepath.VolumeName(t.TempDir())
	if vol == "" {
		t.Skip("temp dir has no volume name; cannot build a drive root")
	}
	return vol + `\`
}

func TestIndexableRoot(t *testing.T) {
	projectDir := t.TempDir()

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"empty", "", false},
		{"blank", "   ", false},
		{"project dir", projectDir, true},
		{"relative", filepath.Base(projectDir), true}, // resolves against cwd; not a root
		{"filesystem root", filesystemRootForTest(t), false},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct {
				name, dir string
				want      bool
			}{"unc share root", `\\server\share`, false},
			struct {
				name, dir string
				want      bool
			}{"unc share root trailing sep", `\\server\share\`, false},
			struct {
				name, dir string
				want      bool
			}{"unc share dir", `\\server\share\proj`, true},
		)
	}

	for _, tc := range cases {
		if got := indexableRoot(tc.dir); got != tc.want {
			t.Errorf("%s: indexableRoot(%q) = %v, want %v", tc.name, tc.dir, got, tc.want)
		}
	}
}

func TestNewStdioTransportRefusesCodeGraphFilesystemRootDir(t *testing.T) {
	spec := Spec{
		Name:    "codegraph",
		Type:    "stdio",
		Command: "codegraph", // must never be spawned; rejection happens first
		Dir:     filesystemRootForTest(t),
	}
	_, err := newStdioTransport(context.Background(), spec)
	if err == nil {
		t.Fatal("codegraph at a filesystem root must be refused")
	}
	if !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("error should name the filesystem-root refusal, got: %v", err)
	}
	if liveCodeGraphInstances.Load() != 0 {
		t.Fatalf("refused start must not leak an instance slot, got %d", liveCodeGraphInstances.Load())
	}
}

func TestNewStdioTransportRefusesCodeGraphEmptyDir(t *testing.T) {
	spec := Spec{
		Name:    "codegraph",
		Type:    "stdio",
		Command: "codegraph",
		Dir:     "",
	}
	_, err := newStdioTransport(context.Background(), spec)
	if err == nil {
		t.Fatal("codegraph with no working dir must be refused (it would inherit the host cwd)")
	}
	if !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("error should name the refusal, got: %v", err)
	}
	if liveCodeGraphInstances.Load() != 0 {
		t.Fatalf("refused start must not leak an instance slot, got %d", liveCodeGraphInstances.Load())
	}
}

// TestNewStdioTransportAllowsCodeGraphProjectDir proves the refusal is scoped:
// a real project directory still reaches the spawn path. The transport test
// harness uses the test binary as a well-behaved MCP-less child.
func TestNewStdioTransportAllowsCodeGraphProjectDir(t *testing.T) {
	spec := Spec{
		Name:    "codegraph",
		Type:    "stdio",
		Command: exec.Command(os.Args[0]).Path,
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Dir:     t.TempDir(),
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}
	tr, err := newStdioTransport(context.Background(), spec)
	if err != nil {
		t.Fatalf("codegraph with a project dir must not be refused: %v", err)
	}
	tr.close()
}
