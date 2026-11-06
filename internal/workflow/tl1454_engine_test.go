package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// presetShapeGoldenEvents is the node event sequence the pre-tl1454 engine
// (base 2ac2ea4c8, captured by running this fixture against an extracted copy
// of that package) emits for presetShapeDoc. Off-state equivalence: a
// document that declares no budget/timeout must run byte-identically.
const presetShapeGoldenEvents = `phase-scan:queued
scan:queued
verify:queued
gh_gate:queued
triage:queued
fallback:queued
phase-scan:running
phase-scan:completed
scan:running
scan:completed
verify:running
gh_gate:running:r1
gh_gate:gate_passed:r1:exit 0
triage:running
verify:completed
triage:completed`

type observations struct {
	mu   sync.Mutex
	list []NodeObservation
}

func (o *observations) hook(n NodeObservation) {
	o.mu.Lock()
	o.list = append(o.list, n)
	o.mu.Unlock()
}

func (o *observations) reasonOf(node string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, n := range o.list {
		if n.NodeID == node && n.Reason != "" {
			return n.Reason
		}
	}
	return ""
}

func (o *observations) tokensOf(node string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	total := 0
	for _, n := range o.list {
		if n.NodeID == node {
			total += n.Tokens
		}
	}
	return total
}

func (o *observations) contractOf(node string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, n := range o.list {
		if n.NodeID == node && n.Contract != "" {
			return n.Contract
		}
	}
	return ""
}

// A1/A2: a preset-shaped document runs exactly as before tl1454.
func TestTL1454PresetEventSequenceUnchanged(t *testing.T) {
	wf := mustParse(t, presetShapeDoc)
	log := &eventLog{}
	obs := &observations{}
	eng := New(presetShapeRunner(), presetGateRunner, log)
	eng.Observe = obs.hook
	rep, err := eng.RunWithID(context.Background(), wf, "wf-golden")
	if err != nil {
		t.Fatalf("preset run failed: %v", err)
	}
	if got := log.joined(); got != presetShapeGoldenEvents {
		t.Fatalf("event sequence drifted from the pre-tl1454 engine:\n%s\n--- want ---\n%s", got, presetShapeGoldenEvents)
	}
	if rep.Conclusion != "done: one bug" {
		t.Fatalf("report conclusion drifted: %q", rep.Conclusion)
	}
	for _, o := range obs.list {
		if o.Reason != "" {
			t.Fatalf("a clean preset run produced a failure observation: %+v", o)
		}
	}
}

// B3: a subagent that outlives its declared timeout is stopped and the run
// fails on that node with reason=timeout.
func TestTL1454NodeTimeoutStopsTheNode(t *testing.T) {
	wf := mustParse(t, `version: 1
name: slow
nodes:
  - {id: quick, type: subagent, prompt: q}
  - {id: stuck, type: subagent, prompt: s, timeout: 60ms}
  - {id: after, type: subagent, prompt: a}
edges:
  - {from: quick, to: stuck}
  - {from: stuck, to: after}
`)
	runner := &scriptRunner{block: map[string]bool{"stuck": true}}
	log := &eventLog{}
	obs := &observations{}
	eng := New(runner, presetGateRunner, log)
	eng.Observe = obs.hook
	start := time.Now()
	_, err := eng.RunWithID(context.Background(), wf, "wf-timeout")
	if time.Since(start) > 5*time.Second {
		t.Fatal("the node timeout did not stop the stuck node")
	}
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) || nodeErr.NodeID != "stuck" || nodeErr.Reason != ReasonTimeout {
		t.Fatalf("run error = %#v, want NodeError{stuck, timeout}", err)
	}
	if !strings.Contains(log.joined(), "stuck:failed:") || !strings.Contains(log.joined(), "timed out") {
		t.Fatalf("failed event must name the timeout:\n%s", log.joined())
	}
	if obs.reasonOf("stuck") != ReasonTimeout {
		t.Fatalf("observation reason = %q", obs.reasonOf("stuck"))
	}
	if runner.callCount("after") != 0 {
		t.Fatal("a node downstream of the timed-out node must not run")
	}
}

