package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// Harness:

// fakeSubs is a scripted SubagentRunner: answers keyed by node id (taken
// from the request), with an optional per-node error.
type fakeSubs struct {
	answers  map[string]string
	errors   map[string]error
	prompts  []SubagentRequest
	readOnly map[string]bool
}

func newFakeSubs() *fakeSubs {
	return &fakeSubs{
		answers:  map[string]string{},
		errors:   map[string]error{},
		readOnly: map[string]bool{},
	}
}

func (f *fakeSubs) Run(_ context.Context, req SubagentRequest) (string, error) {
	f.prompts = append(f.prompts, req)
	f.readOnly[req.NodeID] = req.ReadOnly
	if err := f.errors[req.NodeID]; err != nil {
		return "", err
	}
	if a, ok := f.answers[req.NodeID]; ok {
		return a, nil
	}
	return "ok:" + req.NodeID, nil
}

// fakeGate is a scripted GateRunner: results per command in call order.
type fakeGate struct {
	byCmd map[string][]GateResult
	calls map[string]int
	errs  map[string]error
}

func newFakeGate() *fakeGate {
	return &fakeGate{byCmd: map[string][]GateResult{}, calls: map[string]int{}, errs: map[string]error{}}
}

func (g *fakeGate) run(_ context.Context, command string, _ time.Duration) (GateResult, error) {
	if err := g.errs[command]; err != nil {
		return GateResult{}, err
	}
	seq := g.byCmd[command]
	i := g.calls[command]
	g.calls[command]++
	if i >= len(seq) {
		if len(seq) == 0 {
			return GateResult{ExitCode: 0}, nil
		}
		return seq[len(seq)-1], nil
	}
	return seq[i], nil
}

// eventRecorder collects workflow_node transitions off the bus.
type eventRecorder struct{ events []event.WorkflowNodeInfo }

func (r *eventRecorder) Emit(e event.Event) {
	if e.Kind == event.WorkflowNode && e.Workflow != nil {
		r.events = append(r.events, *e.Workflow)
	}
}

func (r *eventRecorder) statesOf(nodeID string) []event.WorkflowNodeState {
	var out []event.WorkflowNodeState
	for _, e := range r.events {
		if e.NodeID == nodeID {
			out = append(out, e.State)
		}
	}
	return out
}

func (r *eventRecorder) hasState(nodeID string, st event.WorkflowNodeState) bool {
	for _, e := range r.events {
		if e.NodeID == nodeID && e.State == st {
			return true
		}
	}
	return false
}

func newTestEngine(subs SubagentRunner, gate GateRunner, rec *eventRecorder) *Engine {
	var sink event.Sink
	if rec != nil {
		sink = rec
	}
	return New(subs, gate, sink)
}

// ② subagent node execution tests:

// TestSubagentNodeRunsAndBagsFindings is the ② e2e: producer node with a
// typed result answers JSON, findings enter the bag unconfirmed, an ancestor
// ref resolves into the consumer's prompt, and an illegal ref is rejected at
// parse time (validation precedes execution).
func TestSubagentNodeRunsAndBagsFindings(t *testing.T) {
	if _, err := Parse([]byte(`
version: 1
name: hunt
nodes:
  - {id: a, type: subagent, prompt: p}
  - {id: b, type: subagent, prompt: "{{ghost}}"}
edges: []`)); err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("err = %v, want unknown-node rejection before any run", err)
	}
	subs := newFakeSubs()
	subs.answers["hunt"] = `{"findings": [{"where": "src/a.go:42", "what": "nil deref", "evidence": "a[i] without guard", "severity": "high"}]}`
	rec := &eventRecorder{}
	eng := newTestEngine(subs, newFakeGate().run, rec)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - id: hunt
    type: subagent
    prompt: "find bugs"
    result:
      - {name: findings, type: array, description: bugs found}
    findings_field: findings
  - id: report
    type: subagent
    prompt: "summarize {{hunt.findings}}"
