package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// answeringProvider is a minimal provider.Provider for the e2e: it inspects
// the sub-agent's user message and returns the scripted reply for whichever
// node is asking (routing by prompt content, order-independent).
type answeringProvider struct {
	routes []route
	last   []string
}

type route struct {
	match  string
	answer string
}

func (p *answeringProvider) Name() string { return "e2e-mock" }

// dummyReadOnlyTool gives the parent registry one read-only tool so the
// read-only confirm path has a non-empty intersection (production registries
// always carry read_file & friends).
type dummyReadOnlyTool struct{}

func (dummyReadOnlyTool) Name() string        { return "e2e_read" }
func (dummyReadOnlyTool) Description() string { return "e2e read-only probe" }
func (dummyReadOnlyTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (dummyReadOnlyTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}
func (dummyReadOnlyTool) ReadOnly() bool { return true }

func (p *answeringProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	// Find the last user message — it carries the composed child prompt.
	prompt := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			prompt = req.Messages[i].Content
			break
		}
	}
	p.last = append(p.last, prompt)
	answer := "ok"
	for _, r := range p.routes {
		if strings.Contains(prompt, r.match) {
			answer = r.answer
			break
		}
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: answer}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// e2eWorkflowDoc is the fan-out → join → confirm → gate-loop → strong-gate
// orchestration the ② acceptance runs end to end.
const e2eWorkflowDoc = `
version: 1
name: fanout-confirm-gate
report:
  conclusion: "Reviewed everything; unit gate green after repair."
  not_covered: ["e2e suite; needs docker"]
nodes:
  - id: hunt-a
    type: subagent
    prompt: "find bugs in src/a.go"
    result:
      - {name: findings, type: array, description: bugs found}
    findings_field: findings
  - id: hunt-b
    type: subagent
    prompt: "find bugs in src/b.go"
    result:
      - {name: findings, type: array, description: bugs found}
    findings_field: findings
  - id: join1
    type: join
  - id: conf-a
    type: confirm
    target: hunt-a
  - id: unit-gate
    type: gate
    command: "go test ./..."
    loop:
      fixer: fix-unit
      max_rounds: 4
  - id: fix-unit
    type: subagent
    prompt: "fix the failing tests"
  - id: ship-gate
    type: gate
    command: "go build ./..."
    tier: strong
  - id: done
    type: phase
    label: delivered
edges:
  - {from: hunt-a, to: join1}
  - {from: hunt-b, to: join1}
  - {from: join1, to: conf-a}
  - {from: conf-a, to: unit-gate}
  - {from: unit-gate, to: ship-gate, when: pass}
  - {from: ship-gate, to: done}`

// e2eHarness wires a REAL agent.TaskTool (the 2.9k-line sub-agent substrate),
// a scripted gate (fail → fixer → pass), and an event recorder.
type e2eHarness struct {
	prov *answeringProvider
	gate *fakeGate
	rec  *eventRecorder
	eng  *Engine
}

func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	prov := &answeringProvider{routes: []route{
		// Confirmer protocol check comes first (its prompt also contains
		// the evidence text, so match on the protocol sentence).
		{match: "independent confirmer sub-agent", answer: `{"confirmed": true, "reason": "reproduced"}`},
		{match: "find bugs in src/a.go", answer: `{"findings": [{"where": "src/a.go:42", "what": "nil deref", "evidence": "unguarded index", "severity": "high"}]}`},
		{match: "find bugs in src/b.go", answer: `{"findings": []}`},
		{match: "fix the failing tests", answer: "patched the test"},
	}}
	parentReg := tool.NewRegistry()
	parentReg.Add(dummyReadOnlyTool{})
	task := agent.NewTaskToolWithOptions(agent.TaskToolOptions{
		Provider:       prov,
		ParentRegistry: parentReg,
		MaxSteps:       20,
		SysPrompt:      "e2e sub-agent",
	}).WithTranscripts(agent.NewSubagentStore(t.TempDir()), t.TempDir(), "base-model", "base-effort")

	gate := newFakeGate()
	gate.byCmd["go test ./..."] = []GateResult{
		{ExitCode: 1, Output: "FAIL internal/x TestFoo"},
		{ExitCode: 0},
	}
	rec := &eventRecorder{}
	return &e2eHarness{prov: prov, gate: gate, rec: rec, eng: newTestEngine(NewTaskSubagentRunner(task), gate.run, rec)}
}

