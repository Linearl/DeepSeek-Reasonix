package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
)

// ④ independent confirmation tests:

// confirmWorkflow builds: producer (findings) → confirm(target=producer).
func confirmWorkflow(t *testing.T) *Workflow {
	t.Helper()
	return mustParse(t, `
version: 1
name: x
nodes:
  - id: hunt
    type: subagent
    prompt: "find bugs"
    result:
      - {name: findings, type: array, description: bugs}
    findings_field: findings
  - id: conf
    type: confirm
    target: hunt
edges: []`)
}

const huntAnswer = `{"findings": [{"where": "src/a.go:42", "what": "nil deref", "evidence": "unguarded index", "severity": "high"}]}`

// TestConfirmSuccessPromotesFindings: confirmer reproduces → findings flip
// unconfirmed → verified in place, confirm recorded in verified[].
func TestConfirmSuccessPromotesFindings(t *testing.T) {
	subs := newFakeSubs()
	subs.answers["hunt"] = huntAnswer
	subs.answers["conf"] = `{"confirmed": true, "reason": "reproduced from evidence"}`
	eng := newTestEngine(subs, newFakeGate().run, nil)
	rep, err := eng.Run(context.Background(), confirmWorkflow(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Status != FindingVerified {
		t.Fatalf("findings = %+v, want verified", rep.Findings)
	}
	if len(rep.Verified) != 1 || !strings.Contains(rep.Verified[0], "independent confirmation of hunt by conf") {
		t.Fatalf("verified = %v", rep.Verified)
	}
	// The confirmer must take the read-only path — reproduce, never edit.
	if !subs.readOnly["conf"] {
		t.Fatalf("confirmer must run read-only, prompts=%+v", subs.prompts)
	}
	// The confirmer receives the producer's EVIDENCE (its typed result).
	if !strings.Contains(subs.prompts[1].Prompt, "src/a.go:42") ||
		!strings.Contains(subs.prompts[1].Prompt, "independent confirmer") {
		t.Fatalf("confirmer prompt missing evidence/protocol:\n%s", subs.prompts[1].Prompt)
	}
}

// TestConfirmFailureKeepsFindingsUnconfirmed pins the core ④ semantics: a
// verdict of false KEEPS the finding, labels it unconfirmed, does NOT abort
// the run, and the panel gets the unconfirmed transition on both the confirm
// node and the target.
func TestConfirmFailureKeepsFindingsUnconfirmed(t *testing.T) {
	subs := newFakeSubs()
	subs.answers["hunt"] = huntAnswer
	subs.answers["conf"] = `{"confirmed": false, "reason": "could not reproduce"}`
	rec := &eventRecorder{}
	eng := newTestEngine(subs, newFakeGate().run, rec)
	rep, err := eng.Run(context.Background(), confirmWorkflow(t))
	if err != nil {
		t.Fatalf("Run must not fail on a negative verdict: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Status != FindingUnconfirmed {
		t.Fatalf("findings = %+v, want kept + unconfirmed", rep.Findings)
	}
	if len(rep.Verified) != 0 {
		t.Fatalf("verified = %v, want empty", rep.Verified)
	}
	if !rec.hasState("conf", event.WorkflowNodeUnconfirmed) ||
		!rec.hasState("hunt", event.WorkflowNodeUnconfirmed) {
		t.Fatalf("events = %+v, want unconfirmed on conf and hunt", rec.events)
	}
}

// TestConfirmConfirmerErrorKeepsFindingsUnconfirmed: a broken confirmer is
// ALSO not a workflow failure — the doubt label carries the finding.
func TestConfirmConfirmerErrorKeepsFindingsUnconfirmed(t *testing.T) {
	subs := newFakeSubs()
	subs.answers["hunt"] = huntAnswer
	subs.errors["conf"] = fmt.Errorf("provider 503")
	eng := newTestEngine(subs, newFakeGate().run, nil)
	rep, err := eng.Run(context.Background(), confirmWorkflow(t))
	if err != nil {
		t.Fatalf("Run must not fail on confirmer error: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Status != FindingUnconfirmed {
		t.Fatalf("findings = %+v, want kept + unconfirmed", rep.Findings)
	}
}

// TestUnconfirmableGarbageVerdict: an unparseable confirmer answer means NOT
// confirmed — doubt must survive sloppy prose.
func TestUnconfirmableGarbageVerdict(t *testing.T) {
	for _, ans := range []string{"looks fine to me", "maybe {confirmed: yes}", ""} {
		if parseConfirmed(ans) {
			t.Fatalf("parseConfirmed(%q) = true, want false", ans)
		}
	}
	if !parseConfirmed(`{"confirmed": true}`) || !parseConfirmed("blah\n{\"confirmed\":true}\ntrailer") {
		t.Fatal("clean verdicts must parse")
	}
}

// ⑤ report salvage tests:

// TestReportSalvageOnMidRunFailure pins 落袋: node1's finding was bagged
// before node2 died; Run returns the partial report carrying it.
func TestReportSalvageOnMidRunFailure(t *testing.T) {
	subs := newFakeSubs()
	subs.answers["hunt"] = huntAnswer
	subs.errors["doomed"] = fmt.Errorf("node exploded")
	rec := &eventRecorder{}
	eng := newTestEngine(subs, newFakeGate().run, rec)
	wf := mustParse(t, `
version: 1
name: x
report:
  not_covered: ["perf suite; no bench machine"]
nodes:
  - id: hunt
    type: subagent
    prompt: "find bugs"
    result:
      - {name: findings, type: array, description: bugs}
    findings_field: findings
  - id: doomed
    type: subagent
    prompt: "wip"
  - id: never
    type: phase
    label: L
edges:
  - {from: hunt, to: doomed}
  - {from: doomed, to: never}`)
	rep, err := eng.Run(context.Background(), wf)
	if err == nil || !strings.Contains(err.Error(), "node exploded") {
		t.Fatalf("err = %v", err)
	}
	if rep == nil {
		t.Fatal("report must survive the mid-run failure (落袋)")
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Where != "src/a.go:42" {
		t.Fatalf("findings = %+v, want the pre-failure finding", rep.Findings)
	}
	if len(rep.NotCovered) != 1 || rep.NotCovered[0] != "perf suite; no bench machine" {
		t.Fatalf("notCovered must pass through verbatim: %v", rep.NotCovered)
	}
	// notCovered stays STRICT: nothing auto-appended for the nodes that
	// never ran (a check that exists but did not run is unverified, not
	// notCovered — the conclusion says the run stopped instead).
	if len(rep.NotCovered) != 1 {
		t.Fatalf("notCovered grew: %v", rep.NotCovered)
	}
	if !strings.Contains(rep.Conclusion, `stopped at node "doomed"`) {
		t.Fatalf("conclusion = %q", rep.Conclusion)
	}
	if !rec.hasState("never", event.WorkflowNodeQueued) || rec.hasState("never", event.WorkflowNodeCompleted) {
		t.Fatalf("never must stay queued: %+v", rec.events)
	}
}

// TestReportSalvageOnCancellation: killing the run mid-flight (ctx cancel)
// still hands back what was reported before the kill.
func TestReportSalvageOnCancellation(t *testing.T) {
	subs := &blockingSubs{answers: map[string]string{
		"hunt": huntAnswer,
	}, cancelAfterFirst: true}
	eng := newTestEngine(subs, newFakeGate().run, nil)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - id: hunt
    type: subagent
    prompt: "find bugs"
    result:
      - {name: findings, type: array, description: bugs}
    findings_field: findings
  - id: slow
    type: subagent
    prompt: "slow work"
edges:
  - {from: hunt, to: slow}`)
	rep, err := eng.Run(context.Background(), wf)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if rep == nil || len(rep.Findings) != 1 {
		t.Fatalf("report = %+v, want salvaged finding", rep)
	}
}

// blockingSubs cancels the context on its second call, simulating a user
// killing the run mid-flight.
type blockingSubs struct {
	answers          map[string]string
	prompts          []SubagentRequest
	cancelAfterFirst bool
	calls            int
	onSecond         func()
}

func (b *blockingSubs) Run(ctx context.Context, req SubagentRequest) (string, error) {
	b.prompts = append(b.prompts, req)
	b.calls++
	if b.calls >= 2 {
		if b.onSecond != nil {
			b.onSecond()
		}
		if b.cancelAfterFirst {
			return "", context.Canceled
		}
	}
	if a, ok := b.answers[req.NodeID]; ok {
		return a, nil
	}
	return "ok", nil
}

// ⑥ six-state event stream tests:

// TestSixStatesCrossTheEventBus runs one workflow that exercises all six
// states in a single stream and pins payload fields (run id, workflow, node
// type, phase label after a phase node opens).
func TestSixStatesCrossTheEventBus(t *testing.T) {
	subs := newFakeSubs()
	subs.answers["hunt"] = huntAnswer
	subs.answers["conf"] = `{"confirmed": false, "reason": "nope"}` // unconfirmed
	subs.errors["doomed"] = fmt.Errorf("kaboom")                    // failed
	gate := newFakeGate()
	gate.byCmd["check"] = []GateResult{{ExitCode: 0}} // gate_passed
	rec := &eventRecorder{}
	eng := newTestEngine(subs, gate.run, rec)
	wf := mustParse(t, `
version: 1
name: six-states
nodes:
  - id: setup
    type: phase
    label: prepare
  - id: hunt
    type: subagent
    prompt: "find bugs"
    result:
      - {name: findings, type: array, description: bugs}
    findings_field: findings
  - id: conf
    type: confirm
    target: hunt
  - id: check
    type: gate
    command: check
  - id: doomed
    type: subagent
    prompt: "wip"
edges:
  - {from: setup, to: hunt}
  - {from: hunt, to: check}
  - {from: check, to: doomed, when: pass}`)
	_, _ = eng.Run(context.Background(), wf)

	saw := map[event.WorkflowNodeState]bool{}
	for _, e := range rec.events {
		saw[e.State] = true
		if e.RunID == "" || e.Workflow != "six-states" || e.NodeType == "" {
			t.Fatalf("payload incomplete: %+v", e)
		}
	}
	for _, want := range []event.WorkflowNodeState{
		event.WorkflowNodeQueued, event.WorkflowNodeRunning, event.WorkflowNodeCompleted,
		event.WorkflowNodeUnconfirmed, event.WorkflowNodeFailed, event.WorkflowNodeGatePassed,
	} {
		if !saw[want] {
			t.Fatalf("missing state %q in stream: %+v", want, rec.events)
		}
	}
	// Phase label rides the events after the phase node ran.
	var sawPhase bool
	for _, e := range rec.events {
		if e.Phase == "prepare" {
			sawPhase = true
		}
	}
	if !sawPhase {
		t.Fatalf("no event carried phase label; events=%+v", rec.events)
	}
	// Queued fires once per node up front: hunt got queued then ran.
	if st := rec.statesOf("hunt"); st[0] != event.WorkflowNodeQueued {
		t.Fatalf("hunt states = %v, want queued first", st)
	}
}

// TestConfirmSkippedWithSkippedTarget: a confirm whose target sat on an
// untaken branch must skip alongside it, not confirm empty air.
func TestConfirmSkippedWithSkippedTarget(t *testing.T) {
	subs := newFakeSubs()
	gate := newFakeGate()
	gate.byCmd["g"] = []GateResult{{ExitCode: 1}}
	rec := &eventRecorder{}
	eng := newTestEngine(subs, gate.run, rec)
	wf := mustParse(t, `
version: 1
name: x
nodes:
  - id: g
    type: gate
    command: g
  - id: hunt
    type: subagent
    prompt: "find"
    result:
      - {name: findings, type: array, description: b}
    findings_field: findings
  - id: conf
    type: confirm
    target: hunt
  - id: other
    type: phase
    label: L
edges:
  - {from: g, to: hunt, when: pass}
  - {from: g, to: other, when: fail}`)
	if _, err := eng.Run(context.Background(), wf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// hunt sits behind the pass edge of a failing gate → skipped; conf must
	// skip with it (its only dependency is the target).
	for _, id := range []string{"hunt", "conf"} {
		for _, e := range rec.events {
			if e.NodeID == id && e.State != event.WorkflowNodeQueued {
				t.Fatalf("%s must stay queued, saw %s", id, e.State)
			}
		}
	}
	for _, p := range subs.prompts {
		if p.NodeID == "conf" {
			t.Fatalf("confirmer must not run when its target never did: %+v", subs.prompts)
		}
	}
}

// Strict notCovered / verified semantics:

// TestVerifiedRecordsDecidingCommandsOnly: verified[] names exactly what
// decided — passing gates and successful confirmations, nothing else.
func TestVerifiedRecordsDecidingCommandsOnly(t *testing.T) {
	subs := newFakeSubs()
	subs.answers["conf"] = `{"confirmed": true}`
	gate := newFakeGate()
	gate.byCmd["unit"] = []GateResult{{ExitCode: 0}}
	eng := newTestEngine(subs, gate.run, nil)
	wf := mustParse(t, `
version: 1
name: x
report:
  conclusion: "all clear {{g}}"
nodes:
  - {id: g, type: gate, command: unit}
  - {id: end, type: phase, label: L}
edges:
  - {from: g, to: end, when: pass}`)
	rep, err := eng.Run(context.Background(), wf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Verified) != 1 || rep.Verified[0] != "unit (exit 0)" {
		t.Fatalf("verified = %v, want exactly the deciding command", rep.Verified)
	}
	if rep.NotCovered != nil {
		t.Fatalf("notCovered must stay empty unless declared: %v", rep.NotCovered)
	}
	// Conclusion template interpolates the gate's output.
	if rep.Conclusion != "all clear " { // gate output from fake is ""
		t.Fatalf("conclusion = %q", rep.Conclusion)
	}
}
