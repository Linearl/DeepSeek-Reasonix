package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/event"
)

// gate returns the configured gate runner (shell default).
func (e *Engine) gate() GateRunner {
	if e.Gate == nil {
		return ShellGateRunner
	}
	return e.Gate
}

// runSubagent dispatches through the injected sub-agent runner.
func (e *Engine) runSubagent(ctx context.Context, req SubagentRequest) (string, error) {
	if e.Subagents == nil {
		return "", fmt.Errorf("workflow: no subagent runner configured")
	}
	return e.Subagents.Run(ctx, req)
}

// emit publishes one node state transition on the event bus (point 6). The
// payload is the frozen 686 contract — see the handoff schema chapter.
func (s *runState) emit(n *Node, state event.WorkflowNodeState, round int, detail string) {
	if s.eng.Sink == nil {
		return
	}
	s.eng.Sink.Emit(event.Event{
		Kind: event.WorkflowNode,
		Workflow: &event.WorkflowNodeInfo{
			RunID:    s.runID,
			Workflow: s.wf.Name,
			NodeID:   n.ID,
			NodeType: n.Type,
			State:    state,
			Phase:    s.phase,
			Round:    round,
			Detail:   detail,
		},
	})
}

// emitAll fires one state for every node in the graph (run start: queued for
// the whole board, fixers included so the panel draws them gray too).
func (s *runState) emitAll(state event.WorkflowNodeState, round int, detail string) {
	for i := range s.wf.Nodes {
		s.emit(&s.wf.Nodes[i], state, round, detail)
	}
}

// interpolate resolves {{node}} / {{node.field}} references against outputs
// of nodes that have run. Validation already proved every reference names an
// ancestor, so resolution can only miss when a run died first — missing refs
// resolve empty rather than leaking template syntax downstream.
func (s *runState) interpolate(tmpl string) string {
	if !strings.Contains(tmpl, "{{") {
		return tmpl
	}
	return refPattern.ReplaceAllStringFunc(tmpl, func(m string) string {
		sub := refPattern.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		nodeID, field := sub[1], sub[2]
		if field == "" {
			return s.output[nodeID]
		}
		obj, ok := s.result[nodeID].(map[string]any)
		if !ok {
			return ""
		}
		return stringifyValue(obj[field])
	})
}

func stringifyValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(raw)
	}
}

// resultHint renders the typed-result contract the sub-agent actually reads
// (SKILL.md §4: field descriptions become the prompt's calibration).
func resultHint(n *Node) string {
	var b strings.Builder
	b.WriteString("Return your final answer as a single JSON object with exactly these fields:\n")
	for _, f := range n.Result {
		fmt.Fprintf(&b, "- %s (%s): %s\n", f.Name, f.Type, f.Description)
	}
	if n.FindingsField != "" {
		fmt.Fprintf(&b, "The %s field is an array of objects with keys: where (string), what (string), evidence (string), severity (low|medium|high).\n", n.FindingsField)
	}
	return b.String()
}

// parseResultJSON decodes a typed result: the whole answer, or the first
// JSON object embedded in prose/fences (sub-agents embellish). The task
// substrate may wrap the final answer with a reference block — prefer the
// text after "Final answer:" when present so the wrapper's braces never
// pollute the slice.
func parseResultJSON(answer string) (map[string]any, error) {
	trimmed := finalAnswerBody(answer)
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		return obj, nil
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(trimmed[start:end+1]), &obj); err == nil {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("answer is not a JSON object: %s", truncate(trimmed, 120))
}

// extractFindings pulls the findings array out of a typed result. The array
// itself must exist when declared (a producer that silently drops its
// findings field breaks the confirmation contract); individual malformed
// entries are skipped rather than failing the node.
func extractFindings(obj map[string]any, field string) ([]Finding, error) {
	raw, ok := obj[field]
	if !ok {
		return nil, fmt.Errorf("declared findings_field %q missing from result", field)
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("findings_field %q is not an array", field)
	}
	var out []Finding
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		f := Finding{
			Where:    findingString(m, "where"),
			What:     findingString(m, "what"),
			Evidence: findingString(m, "evidence"),
			Severity: findingString(m, "severity"),
			Status:   FindingUnconfirmed, // never verified before confirmation
		}
		if f.Where == "" && f.What == "" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func findingString(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(stringifyValue(v))
	}
}

// finalAnswerBody returns the part of a sub-agent reply after the task
// substrate's "Final answer:" marker (or the whole reply when unwrapped).
func finalAnswerBody(answer string) string {
	if idx := strings.LastIndex(answer, "Final answer:"); idx >= 0 {
		return strings.TrimSpace(answer[idx+len("Final answer:"):])
	}
	return strings.TrimSpace(answer)
}

// truncate shortens detail strings for event payloads (never the full
// output — the panel shows a chip, not a log).
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// buildReport assembles the final WorkflowReport from the bag plus the
// static report spec. It runs on EVERY exit path — success, node failure,
// cancellation — so reported findings are never lost (point 5).
func (s *runState) buildReport(runErr error) *WorkflowReport {
	total := len(s.wf.Nodes)
	ran := 0
	for i := range s.wf.Nodes {
		if s.status[s.wf.Nodes[i].ID] == stDone {
			ran++
		}
	}
	var conclusion string
	switch {
	case runErr != nil && s.failedNode != "":
		conclusion = fmt.Sprintf("Workflow %q stopped at node %q: %v. Findings reported before the stop are preserved below.",
			s.wf.Name, s.failedNode, runErr)
	case runErr != nil:
		conclusion = fmt.Sprintf("Workflow %q stopped before completion: %v. Findings reported before the stop are preserved below.",
			s.wf.Name, runErr)
	default:
		conclusion = buildConclusion(s.wf, s.interpolate(s.wf.Report.Conclusion), ran, total, "")
	}
	notCovered := append([]string(nil), s.wf.Report.NotCovered...)
	return &WorkflowReport{
		Conclusion: conclusion,
		Findings:   s.bag.snapshot(),
		Verified:   append([]string(nil), s.verifs...),
		NotCovered: notCovered,
	}
}
