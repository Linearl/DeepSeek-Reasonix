package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"reasonix/internal/event"
)

// Engine executes declarative DAG workflows (task 685). All three
// collaborators are injected: Subagents for subagent/confirm/fixer nodes,
// Gate for deterministic gates, Sink for the six-state node event stream the
// 686 panel subscribes to.
type Engine struct {
	Subagents SubagentRunner
	Gate      GateRunner
	Sink      event.Sink
}

// New builds an engine with production defaults (shell gate, discard sink).
func New(sub SubagentRunner, gate GateRunner, sink event.Sink) *Engine {
	if gate == nil {
		gate = ShellGateRunner
	}
	if sink == nil {
		sink = event.Discard
	}
	return &Engine{Subagents: sub, Gate: gate, Sink: sink}
}

// scheduler status per node (distinct from the six EVENT states: unconfirmed
// and gate_passed are emit-time states, all scheduling-resolvable terminals
// share termPassed/termFailedBranched below).
type nodeStatus int

const (
	stPending nodeStatus = iota
	stFixer              // never scheduled; runs only inside its gate loop
	stRunning
	stDone    // executed to a normal terminal
	stSkipped // all in-edges decided inactive
	stAborted // run stopped on this node
)

// terminal class decides how a finished node's out-edges resolve.
type terminal int

const (
	termPassed     terminal = iota // subagent/join/phase/confirm done, gate exit 0
	termGateFailed                 // gate exit ≠ 0 with a receiving branch
	termSkipped                    // no active in-edge; never ran
)

// schedEdge is one dependency with branch semantics (real edges plus the
// implicit target→confirm dependency).
type schedEdge struct {
	from, to string
	when     string
	decided  bool
	active   bool
}

// runState is one Run's mutable bookkeeping.
type runState struct {
	eng    *Engine
	wf     *Workflow
	runID  string
	bag    *reportBag
	verifs []string

	index    map[string]int
	edges    []schedEdge
	inEdges  map[string][]int
	outEdges map[string][]int
	fixers   map[string]bool

	status     map[string]nodeStatus
	term       map[string]terminal
	gatePassed map[string]bool
	output     map[string]string    // interpolatable node output
	result     map[string]any       // parsed typed result / join map
	findings   map[string][]Finding // per producer node, for promotion

	phase      string
	failedNode string
	gateRound  int // current gate attempt, so a world-failure reports its round
}

// Run executes the whole workflow sequentially in topological order. It
// ALWAYS returns a non-nil report once anything was emitted — a run that dies
// mid-graph hands back everything reported so far (point 5 落袋).
func (e *Engine) Run(ctx context.Context, wf *Workflow) (*WorkflowReport, error) {
	if err := wf.Validate(); err != nil {
		return nil, err
	}
	s := newRunState(e, wf)
	s.emitAll(event.WorkflowNodeQueued, 0, "")
	err := s.runAll(ctx)
	return s.buildReport(err), err
}

func newRunState(e *Engine, wf *Workflow) *runState {
	index := make(map[string]int, len(wf.Nodes))
	for i := range wf.Nodes {
		index[wf.Nodes[i].ID] = i
	}
	fixers := wf.fixerSet(index)
	s := &runState{
		eng:        e,
		wf:         wf,
		runID:      newRunID(),
		bag:        &reportBag{},
		index:      index,
		inEdges:    make(map[string][]int, len(wf.Nodes)),
		outEdges:   make(map[string][]int, len(wf.Nodes)),
		fixers:     fixers,
		status:     make(map[string]nodeStatus, len(wf.Nodes)),
		term:       make(map[string]terminal, len(wf.Nodes)),
		gatePassed: make(map[string]bool, len(wf.Nodes)),
		output:     make(map[string]string, len(wf.Nodes)),
		result:     make(map[string]any, len(wf.Nodes)),
		findings:   make(map[string][]Finding, len(wf.Nodes)),
	}
	addEdge := func(from, to, when string) {
		if when == "" {
			when = WhenAlways
		}
		idx := len(s.edges)
		s.edges = append(s.edges, schedEdge{from: from, to: to, when: when})
		s.inEdges[to] = append(s.inEdges[to], idx)
		s.outEdges[from] = append(s.outEdges[from], idx)
	}
	for _, ed := range wf.Edges {
		addEdge(ed.From, ed.To, ed.When)
	}
	// Implicit target→confirm dependency (validateEdges accepted the same
	// rule during validation).
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type == NodeConfirm {
			addEdge(n.Target, n.ID, WhenAlways)
		}
	}
	for i := range wf.Nodes {
		id := wf.Nodes[i].ID
		if fixers[id] {
			s.status[id] = stFixer
		} else {
			s.status[id] = stPending
		}
	}
	return s
}

func newRunID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "wf-unknown"
	}
	return "wf-" + hex.EncodeToString(b[:])
}

// runAll is the sequential scheduler: repeatedly scan in declaration order,
// execute the first node whose in-edges have all decided with at least one
// active, or skip a node whose in-edges all decided inactive (skip
// propagates by deciding its out-edges inactive). Terminates because the
// graph is acyclic and each pass either runs or skips exactly one node.
func (s *runState) runAll(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		progressed := false
		for i := range s.wf.Nodes {
			n := &s.wf.Nodes[i]
			if s.status[n.ID] != stPending {
				continue
			}
			// A confirm whose target never ran (skipped branch) has nothing
			// to confirm: skip alongside it rather than confirm empty air.
			if n.Type == NodeConfirm && s.status[n.Target] == stSkipped {
				s.skip(n)
				progressed = true
				break
			}
			decided, active := s.inState(n.ID)
			if !decided {
				continue
			}
			if !active {
				s.skip(n)
				progressed = true
				break
			}
			if err := s.execute(ctx, n); err != nil {
				return err
			}
			progressed = true
			break
		}
		if !progressed {
			return nil
		}
	}
}

// inState reports whether every in-edge has decided and whether any is active.
// A node with no in-edges (root) is immediately decided+active.
func (s *runState) inState(id string) (decided, active bool) {
	idxs := s.inEdges[id]
	if len(idxs) == 0 {
		return true, true
	}
	decided = true
	for _, ei := range idxs {
		e := &s.edges[ei]
		if !e.decided {
			decided = false
		}
		if e.active {
			active = true
		}
	}
	return decided, active
}

// skip marks a node unreachable (no active in-edge): no terminal event — it
// stays queued-gray in the panel — and its out-edges decide inactive so the
// skip cascades.
func (s *runState) skip(n *Node) {
	s.status[n.ID] = stSkipped
	s.term[n.ID] = termSkipped
	s.resolveOutEdges(n.ID, termSkipped)
}

// resolveOutEdges decides every out-edge of a terminal node per its branch
// semantics: pass edges activate only when the source is a gate that passed,
// fail edges only when the source is a gate that failed, always edges on any
// node that actually ran (skipped sources decide inactive — never ran, never
// feeds downstream).
func (s *runState) resolveOutEdges(id string, t terminal) {
	for _, ei := range s.outEdges[id] {
		e := &s.edges[ei]
		e.decided = true
		switch {
		case t == termSkipped:
			e.active = false
		case e.when == WhenPass:
			e.active = s.gatePassed[id]
		case e.when == WhenFail:
			e.active = s.gateFailed(id)
		default:
			e.active = true
		}
	}
}

// gateFailed reports a gate that ran and exited non-zero (the run only
// reaches edge resolution for such a gate when a receiving branch exists).
func (s *runState) gateFailed(id string) bool {
	passed, ran := s.gatePassed[id]
	return ran && !passed
}
