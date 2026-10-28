package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl-1218 ②（experimental_multi_edit_evidence_narrow）验收矩阵：
//
//	E2-off（默认）：现行连坐逐字节保留——outstanding 证据要求连带拦截
//	              无关工具调用（跨会话发信类）与一切不可声明写者。
//	E2-on ：outstanding 只拦「可证明写路径涉及该文件」的写类操作；
//	              无关调用放行；不可分析的 bash、声明失败的写者仍拦（闭失败）。
//	三关等价：narrow 关即基线（本文件 E2-off + 既有
//	              TestEvidenceGateBlocksUnknownScopeWriterAfterABlock 钉）。

// narrowFixture builds an agent with a declarable writer (already blocked once
// so a requirement is outstanding) plus one extra tool.
func narrowFixture(t *testing.T, extra tool.Tool, narrow bool) *Agent {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}})
	if extra != nil {
		reg.Add(extra)
	}
	opts := Options{}
	if narrow {
		opts.MultiEditEvidenceNarrow = true
	}
	a := New(&userInputCaptureProvider{}, reg, NewSession("system"), opts, event.Discard)
	a.task.ledger = evidence.NewLedger()
	if out, blocked := runEvidenceGate(a, "/w/a.go"); !blocked {
		t.Fatalf("fixture missing initial block: %+v", out)
	}
	if len(a.turn.evidenceBlocked.snapshot()) != 1 {
		t.Fatalf("fixture requirement missing: %v", a.turn.evidenceBlocked.snapshot())
	}
	return a
}

// TestEvidenceNarrowOffKeepsCollectiveBlock pins E2-off: an opaque unrelated
// tool (messaging-shaped) stays blocked while a requirement is outstanding,
// and the evidence-only cascade keeps skipping it.
func TestEvidenceNarrowOffKeepsCollectiveBlock(t *testing.T) {
	a := narrowFixture(t, undeclaredWriter{name: "send_note"}, false)
	plan := &toolCallPlan{call: provider.ToolCall{Name: "send_note", Arguments: `{}`}}
	out, blocked := a.applyEvidenceGates(context.Background(), plan)
	if !blocked || !strings.Contains(out.output, "cannot declare which files it changes") {
		t.Fatalf("off must keep the collective block on an opaque writer: %+v blocked=%v", out, blocked)
	}
	// Cascade face: an evidence-only barrier skips the unrelated call too.
	a.mutationDependencyBarrier.Store(&mutationBarrierCause{evidenceOnly: true, blockingPhase: "blocked"})
	plan = &toolCallPlan{call: provider.ToolCall{Name: "send_note", Arguments: `{}`}, effects: evidence.ToolEffects{StateMutation: true}}
	if out, blocked := a.applyMutationDependencyBarrier(plan); !blocked {
		t.Fatalf("off must keep the cascade skipping the unrelated call: %+v blocked=%v", out, blocked)
	}
}

// TestEvidenceNarrowPassesUnrelatedCalls pins E2-on: messaging-shaped opaque
// calls pass both the undeclared-writer gate and the evidence-only cascade.
func TestEvidenceNarrowPassesUnrelatedCalls(t *testing.T) {
	a := narrowFixture(t, undeclaredWriter{name: "send_note"}, true)
	plan := &toolCallPlan{call: provider.ToolCall{Name: "send_note", Arguments: `{}`}}
	if out, blocked := a.applyEvidenceGates(context.Background(), plan); blocked {
		t.Fatalf("on must pass an unrelated opaque call: %+v", out)
	}
	a.mutationDependencyBarrier.Store(&mutationBarrierCause{evidenceOnly: true, blockingPhase: "blocked"})
	plan = &toolCallPlan{call: provider.ToolCall{Name: "send_note", Arguments: `{}`}, effects: evidence.ToolEffects{StateMutation: true}}
	if out, blocked := a.applyMutationDependencyBarrier(plan); blocked {
		t.Fatalf("on must pass the unrelated call through the cascade: %+v", out)
	}
}

