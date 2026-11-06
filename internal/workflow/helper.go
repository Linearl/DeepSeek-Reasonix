package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/jsonutil"
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
		fmt.Fprintf(&b, "If your input evidence lists findings that carry an \"id\" key, echo that id on your re-reported entry for the same issue — the engine merges duplicates by it instead of double-counting. Omit the key for issues you report new.\n")
	}
	return b.String()
}

// parseResultJSON decodes a typed result, escalating through tolerant
// extractions before giving up (tl1432 A — the audit found the single strict
// parse "overly strict": one embellished answer used to scrap the whole node):
//
//  1. the whole answer after "Final answer:" (the task substrate may wrap the
//     final answer with a reference block — prefer the text after the marker
//     so the wrapper's braces never pollute the slice);
//  2. the naive first-"{ … last-"}" slice (plain prose wrappers);
//  3. the last balanced top-level JSON object (jsonutil — the same extractor
//     goaleval uses). Markdown fences, surrounding prose and stray braces are
//     noise the balanced scan skips by construction; "last" (not first)
//     because thinking models routinely quote example objects before the real
//     answer.
//
// Truncated output is never salvaged — an object that never closes is
// incomplete data, and guessing a completion would fabricate results. The
// error names that case so the directed retry (tl1432 B) and the reader can
// tell it apart from mere noise.
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
	if candidate, ok := jsonutil.LastJSONObject(trimmed); ok {
		if err := json.Unmarshal([]byte(candidate), &obj); err == nil {
			return obj, nil
		}
	}
	msg := fmt.Sprintf("answer is not a JSON object: %s", truncate(trimmed, 120))
	if jsonutil.HasUnclosedObject(trimmed) {
		msg += " (output looks truncated — an object never closes)"
	}
	return nil, errors.New(msg)
}

