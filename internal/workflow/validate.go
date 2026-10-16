package workflow

import (
	"fmt"
	"strings"
	"time"
)

// Validate checks structural integrity before anything runs: ids, types,
// per-type required fields, edges, acyclicity, interpolation safety, fixer
// isolation, and the strong-gate dominance rule. A workflow that starts and
// then discovers it cannot finish has already spent tokens — so all of this
// happens up front.
func (wf *Workflow) Validate() error {
	if wf.Version != SchemaVersion {
		return fmt.Errorf("workflow: version %d unsupported (want %d)", wf.Version, SchemaVersion)
	}
	if strings.TrimSpace(wf.Name) == "" {
		return fmt.Errorf("workflow: name is required")
	}
	if len(wf.Nodes) == 0 {
		return fmt.Errorf("workflow: at least one node is required")
	}
	index, err := wf.validateNodes()
	if err != nil {
		return err
	}
	deps, err := wf.validateEdges(index)
	if err != nil {
		return err
	}
	if err := detectCycle(wf, deps); err != nil {
		return err
	}
	if err := wf.validateFixers(index); err != nil {
		return err
	}
	if err := wf.validateInterpolation(index, deps); err != nil {
		return err
	}
	return wf.validateStrongGates(index, deps)
}

// validateNodes checks id shape/uniqueness and per-type required fields, and
// returns the id → node index.
func (wf *Workflow) validateNodes() (map[string]int, error) {
	index := make(map[string]int, len(wf.Nodes))
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if !nodeIDPattern.MatchString(n.ID) {
			return nil, fmt.Errorf("workflow: node %d: id %q must match %s", i+1, n.ID, nodeIDPattern)
		}
		if prior, dup := index[n.ID]; dup {
			return nil, fmt.Errorf("workflow: node %d: id %q already used by node %d", i+1, n.ID, prior+1)
		}
		index[n.ID] = i
		if err := validateNodeType(n); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func validateNodeType(n *Node) error {
	switch n.Type {
	case NodeSubagent:
		if strings.TrimSpace(n.Prompt) == "" {
			return fmt.Errorf("workflow: subagent node %q: prompt is required", n.ID)
		}
		for _, f := range n.Result {
			if !fieldPattern.MatchString(f.Name) {
				return fmt.Errorf("workflow: subagent node %q: result field name %q must match %s", n.ID, f.Name, fieldPattern)
			}
		}
		if n.FindingsField != "" {
			if _, ok := n.ResultField(n.FindingsField); !ok {
				return fmt.Errorf("workflow: subagent node %q: findings_field %q is not a declared result field", n.ID, n.FindingsField)
			}
		}
	case NodeGate:
		if strings.TrimSpace(n.Command) == "" {
			return fmt.Errorf("workflow: gate node %q: command is required", n.ID)
		}
		if strings.Contains(n.Command, "{{") {
			return fmt.Errorf("workflow: gate node %q: command is a literal and may not contain interpolations", n.ID)
		}
		if n.Tier != "" && n.Tier != TierFast && n.Tier != TierStrong {
			return fmt.Errorf("workflow: gate node %q: tier %q must be %s or %s", n.ID, n.Tier, TierFast, TierStrong)
		}
		if t := strings.TrimSpace(n.Timeout); t != "" {
			d, err := time.ParseDuration(t)
			if err != nil || d <= 0 {
				return fmt.Errorf("workflow: gate node %q: timeout %q is not a positive Go duration", n.ID, n.Timeout)
			}
		}
		if n.Loop != nil {
			if !nodeIDPattern.MatchString(n.Loop.Fixer) {
				return fmt.Errorf("workflow: gate node %q: loop.fixer %q must be a node id", n.ID, n.Loop.Fixer)
			}
			if n.Loop.MaxRounds < 1 {
				return fmt.Errorf("workflow: gate node %q: loop.max_rounds must be >= 1", n.ID)
			}
		}
	case NodePhase:
		if strings.TrimSpace(n.Label) == "" {
			return fmt.Errorf("workflow: phase node %q: label is required", n.ID)
		}
	case NodeJoin:
		// no per-type fields
	case NodeConfirm:
		if !nodeIDPattern.MatchString(n.Target) {
			return fmt.Errorf("workflow: confirm node %q: target %q must be a node id", n.ID, n.Target)
		}
	default:
		return fmt.Errorf("workflow: node %q: unknown type %q", n.ID, n.Type)
	}
	return nil
}

// validateEdges checks endpoints, branch legality, fixer isolation (no plain
// edges touching a fixer), and returns the dependency map (node → upstream
// ids) with conditional edges included: a conditional edge still orders the
// nodes even when its branch does not activate.
func (wf *Workflow) validateEdges(index map[string]int) (map[string][]string, error) {
	fixers := wf.fixerSet(index)
	deps := make(map[string][]string, len(wf.Nodes))
	for _, n := range wf.Nodes {
		deps[n.ID] = nil
	}
	for i, e := range wf.Edges {
		from, ok := index[e.From]
		if !ok {
			return nil, fmt.Errorf("workflow: edge %d: unknown from %q", i+1, e.From)
		}
		if _, ok := index[e.To]; !ok {
			return nil, fmt.Errorf("workflow: edge %d: unknown to %q", i+1, e.To)
		}
		if e.From == e.To {
			return nil, fmt.Errorf("workflow: edge %d: self edge on %q", i+1, e.From)
		}
		switch e.When {
		case "", WhenAlways:
		case WhenPass, WhenFail:
			if wf.Nodes[from].Type != NodeGate {
				return nil, fmt.Errorf("workflow: edge %d: when=%s requires a gate source, %q is %s", i+1, e.When, e.From, wf.Nodes[from].Type)
			}
		default:
			return nil, fmt.Errorf("workflow: edge %d: unknown when %q", i+1, e.When)
		}
		if fixers[e.From] || fixers[e.To] {
			return nil, fmt.Errorf("workflow: edge %d: fixer node %q may not carry edges (it runs only inside its gate loop)", i+1, e.From)
		}
		deps[e.To] = append(deps[e.To], e.From)
	}
	// A gate's loop.fixer must point at a subagent node; a confirm target must
	// exist, be subagent, and not be a fixer (confirmation applies to DAG
	// findings, not in-loop repairs).
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type == NodeGate && n.Loop != nil {
			ti, ok := index[n.Loop.Fixer]
			if !ok {
				return nil, fmt.Errorf("workflow: gate node %q: loop.fixer %q matches no node", n.ID, n.Loop.Fixer)
			}
			if wf.Nodes[ti].Type != NodeSubagent {
				return nil, fmt.Errorf("workflow: gate node %q: loop.fixer %q must be a subagent node", n.ID, n.Loop.Fixer)
			}
			if !fixers[n.Loop.Fixer] {
				fixers[n.Loop.Fixer] = true
			}
		}
		if n.Type == NodeConfirm {
			ti, ok := index[n.Target]
			if !ok {
				return nil, fmt.Errorf("workflow: confirm node %q: target %q matches no node", n.ID, n.Target)
			}
			if wf.Nodes[ti].Type != NodeSubagent {
				return nil, fmt.Errorf("workflow: confirm node %q: target %q must be a subagent node", n.ID, n.Target)
			}
			if fixers[n.Target] {
				return nil, fmt.Errorf("workflow: confirm node %q: target %q is a fixer node", n.ID, n.Target)
			}
			// The engine adds target→confirm as an implicit dependency.
			deps[n.ID] = append(deps[n.ID], n.Target)
		}
	}
	return deps, nil
}

