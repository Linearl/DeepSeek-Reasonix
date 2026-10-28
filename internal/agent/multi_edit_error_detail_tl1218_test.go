package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl-1218 ①（experimental_multi_edit_error_detail）验收矩阵：
//
//	E1-off：证据类拦截信息与基线逐字节一致（不附任何盘上内容）。
//	E1-on ：拦截信息附缺失/失配行的当前盘上内容；截断上限
//	        ≤5 行/行≤200 字符（按字节保守口径）/总量≤2KB。
//	三关等价：error_detail 关即基线（本文件 E1-off + 边界不越界断言）。
func newEvidenceDetailAgent(t *testing.T, path string, lines []string, detail bool) *Agent {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	// The writer declares whole-file evidence over the CURRENT disk content;
	// the ledger below holds a STALE observation, so the gate reports
	// stale_or_partial_evidence with Missing covering every line.
	hashes := make([]string, 0, len(lines))
	for _, line := range lines {
		hashes = append(hashes, hashLine(line))
	}
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: path, WholeFile: true, Hashes: hashes,
	}}
	reg := tool.NewRegistry()
	reg.Add(writer)
	read, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file builtin missing")
	}
	reg.Add(read)
	opts := Options{}
	if detail {
		opts.MultiEditErrorDetail = true
	}
	a := New(&userInputCaptureProvider{}, reg, NewSession("system"), opts, event.Discard)
	a.task.ledger = evidence.NewLedger()
	stale := make([]string, 0, len(lines))
	for i := range lines {
		stale = append(stale, "stale-"+string(rune('a'+i)))
	}
	a.task.ledger.RecordTextObservation(evidence.TextObservation{
		Path: path, StartLine: 1, Snapshot: "stale:1", LineHashes: hashesFor(stale...),
	})
	return a
}

func runDetailGate(t *testing.T, a *Agent, path string) toolOutcome {
	t.Helper()
	plan := &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: `{"path":"` + path + `"}`}}
	out, blocked := a.applyEvidenceGates(context.Background(), plan)
	if !blocked {
		t.Fatalf("fixture must block (stale evidence) for %s", path)
	}
	return out
}

// TestEvidenceErrorDetailOffKeepsBaselineMessage pins E1-off: the blocked
// message stays byte-identical to the pre-1218 baseline (describeEvidence
// copy, no disk content, regardless of the file being readable).
func TestEvidenceErrorDetailOffKeepsBaselineMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	a := newEvidenceDetailAgent(t, path, []string{"alpha", "beta", "gamma"}, false)
	out := runDetailGate(t, a, path)
	if strings.Contains(out.output, "current content of the missing lines") {
		t.Fatalf("off must not append the line detail: %q", out.output)
	}
	if strings.Contains(out.output, "alpha") || strings.Contains(out.output, "beta") {
		t.Fatalf("off must never include source text: %q", out.output)
	}
	if !strings.Contains(out.output, "missing lines 1-3") || !strings.Contains(out.output, "evidence required") {
		t.Fatalf("off must keep the baseline copy verbatim: %q", out.output)
	}
}

// TestEvidenceErrorDetailAppendsCurrentLineContent pins E1-on: the message
// carries the current on-disk content of the missing lines with line numbers.
func TestEvidenceErrorDetailAppendsCurrentLineContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	a := newEvidenceDetailAgent(t, path, []string{"alpha", "beta", "gamma"}, true)
	out := runDetailGate(t, a, path)
	for _, want := range []string{"current content of the missing lines", "L1: alpha", "L2: beta", "L3: gamma"} {
		if !strings.Contains(out.output, want) {
			t.Fatalf("detail must carry %q, got:\n%s", want, out.output)
		}
	}
}

// TestEvidenceErrorDetailCapsAtFiveLines pins the per-file ≤5-line bound: a
// nine-line target only samples the first five missing lines.
func TestEvidenceErrorDetailCapsAtFiveLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	lines := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	a := newEvidenceDetailAgent(t, path, lines, true)
	out := runDetailGate(t, a, path)
	if !strings.Contains(out.output, "L5: l5") {
		t.Fatalf("detail must include the fifth line, got:\n%s", out.output)
	}
	if strings.Contains(out.output, "L6: l6") {
		t.Fatalf("detail must cap at 5 lines, got:\n%s", out.output)
	}
}

// TestEvidenceErrorDetailTruncatesLongLinesAndTotal pins the per-line 200-char
// (byte-conservative) and the 2KB total bounds.
func TestEvidenceErrorDetailTruncatesLongLinesAndTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	long := strings.Repeat("x", 500)
	a := newEvidenceDetailAgent(t, path, []string{long, "short"}, true)
	out := runDetailGate(t, a, path)
	if !strings.Contains(out.output, "...") {
		t.Fatalf("an over-long line must be truncated with an ellipsis, got:\n%s", out.output)
	}
	if strings.Count(out.output, "x") > 500 {
		t.Fatalf("truncation must bound the echoed line, got %d x's", strings.Count(out.output, "x"))
	}
	// The whole message stays bounded: baseline copy + detail ≤ 2KB + slack.
	if len(out.output) > 2048+1024 {
		t.Fatalf("message exceeded the detail budget: %d bytes", len(out.output))
	}
}

// TestEvidenceErrorDetailSurvivesMissingFile pins best-effort: an unreadable
// file keeps the baseline message (no panic, no fabricated content).
func TestEvidenceErrorDetailSurvivesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.go")
	hashes := hashesFor("alpha")
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{Path: path, WholeFile: true, Hashes: hashes}}
	reg := tool.NewRegistry()
	reg.Add(writer)
	read, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file builtin missing")
	}
	reg.Add(read)
	a := New(&userInputCaptureProvider{}, reg, NewSession("system"), Options{MultiEditErrorDetail: true}, event.Discard)
	a.task.ledger = evidence.NewLedger()
	a.task.ledger.RecordTextObservation(evidence.TextObservation{
		Path: path, StartLine: 1, Snapshot: "stale:1", LineHashes: hashesFor("stale"),
	})
	plan := &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: `{\"path\":\"` + path + `\"}`}}
	out, blocked := a.applyEvidenceGates(context.Background(), plan)
	if !blocked {
		t.Fatal("fixture must block")
	}
	if strings.Contains(out.output, "current content of the missing lines") {
		t.Fatalf("an unreadable file must keep the baseline message: %q", out.output)
	}
}
