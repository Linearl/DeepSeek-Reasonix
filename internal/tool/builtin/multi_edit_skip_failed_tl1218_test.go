package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl-1218 ③（experimental_multi_edit_skip_failed）验收矩阵：
//
//	E3-off（默认）：原子语义逐字不变——任一 edit 失败整调用报错，文件不动。
//	E3-on ：单个 edit 失败不中止整批——跳过继续，最终响应逐条报告各 edit
//	        （索引+成功替换数/失败原因）；全部失败=与基线同因报错、文件不动；
//	        纯成功=输出与基线逐字节一致（不附报告）。

func runMultiEdit(t *testing.T, m multiEdit, path string, edits []map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"path": path, "edits": edits})
	if err != nil {
		t.Fatal(err)
	}
	return m.Execute(context.Background(), raw)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mixedEdits() []map[string]any {
	return []map[string]any{
		{"old_string": "alpha", "new_string": "ALPHA"},
		{"old_string": "missing-anchor", "new_string": "nope"},
		{"old_string": "gamma", "new_string": "GAMMA"},
	}
}

// TestMultiEditSkipFailedOffStaysAtomic pins E3-off: one failing edit aborts
// the whole call with the baseline error, file untouched.
func TestMultiEditSkipFailedOffStaysAtomic(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	mustWrite(t, f, "alpha\nbeta\ngamma\n")
	_, err := runMultiEdit(t, multiEdit{}, f, mixedEdits())
	if err == nil {
		t.Fatal("off must fail the whole call on one bad edit")
	}
	if !strings.Contains(err.Error(), "edit 2:") || !strings.Contains(err.Error(), "old_string not found") {
		t.Fatalf("off must carry the baseline per-edit error: %v", err)
	}
	if b, _ := os.ReadFile(f); string(b) != "alpha\nbeta\ngamma\n" {
		t.Fatalf("off must leave the file untouched, got %q", b)
	}
}

// TestMultiEditSkipFailedOnSkipsAndReports pins E3-on: the failed step is
// skipped, the other edits apply, and the response reports every edit.
func TestMultiEditSkipFailedOnSkipsAndReports(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	mustWrite(t, f, "alpha\nbeta\ngamma\n")
	out, err := runMultiEdit(t, multiEdit{skipFailed: true}, f, mixedEdits())
	if err != nil {
		t.Fatalf("on must not fail the batch on one bad edit: %v", err)
	}
	for _, want := range []string{
		"2 edits applied",
		"per-edit report",
		"edit 1: applied (1 replacement(s))",
		"edit 2: failed:",
		"old_string not found",
		"edit 3: applied (1 replacement(s))",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report must carry %q, got:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(f); string(b) != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("applied edits must land, got %q", b)
	}
}

// TestMultiEditSkipFailedOnAllFailedKeepsFile pins the nothing-applied case:
// the call fails with the first failure's own error (diagnostics intact) and
// the file stays untouched.
func TestMultiEditSkipFailedOnAllFailedKeepsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	mustWrite(t, f, "alpha\nbeta\ngamma\n")
	_, err := runMultiEdit(t, multiEdit{skipFailed: true}, f, []map[string]any{
		{"old_string": "nope-one", "new_string": "x"},
		{"old_string": "nope-two", "new_string": "y"},
	})
	if err == nil {
		t.Fatal("an all-failed batch must still fail (nothing to write)")
	}
	if !strings.Contains(err.Error(), "edit 1:") || !strings.Contains(err.Error(), "old_string not found") {
		t.Fatalf("the first failure's error must surface: %v", err)
	}
	if b, _ := os.ReadFile(f); string(b) != "alpha\nbeta\ngamma\n" {
		t.Fatalf("file must stay untouched, got %q", b)
	}
}

// TestMultiEditSkipFailedOnPureSuccessStaysVerbatim pins three-switch-off
// equivalence on the success face: without failures the output carries no
// per-edit report — byte-identical to the baseline summary face. Both runs
// share one path so the embedded file location cannot differ.
func TestMultiEditSkipFailedOnPureSuccessStaysVerbatim(t *testing.T) {
	body := "alpha\nbeta\ngamma\n"
	edits := []map[string]any{
		{"old_string": "alpha", "new_string": "ALPHA"},
		{"old_string": "gamma", "new_string": "GAMMA"},
	}
	f := filepath.Join(t.TempDir(), "shared.txt")
	mustWrite(t, f, body)
	outOff, errOff := runMultiEdit(t, multiEdit{}, f, edits)
	mustWrite(t, f, body)
	outOn, errOn := runMultiEdit(t, multiEdit{skipFailed: true}, f, edits)
	if (errOff == nil) != (errOn == nil) || outOff != outOn {
		t.Fatalf("pure success must stay byte-identical across the switch:\noff=%q (%v)\non=%q (%v)", outOff, errOff, outOn, errOn)
	}
	if strings.Contains(outOn, "per-edit report") {
		t.Fatalf("a pure success must not carry the report:\n%s", outOn)
	}
}

// TestMultiEditSkipFailedReportBounded pins the report bounds: a long failure
// reason truncates at 300 bytes and the whole report stays under 4KB.
func TestMultiEditSkipFailedReportBounded(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	mustWrite(t, f, "anchor\n")
	long := strings.Repeat("y", 500)
	out, err := runMultiEdit(t, multiEdit{skipFailed: true}, f, []map[string]any{
		{"old_string": "anchor", "new_string": "ANCHOR"},
		{"old_string": long, "new_string": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(out, "edit 2: failed:")
	if idx < 0 {
		t.Fatalf("report must name the failed edit:\n%s", out)
	}
	rowEnd := idx
	for rowEnd < len(out) && out[rowEnd] != '\n' {
		rowEnd++
	}
	row := out[idx:rowEnd]
	if len(row) > 300 {
		t.Fatalf("failure line must truncate at ~300 bytes, got %d", len(row))
	}
}
