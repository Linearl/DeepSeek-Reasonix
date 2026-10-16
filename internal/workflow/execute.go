package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/event"
)

// execute runs one ready node to a terminal and resolves its out-edges.
// Errors abort the whole run (fail-fast for subagents, world-failures for
// gates); the caller always keeps the report built so far.
func (s *runState) execute(ctx context.Context, n *Node) error {
	s.status[n.ID] = stRunning
	// Gates emit their own per-round running events (with round numbers), so
	// the generic running emit only covers the other four node types.
	if n.Type != NodeGate {
		s.emit(n, event.WorkflowNodeRunning, 0, "")
	}
	var err error
	switch n.Type {
	case NodeSubagent:
		err = s.runSubagent(ctx, n)
	case NodeGate:
		err = s.runGate(ctx, n)
	case NodePhase:
		s.phase = n.Label
		s.finish(n, event.WorkflowNodeCompleted, termPassed, "")
	case NodeJoin:
		err = s.runJoin(n)
	case NodeConfirm:
		err = s.runConfirm(ctx, n)
	default:
		err = fmt.Errorf("workflow: node %q: unknown type %q", n.ID, n.Type)
	}
	if err != nil {
		s.status[n.ID] = stAborted
		s.failedNode = n.ID
		round := 0
		if n.Type == NodeGate {
			round = s.gateRound // the attempt the world-failure hit
		}
		s.emit(n, event.WorkflowNodeFailed, round, truncate(err.Error(), 240))
		return err
	}
	return nil
}

// finish stamps a successful terminal and resolves out-edges.
func (s *runState) finish(n *Node, state event.WorkflowNodeState, t terminal, detail string) {
	s.status[n.ID] = stDone
	s.term[n.ID] = t
	s.emit(n, state, 0, detail)
	s.resolveOutEdges(n.ID, t)
}

// runSubagent dispatches one sub-agent node. A node declaring a result
// schema must answer JSON; findings extracted from findings_field land in the
// report bag IMMEDIATELY with status unconfirmed — confirmation later
// promotes them in place. (Fixers never come here: they run only inside
// their gate's loop via runFixer, and validation gives them no edges.)
func (s *runState) runSubagent(ctx context.Context, n *Node) error {
	prompt := s.interpolate(n.Prompt)
	if len(n.Result) > 0 {
		prompt += "\n\n" + resultHint(n)
	}
	answer, err := s.eng.runSubagent(ctx, SubagentRequest{
		NodeID:      n.ID,
		Prompt:      prompt,
		Description: n.Description,
		Tools:       n.Tools,
		Model:       n.Model,
		Effort:      n.Effort,
		MaxSteps:    n.MaxSteps,
	})
	if err != nil {
		return fmt.Errorf("workflow: subagent %q: %w", n.ID, err)
	}
	s.output[n.ID] = answer
	if len(n.Result) > 0 {
		obj, err := parseResultJSON(answer)
		if err != nil {
			return fmt.Errorf("workflow: subagent %q: typed result: %w", n.ID, err)
		}
		s.result[n.ID] = obj
		if n.FindingsField != "" {
			fs, err := extractFindings(obj, n.FindingsField)
			if err != nil {
				return fmt.Errorf("workflow: subagent %q: %w", n.ID, err)
			}
			for _, f := range fs {
				s.bag.add(f)
			}
			s.findings[n.ID] = fs
		}
	}
	s.finish(n, event.WorkflowNodeCompleted, termPassed, "")
	return nil
}

// runFixer executes the loop's repair sub-agent with the gate's failing
// output appended as feedback (the stderr feedback loop of point 3).
func (s *runState) runFixer(ctx context.Context, fixer *Node, feedback string, round int) (string, error) {
	s.status[fixer.ID] = stRunning
	s.emit(fixer, event.WorkflowNodeRunning, round, "")
	prompt := fixer.Prompt + "\n\n" + feedback
	answer, err := s.eng.runSubagent(ctx, SubagentRequest{
		NodeID:      fixer.ID,
		Prompt:      prompt,
		Description: fixer.Description,
		Tools:       fixer.Tools,
		Model:       fixer.Model,
		Effort:      fixer.Effort,
		MaxSteps:    fixer.MaxSteps,
	})
	if err != nil {
		s.status[fixer.ID] = stAborted
		s.emit(fixer, event.WorkflowNodeFailed, round, truncate(err.Error(), 240))
		return "", fmt.Errorf("workflow: fixer %q: %w", fixer.ID, err)
	}
	s.status[fixer.ID] = stDone
	s.output[fixer.ID] = answer
	s.emit(fixer, event.WorkflowNodeCompleted, round, "")
	return answer, nil
}

