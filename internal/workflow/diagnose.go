package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Diagnostic kinds (tl1454): the UI renders a plain-language sentence per kind
// next to the engine's verbatim message.
const (
	DiagSyntax    = "syntax"    // the document does not parse as YAML/JSON
	DiagField     = "field"     // a field is missing, unknown, or holds an invalid value
	DiagStructure = "structure" // graph shape: duplicate ids, edges, cycles, gate dominance
	DiagReference = "reference" // a reference names a node/field that does not qualify
	DiagVersion   = "version"   // unsupported schema version (the file is never rewritten)
)

// ValidationError is a located validation failure. Error() is byte-identical
// to the pre-tl1454 message, so agent self-correction and existing pins keep
// reading the same text; Path ("nodes[2].timeout") locates it in the source.
type ValidationError struct {
	Kind   string
	Path   string
	NodeID string
	msg    string
}

func (e *ValidationError) Error() string { return e.msg }

func verr(kind, path, nodeID, format string, args ...any) error {
	return &ValidationError{Kind: kind, Path: path, NodeID: nodeID, msg: fmt.Sprintf(format, args...)}
}

func nodePath(i int, field string) string {
	if field == "" {
		return fmt.Sprintf("nodes[%d]", i)
	}
	return fmt.Sprintf("nodes[%d].%s", i, field)
}

func edgePath(i int, field string) string {
	if field == "" {
		return fmt.Sprintf("edges[%d]", i)
	}
	return fmt.Sprintf("edges[%d].%s", i, field)
}

// Diagnostic is one located problem in a workflow document. Line/Column are
// 1-based; zero means the location could not be resolved.
type Diagnostic struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	NodeID  string `json:"nodeId,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

var yamlLinePattern = regexp.MustCompile(`line (\d+)`)

// Diagnose parses and validates src through the one schema contract (Parse)
// and, on failure, locates the first problem in the source text. JSON is a
// YAML subset, so a single yaml.v3 node tree resolves paths for both inputs.
func Diagnose(src []byte) (*Workflow, *Diagnostic) {
	if len(bytes.TrimSpace(src)) == 0 {
		return nil, &Diagnostic{Kind: DiagSyntax, Message: "workflow: document is empty", Line: 1, Column: 1}
	}
	wf, err := Parse(src)
	if err == nil {
		return wf, nil
	}
	d := &Diagnostic{Kind: DiagField, Message: err.Error()}
	var verrv *ValidationError
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &verrv):
		d.Kind, d.Path, d.NodeID = verrv.Kind, verrv.Path, verrv.NodeID
		d.Line, d.Column = locatePath(src, verrv.Path)
	case errors.As(err, &syn):
		d.Kind = DiagSyntax
		d.Line, d.Column = offsetToLineCol(src, syn.Offset)
	case errors.As(err, &typ):
		d.Path = typ.Field
		d.Line, d.Column = offsetToLineCol(src, typ.Offset)
	default:
		msg := err.Error()
		if m := yamlLinePattern.FindStringSubmatch(msg); m != nil {
			d.Line, _ = strconv.Atoi(m[1])
			d.Column = 1
		}
		if key, ok := unknownJSONField(msg); ok {
			d.Line, d.Column = locateKey(src, key)
			d.Kind = DiagField
		} else if d.Line == 0 || !strings.Contains(msg, "field") {
			d.Kind = DiagSyntax
		}
	}
	if d.NodeID == "" {
		d.NodeID = nodeIDAtPath(src, d.Path)
	}
	return nil, d
}

func unknownJSONField(msg string) (string, bool) {
	const marker = `json: unknown field "`
	// The message may carry a "workflow: parse: " wrap before the decoder's
	// own text, so match the marker anywhere rather than only as a prefix.
	_, rest, ok := strings.Cut(msg, marker)
	if !ok {
		return "", false
	}
	field, _, found := strings.Cut(rest, `"`)
	if !found {
		return "", false
	}
	return field, true
}

func offsetToLineCol(src []byte, off int64) (int, int) {
	if off < 0 {
		return 0, 0
	}
	if off > int64(len(src)) {
		off = int64(len(src))
	}
	line, col := 1, 1
	for _, b := range src[:off] {
		if b == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

// sourceTree parses src into a yaml.v3 node tree (nil when it cannot).
func sourceTree(src []byte) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

type pathStep struct {
	key   string
	index int
}

// splitPath turns "nodes[2].result[0].name" into key/index steps.
func splitPath(path string) []pathStep {
	var steps []pathStep
	for part := range strings.SplitSeq(path, ".") {
		if part == "" {
			continue
		}
		name := part
		var idx []int
		for {
			open := strings.LastIndex(name, "[")
			if open < 0 || !strings.HasSuffix(name, "]") {
				break
			}
			n, err := strconv.Atoi(name[open+1 : len(name)-1])
			if err != nil {
				break
			}
			idx = append([]int{n}, idx...)
			name = name[:open]
		}
		if name != "" {
			steps = append(steps, pathStep{key: name, index: -1})
		}
		for _, n := range idx {
			steps = append(steps, pathStep{index: n})
		}
	}
	return steps
}

// locatePath walks the path as far as the source allows and returns the
// deepest reached position: a missing field ("prompt is required") resolves
// to its enclosing node rather than to nothing.
func locatePath(src []byte, path string) (int, int) {
	cur := sourceTree(src)
	if cur == nil {
		return 0, 0
	}
	line, col := cur.Line, cur.Column
	for _, step := range splitPath(path) {
		next, keyNode := childAt(cur, step)
		if next == nil {
			break
		}
		if keyNode != nil {
			line, col = keyNode.Line, keyNode.Column
		} else {
			line, col = next.Line, next.Column
		}
		cur = next
	}
	return line, col
}

func childAt(n *yaml.Node, step pathStep) (value, key *yaml.Node) {
	if step.index >= 0 {
		if n.Kind == yaml.SequenceNode && step.index < len(n.Content) {
			return n.Content[step.index], nil
		}
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == step.key {
			return n.Content[i+1], n.Content[i]
		}
	}
	return nil, nil
}

// locateKey finds the first mapping key named key anywhere in the document
// (the strict JSON decoder reports unknown fields without an offset).
func locateKey(src []byte, key string) (int, int) {
	root := sourceTree(src)
	if root == nil {
		return 0, 0
	}
	var found *yaml.Node
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if found != nil || n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == key {
					found = n.Content[i]
					return
				}
				walk(n.Content[i+1])
			}
			return
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		return 0, 0
	}
	return found.Line, found.Column
}

// nodeIDAtPath reads the id of the node a nodes[i] path points into.
func nodeIDAtPath(src []byte, path string) string {
	steps := splitPath(path)
	if len(steps) < 2 || steps[0].key != "nodes" || steps[1].index < 0 {
		return ""
	}
	root := sourceTree(src)
	if root == nil {
		return ""
	}
	nodes, _ := childAt(root, steps[0])
	if nodes == nil {
		return ""
	}
	node, _ := childAt(nodes, steps[1])
	if node == nil {
		return ""
	}
	id, _ := childAt(node, pathStep{key: "id", index: -1})
	if id == nil || id.Kind != yaml.ScalarNode {
		return ""
	}
	return id.Value
}
