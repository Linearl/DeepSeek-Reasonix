package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lineRangeTestFile(t *testing.T, crlf bool) string {
	t.Helper()
	lines := []string{"package main", "", "func a() {}", "func b() {}", "func c() {}", "func d() {}", "func e() {}"}
	sep := "\n"
	if crlf {
		sep = "\r\n"
	}
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte(strings.Join(lines, sep)+sep), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEditFileLineRangeReplacesBlock(t *testing.T) {
	path := lineRangeTestFile(t, false)
	// Lines 4-6 hold func b/c/d (line 2 is the empty import slot).
	out := runTool(t, editFile{}, map[string]any{
		"path": path, "line_range": "4-6", "new_string": "func b2() {}",
		"source_token": "tok-1", "anchor_head": "func b()", "anchor_tail": "func d()",
	})
	if !strings.Contains(out, "line_range 4-6 replaced with 1 lines") {
		t.Fatalf("summary missing: %s", out)
	}
	if !strings.Contains(out, "4 | func b2()") || !strings.Contains(out, "3 | func a()") {
		t.Fatalf("snippet missing or wrong window:\n%s", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nfunc a() {}\nfunc b2() {}\nfunc e() {}\n"
	if string(data) != want {
		t.Fatalf("updated = %q, want %q", data, want)
	}
}

func TestEditFileLineRangeDeletesBlock(t *testing.T) {
	path := lineRangeTestFile(t, false)
	runTool(t, editFile{}, map[string]any{
		"path": path, "line_range": "4-6", "source_token": "tok-1",
		"anchor_head": "func b()", "anchor_tail": "func d()",
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nfunc a() {}\nfunc e() {}\n"
	if string(data) != want {
		t.Fatalf("updated = %q, want %q", data, want)
	}
}

// Task 207 acceptance: the same edit through the line-range variant must
// produce exactly the same file as through old_string. The byte counts
// logged below are the kept comparison record.
func TestEditFileLineRangeMatchesOldStringResultAndSavesTokens(t *testing.T) {
	lines := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen"}
	block := strings.Join(lines[2:17], "\n") // lines 3-17
	base := strings.Join(lines, "\n") + "\n"

	mkFile := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "doc.txt")
		if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	byLines := mkFile(t)
	runTool(t, editFile{}, map[string]any{
		"path": byLines, "line_range": "3-17", "new_string": "REPLACED",
		"source_token": "tok-1", "anchor_head": "three", "anchor_tail": "seventeen",
	})
	byString := mkFile(t)
	runTool(t, editFile{}, map[string]any{
		"path": byString, "old_string": block, "new_string": "REPLACED",
	})

	got, err := os.ReadFile(byLines)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(byString)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("line_range product differs from old_string product:\nline_range=%q\nold_string=%q", got, want)
	}

	rangeArgs, _ := json.Marshal(map[string]any{
		"path": byLines, "line_range": "3-17", "new_string": "REPLACED",
		"source_token": "tok-1", "anchor_head": "three", "anchor_tail": "seventeen",
	})
	stringArgs, _ := json.Marshal(map[string]any{
		"path": byString, "old_string": block, "new_string": "REPLACED",
	})
	t.Logf("call bytes: line_range=%d old_string=%d (15-line block)", len(rangeArgs), len(stringArgs))
	if len(rangeArgs) >= len(stringArgs) {
		t.Fatalf("line_range args (%d B) should be smaller than old_string args (%d B)", len(rangeArgs), len(stringArgs))
	}
}

// Realistic-width variant of the token comparison: the motivating case was a
// 15-line block of real Go code, where old_string re-emission is at its
// worst. This is the acceptance record for the token comparison.
func TestEditFileLineRangeTokenSavingWithRealisticWidth(t *testing.T) {
	var lines []string
	for i := 0; i < 18; i++ {
		lines = append(lines, fmt.Sprintf("\thandler%02d := func(ctx context.Context, req *Request) (*Response, error) { return nil, nil }", i))
	}
	block := strings.Join(lines[2:17], "\n")
	base := strings.Join(lines, "\n") + "\n"

	mkFile := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "doc.txt")
		if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	byLines := mkFile(t)
	runTool(t, editFile{}, map[string]any{
		"path": byLines, "line_range": "3-17", "new_string": "REPLACED",
		"source_token": "tok-1", "anchor_head": "handler02", "anchor_tail": "handler16",
	})
	rangeArgs, _ := json.Marshal(map[string]any{
		"path": byLines, "line_range": "3-17", "new_string": "REPLACED",
		"source_token": "tok-1", "anchor_head": "handler02", "anchor_tail": "handler16",
	})
	stringArgs, _ := json.Marshal(map[string]any{
		"path": byLines, "old_string": block, "new_string": "REPLACED",
	})
	t.Logf("realistic-width call bytes: line_range=%d old_string=%d", len(rangeArgs), len(stringArgs))
	if len(rangeArgs) >= len(stringArgs)/4 {
		t.Fatalf("line_range args (%d B) should be well under a quarter of old_string args (%d B)", len(rangeArgs), len(stringArgs))
	}
}

func TestEditFileLineRangeRequiresSourceToken(t *testing.T) {
	path := lineRangeTestFile(t, false)
	_, err := (editFile{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"path": path, "line_range": "4-6", "new_string": "x",
	}))
	if err == nil || !strings.Contains(err.Error(), "source_token") {
		t.Fatalf("err = %v, want a source_token requirement", err)
	}
}

func TestEditFileLineRangeDriftAnchorsRejectWithoutTouchingFile(t *testing.T) {
	path := lineRangeTestFile(t, false)
	before, _ := os.ReadFile(path)
	_, err := (editFile{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"path": path, "line_range": "4-6", "source_token": "tok-1",
		"anchor_head": "func totally-different()", "anchor_tail": "func d()",
	}))
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("err = %v, want a drift rejection", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("file changed despite a drift rejection")
	}
}