// fixerSet returns the node ids referenced as some gate's loop.fixer.
func (wf *Workflow) fixerSet(index map[string]int) map[string]bool {
	fixers := make(map[string]bool)
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type == NodeGate && n.Loop != nil {
			if _, ok := index[n.Loop.Fixer]; ok {
				fixers[n.Loop.Fixer] = true
			}
		}
	}
	return fixers
}

// detectCycle rejects any dependency cycle — the graph must stay a DAG so the
// 686 panel can draw the whole plan before the run starts.
func detectCycle(wf *Workflow, deps map[string][]string) error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(wf.Nodes))
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		switch color[id] {
		case black:
			return nil
		case grey:
			return fmt.Errorf("workflow: dependency cycle: %s", strings.Join(append(path, id), " -> "))
		}
		color[id] = grey
		for _, d := range deps[id] {
			if err := visit(d, append(path, id)); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}
	for i := range wf.Nodes {
		if err := visit(wf.Nodes[i].ID, nil); err != nil {
			return err
		}
	}
	return nil
}

// validateFixers rejects a loop.fixer that also carries prompt interpolations
// of non-ancestor nodes: a fixer has no DAG ancestors of its own, so every
// {{ref}} in its prompt is supplied by the gate at loop time (feedback) or is
// illegal. Interpolation of the fixer's own prompt is checked like any
// subagent prompt — with an empty ancestor set, i.e. refs are illegal.
func (wf *Workflow) validateFixers(index map[string]int) error {
	fixers := wf.fixerSet(index)
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if !fixers[n.ID] {
			continue
		}
		refs := refPattern.FindAllStringSubmatch(n.Prompt, -1)
		if len(refs) > 0 {
			return fmt.Errorf("workflow: fixer node %q: fixer prompts may not interpolate {{refs}} (no ancestors; feedback is appended by the gate)", n.ID)
		}
	}
	return nil
}