edges:
  - {from: hunt, to: report}`)
	rep, err := eng.Run(context.Background(), wf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Status != FindingUnconfirmed {
		t.Fatalf("findings = %+v, want one unconfirmed", rep.Findings)
	}
	// The typed-result hint must be part of what the producer read.
	if !strings.Contains(subs.prompts[0].Prompt, "findings (array): bugs found") {
		t.Fatalf("producer prompt missing result hint:\n%s", subs.prompts[0].Prompt)
	}
	// The consumer prompt must carry the interpolated ancestor output (the
	// findings array re-marshaled compactly with map keys sorted — Go's
	// encoding/json orders them alphabetically).
	want := `[{"evidence":"a[i] without guard","severity":"high","what":"nil deref","where":"src/a.go:42"}]`
	if !strings.Contains(subs.prompts[1].Prompt, want) {
		t.Fatalf("consumer prompt missing ancestor ref, got:\n%s", subs.prompts[1].Prompt)
	}
	if rep.Conclusion == "" || len(rep.Verified) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

// TestSubagentFailureAbortsRun pins fail-fast: a producer error stops the
// run with the failing node named, and the report still comes back.
func TestSubagentFailureAbortsRun(t *testing.T) {
	subs := newFakeSubs()
	subs.errors["boom"] = fmt.Errorf("provider exploded")
	rec := &eventRecorder{}
	eng := newTestEngine(subs, newFakeGate().run, rec)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - {id: boom, type: subagent, prompt: p}
  - {id: after, type: phase, label: L}
edges:
  - {from: boom, to: after}`)
	rep, err := eng.Run(context.Background(), wf)
	if err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("err = %v", err)
	}
	if rep == nil || !strings.Contains(rep.Conclusion, `stopped at node "boom"`) {
		t.Fatalf("report = %+v", rep)
	}
	if !rec.hasState("boom", event.WorkflowNodeFailed) {
		t.Fatalf("events = %+v, want boom failed", rec.events)
	}
	if rec.hasState("after", event.WorkflowNodeRunning) {
		t.Fatalf("after must not run: %+v", rec.events)
	}
}

// ③ deterministic gate tests:

// TestGateBranchOnExitCode pins the exit-code-as-value branch: pass edge
// activates on exit 0, fail edge activates on exit ≠ 0, and pass edges do
// NOT activate on failure.
func TestGateBranchOnExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  []GateResult
		want string // node that should run
		skip string // node that must stay queued
	}{
		{"pass branch", []GateResult{{ExitCode: 0}}, "ok-side", "fail-side"},
		{"fail branch", []GateResult{{ExitCode: 2, Output: "boom"}}, "fail-side", "ok-side"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := newFakeGate()
			gate.byCmd["check"] = tc.seq
			rec := &eventRecorder{}
			eng := newTestEngine(newFakeSubs(), gate.run, rec)
			wf := mustParse(t, `
version: 1
name: x
nodes:
  - {id: g, type: gate, command: check}
  - {id: ok-side, type: phase, label: OK}
  - {id: fail-side, type: phase, label: BAD}
edges:
  - {from: g, to: ok-side, when: pass}
  - {from: g, to: fail-side, when: fail}`)
			rep, err := eng.Run(context.Background(), wf)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !rec.hasState(tc.want, event.WorkflowNodeCompleted) {
				t.Fatalf("%q should run; events=%+v", tc.want, rec.events)
			}
			for _, e := range rec.events {
				if e.NodeID == tc.skip && e.State != event.WorkflowNodeQueued {
					t.Fatalf("%q must stay queued, got %s", tc.skip, e.State)
				}
			}
			_ = rep
		})
	}
}

