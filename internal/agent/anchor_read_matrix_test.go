package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// 任务 990 — 门禁异常分支测试矩阵（anchor_safety_gate 读形态部分）。
//
// 架构决策 msg_7a50d8fd1325a810：读形态覆盖矩阵 + blocked→解锁 e2e，不动语义。
// 判定面两层（同一结果）：shadow 指纹审计（recordAnchorSafetyAudit 的
// TextObservation 匹配）与 legacy 全文件读回执（HasSuccessfulAnchorRefreshReadAfter，
// evidence.go:1407 注释论证「窗口读/grep/ls 可能没看锚点区域」故不算数）。
// grep / bash cat 读不被记账是设计选择——本矩阵把「不解锁」钉成显式契约：
// 若将来有改动让它们开始解锁，这里会先红。
//
// 每格一个子测试 = 一次「写 → 读形态 → delete_range」三轮链路 + 一条主断言。

const matrixFileContent = "before\nstart\nmiddle\nend\nafter\n"

// matrixFileContentJSON is the same content as a JSON string body (escaped
// newlines) so tool-call args can be built by plain concatenation.
const matrixFileContentJSON = "before\\nstart\\nmiddle\\nend\\nafter\\n"

type anchorMatrixCell struct {
	name string
	// readRound is the provider round performing the read form; nil = no read.
	readTool string
	readArgs func(path string) string
	// wantAllow: delete_range executes (and the file is actually cut).
	wantAllow bool
	// wantAuditReason pins the shadow audit verdict for this read form.
	wantAuditReason string
	// wantBlockedNote, when non-empty, must appear in the blocked tool result.
	wantBlockedNote string
}

func anchorMatrixCells() []anchorMatrixCell {
	return []anchorMatrixCell{
		{
			name:            "read_file full file",
			readTool:        "read_file",
			readArgs:        func(path string) string { return `{"path":"` + path + `"}` },
			wantAllow:       true,
			wantAuditReason: anchorReasonExactMatch,
		},
		{
			name:            "read_file windowed covering both anchors",
			readTool:        "read_file",
			readArgs:        func(path string) string { return `{"path":"` + path + `","offset":1,"limit":3}` },
			wantAllow:       true,
			wantAuditReason: anchorReasonExactMatch,
		},
		{
			name:            "read_file windowed partial",
			readTool:        "read_file",
			readArgs:        func(path string) string { return `{"path":"` + path + `","offset":0,"limit":2}` },
			wantAllow:       false,
			wantAuditReason: anchorReasonPartialWindow,
			wantBlockedNote: "[fresh read required]",
		},
		{
			name:            "grep",
			readTool:        "grep",
			readArgs:        func(path string) string { return `{"pattern":"middle","path":"` + path + `"}` },
			wantAllow:       false,
			wantAuditReason: anchorReasonNoEligibleRead,
			wantBlockedNote: "[fresh read required]",
		},
		{
			name:            "bash cat",
			readTool:        "bash",
			readArgs:        func(path string) string { return `{"command":"cat ` + path + `"}` },
			wantAllow:       false,
			wantAuditReason: anchorReasonNoEligibleRead,
			wantBlockedNote: "[fresh read required]",
		},
	}
}

