// Package workflow is the route-C declarative DAG orchestration engine
// (task 685). A workflow is data — YAML/JSON nodes and edges — executed by a
// sequential topological scheduler over injected subagent/gate runners. It
// keeps the quality disciplines of the zcode dynamic-workflow skill (typed
// results, deterministic gates, independent confirmation, report salvage)
// without the TypeScript script language; the schema below is the frozen
// contract the 686 visualization panel consumes.
package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Node types. Each type carries its own required fields (see Node).
const (
	NodeSubagent = "subagent"
	NodeGate     = "gate"
	NodePhase    = "phase"
	NodeJoin     = "join"
	NodeConfirm  = "confirm"
)

// Gate tiers: fast may drive a repair loop; strong must dominate every
// delivery sink (structural "runs before you ship" guarantee).
const (
	TierFast   = "fast"
	TierStrong = "strong"
)

// Edge activation conditions. pass/fail are only legal on edges leaving a
// gate node and branch on its exit code.
const (
	WhenAlways = "always"
	WhenPass   = "pass"
	WhenFail   = "fail"
)

// SchemaVersion is the only workflow format version this build accepts.
const SchemaVersion = 1

var (
	nodeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	fieldPattern  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
	// refPattern finds {{node_id}} and {{node_id.field}} interpolations. The
	// field group allows dashes so join results can be addressed by node id
	// ({{join1.hunt-a}}); subagent field refs stay restricted by the declared
	// result schema check.
	refPattern = regexp.MustCompile(`\{\{\s*([a-z0-9][a-z0-9_-]*)(?:\.([a-zA-Z][a-zA-Z0-9_-]*))?\s*\}\}`)
)

// Workflow is one declarative orchestration document.
type Workflow struct {
	Version     int        `json:"version" yaml:"version"`
	Name        string     `json:"name" yaml:"name"`
	Description string     `json:"description,omitempty" yaml:"description,omitempty"`
	Nodes       []Node     `json:"nodes" yaml:"nodes"`
	Edges       []Edge     `json:"edges,omitempty" yaml:"edges,omitempty"`
	Report      ReportSpec `json:"report,omitempty" yaml:"report,omitempty"`
}

// ResultField describes one typed-result field. Its Description is the field
// hint the sub-agent actually reads (SKILL.md §4: the cheapest quality lever).
type ResultField struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description" yaml:"description"`
}

// GateLoop is the "fix until the check passes" repair loop embedded in a gate
// node. The DAG itself stays acyclic: the loop lives inside the gate.
type GateLoop struct {
	Fixer     string `json:"fixer" yaml:"fixer"`
	MaxRounds int    `json:"max_rounds" yaml:"max_rounds"`
}

// Node is one vertex. Only the fields matching Type are read; carrying a
// foreign type's field is rejected so a typo cannot silently disable a gate.
type Node struct {
	ID   string `json:"id" yaml:"id"`
	Type string `json:"type" yaml:"type"`

	// subagent
	Prompt        string        `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	Result        []ResultField `json:"result,omitempty" yaml:"result,omitempty"`
	FindingsField string        `json:"findings_field,omitempty" yaml:"findings_field,omitempty"`
	Tools         []string      `json:"tools,omitempty" yaml:"tools,omitempty"`
	Model         string        `json:"model,omitempty" yaml:"model,omitempty"`
	Effort        string        `json:"effort,omitempty" yaml:"effort,omitempty"`
	MaxSteps      int           `json:"max_steps,omitempty" yaml:"max_steps,omitempty"`

	// gate
	Command string    `json:"command,omitempty" yaml:"command,omitempty"`
	Timeout string    `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Tier    string    `json:"tier,omitempty" yaml:"tier,omitempty"`
	Loop    *GateLoop `json:"loop,omitempty" yaml:"loop,omitempty"`

	// phase
	Label string `json:"label,omitempty" yaml:"label,omitempty"`

	// confirm
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
}

// Edge is one dependency (and, for gate sources, branch) relation.
type Edge struct {
	From  string `json:"from" yaml:"from"`
	To    string `json:"to" yaml:"to"`
	When  string `json:"when,omitempty" yaml:"when,omitempty"`
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
}

// ReportSpec is the author-declared report surface. NotCovered is passed
// through verbatim under strict semantics: the engine never auto-fills it
// (a check that exists and did not run is unverified work, not notCovered).
type ReportSpec struct {
	Conclusion string   `json:"conclusion,omitempty" yaml:"conclusion,omitempty"`
	NotCovered []string `json:"not_covered,omitempty" yaml:"not_covered,omitempty"`
}

// Parse decodes one workflow document (JSON when the first non-space byte is
// '{', otherwise YAML), then validates it. Order matters: the lenient-shape
// pass runs first so a field on the WRONG node type (a `tier` typo'd onto a
// subagent node, where the strict struct decode would happily accept it)
// still gets an error naming the node; the strict struct decode behind it
// catches everything else (unknown keys inside nested result/report blocks).
func Parse(data []byte) (*Workflow, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	isJSON := len(trimmed) > 0 && trimmed[0] == '{'
	if err := rejectForeignNodeFields(data, isJSON); err != nil {
		return nil, err
	}
	var wf Workflow
	var err error
	if isJSON {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		err = dec.Decode(&wf)
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		err = dec.Decode(&wf)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow: parse: %w", err)
	}
	if err := wf.Validate(); err != nil {
		return nil, err
	}
	return &wf, nil
}

// nodeFieldKeys maps each node type to the full set of keys legal on that
// node (common keys plus the type's own).
var nodeFieldKeys = map[string]map[string]bool{
	NodeSubagent: keys("id", "type", "prompt", "description", "result", "findings_field", "tools", "model", "effort", "max_steps"),
	NodeGate:     keys("id", "type", "command", "timeout", "tier", "loop"),
	NodePhase:    keys("id", "type", "label"),
	NodeJoin:     keys("id", "type"),
	NodeConfirm:  keys("id", "type", "target", "prompt"),
}

func keys(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// rejectForeignNodeFields re-decodes leniently and checks every node's key
// set against its declared type. Top-level keys are checked too, so a
// misspelled `edge:` does not silently vanish.
func rejectForeignNodeFields(data []byte, isJSON bool) error {
	var doc any
	if isJSON {
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil // strict decode already failed with the better error
		}
	} else {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil
		}
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	topKeys := keys("version", "name", "description", "nodes", "edges", "report")
	for k := range root {
		if !topKeys[k] {
			return fmt.Errorf("workflow: unknown top-level field %q", k)
		}
	}
	list, ok := root["nodes"].([]any)
	if !ok {
		return nil
	}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nodeType, _ := m["type"].(string)
		allowed, known := nodeFieldKeys[nodeType]
		if !known {
			continue // the strict decoder plus Validate will reject it
		}
		for k := range m {
			if !allowed[k] {
				return fmt.Errorf("workflow: node %d: field %q is not valid on a %s node", i+1, k, nodeType)
			}
		}
	}
	return nil
}

// Timeout parses the gate timeout field, defaulting to DefaultGateTimeout.
func (n *Node) TimeoutDuration() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(n.Timeout))
	if err != nil || d <= 0 {
		return DefaultGateTimeout
	}
	return d
}

// ResultField looks up a declared typed-result field.
func (n *Node) ResultField(name string) (ResultField, bool) {
	for _, f := range n.Result {
		if f.Name == name {
			return f, true
		}
	}
	return ResultField{}, false
}