func TestEditFileLineRangeOutOfRange(t *testing.T) {
	path := lineRangeTestFile(t, false)
	_, err := (editFile{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"path": path, "line_range": "3-99", "source_token": "tok-1",
	}))
	if err == nil || !strings.Contains(err.Error(), "exceeds the file") {
		t.Fatalf("err = %v, want an out-of-range rejection", err)
	}
}

func TestEditFileLineRangeMutuallyExclusiveWithOldString(t *testing.T) {
	path := lineRangeTestFile(t, false)
	_, err := (editFile{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"path": path, "line_range": "4-6", "old_string": "func b() {}", "new_string": "x", "source_token": "tok-1",
	}))
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("err = %v, want mutual-exclusion error", err)
	}
}

func TestEditFileLineRangePreservesCRLF(t *testing.T) {
	path := lineRangeTestFile(t, true)
	runTool(t, editFile{}, map[string]any{
		"path": path, "line_range": "4-6", "new_string": "func b2() {}\nfunc c2() {}\nfunc d2() {}",
		"source_token": "tok-1", "anchor_head": "func b()", "anchor_tail": "func d()",
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\r\n\r\nfunc a() {}\r\nfunc b2() {}\r\nfunc c2() {}\r\nfunc d2() {}\r\nfunc e() {}\r\n"
	if string(data) != want {
		t.Fatalf("updated = %q, want %q", data, want)
	}
}

func TestEditFileLineRangePreviewDeclaresRangeEvidence(t *testing.T) {
	path := lineRangeTestFile(t, false)
	change, err := (editFile{}).Preview(context.Background(), argsJSON(t, map[string]any{
		"path": path, "line_range": "4-6", "new_string": "x", "anchor_head": "func b()", "anchor_tail": "func d()",
	}))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if change.OldText == change.NewText {
		t.Fatal("preview produced no change")
	}
	if !strings.Contains(change.NewText, "x") || strings.Contains(change.NewText, "func d()") {
		t.Fatalf("preview text unexpected: %q", change.NewText)
	}
	// Drifted anchors must fail in the evidence preview too.
	_, err = (editFile{}).Preview(context.Background(), argsJSON(t, map[string]any{
		"path": path, "line_range": "3-5", "anchor_head": "func b()", "anchor_tail": "func d()",
	}))
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("preview err = %v, want drift rejection", err)
	}
}

func TestParseLineRange(t *testing.T) {
	tests := []struct {
		raw     string
		start   int
		end     int
		wantErr bool
	}{
		{raw: "3-17", start: 3, end: 17},
		{raw: "1-1", start: 1, end: 1},
		{raw: " 3 - 17 ", start: 3, end: 17},
		{raw: "17-3", wantErr: true},
		{raw: "0-3", wantErr: true},
		{raw: "3", wantErr: true},
		{raw: "a-b", wantErr: true},
		{raw: "", wantErr: true},
	}
	for _, test := range tests {
		start, end, err := parseLineRange(test.raw)
		if test.wantErr {
			if err == nil {
				t.Fatalf("parseLineRange(%q) succeeded with %d-%d", test.raw, start, end)
			}
			continue
		}
		if err != nil || start != test.start || end != test.end {
			t.Fatalf("parseLineRange(%q) = %d-%d, %v; want %d-%d", test.raw, start, end, err, test.start, test.end)
		}
	}
}