// A confirm node keeps its non-fatal contract under a timeout: findings stay
// unconfirmed and the run goes on.
func TestTL1454ConfirmTimeoutStaysNonFatal(t *testing.T) {
	wf := mustParse(t, `version: 1
name: confirm-slow
nodes:
  - {id: scan, type: subagent, prompt: s}
  - {id: verify, type: confirm, target: scan, timeout: 50ms}
  - {id: tail, type: subagent, prompt: t}
edges:
  - {from: verify, to: tail}
`)
	runner := &scriptRunner{block: map[string]bool{"verify": true}}
	log := &eventLog{}
	eng := New(runner, presetGateRunner, log)
	if _, err := eng.RunWithID(context.Background(), wf, "wf-ctimeout"); err != nil {
		t.Fatalf("confirm timeout must not fail the run: %v", err)
	}
	if !strings.Contains(log.joined(), "verify:unconfirmed:") || runner.callCount("tail") != 1 {
		t.Fatalf("confirm timeout must land unconfirmed and the run continue:\n%s", log.joined())
	}
}

// B4 wall: the run's wall budget stops in-flight work with a budget error.
func TestTL1454WallBudgetStopsTheRun(t *testing.T) {
	wf := mustParse(t, `version: 1
name: wall
budget: {wall: 80ms}
nodes:
  - {id: long, type: subagent, prompt: l}
`)
	runner := &scriptRunner{block: map[string]bool{"long": true}}
	obs := &observations{}
	eng := New(runner, presetGateRunner, &eventLog{})
	eng.Observe = obs.hook
	_, err := eng.RunWithID(context.Background(), wf, "wf-wall")
	var budget *BudgetExceededError
	if !errors.As(err, &budget) || budget.Axis != "wall" {
		t.Fatalf("run error = %v, want wall budget exceeded", err)
	}
	if !strings.Contains(err.Error(), "workflow budget exceeded: wall clock") {
		t.Fatalf("budget error must say so plainly: %v", err)
	}
	if obs.reasonOf("long") != ReasonBudget {
		t.Fatalf("in-flight node reason = %q, want budget", obs.reasonOf("long"))
	}
}

// B4 tokens: spend accumulates across nodes; the node that crosses keeps its
// result, nothing after it is dispatched, the error names axis/used/limit/node.
func TestTL1454TokenBudgetStopsAfterCrossingNode(t *testing.T) {
	wf := mustParse(t, `version: 1
name: tokens
budget: {tokens: 1000}
nodes:
  - {id: a, type: subagent, prompt: a}
  - {id: b, type: subagent, prompt: b}
  - {id: c, type: subagent, prompt: c}
edges:
  - {from: a, to: b}
  - {from: b, to: c}
`)
	runner := &scriptRunner{usage: map[string][2]int{"a": {600, 3}, "b": {600, 2}, "c": {10, 1}}}
	log := &eventLog{}
	obs := &observations{}
	eng := New(runner, presetGateRunner, log)
	eng.Observe = obs.hook
	rep, err := eng.RunWithID(context.Background(), wf, "wf-tokens")
	var budget *BudgetExceededError
	if !errors.As(err, &budget) || budget.Axis != "tokens" || budget.Used != 1200 || budget.Limit != 1000 || budget.After != "b" {
		t.Fatalf("run error = %#v, want tokens 1200/1000 after b", err)
	}
	if runner.callCount("c") != 0 {
		t.Fatal("no node may be dispatched after the budget is spent")
	}
	if !strings.Contains(log.joined(), "b:completed") {
		t.Fatalf("the crossing node keeps its completed result:\n%s", log.joined())
	}
	if obs.tokensOf("a") != 600 || obs.tokensOf("b") != 600 {
		t.Fatalf("per-node usage not observed: a=%d b=%d", obs.tokensOf("a"), obs.tokensOf("b"))
	}
	if rep == nil || !strings.Contains(rep.Conclusion, "workflow budget exceeded: tokens 1200 >= 1000 budget (crossed after node \"b\")") {
		t.Fatalf("report must carry the plain budget stop: %+v", rep)
	}
}

func TestTL1454RoundsBudgetStopsTheRun(t *testing.T) {
	wf := mustParse(t, `version: 1
name: rounds
budget: {rounds: 4}
nodes:
  - {id: a, type: subagent, prompt: a}
  - {id: b, type: subagent, prompt: b}
edges:
  - {from: a, to: b}
`)
	runner := &scriptRunner{usage: map[string][2]int{"a": {10, 5}}}
	eng := New(runner, presetGateRunner, &eventLog{})
	_, err := eng.RunWithID(context.Background(), wf, "wf-rounds")
	var budget *BudgetExceededError
	if !errors.As(err, &budget) || budget.Axis != "rounds" || budget.Used != 5 {
		t.Fatalf("run error = %v, want rounds budget exceeded", err)
	}
	if runner.callCount("b") != 0 {
		t.Fatal("rounds budget must stop dispatch")
	}
}