// runGate is the deterministic gate: literal command, exit code as a VALUE,
// optional fix-until-pass loop with a round cap, and two-tier semantics.
func (s *runState) runGate(ctx context.Context, n *Node) error {
	timeout := n.TimeoutDuration()
	maxRounds := 1
	if n.Loop != nil {
		maxRounds = n.Loop.MaxRounds
	}
	var last GateResult
	for round := 1; ; round++ {
		s.gateRound = round
		s.emit(n, event.WorkflowNodeRunning, round, "")
		res, err := s.eng.gate()(ctx, n.Command, timeout)
		if err != nil {
			// World failure (timeout/launch/cancel): not a branch value.
			return fmt.Errorf("workflow: gate %q: %w", n.ID, err)
		}
		last = res
		if res.ExitCode == 0 {
			s.gatePassed[n.ID] = true
			s.output[n.ID] = res.Output
			s.verifs = append(s.verifs, fmt.Sprintf("%s (exit 0)", n.Command))
			s.status[n.ID] = stDone
			s.term[n.ID] = termPassed
			s.emit(n, event.WorkflowNodeGatePassed, round, "exit 0")
			s.resolveOutEdges(n.ID, termPassed)
			return nil
		}
		if n.Loop == nil || round >= maxRounds {
			break
		}
		// Round budget remains: hand stderr/output to the fixer, retry.
		fixer := &s.wf.Nodes[s.index[n.Loop.Fixer]]
		feedback := fmt.Sprintf(
			"The check failed (round %d/%d, exit %d):\n%s",
			round, maxRounds, res.ExitCode, strings.TrimSpace(res.Output),
		)
		if _, err := s.runFixer(ctx, fixer, feedback, round); err != nil {
			return err
		}
	}
	// Non-zero exit with no rounds left: a value the branches read.
	s.gatePassed[n.ID] = false
	s.output[n.ID] = last.Output
	if !s.hasFailReceiver(n.ID) {
		return fmt.Errorf("workflow: gate %q failed (exit %d) and no fail branch receives it", n.ID, last.ExitCode)
	}
	s.status[n.ID] = stDone
	s.term[n.ID] = termGateFailed
	s.emit(n, event.WorkflowNodeFailed, maxRounds, fmt.Sprintf("exit %d", last.ExitCode))
	s.resolveOutEdges(n.ID, termGateFailed)
	return nil
}

// hasFailReceiver reports whether some out-edge would activate on failure
// (fail or always edges — a pass-only gate failure strands the graph).
func (s *runState) hasFailReceiver(id string) bool {
	for _, ei := range s.outEdges[id] {
		if s.edges[ei].when != WhenPass {
			return true
		}
	}
	return false
}

// runJoin aggregates every active upstream output into a {node: result} map
// for downstream interpolation.
func (s *runState) runJoin(n *Node) error {
	joined := make(map[string]any, len(s.inEdges[n.ID]))
	for _, ei := range s.inEdges[n.ID] {
		e := s.edges[ei]
		if !e.active {
			continue
		}
		if r, ok := s.result[e.from]; ok {
			joined[e.from] = r
		} else {
			joined[e.from] = s.output[e.from]
		}
	}
	raw, err := json.Marshal(joined)
	if err != nil {
		return fmt.Errorf("workflow: join %q: %w", n.ID, err)
	}
	s.result[n.ID] = joined
	s.output[n.ID] = string(raw)
	s.finish(n, event.WorkflowNodeCompleted, termPassed, "")
	return nil
}

// confirmOutcome is the confirmer's verdict.
type confirmOutcome struct {
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason"`
}

// runConfirm is the independent confirmation protocol (point 4): a second,
// read-only sub-agent reproduces the target's findings from evidence alone.
// Failure KEEPS every finding and labels it unconfirmed — never drops, never
// self-confirms (the confirmer is a distinct node from the finder).
func (s *runState) runConfirm(ctx context.Context, n *Node) error {
	target := &s.wf.Nodes[s.index[n.Target]]
	evidence := s.output[n.Target]
	if len(target.Result) > 0 {
		if obj, ok := s.result[n.Target]; ok {
			raw, _ := json.Marshal(obj)
			evidence = string(raw)
		}
	}
	prompt := fmt.Sprintf(
		"You are an independent confirmer sub-agent. Reproduce the following finding from its evidence alone — inspect the code, run a check if one decides it, never edit anything. The original finder does not get to confirm itself.\n\nEvidence:\n%s",
		evidence,
	)
	if strings.TrimSpace(n.Prompt) != "" {
		prompt = s.interpolate(n.Prompt) + "\n\n" + prompt
	}
	answer, err := s.eng.runSubagent(ctx, SubagentRequest{
		NodeID:      n.ID,
		Prompt:      prompt,
		Description: n.Description,
		Tools:       n.Tools,
		Model:       n.Model,
		Effort:      n.Effort,
		MaxSteps:    n.MaxSteps,
		ReadOnly:    true,
	})
	if err != nil || !parseConfirmed(answer) {
		// Confirmation failed (confirmer error or verdict false): KEEP the
		// findings, label unconfirmed, keep the run going — a suspicion worth
		// a human's eyes is not a workflow failure.
		detail := "not reproduced"
		if err != nil {
			detail = truncate(err.Error(), 200)
		}
		s.status[n.ID] = stDone
		s.term[n.ID] = termPassed
		s.emit(n, event.WorkflowNodeUnconfirmed, 0, detail)
		// The target's findings transition to unconfirmed state for the panel.
		s.emit(&s.wf.Nodes[s.index[n.Target]], event.WorkflowNodeUnconfirmed, 0, "confirmation failed")
		s.resolveOutEdges(n.ID, termPassed)
		return nil
	}
	// Reproduced: the target's bagged findings upgrade unconfirmed → verified
	// in place (never a re-add, never a drop).
	for _, f := range s.findings[n.Target] {
		s.bag.promote(f)
	}
	s.verifs = append(s.verifs, fmt.Sprintf("independent confirmation of %s by %s", n.Target, n.ID))
	s.finish(n, event.WorkflowNodeCompleted, termPassed, "")
	return nil
}

// parseConfirmed extracts the confirmer's boolean verdict. Unparseable,
// ambiguous, or negative answers all mean NOT confirmed (doubt survives).
func parseConfirmed(answer string) bool {
	trimmed := finalAnswerBody(answer)
	// Prefer a fenced/JSON blob inside prose.
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return false
	}
	var out confirmOutcome
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err != nil {
		return false
	}
	return out.Confirmed
}