// TestGateFailureWithoutFailBranchFailsRun: a failing gate with only pass
// edges strands the graph — the engine must refuse rather than quietly end.
func TestGateFailureWithoutFailBranchFailsRun(t *testing.T) {
	gate := newFakeGate()
	gate.byCmd["check"] = []GateResult{{ExitCode: 1, Output: "stderr text"}}
	eng := newTestEngine(newFakeSubs(), gate.run, nil)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - {id: g, type: gate, command: check}
  - {id: next, type: phase, label: L}
edges:
  - {from: g, to: next, when: pass}`)
	_, err := eng.Run(context.Background(), wf)
	if err == nil || !strings.Contains(err.Error(), "no fail branch") {
		t.Fatalf("err = %v, want no-fail-branch failure", err)
	}
}

// TestGateLoopFixesUntilPass pins the fix-until-pass loop: rounds 1..2 fail
// with stderr fed to the fixer, round 3 passes, gate ends gate_passed and
// the fixer saw both the round number and the failing output.
func TestGateLoopFixesUntilPass(t *testing.T) {
	gate := newFakeGate()
	gate.byCmd["test"] = []GateResult{
		{ExitCode: 1, Output: "FAIL foo_test.go:17"},
		{ExitCode: 1, Output: "FAIL bar_test.go:3"},
		{ExitCode: 0},
	}
	subs := newFakeSubs()
	rec := &eventRecorder{}
	eng := newTestEngine(subs, gate.run, rec)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - id: t
    type: gate
    command: test
    loop:
      fixer: fx
      max_rounds: 5
  - id: fx
    type: subagent
    prompt: "fix the tests"
  - id: done
    type: phase
    label: L
edges:
  - {from: t, to: done, when: pass}`)
	_, err := eng.Run(context.Background(), wf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gate.calls["test"] != 3 {
		t.Fatalf("gate calls = %d, want 3", gate.calls["test"])
	}
	if len(subs.prompts) != 2 {
		t.Fatalf("fixer runs = %d, want 2", len(subs.prompts))
	}
	fb := subs.prompts[0].Prompt
	if !strings.Contains(fb, "fix the tests") || !strings.Contains(fb, "round 1/5") || !strings.Contains(fb, "FAIL foo_test.go:17") {
		t.Fatalf("fixer prompt missing feedback:\n%s", fb)
	}
	if !rec.hasState("t", event.WorkflowNodeGatePassed) {
		t.Fatalf("events = %+v, want gate_passed", rec.events)
	}
	// Fixer's own transitions are visible with the loop round.
	states := rec.statesOf("fx")
	if len(states) < 4 { // queued + (running, completed) × 2 rounds
		t.Fatalf("fx states = %v", states)
	}
}

// TestGateLoopRoundCapStopsAtMaxRounds pins the cap: a never-passing gate
// stops after exactly max_rounds command runs and max_rounds-1 fixer rounds.
func TestGateLoopRoundCapStopsAtMaxRounds(t *testing.T) {
	gate := newFakeGate()
	gate.byCmd["test"] = []GateResult{{ExitCode: 1, Output: "always red"}}
	subs := newFakeSubs()
	rec := &eventRecorder{}
	eng := newTestEngine(subs, gate.run, rec)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - id: t
    type: gate
    command: test
    loop: {fixer: fx, max_rounds: 3}
  - id: fx
    type: subagent
    prompt: "fix"
  - id: fallback
    type: phase
    label: L
edges:
  - {from: t, to: fallback, when: fail}`)
	_, err := eng.Run(context.Background(), wf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gate.calls["test"] != 3 {
		t.Fatalf("gate calls = %d, want exactly 3 (cap)", gate.calls["test"])
	}
	if len(subs.prompts) != 2 {
		t.Fatalf("fixer runs = %d, want max_rounds-1 = 2", len(subs.prompts))
	}
	if !rec.hasState("t", event.WorkflowNodeFailed) {
		t.Fatalf("events = %+v, want gate failed after cap", rec.events)
	}
}

// TestGateWorldFailureFailsRun: timeout/launch errors are NOT branch values.
func TestGateWorldFailureFailsRun(t *testing.T) {
	gate := newFakeGate()
	gate.errs["hang"] = fmt.Errorf("command timed out (> 1s)")
	eng := newTestEngine(newFakeSubs(), gate.run, nil)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - {id: g, type: gate, command: hang}
  - {id: fb, type: phase, label: L}
edges:
  - {from: g, to: fb, when: fail}`)
	_, err := eng.Run(context.Background(), wf)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want world-failure", err)
	}
}

// TestStrongGateTierReachableAtRuntime: tier is a validation-time property;
// runtime just executes it — this pins that a strong gate records its
// command in verified like any other passing gate.
func TestStrongGateTierReachableAtRuntime(t *testing.T) {
	gate := newFakeGate()
	gate.byCmd["ship"] = []GateResult{{ExitCode: 0}}
	rec := &eventRecorder{}
	eng := newTestEngine(newFakeSubs(), gate.run, rec)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - {id: a, type: subagent, prompt: p}
  - {id: g, type: gate, command: ship, tier: strong}
  - {id: end, type: phase, label: L}
edges:
  - {from: a, to: g}
  - {from: g, to: end}`)
	rep, err := eng.Run(context.Background(), wf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Verified) != 1 || rep.Verified[0] != "ship (exit 0)" {
		t.Fatalf("verified = %v", rep.Verified)
	}
}
