package workflow

import (
	"strings"
	"testing"
)

// validWorkflow is the canonical happy-path document: fan-out → join →
// confirm → fast gate with fix loop → strong gate → phase end.
const validWorkflow = `
version: 1
name: sample-review
nodes:
  - id: hunt-a
    type: subagent
    prompt: "Review src/a.go for bugs"
    result:
      - name: finding
        type: string
        description: one sentence problem
      - name: severity
        type: string
        description: low|medium|high
    findings_field: finding
  - id: hunt-b
    type: subagent
    prompt: "Review src/b.go for bugs"
  - id: join1
    type: join
  - id: confirm-a
    type: confirm
    target: hunt-a
  - id: unit-gate
    type: gate
    command: go test ./...
    tier: fast
    loop:
      fixer: fix-unit
      max_rounds: 3
  - id: fix-unit
    type: subagent
    prompt: "The unit tests failed. Fix them."
  - id: ship-gate
    type: gate
    command: go build ./...
    tier: strong
  - id: done
    type: phase
    label: delivered
edges:
  - {from: hunt-a, to: join1}
  - {from: hunt-b, to: join1}
  - {from: join1, to: confirm-a}
  - {from: confirm-a, to: unit-gate}
  - {from: unit-gate, to: ship-gate, when: pass}
  - {from: ship-gate, to: done}
report:
  conclusion: "Reviewed with {{ship-gate}}"
  not_covered: ["e2e suite; environment lacks Docker"]
`

func mustParse(t *testing.T, doc string) *Workflow {
	t.Helper()
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return wf
}

func TestParseYAMLValidWorkflow(t *testing.T) {
	wf := mustParse(t, validWorkflow)
	if wf.Name != "sample-review" || len(wf.Nodes) != 8 || len(wf.Edges) != 6 {
		t.Fatalf("shape = name %q nodes %d edges %d", wf.Name, len(wf.Nodes), len(wf.Edges))
	}
	if len(wf.Report.NotCovered) != 1 {
		t.Fatalf("not_covered = %v", wf.Report.NotCovered)
	}
	fixers := wf.fixerSet(mustIndex(t, wf))
	if !fixers["fix-unit"] || len(fixers) != 1 {
		t.Fatalf("fixers = %v", fixers)
	}
}

// TestParseJSONValidWorkflow pins that a JSON document (first byte '{')
// parses under the same schema — 686 may ship either encoding.
func TestParseJSONValidWorkflow(t *testing.T) {
	doc := `{"version":1,"name":"j","nodes":[
		{"id":"a","type":"subagent","prompt":"do it"},
		{"id":"g","type":"gate","command":"true"}],
		"edges":[{"from":"a","to":"g"}]}`
	wf := mustParse(t, doc)
	if len(wf.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(wf.Nodes))
	}
}

func mustIndex(t *testing.T, wf *Workflow) map[string]int {
	t.Helper()
	index := make(map[string]int, len(wf.Nodes))
	for i := range wf.Nodes {
		index[wf.Nodes[i].ID] = i
	}
	return index
}

