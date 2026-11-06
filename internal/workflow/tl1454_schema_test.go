package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// presetShapeDoc mirrors the shapes of the user's real agent-defined presets
// (code-audit / github-*-triage / resume-probe-long / selfcheck-mini): phases,
// tool-allowlisted subagents with max_steps and typed results, a confirm, a
// gate with timeout and pass/fail branches, and a report conclusion.
const presetShapeDoc = `version: 1
name: preset-shape
description: "mirror of the agent-defined presets"
nodes:
  - id: phase-scan
    type: phase
    label: scan
  - id: scan
    type: subagent
    prompt: "scan the repo"
    description: "scanner"
    tools: [read_file, grep, glob, ls, read_only_task]
    max_steps: 60
    result:
      - {name: findings, type: array, description: "issues"}
      - {name: summary, type: string, description: "one line"}
    findings_field: findings
  - id: verify
    type: confirm
    target: scan
    description: "independent confirm"
  - id: gh_gate
    type: gate
    command: "gh auth status"
    timeout: 60s
  - id: triage
    type: subagent
    prompt: "triage {{scan.summary}}"
    tools: [bash]
  - id: fallback
    type: subagent
    prompt: "explain why the gate failed"
edges:
  - {from: phase-scan, to: scan}
  - {from: scan, to: gh_gate}
  - {from: gh_gate, to: triage, when: pass}
  - {from: gh_gate, to: fallback, when: fail}
report:
  conclusion: "done: {{scan.summary}}"
  not_covered: ["runtime behavior"]
`

// presetShapeFingerprint was computed with the pre-tl1454 engine (base
// 2ac2ea4c8). The schema extension must leave every pre-existing document's
// canonical form — hence its resume fingerprint — byte-identical.
const presetShapeFingerprint = "331aeb0e4822d6c6de736d065cd56e332d4cb0467eff297af767b18f5bbc9f02"