// validateInterpolation checks every {{ref}} in subagent prompts and the
// report conclusion: the node must exist, a prompt ref must name an ancestor,
// and a field ref must be declared in the target's result schema when one is
// declared.
func (wf *Workflow) validateInterpolation(index map[string]int, deps map[string][]string) error {
	ancestors := transitiveDeps(deps)
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type != NodeSubagent {
			continue
		}
		if _, isFixer := wf.fixerSet(index)[n.ID]; isFixer {
			continue // checked in validateFixers
		}
		for _, ref := range refPattern.FindAllStringSubmatch(n.Prompt, -1) {
			if err := wf.checkRef(index, ancestors, n.ID, ref[1], ref[2], "prompt"); err != nil {
				return err
			}
		}
	}
	for _, ref := range refPattern.FindAllStringSubmatch(wf.Report.Conclusion, -1) {
		if _, ok := index[ref[1]]; !ok {
			return fmt.Errorf("workflow: report.conclusion: unknown node %q", ref[1])
		}
		if ref[2] != "" && wf.Nodes[index[ref[1]]].Type == NodeSubagent {
			if _, declared := wf.Nodes[index[ref[1]]].ResultField(ref[2]); !declared {
				return fmt.Errorf("workflow: report.conclusion: node %q has no result field %q", ref[1], ref[2])
			}
		}
	}
	return nil
}

func (wf *Workflow) checkRef(index map[string]int, ancestors map[string]map[string]bool, from, target, field, where string) error {
	if _, ok := index[target]; !ok {
		return fmt.Errorf("workflow: %s node %q: %s references unknown node %q", where, from, where, target)
	}
	if !ancestors[from][target] {
		return fmt.Errorf("workflow: %s node %q: %s references %q which is not an ancestor", where, from, where, target)
	}
	if field != "" && wf.Nodes[index[target]].Type == NodeSubagent {
		if _, declared := wf.Nodes[index[target]].ResultField(field); !declared {
			return fmt.Errorf("workflow: %s node %q: node %q has no result field %q", where, from, target, field)
		}
	}
	return nil
}

// transitiveDeps computes each node's ancestor set (itself excluded). The
// graph is already acyclic when this runs, so a fresh per-node walk is both
// correct and cheap at workflow scale.
func transitiveDeps(deps map[string][]string) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(deps))
	var collect func(id string, seen map[string]bool)
	collect = func(id string, seen map[string]bool) {
		for _, d := range deps[id] {
			if seen[d] {
				continue
			}
			seen[d] = true
			collect(d, seen)
		}
	}
	for id := range deps {
		seen := make(map[string]bool)
		collect(id, seen)
		out[id] = seen
	}
	return out
}

// validateStrongGates enforces the two-tier gate rule structurally: every
// strong gate must dominate every delivery sink, i.e. removing it makes the
// sink unreachable. "The strongest tier decides the exit and runs for real
// before delivery" stops being a discipline and becomes a property of the
// graph.
func (wf *Workflow) validateStrongGates(index map[string]int, deps map[string][]string) error {
	fixers := wf.fixerSet(index)
	dependents := make(map[string][]string, len(wf.Nodes))
	sinks := make([]string, 0, len(wf.Nodes))
	for id, ds := range deps {
		for _, d := range ds {
			dependents[d] = append(dependents[d], id)
		}
	}
	for i := range wf.Nodes {
		id := wf.Nodes[i].ID
		// A fixer runs inside its gate's loop, not as a delivery endpoint.
		if fixers[id] {
			continue
		}
		if len(dependents[id]) == 0 {
			sinks = append(sinks, id)
		}
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type != NodeGate || n.Tier != TierStrong {
			continue
		}
		for _, sink := range sinks {
			if sink == n.ID {
				continue
			}
			if reachableWithout(deps, sink, n.ID) {
				return fmt.Errorf("workflow: strong gate %q does not dominate sink %q: delivery could complete without this check running", n.ID, sink)
			}
		}
	}
	return nil
}

// reachableWithout reports whether target is reachable from any source when
// the removed node's incoming edges are dropped (its outgoing edges stay:
// work downstream of the gate still counts as fed by it).
func reachableWithout(deps map[string][]string, target, removed string) bool {
	// Walk backwards from target through deps, treating the removed node as
	// absent: we cannot pass through it.
	seen := map[string]bool{target: true}
	stack := []string{target}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == removed {
			continue
		}
		for _, d := range deps[id] {
			if d == removed {
				continue
			}
			if seen[d] {
				continue
			}
			seen[d] = true
			stack = append(stack, d)
		}
	}
	// target is dominated when no source other than paths through the removed
	// node exists: i.e. after removal there is no upstream chain reaching a
	// node with zero deps (a real source).
	for id := range seen {
		if id == removed {
			continue
		}
		if len(deps[id]) == 0 {
			return true
		}
	}
	return false
}