// TestParseRejects covers the rejection surface: each case must fail with an
// error naming the offending node/field — a validation error nobody can act
// on is a bug per repo discipline.
func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"unsupported version", "version: 99\nname: x\nnodes: [{id: a, type: phase, label: L}]", "version 99"},
		{"missing name", "version: 1\nnodes: [{id: a, type: phase, label: L}]", "name is required"},
		{"no nodes", "version: 1\nname: x\nnodes: []", "at least one node"},
		{"bad id", "version: 1\nname: x\nnodes: [{id: Bad_ID, type: phase, label: L}]", "must match"},
		{"duplicate id", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L}, {id: a, type: phase, label: L2}]", "already used"},
		{"unknown type", "version: 1\nname: x\nnodes: [{id: a, type: wat}]", "unknown type"},
		{"subagent without prompt", "version: 1\nname: x\nnodes: [{id: a, type: subagent}]", "prompt is required"},
		{"gate without command", "version: 1\nname: x\nnodes: [{id: a, type: gate}]", "command is required"},
		{"interpolated gate command", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: \"go test {{x}}\"}]", "literal"},
		{"bad tier", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: x, tier: ultra}]", "tier"},
		{"bad timeout", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: x, timeout: never}]", "positive Go duration"},
		{"loop rounds cap", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: x, loop: {fixer: f, max_rounds: 0}}, {id: f, type: subagent, prompt: p}]", "max_rounds"},
		{"loop fixer unknown", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: x, loop: {fixer: nope, max_rounds: 2}}]", "matches no node"},
		{"loop fixer not subagent", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: x, loop: {fixer: p, max_rounds: 2}}, {id: p, type: phase, label: L}]", "must be a subagent"},
		{"phase without label", "version: 1\nname: x\nnodes: [{id: a, type: phase}]", "label is required"},
		{"confirm unknown target", "version: 1\nname: x\nnodes: [{id: c, type: confirm, target: nope}]", "matches no node"},
		{"confirm target not subagent", "version: 1\nname: x\nnodes: [{id: c, type: confirm, target: p}, {id: p, type: phase, label: L}]", "must be a subagent"},
		{"findings_field undeclared", "version: 1\nname: x\nnodes: [{id: a, type: subagent, prompt: p, findings_field: f}]", "not a declared result field"},
		{"edge unknown from", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L}]\nedges: [{from: ghost, to: a}]", "unknown from"},
		{"edge unknown to", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L}]\nedges: [{from: a, to: ghost}]", "unknown to"},
		{"self edge", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L}]\nedges: [{from: a, to: a}]", "self edge"},
		{"when fail on non-gate", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L}, {id: b, type: phase, label: L2}]\nedges: [{from: a, to: b, when: fail}]", "requires a gate source"},
		{"unknown when", "version: 1\nname: x\nnodes: [{id: a, type: gate, command: c}, {id: b, type: phase, label: L}]\nedges: [{from: a, to: b, when: maybe}]", "unknown when"},
		{"edge touching fixer", "version: 1\nname: x\nnodes: [{id: g, type: gate, command: c, loop: {fixer: f, max_rounds: 1}}, {id: f, type: subagent, prompt: p}, {id: e, type: phase, label: L}]\nedges: [{from: f, to: e}]", "fixer node"},
		{"fixer prompt interpolation", "version: 1\nname: x\nnodes: [{id: g, type: gate, command: c, loop: {fixer: f, max_rounds: 1}}, {id: f, type: subagent, prompt: \"see {{g}}\"}]", "fixer prompts may not interpolate"},
		{"cycle", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L}, {id: b, type: phase, label: L2}]\nedges: [{from: a, to: b}, {from: b, to: a}]", "cycle"},
		{"prompt ref unknown node", "version: 1\nname: x\nnodes: [{id: a, type: subagent, prompt: \"see {{ghost}}\"}]", "unknown node"},
		{"prompt ref non-ancestor", "version: 1\nname: x\nnodes: [{id: a, type: subagent, prompt: p}, {id: b, type: subagent, prompt: \"see {{a}}\"}]\nedges: [{from: b, to: a}]", "not an ancestor"},
		{"prompt ref undeclared field", "version: 1\nname: x\nnodes: [{id: a, type: subagent, prompt: p, result: [{name: r, type: string, description: d}]}, {id: b, type: subagent, prompt: \"{{a.nope}}\"}]\nedges: [{from: a, to: b}]", "no result field"},
		{"conclusion ref unknown", "version: 1\nname: x\nreport: {conclusion: \"{{ghost}}\"}\nnodes: [{id: a, type: phase, label: L}]", "unknown node"},
		{"strong gate not dominating", "version: 1\nname: x\nnodes: [{id: s, type: gate, command: c, tier: strong}, {id: a, type: phase, label: L}, {id: b, type: phase, label: L2}]\nedges: [{from: a, to: b}]", "does not dominate"},
		{"unknown top-level field", "version: 1\nname: x\nnodez: []", "unknown top-level field"},
		{"foreign field on node", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L, command: oops}]", "not valid on a phase node"},
		{"node key not valid for type", "version: 1\nname: x\nnodes: [{id: a, type: phase, label: L, bogus: 1}]", "not valid on a phase node"},
		{"nested unknown field strict", "version: 1\nname: x\nnodes: [{id: a, type: subagent, prompt: p, result: [{name: r, type: string, description: d, bogus: 1}]}]", "not found in type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			if err == nil {
				t.Fatalf("Parse accepted %q", tc.doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

// TestStrongGateDominanceAccepted pins the acceptance side: a strong gate
// sitting on the single path to delivery validates.
func TestStrongGateDominanceAccepted(t *testing.T) {
	doc := `version: 1
name: x
nodes:
  - {id: a, type: subagent, prompt: p}
  - {id: g, type: gate, command: c, tier: strong}
  - {id: end, type: phase, label: L}
edges:
  - {from: a, to: g}
  - {from: g, to: end}`
	mustParse(t, doc)
}

// TestStrongGateDominanceParallelSinkRejected covers the two-tier rule's
// teeth: a parallel delivery path that bypasses the strong gate must be
// rejected — the check could be skipped on delivery.
func TestStrongGateDominanceParallelSinkRejected(t *testing.T) {
	doc := `version: 1
name: x
nodes:
  - {id: a, type: subagent, prompt: p}
  - {id: g, type: gate, command: c, tier: strong}
  - {id: side, type: phase, label: L}
edges:
  - {from: a, to: g}
  - {from: a, to: side}`
	if _, err := Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "does not dominate") {
		t.Fatalf("err = %v, want dominance rejection", err)
	}
}

// TestImplicitConfirmDependency pins the engine's implicit ordering rule in
// the ancestor contract: a confirm node's prompt may interpolate its target
// without an explicit edge.
func TestImplicitConfirmDependency(t *testing.T) {
	doc := `version: 1
name: x
nodes:
  - {id: hunt, type: subagent, prompt: p, result: [{name: finding, type: string, description: d}]}
  - {id: conf, type: confirm, target: hunt, prompt: "{{hunt.finding}}"}
edges: []`
	mustParse(t, doc)
}

// TestFixerIsolation pins that a fixer carries no edges and no ancestors.
func TestFixerIsolation(t *testing.T) {
	index := mustIndex(t, mustParse(t, validWorkflow))
	wf := mustParse(t, validWorkflow)
	deps, err := wf.validateEdges(index)
	if err != nil {
		t.Fatalf("validateEdges: %v", err)
	}
	if len(deps["fix-unit"]) != 0 {
		t.Fatalf("fixer deps = %v, want none", deps["fix-unit"])
	}
	for _, ds := range deps {
		for _, d := range ds {
			if d == "fix-unit" {
				t.Fatalf("fixer referenced as a dependency: %v", deps)
			}
		}
	}
}