// TestEvidenceNarrowStillBlocksTheInvolvedFile pins E2-on's protective half:
// shell writes whose static scope involves the outstanding file stay blocked
// (analyzable), an unanalyzable command stays blocked (fail-closed), and a
// declarable writer targeting the file stays blocked in the cascade.
func TestEvidenceNarrowStillBlocksTheInvolvedFile(t *testing.T) {
	a := narrowFixture(t, undeclaredWriter{name: "bash"}, true)
	for _, command := range []string{`echo x > /w/a.go`, `python write.py`} {
		plan := &toolCallPlan{call: provider.ToolCall{Name: "bash", Arguments: `{"command":` + quoteJSON(command) + `}`}}
		if out, blocked := a.applyEvidenceGates(context.Background(), plan); !blocked {
			t.Fatalf("on must still block a write involving the file (%q): %+v", command, out)
		}
	}
	// Cascade face: a declarable writer naming the file stays blocked.
	a.mutationDependencyBarrier.Store(&mutationBarrierCause{evidenceOnly: true, blockingPhase: "blocked"})
	args := `{"path":"/w/a.go","old_string":"alpha","new_string":"beta"}`
	plan := &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: args}, effects: evidence.ToolEffects{StateMutation: true}}
	if out, blocked := a.applyMutationDependencyBarrier(plan); !blocked {
		t.Fatalf("on must keep the cascade on a write to the involved file: %+v blocked=%v", out, blocked)
	}
}

// TestEvidenceNarrowPassesDisjointWriters pins E2-on's precision: a
// declarable writer naming ANOTHER file passes the cascade, and a shell
// command writing elsewhere passes the gate.
func TestEvidenceNarrowPassesDisjointWriters(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha"),
	}})
	// A second, differently named declarable writer so ResolveCall stays
	// unambiguous and the disjoint declaration is reachable.
	reg.Add(namedEvidenceWriter{evidenceWriter: evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/other.go", WholeFile: true, Hashes: hashesFor("omega"),
	}}, name: "other_writer"})
	reg.Add(undeclaredWriter{name: "bash"})
	a := New(&userInputCaptureProvider{}, reg, NewSession("system"), Options{MultiEditEvidenceNarrow: true}, event.Discard)
	a.task.ledger = evidence.NewLedger()
	if _, blocked := runEvidenceGate(a, "/w/a.go"); !blocked {
		t.Fatal("fixture missing initial block")
	}
	// A shell command whose static scope is disjoint passes the gate.
	plan := &toolCallPlan{call: provider.ToolCall{Name: "bash", Arguments: `{"command":"echo x > /w/b.txt"}`}}
	if out, blocked := a.applyEvidenceGates(context.Background(), plan); blocked {
		t.Fatalf("on must pass a shell write disjoint from the file: %+v", out)
	}
	// Cascade face: a declarable writer on a disjoint file passes.
	a.mutationDependencyBarrier.Store(&mutationBarrierCause{evidenceOnly: true, blockingPhase: "blocked"})
	plan = &toolCallPlan{
		call:    provider.ToolCall{Name: "other_writer", Arguments: `{"path":"/w/other.go"}`},
		effects: evidence.ToolEffects{StateMutation: true},
	}
	if out, blocked := a.applyMutationDependencyBarrier(plan); blocked {
		t.Fatalf("on must pass a disjoint declarable writer through the cascade: %+v blocked=%v", out, blocked)
	}
}

// namedEvidenceWriter renames the declarable fixture writer so a registry can
// carry two declarers without making ResolveCall ambiguous.
type namedEvidenceWriter struct {
	evidenceWriter
	name string
}

func (w namedEvidenceWriter) Name() string { return w.name }

// quoteJSON embeds a shell command into a JSON string literal for fixtures.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
