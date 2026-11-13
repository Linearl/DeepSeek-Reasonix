package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessiontemp"
	"reasonix/internal/tool"
)

func newArchiveTestAgent(t *testing.T, withTemp bool) (*Agent, string) {
	t.Helper()
	var mgr *sessiontemp.Manager
	tempRoot := ""
	if withTemp {
		tempRoot = t.TempDir()
		mgr = sessiontemp.NewWithRoot(tempRoot)
		mgr.Retain()
		t.Cleanup(mgr.Release)
	}
	reg := tool.NewRegistry()
	proxy := NewUseCapabilityTool(t.Context(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, nil, Options{SessionTemp: mgr}, event.Discard)
	return a, tempRoot
}

// tl1561 验收②:截断落盘后提示里的路径必须真实可读。本钉覆盖 archive→落盘→按
// 路径读回完整原文(含多字节与换行逐字节一致),并约束文件名是单段安全路径。
func TestArchiveTruncatedToolOutputWritesReadableOriginal(t *testing.T) {
	a, tempRoot := newArchiveTestAgent(t, true)
	payload := strings.Repeat("ASCII行-界-🧪\n", 6000) // ~78KB 量级,与 heartbeat_task_list 事故同数量级

	path := a.archiveTruncatedToolOutput("heartbeat_task_list", "call-🧪-123", payload)
	if path == "" {
		t.Fatal("archive returned no path with a live session temp")
	}
	if !strings.HasPrefix(path, tempRoot) || !strings.Contains(path, toolOutputArchiveDirName) {
		t.Fatalf("archive path %q escaped the session temp root", path)
	}
	seg := filepath.Base(path)
	if !strings.HasPrefix(seg, "trunc-") || !strings.HasSuffix(seg, ".txt") {
		t.Fatalf("archive filename %q misses the trunc- naming convention", seg)
	}
	for _, r := range seg {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			t.Fatalf("archive filename %q contains unsafe rune %q", seg, r)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("archived original not readable at %q: %v", path, err)
	}
	if string(got) != payload {
		t.Fatalf("archived original differs: got %d bytes want %d", len(got), len(payload))
	}
}

// 连续两次截断落盘不得互相覆盖(同名即静默丢一份原件)。
func TestArchiveTruncatedToolOutputKeepsDistinctFiles(t *testing.T) {
	a, _ := newArchiveTestAgent(t, true)
	first := a.archiveTruncatedToolOutput("bash", "dup-call", "first-original")
	second := a.archiveTruncatedToolOutput("bash", "dup-call", "second-original")
	if first == "" || second == "" || first == second {
		t.Fatalf("distinct archives required: first=%q second=%q", first, second)
	}
	for path, want := range map[string]string{first: "first-original", second: "second-original"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("archive %q = %q err=%v, want %q", path, got, err, want)
		}
	}
}

// 无会话临时目录或空原件时不落盘、不报错——截断 marker 走 re-run 兜底。
func TestArchiveTruncatedToolOutputDegradesWithoutSessionTemp(t *testing.T) {
	a, _ := newArchiveTestAgent(t, false)
	if path := a.archiveTruncatedToolOutput("bash", "call", "payload"); path != "" {
		t.Fatalf("archive without session temp returned %q", path)
	}
	withTemp, _ := newArchiveTestAgent(t, true)
	if path := withTemp.archiveTruncatedToolOutput("bash", "call", ""); path != "" {
		t.Fatalf("archive of empty original returned %q", path)
	}
}