// TestAnchorSafetyGateReadFormMatrix drives each read form between a same-turn
// write and a delete_range retry, one subtest per cell, and pins the gate verdict.
func TestAnchorSafetyGateReadFormMatrix(t *testing.T) {
	for _, cell := range anchorMatrixCells() {
		t.Run(cell.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sample.txt")
			rel := "sample.txt" // builtin tools resolve args relative to Workspace.Dir
			sink := &anchorAuditSink{}
			var bashCalls int32

			reg := tool.NewRegistry()
			for _, tl := range (builtin.Workspace{Dir: dir, WriteRoots: []string{dir}}).Tools("write_file", "read_file", "delete_range", "grep") {
				reg.Add(tl)
			}
			if cell.readTool == "bash" {
				// The gate never inspects bash output — only receipts and
				// observations — so a recording stand-in exercises exactly the
				// code path a real shell `cat` would, without a shell.
				reg.Add(&fakeTool{name: "bash", readOnly: true, calls: &bashCalls})
			}
			if tl, ok := reg.Get(cell.readTool); !ok || tl == nil {
				t.Fatalf("read tool %q unavailable in registry", cell.readTool)
			}

			deleteArgs := `{"path":"` + rel + `","start_anchor":"start","end_anchor":"end"}`
			rounds := [][]provider.Chunk{
				{
					toolCallChunk("w", "write_file", `{"path":"`+rel+`","content":"`+matrixFileContentJSON+`"}`),
					{Type: provider.ChunkDone},
				},
				{toolCallChunk("r", cell.readTool, cell.readArgs(rel)), {Type: provider.ChunkDone}},
				{toolCallChunk("d", "delete_range", deleteArgs), {Type: provider.ChunkDone}},
				{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
			}
			prov := &scriptedProvider{name: "anchor-matrix", turns: rounds}
			a := New(prov, reg, NewSession(""), Options{}, sink)
			if err := a.Run(withNoClosedLoop(context.Background()), "cut the range after reading"); err != nil {
				t.Fatalf("Run: %v", err)
			}

			// One audit per delete_range attempt, verdict pinned per cell.
			if len(sink.audits) != 1 {
				t.Fatalf("anchor audits = %+v, want exactly one", sink.audits)
			}
			audit := sink.audits[0]
			if audit.Reason != cell.wantAuditReason {
				t.Fatalf("audit reason = %q, want %q (audit: %+v)", audit.Reason, cell.wantAuditReason, audit)
			}
			if audit.ShadowAllowed != cell.wantAllow {
				t.Fatalf("shadowAllowed = %v, want %v", audit.ShadowAllowed, cell.wantAllow)
			}

			// Behaviour: allow ⇒ the range is gone; block ⇒ message + file intact.
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if cell.wantAllow {
				if string(got) != "before\nafter\n" {
					t.Fatalf("allowed delete left content %q, want middle range removed", got)
				}
				if blocked := lastToolResult(a.Session(), "delete_range"); strings.Contains(blocked, "[fresh read required]") {
					t.Fatalf("allowed delete still blocked: %q", blocked)
				}
			} else {
				if string(got) != matrixFileContent {
					t.Fatalf("blocked delete mutated file: %q", got)
				}
				blocked := lastToolResult(a.Session(), "delete_range")
				if !strings.Contains(blocked, cell.wantBlockedNote) {
					t.Fatalf("blocked result = %q, want note %q", blocked, cell.wantBlockedNote)
				}
			}
		})
	}
}

// TestAnchorSafetyGateBlockedThenUnlockedByPromptRead is the e2e unlock case
// the blocked message itself prescribes: after a blocked delete_range the model
// follows the hint (read_file full file in a separate provider round), retries,
// and the gate lets the delete through — one block, one unlock, no loop.
func TestAnchorSafetyGateBlockedThenUnlockedByPromptRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	rel := "sample.txt"
	sink := &anchorAuditSink{}
	reg := tool.NewRegistry()
	for _, tl := range (builtin.Workspace{Dir: dir, WriteRoots: []string{dir}}).Tools("write_file", "read_file", "delete_range") {
		reg.Add(tl)
	}

	prov := &scriptedProvider{name: "anchor-unlock", turns: [][]provider.Chunk{
		{
			toolCallChunk("w", "write_file", `{"path":"`+rel+`","content":"`+matrixFileContentJSON+`"}`),
			toolCallChunk("d1", "delete_range", `{"path":"`+rel+`","start_anchor":"start","end_anchor":"end"}`),
			{Type: provider.ChunkDone},
		},
		{toolCallChunk("r", "read_file", `{"path":"`+rel+`"}`), {Type: provider.ChunkDone}},
		{
			toolCallChunk("d2", "delete_range", `{"path":"`+rel+`","start_anchor":"start","end_anchor":"end"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "cut the range, re-read and retry when blocked"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Attempt 1: blocked with the prescribed hint.
	results := toolResults(a.Session(), "delete_range")
	if len(results) != 2 {
		t.Fatalf("delete_range results = %d, want 2 (blocked + unlocked)", len(results))
	}
	if first := results[0]; !strings.Contains(first, "[fresh read required]") || !strings.Contains(first, "read_file") {
		t.Fatalf("first delete result = %q, want blocked with read_file guidance", first)
	}
	if second := results[1]; strings.Contains(second, "[fresh read required]") {
		t.Fatalf("retry after the prompt-prescribed read stayed blocked: %q", second)
	}

	// The retry actually cut the range — unlock is real, not a silent skip.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before\nafter\n" {
		t.Fatalf("unlocked delete content = %q, want middle range removed", got)
	}

	// Both attempts audited: block first, exact-match allow after the fresh read.
	if len(sink.audits) != 2 {
		t.Fatalf("anchor audits = %+v, want 2", sink.audits)
	}
	if sink.audits[0].ShadowAllowed || sink.audits[0].Reason != anchorReasonNoEligibleRead {
		t.Fatalf("first audit = %+v, want no-eligible-read block", sink.audits[0])
	}
	if !sink.audits[1].ShadowAllowed || sink.audits[1].Reason != anchorReasonExactMatch {
		t.Fatalf("second audit = %+v, want exact-match allow", sink.audits[1])
	}
}