func TestTL1454LegacyDocumentFingerprintUnchanged(t *testing.T) {
	wf, err := Parse([]byte(presetShapeDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := defFingerprint(wf); got != presetShapeFingerprint {
		t.Fatalf("fingerprint drifted: %s (pre-tl1454 %s) — an additive schema field leaked into the canonical form", got, presetShapeFingerprint)
	}
	raw, _ := json.Marshal(wf)
	for _, key := range []string{`"budget"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("canonical JSON of a document without %s must not carry it:\n%s", key, raw)
		}
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != presetShapeFingerprint {
		t.Fatal("defFingerprint is no longer the sha256 of the canonical JSON")
	}
}

func TestTL1454BudgetAndTimeoutFieldsAccepted(t *testing.T) {
	doc := `version: 1
name: budgeted
budget: {tokens: 1000, rounds: 20, wall: 30m}
nodes:
  - {id: a, type: subagent, prompt: "x", timeout: 10m}
  - {id: c, type: confirm, target: a, timeout: 2m}
`
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if wf.Budget == nil || wf.Budget.Tokens != 1000 || wf.Budget.Rounds != 20 || wf.Budget.WallDuration().Minutes() != 30 {
		t.Fatalf("budget not decoded: %+v", wf.Budget)
	}
	if d, ok := wf.Nodes[0].ExecutionTimeout(); !ok || d.Minutes() != 10 {
		t.Fatalf("subagent timeout = %v %v", d, ok)
	}
	if d, ok := wf.Nodes[1].ExecutionTimeout(); !ok || d.Minutes() != 2 {
		t.Fatalf("confirm timeout = %v %v", d, ok)
	}
	raw, _ := json.Marshal(wf)
	if !strings.Contains(string(raw), `"budget":{"tokens":1000,"rounds":20,"wall":"30m"}`) {
		t.Fatalf("budget must round-trip through the canonical form:\n%s", raw)
	}
}

// Every invalid template is rejected with a located diagnostic — never a
// crash, never a silent accept (acceptance B6).
func TestTL1454DiagnoseLocatesEveryRejection(t *testing.T) {
	cases := []struct {
		name, doc, kind, path, node string
		line                         int
	}{
		{"yaml syntax", "version: 1\nname: x\nnodes:\n  - id: a\n   type: [", DiagSyntax, "", "", 3},
		{"json syntax", "{\n  \"version\": 1,\n  \"name\": \"x\",\n  \"nodes\": [\n}", DiagSyntax, "", "", 5},
		{"missing prompt", "version: 1\nname: x\nnodes:\n  - id: a\n    type: subagent\n", DiagField, "nodes[0].prompt", "a", 4},
		{"bad subagent timeout", "version: 1\nname: x\nnodes:\n  - id: a\n    type: subagent\n    prompt: p\n    timeout: soon\n", DiagField, "nodes[0].timeout", "a", 7},
		{"foreign field", "version: 1\nname: x\nnodes:\n  - id: a\n    type: subagent\n    prompt: p\n    tier: strong\n", DiagField, "nodes[0].tier", "a", 7},
		{"unknown top-level", "version: 1\nname: x\nedge: []\nnodes:\n  - {id: a, type: subagent, prompt: p}\n", DiagField, "edge", "", 3},
		{"unknown edge target", "version: 1\nname: x\nnodes:\n  - {id: a, type: subagent, prompt: p}\nedges:\n  - {from: a, to: ghost}\n", DiagReference, "edges[0].to", "", 6},
		{"cycle", "version: 1\nname: x\nnodes:\n  - {id: a, type: subagent, prompt: p}\n  - {id: b, type: subagent, prompt: q}\nedges:\n  - {from: a, to: b}\n  - {from: b, to: a}\n", DiagStructure, "nodes[0]", "a", 4},
		{"non-ancestor ref", "version: 1\nname: x\nnodes:\n  - {id: a, type: subagent, prompt: p}\n  - {id: b, type: subagent, prompt: \"see {{a}}\"}\n", DiagReference, "nodes[1].prompt", "b", 5},
		{"future version", "version: 2\nname: x\nnodes:\n  - {id: a, type: subagent, prompt: p}\n", DiagVersion, "version", "", 1},
		{"empty budget", "version: 1\nname: x\nbudget: {}\nnodes:\n  - {id: a, type: subagent, prompt: p}\n", DiagField, "budget", "", 3},
		{"bad budget wall", "version: 1\nname: x\nbudget:\n  wall: forever\nnodes:\n  - {id: a, type: subagent, prompt: p}\n", DiagField, "budget.wall", "", 4},
		{"negative budget", "version: 1\nname: x\nbudget:\n  tokens: -5\nnodes:\n  - {id: a, type: subagent, prompt: p}\n", DiagField, "budget.tokens", "", 4},
		{"json missing prompt", "{\n  \"version\": 1,\n  \"name\": \"x\",\n  \"nodes\": [\n    {\"id\": \"a\", \"type\": \"subagent\"}\n  ]\n}", DiagField, "nodes[0].prompt", "a", 5},
		{"json unknown nested field", "{\n  \"version\": 1,\n  \"name\": \"x\",\n  \"nodes\": [\n    {\"id\": \"a\", \"type\": \"subagent\", \"prompt\": \"p\",\n     \"result\": [{\"name\": \"r\", \"type\": \"string\", \"description\": \"d\", \"bogus\": 1}]}\n  ]\n}", DiagField, "", "", 6},
		{"empty document", "  \n", DiagSyntax, "", "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, d := Diagnose([]byte(tc.doc))
			if wf != nil || d == nil {
				t.Fatalf("invalid template accepted: wf=%v d=%v", wf != nil, d)
			}
			if d.Kind != tc.kind {
				t.Errorf("kind = %q, want %q (%s)", d.Kind, tc.kind, d.Message)
			}
			if tc.path != "" && d.Path != tc.path {
				t.Errorf("path = %q, want %q (%s)", d.Path, tc.path, d.Message)
			}
			if tc.node != "" && d.NodeID != tc.node {
				t.Errorf("nodeId = %q, want %q", d.NodeID, tc.node)
			}
			if d.Line != tc.line {
				t.Errorf("line = %d, want %d (%s)", d.Line, tc.line, d.Message)
			}
			if d.Message == "" {
				t.Error("diagnostic carries no engine message")
			}
		})
	}
}

// The located error keeps the historical message byte-for-byte (agent
// self-correction and older pins read the same text).
func TestTL1454ValidationErrorKeepsLegacyText(t *testing.T) {
	_, err := Parse([]byte("version: 1\nname: x\nnodes:\n  - id: a\n    type: subagent\n"))
	if err == nil || err.Error() != `workflow: subagent node "a": prompt is required` {
		t.Fatalf("legacy message changed: %v", err)
	}
	_, err = Parse([]byte("version: 1\nname: x\nnodes:\n  - {id: a, type: gate, command: x, timeout: never}\n"))
	if err == nil || err.Error() != `workflow: gate node "a": timeout "never" is not a positive Go duration` {
		t.Fatalf("legacy gate message changed: %v", err)
	}
}