// A spent budget suppresses the directed JSON re-ask: the node fails on the
// budget instead of burning past it.
func TestTL1454BudgetSuppressesDirectedRetry(t *testing.T) {
	wf := mustParse(t, `version: 1
name: retry-budget
budget: {tokens: 100}
nodes:
  - id: typed
    type: subagent
    prompt: give json
    result: [{name: answer, type: string, description: d}]
`)
	runner := &scriptRunner{answers: map[string]string{"typed": "not json"}, usage: map[string][2]int{"typed": {150, 1}}}
	obs := &observations{}
	eng := New(runner, presetGateRunner, &eventLog{})
	eng.Observe = obs.hook
	_, err := eng.RunWithID(context.Background(), wf, "wf-retry-budget")
	if runner.callCount("typed") != 1 {
		t.Fatalf("re-ask must not run once the budget is spent (calls=%d)", runner.callCount("typed"))
	}
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Reason != ReasonBudget {
		t.Fatalf("run error = %#v, want NodeError reason budget", err)
	}
}

// B3 cancel: a user stop keeps its context.Canceled identity (the service's
// interrupted/resumable mapping) and classifies as cancelled.
func TestTL1454UserCancelKeepsCanceledIdentity(t *testing.T) {
	wf := mustParse(t, "version: 1\nname: c\nnodes:\n  - {id: wait, type: subagent, prompt: w}\n")
	runner := &scriptRunner{block: map[string]bool{"wait": true}}
	obs := &observations{}
	eng := New(runner, presetGateRunner, &eventLog{})
	eng.Observe = obs.hook
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	_, err := eng.RunWithID(ctx, wf, "wf-cancel")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel must stay errors.Is(context.Canceled): %v", err)
	}
	if obs.reasonOf("wait") != ReasonCancelled {
		t.Fatalf("reason = %q, want cancelled", obs.reasonOf("wait"))
	}
}

func TestTL1454GateFailureReasons(t *testing.T) {
	strand := mustParse(t, "version: 1\nname: g\nnodes:\n  - {id: check, type: gate, command: nope}\n  - {id: next, type: subagent, prompt: n}\nedges:\n  - {from: check, to: next, when: pass}\n")
	obs := &observations{}
	eng := New(&scriptRunner{}, presetGateRunner, &eventLog{})
	eng.Observe = obs.hook
	_, err := eng.RunWithID(context.Background(), strand, "wf-g1")
	var nodeErr *NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Reason != ReasonGateFailed {
		t.Fatalf("stranded gate failure reason: %#v", err)
	}

	world := func(context.Context, string, time.Duration) (GateResult, error) {
		return GateResult{}, errors.New("launch failed")
	}
	eng2 := New(&scriptRunner{}, world, &eventLog{})
	_, err = eng2.RunWithID(context.Background(), strand, "wf-g2")
	if !errors.As(err, &nodeErr) || nodeErr.Reason != ReasonGateError {
		t.Fatalf("gate world failure reason: %#v", err)
	}

	denied := func(_ context.Context, cmd string, _ time.Duration) (GateResult, error) {
		return GateResult{}, &GateDeniedError{Command: cmd, Rule: "bash(nope)"}
	}
	eng3 := New(&scriptRunner{}, denied, &eventLog{})
	_, err = eng3.RunWithID(context.Background(), strand, "wf-g3")
	if !errors.As(err, &nodeErr) || nodeErr.Reason != ReasonGateDenied || !strings.Contains(err.Error(), "denied by your permission rules") {
		t.Fatalf("denied gate reason: %#v", err)
	}
}

// Contract note: a typed result missing a declared field is reported (not
// fatal — control flow is exactly as before).
func TestTL1454MissingResultFieldIsAContractNote(t *testing.T) {
	wf := mustParse(t, `version: 1
name: contract
nodes:
  - id: typed
    type: subagent
    prompt: p
    result:
      - {name: summary, type: string, description: d}
      - {name: score, type: number, description: d}
`)
	runner := &scriptRunner{answers: map[string]string{"typed": `{"summary":"ok"}`}}
	obs := &observations{}
	eng := New(runner, presetGateRunner, &eventLog{})
	eng.Observe = obs.hook
	if _, err := eng.RunWithID(context.Background(), wf, "wf-contract"); err != nil {
		t.Fatalf("a missing field must stay non-fatal: %v", err)
	}
	if !strings.Contains(obs.contractOf("typed"), "score") {
		t.Fatalf("contract note missing: %q", obs.contractOf("typed"))
	}
}