// TestTaskToolE2EDrivesWholeWorkflow is the ② acceptance e2e: a real
// agent.TaskTool executes every subagent/confirm/fixer node of a fan-out →
// join → confirm → gate-loop → strong-gate workflow, with typed results,
// confirmation promotion, gate feedback, and the event stream all live.
func TestTaskToolE2EDrivesWholeWorkflow(t *testing.T) {
	h := newE2EHarness(t)
	rep, err := h.eng.Run(context.Background(), mustParse(t, e2eWorkflowDoc))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	h.assertReport(t, rep)
	h.assertGateLoop(t)
	h.assertSubstrateDelivery(t)
	h.assertHappyStates(t)
}

// assertReport pins the WorkflowReport: confirmed finding, verified[]
// ordering (confirmation then both deciding gate commands), verbatim
// notCovered, and the author's conclusion template.
func (h *e2eHarness) assertReport(t *testing.T, rep *WorkflowReport) {
	t.Helper()
	if len(rep.Findings) != 1 || rep.Findings[0].Status != FindingVerified {
		t.Fatalf("findings = %+v, want one verified", rep.Findings)
	}
	wantVerified := []string{
		"independent confirmation of hunt-a by conf-a",
		"go test ./... (exit 0)",
		"go build ./... (exit 0)",
	}
	if len(rep.Verified) != len(wantVerified) {
		t.Fatalf("verified = %v, want %v", rep.Verified, wantVerified)
	}
	for i, w := range wantVerified {
		if rep.Verified[i] != w {
			t.Fatalf("verified = %v, want %v", rep.Verified, wantVerified)
		}
	}
	if len(rep.NotCovered) != 1 || rep.NotCovered[0] != "e2e suite; needs docker" {
		t.Fatalf("notCovered = %v", rep.NotCovered)
	}
	if rep.Conclusion != "Reviewed everything; unit gate green after repair." {
		t.Fatalf("conclusion = %q", rep.Conclusion)
	}
}

// assertGateLoop pins the fix-until-pass loop through the real substrate: one
// failed round, the fixer saw the failing output as feedback, second round
// passed — exactly two command runs.
func (h *e2eHarness) assertGateLoop(t *testing.T) {
	t.Helper()
	if h.gate.calls["go test ./..."] != 2 {
		t.Fatalf("gate calls = %d, want 2", h.gate.calls["go test ./..."])
	}
	for _, p := range h.prov.last {
		if strings.Contains(p, "fix the failing tests") &&
			strings.Contains(p, "FAIL internal/x TestFoo") &&
			strings.Contains(p, "round 1/4") {
			return
		}
	}
	t.Fatalf("fixer sub-agent never saw the gate feedback; prompts=%v", h.prov.last)
}

// assertSubstrateDelivery pins that the typed-result hint reached the
// producer through the real task substrate's prompt composition.
func (h *e2eHarness) assertSubstrateDelivery(t *testing.T) {
	t.Helper()
	if len(h.prov.last) == 0 || !strings.Contains(h.prov.last[0], "findings (array): bugs found") {
		t.Fatalf("producer prompt missing typed-result hint: %v", h.prov.last)
	}
}

// assertHappyStates pins the four states this happy-path workflow must
// cross (unconfirmed/failed are pinned by TestSixStatesCrossTheEventBus).
func (h *e2eHarness) assertHappyStates(t *testing.T) {
	t.Helper()
	saw := map[event.WorkflowNodeState]bool{}
	for _, e := range h.rec.events {
		saw[e.State] = true
	}
	for _, want := range []event.WorkflowNodeState{
		event.WorkflowNodeQueued, event.WorkflowNodeRunning,
		event.WorkflowNodeCompleted, event.WorkflowNodeGatePassed,
	} {
		if !saw[want] {
			t.Fatalf("missing state %q in e2e stream", want)
		}
	}
}