// extractFindings pulls the findings array out of a typed result, stamping
// each accepted entry with the engine's source serial ("<nodeID>#<n>",
// tl1326 B1): the serial lands on the extracted Finding (the add/promote
// key) AND on the raw result-map entry, so {{node.findings}} carries it
// downstream and a reviewer can echo it back. An entry that already carries
// an "id" (a reviewer echoing a producer serial, or a producer minting its
// own stable tag) keeps it — the echo IS the merge mechanism. The array
// itself must exist when declared (a producer that silently drops its
// findings field breaks the confirmation contract); individual malformed
// entries are skipped rather than failing the node.
func extractFindings(obj map[string]any, field, sourcePrefix string) ([]Finding, error) {
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
		f.SourceID = findingString(m, "id")
		if f.SourceID == "" {
			f.SourceID = fmt.Sprintf("%s#%d", sourcePrefix, len(out)+1)
			m["id"] = f.SourceID
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

// upstreamCandidates collects the producer serials the interpolated prompt
// actually carries ("nodeID#n" → the producing node's original finding,
// tl1440). Only serials visible to THIS node may become rewrite-merge
// candidates — a reviewer cannot be held to echo (or be merged by) a serial
// it never saw, and a producer node's prompt (task text only) matches
// nothing, keeping scan nodes merge-free. Called in the serial head, where
// prompt interpolation happens; the returned map is a value snapshot, safe
// to carry into the concurrent segment.
func (s *runState) upstreamCandidates(prompt string) map[string]Finding {
	if !strings.Contains(prompt, "#") {
		return nil
	}
	var out map[string]Finding
	for _, fs := range s.findings {
		for _, f := range fs {
			if f.SourceID == "" || !strings.Contains(prompt, f.SourceID) {
				continue
			}
			if out == nil {
				out = make(map[string]Finding, len(fs))
			}
			out[f.SourceID] = f
		}
	}
	return out
}

// mergeEchoedFindings is the deterministic fallback for the rewrite-merge
// contract (tl1440). tl1326 B1 lets a reviewer merge its rewrite into the
// original entry by echoing the source serial the prompt carried — but that
// relies on the model restating the serial, and when it doesn't, the
// confirmed rewrite ("核实成立: …") lands as a NEW bag entry: the
// (where, what) idempotency key misses precisely because the what was
// rewritten (run wf-04d73376e38d: 10 bag entries for 7 real issues, +43%).
//
// For every extracted finding whose serial was engine-minted for THIS node
// ("<nodeID>#n" — i.e. the model did NOT echo an id) and whose where matches
// exactly one candidate serial the prompt carried, the entry is re-keyed to
// that producer serial, in the extracted slice AND the raw result map
// (m["id"]). bag.add then takes its same-source path (wording/evidence/
// severity updated in place, count unchanged), and the confirm node's
// evidence — which serializes the re-keyed raw map — lets verdicts promote
// the ORIGINAL entry by its own serial. One mutation site, no second
// matcher: the add/promote key (sameFinding) is untouched (tl1326 hard rule).
//
// Guards — a mis-merge here would silently destroy distinct findings:
//   - an empty where never matches (ghost entries are counted and appended
//     as-is — audit run wf-04d73376e38d ⑥: the rejected list carried a
//     ghost, empty-where entry);
//   - two candidates sharing the same where are distinct issues in one file:
//     none of them merges (the over-merge guard from G3, preserved);
//   - one candidate serial absorbs at most one rewrite per node completion —
//     a second reviewer entry on the same where stays its own entry.
//
// Returns the counters for the caller's degradation log: merged (rewrites
// re-keyed — the model did not honor the echo contract, engine recovered),
// skippedMulti (same-where candidate sets left unmerged), ghost (empty-where
// entries that can never merge).
func mergeEchoedFindings(nodeID, field string, obj map[string]any, fs []Finding, cands map[string]Finding) (merged, skippedMulti, ghost int) {
	if len(cands) == 0 || len(fs) == 0 {
		return 0, 0, 0
	}
	arr, ok := obj[field].([]any)
	if !ok {
		return 0, 0, 0
	}
	rawByID := make(map[string]map[string]any, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			if id := findingString(m, "id"); id != "" {
				rawByID[id] = m
			}
		}
	}
	selfPrefix := nodeID + "#"
	used := make(map[string]bool, len(fs))
	for i := range fs {
		f := &fs[i]
		if f.SourceID == "" || !strings.HasPrefix(f.SourceID, selfPrefix) {
			continue // echoed serial or model-minted tag: add/promote handle it
		}
		if f.Where == "" {
			ghost++ // ghost where never matches — nothing to key on
			continue
		}
		var orig Finding
		found, multi := false, false
		for sid, cand := range cands {
			if cand.Where != f.Where || used[sid] {
				continue
			}
			if found {
				multi = true
				break
			}
			found, orig = true, cand
		}
		if multi {
			skippedMulti++ // same where, ≥2 upstream issues: over-merge guard
			continue
		}
		if !found {
			continue // genuinely new issue — belongs in the bag as its own entry
		}
		used[orig.SourceID] = true
		if m := rawByID[f.SourceID]; m != nil {
			m["id"] = orig.SourceID // downstream interpolations see the same key
		}
		f.SourceID = orig.SourceID
		merged++
	}
	return merged, skippedMulti, ghost
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
	findings := s.bag.snapshot()
	// Conclusion counting note (tl1440): when reports were merged away
	// (same-source rewrites or the where-fallback), the findings array is
	// deliberately SHORTER than what the nodes reported. Without the note a
	// reader sees "N findings" next to a conclusion counting fewer and cannot
	// tell dedup from loss (run wf-04d73376e38d: 10 entries shown, conclusion
	// counted 6 — the gap was duplicates the reviewer had reworded). The note
	// only appears when a merge actually happened; count-per-status so the
	// two number families reconcile on sight.
	if raw := s.bag.reportedTotal(); raw > len(findings) {
		verified, unconfirmed := 0, 0
		for _, f := range findings {
			switch f.Status {
			case FindingVerified:
				verified++
			case FindingUnconfirmed:
				unconfirmed++
			}
		}
		conclusion += fmt.Sprintf(
			" (dedup note: %d findings were reported across nodes and merged into %d entries — %d verified, %d unconfirmed; merged rewrites update their original entry in place)",
			raw, len(findings), verified, unconfirmed)
	}
	return &WorkflowReport{
		Conclusion: conclusion,
		Findings:   findings,
		Verified:   append([]string(nil), s.verifs...),
		NotCovered: notCovered,
	}
}
