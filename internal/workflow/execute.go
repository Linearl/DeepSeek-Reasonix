package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
)

// execute runs one INLINE node to a terminal and resolves its out-edges —
// gates (with their fixer loop), joins and phases only. Subagent/confirm
// nodes dispatch through runAll's wave instead (tl1326 G4): their whole body
// except the runner call is state mutation, so the runner call is the only
// part that may leave the single writer. Errors abort the whole run
// (fail-fast); the caller always keeps the report built so far.
func (s *runState) execute(ctx context.Context, n *Node) error {
	s.status[n.ID] = stRunning
	// Gates emit their own per-round running events (with round numbers), so
	// the generic running emit only covers the other inline node types.
	if n.Type != NodeGate {
		s.emit(n, event.WorkflowNodeRunning, 0, "")
	}
	var err error
	switch n.Type {
	case NodeGate:
		err = s.runGate(ctx, n)
	case NodePhase:
		// phase 标签只由 phase 节点在串行扫描段写（tl1326 G4 语义决策）：
		// 并发波次里所有节点共享「最近进入的工作流分区」，原义不变——
		// 不删字段、不改集合语义，686 事件契约的 Phase 字段保持原样。
		s.phase = n.Label
		s.finish(n, event.WorkflowNodeCompleted, termPassed, "")
	case NodeJoin:
		err = s.runJoin(n)
	default:
		err = fmt.Errorf("workflow: node %q: unknown type %q", n.ID, n.Type)
	}
	if err != nil {
		round := 0
		if n.Type == NodeGate {
			round = s.gateRound // the attempt the world-failure hit
		}
		return s.abortNode(n, err, round)
	}
	return nil
}

// abortNode is the failure tail shared by inline and dispatched nodes
// (single writer): abort status, failed-node stamp, failed event, and the
// 1316 checkpoint——崩溃前的最后一次投影把已完成节点全部落袋（abort 节点
// 本身不落，恢复时重跑）。
func (s *runState) abortNode(n *Node, err error, round int) error {
	s.status[n.ID] = stAborted
	s.failedNode = n.ID
	s.emit(n, event.WorkflowNodeFailed, round, truncate(err.Error(), 240))
	s.checkpoint()
	return err
}

// nodeCall is one dispatched node: the concurrent segment (call) plus the
// single-writer completion (complete) for the aggregation pass.
type nodeCall struct {
	call     func() (string, error)
	complete func(answer string, err error) error
}

// beginNode is the serial head of a dispatched node (tl1326 G4): running
// status + event, prompt fully interpolated — everything that reads
// runState happens here, on the writer. The returned closures capture only
// values: the concurrent segment touches no runState field.
func (s *runState) beginNode(ctx context.Context, n *Node) *nodeCall {
	s.status[n.ID] = stRunning
	s.emit(n, event.WorkflowNodeRunning, 0, "")
	var req SubagentRequest
	switch n.Type {
	case NodeConfirm:
		req = s.confirmRequest(n)
	default: // NodeSubagent
		req = s.subagentRequest(n)
	}
	// tl1440 候选池在 serial head 收集：插值后的 prompt 是「该节点看得见哪些
	// 来源序号」的权威边界（复核看不见的序号不可归并，扫描节点的纯任务
	// prompt 一个都不含）。cands 是值快照，并发段不碰 runState。
	var cands map[string]Finding
	if n.Type == NodeSubagent {
		cands = s.upstreamCandidates(req.Prompt)
	}
	// tl1432 B 预算在 serial head 解析（读 Engine 字段），并发段只捕获值。
	retries := 0
	if n.Type == NodeSubagent && len(n.Result) > 0 {
		retries = s.eng.resultParseRetries()
	}
	return &nodeCall{
		call: func() (string, error) {
			answer, err := s.eng.runSubagent(ctx, req)
			if err != nil || retries == 0 {
				return answer, err
			}
			// 容错提取也失败才重发；可解析（含三级容错）原样返回。
			if _, perr := parseResultJSON(answer); perr == nil {
				return answer, nil
			}
			return directedJSONRetry(ctx, s.eng, req, answer, retries)
		},
		complete: func(answer string, err error) error {
			if n.Type == NodeConfirm {
				return s.completeConfirm(n, answer, err)
			}
			return s.completeSubagent(n, answer, err, cands)
		},
	}
}

// directedRetryInstruction is the directive appended to the ORIGINAL prompt
// for the re-ask (tl1432 B): the node's typed-result contract (resultHint)
// already sits at the end of that prompt, so "the contract above" resolves.
const directedRetryInstruction = "Your previous answer could not be parsed as a JSON object. Answer again with ONLY one JSON object matching the contract above — no prose before or after it, no markdown fences, no other text."

// directedJSONRetry re-asks the model up to retries times (tl1432 B, default
// 1): the original prompt plus the directive, each attempt under the NEXT
// Occurrence so the task panel registers a fresh row per execution (tl1324 ⑤:
// an id is never reused — plain nodes run at Occurrence 0/1, so the first
// re-ask takes 2; a fixer at round N would take N+1). A world failure
// (timeout / launch / cancel) is NOT retried — it returns immediately, same
// fail-fast semantics as the first call. When every attempt still fails to
// parse, the returned error carries the first AND last output summaries so
// the failure is diagnosable without rerunning the node.
func directedJSONRetry(ctx context.Context, eng *Engine, req SubagentRequest, first string, retries int) (string, error) {
	answer := first
	next := req.Occurrence
	if next < 1 {
		next = 1
	}
	for attempt := 1; attempt <= retries; attempt++ {
		retry := req
		next++
		retry.Occurrence = next
		retry.Prompt = req.Prompt + "\n\n" + directedRetryInstruction
		var err error
		answer, err = eng.runSubagent(ctx, retry)
		if err != nil {
			return "", err
		}
		if _, perr := parseResultJSON(answer); perr == nil {
			return answer, nil
		}
	}
	return "", fmt.Errorf("%s detail: first output: %s | retry output: %s",
		fmt.Sprintf(i18n.M.WorkflowTypedResultRetryExhaustedFmt, retries),
		truncate(first, 120), truncate(answer, 120))
}

// subagentRequest builds one subagent node's dispatch (serial head: reads n
// and other nodes' outputs via interpolate).
func (s *runState) subagentRequest(n *Node) SubagentRequest {
	prompt := s.interpolate(n.Prompt)
	if len(n.Result) > 0 {
		prompt += "\n\n" + resultHint(n)
	}
	return SubagentRequest{
		NodeID:      n.ID,
		Prompt:      prompt,
		Description: n.Description,
		Tools:       n.Tools,
		Model:       n.Model,
		Effort:      n.Effort,
		MaxSteps:    n.MaxSteps,
	}
}

// confirmRequest builds one confirm node's dispatch (serial head). The
// evidence is the target's typed result — whose findings entries carry the
// engine's source serials since tl1326 B1, so the confirmer can answer
// per-item verdicts the engine can promote by.
func (s *runState) confirmRequest(n *Node) SubagentRequest {
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
	prompt += "\n\n" + confirmResultContract
	return SubagentRequest{
		NodeID:      n.ID,
		Prompt:      prompt,
		Description: n.Description,
		Tools:       n.Tools,
		Model:       n.Model,
		Effort:      n.Effort,
		MaxSteps:    n.MaxSteps,
		ReadOnly:    true,
	}
}

// dispatchAndCollect is G4's wave: every call runs its runner concurrently
// (the concurrent segment — pure I/O, no runState access), then the answers
// aggregate through the single writer in wave order (= declaration order),
// so events, bag order and checkpoints stay deterministic. Fail-fast: the
// first failing node in wave order aborts the run; later answers are drained
// (buffered channel — no goroutine leak) and discarded; the report keeps
// everything aggregated before the failure (salvage semantics unchanged).
// Parallel dispatches are bounded by the agent substrate's session scheduler
// (write claims / DefaultMaxParallelWriters) — the engine adds no second
// concurrency limit and creates no new concurrency semantics.
func (s *runState) dispatchAndCollect(calls []*nodeCall) error {
	type answer struct {
		i      int
		answer string
		err    error
	}
	ch := make(chan answer, len(calls))
	for i, c := range calls {
		go func(i int, call func() (string, error)) {
			a, err := call()
			ch <- answer{i: i, answer: a, err: err}
		}(i, c.call)
	}
	got := make([]answer, len(calls))
	for range calls {
		a := <-ch
		got[a.i] = a
	}
	for i, c := range calls {
		if err := c.complete(got[i].answer, got[i].err); err != nil {
			return err
		}
	}
	return nil
}

// completeSubagent is a dispatched subagent node's tail on the single writer
// (the runSubagent body after the runner call): store the answer, parse the
// typed result, land findings (tl1326 B1 stamped with source serials, tl1440
// echo-miss rewrites re-keyed onto their producer serial), stamp the
// terminal. cands is the serial head's value snapshot of the serials this
// node's prompt actually carried (upstreamCandidates).
func (s *runState) completeSubagent(n *Node, answer string, err error, cands map[string]Finding) error {
	if err != nil {
		return s.abortNode(n, fmt.Errorf("workflow: subagent %q: %w", n.ID, err), 0)
	}
	s.output[n.ID] = answer
	if len(n.Result) > 0 {
		obj, err := parseResultJSON(answer)
		if err != nil {
			return s.abortNode(n, fmt.Errorf("workflow: subagent %q: typed result: %w", n.ID, err), 0)
		}
		s.result[n.ID] = obj
		if n.FindingsField != "" {
			fs, err := extractFindings(obj, n.FindingsField, n.ID)
			if err != nil {
				return s.abortNode(n, fmt.Errorf("workflow: subagent %q: %w", n.ID, err), 0)
			}
			merged, skippedMulti, ghost := mergeEchoedFindings(n.ID, n.FindingsField, obj, fs, cands)
			if merged > 0 || ghost > 0 {
				// Degradation made visible (tl1440): the model did not echo
				// the source serials (echo was the primary merge mechanism),
				// the engine recovered by deterministic where-matching; or
				// the output carried ghost (empty-where) entries. Either way
				// this must never happen silently — a silent fallback is how
				// the +43% duplicate inflation went unnoticed for a full run.
				slog.Warn("workflow: findings merge fallback engaged (source-id echo contract not fully honored)",
					"run", s.runID, "node", n.ID,
					"merged_by_where", merged, "skipped_multi_where", skippedMulti, "ghost_where", ghost)
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

// finish stamps a successful terminal and resolves out-edges.
func (s *runState) finish(n *Node, state event.WorkflowNodeState, t terminal, detail string) {
	s.status[n.ID] = stDone
	s.term[n.ID] = t
	s.emit(n, state, 0, detail)
	s.resolveOutEdges(n.ID, t)
	s.checkpoint() // 1316：节点终态即 checkpoint 点
}

// runSubagent was split for tl1326 G4: subagentRequest (serial head) +
// eng.runSubagent (the only concurrent segment) + completeSubagent (serial
// tail). Fixers never come here: they run only inside their gate's loop via
// runFixer, and validation gives them no edges.

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
		// fixer 每轮重跑同一 nodeID：occurrence=轮次保证任务面板登记 id
		// 不复用（tl1324 ⑤）。
		Occurrence: round,
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
			s.checkpoint() // 1316：gate 不走 finish()，终态在此入快照
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
	s.checkpoint() // 1316：gate_failed 分支值也入快照
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

// confirmOutcome is the confirmer's verdict. Verdicts (tl1326 B1) carry the
// per-finding booleans referencing the engine's source serials from the
// evidence; their presence turns promotion into per-item (partial refutation
// keeps the refuted findings unconfirmed), their absence keeps the pre-B1
// all-or-nothing promote of the target's findings.
type confirmOutcome struct {
	Confirmed bool             `json:"confirmed"`
	Reason    string           `json:"reason"`
	Verdicts  []confirmVerdict `json:"verdicts,omitempty"`
}

// confirmVerdict is one per-finding verdict (tl1326 B1): ID is the engine's
// source serial copied verbatim from the evidence ("nodeID#n").
type confirmVerdict struct {
	ID        string `json:"id"`
	Confirmed bool   `json:"confirmed"`
	Note      string `json:"note,omitempty"`
}

// confirmResultContract is the typed-result contract appended to every
// confirmer prompt (audit 2026-11-01 gap G2): without it the confirmer never
// learns the verdict shape, parseConfirmed reads every answer as "missing key
// ⇒ false", and the gate can never open — the target node is written back as
// unconfirmed even when the human-readable verdict was "reproduced".
// The verdicts paragraph (tl1326 B1) lets the confirmer answer per finding
// and merge-deduplicate against the producer's serials.
const confirmResultContract = `Your verdict must be a single JSON object and nothing else:
{"confirmed": true, "reason": "<what you reproduced, or why you could not>"}
Set "confirmed" to true only when you actually reproduced the finding from the evidence. The host reads this object programmatically; a verdict without it counts as not confirmed.
When the evidence contains findings entries carrying an "id" key, also return a "verdicts" array with one entry per finding you checked: {"id": "<copy the id verbatim>", "confirmed": true|false, "note": "<what you reproduced or refuted>"}. Omit the verdict entry for findings you could not check (they stay unconfirmed); a "confirmed": true overall with verdicts promotes only the verdict entries marked true.`

// runConfirm was split for tl1326 G4: confirmRequest (serial head) +
// eng.runSubagent (concurrent segment, read-only) + completeConfirm (serial
// tail) below.
//
// completeConfirm is the independent confirmation protocol's tail (point 4):
// failure KEEPS every finding and labels it unconfirmed — never drops, never
// self-confirms (the confirmer is a distinct node from the finder).
func (s *runState) completeConfirm(n *Node, answer string, err error) error {
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
	// Reproduced: promote the target's findings through the SAME key add
	// merges on (tl1326). With per-item verdicts only the confirmed source
	// serials flip — partial refutation keeps the refuted findings
	// unconfirmed and never drops them; without verdicts every target finding
	// promotes (pre-B1 all-or-nothing, kept for confirmers that predate the
	// serials). A serial the target never produced is not promotable.
	if outcome, ok := parseConfirmOutcome(answer); ok && len(outcome.Verdicts) > 0 {
		known := make(map[string]Finding, len(s.findings[n.Target]))
		for _, f := range s.findings[n.Target] {
			if f.SourceID != "" {
				known[f.SourceID] = f
			}
		}
		for _, v := range outcome.Verdicts {
			if !v.Confirmed {
				continue // refuted: stays unconfirmed, never dropped
			}
			f, ok := known[v.ID]
			if !ok {
				continue // hallucinated serial: nothing to promote
			}
			s.bag.promoteNote(f, v.Note)
		}
	} else {
		for _, f := range s.findings[n.Target] {
			s.bag.promote(f)
		}
	}
	s.verifs = append(s.verifs, fmt.Sprintf("independent confirmation of %s by %s", n.Target, n.ID))
	s.finish(n, event.WorkflowNodeCompleted, termPassed, "")
	return nil
}

// parseConfirmOutcome extracts the confirmer's full verdict (tl1326 B1): the
// overall boolean plus the optional per-finding verdicts. ok is false for
// unparseable, ambiguous, or negative answers — doubt survives.
func parseConfirmOutcome(answer string) (confirmOutcome, bool) {
	trimmed := finalAnswerBody(answer)
	// Prefer a fenced/JSON blob inside prose.
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return confirmOutcome{}, false
	}
	var out confirmOutcome
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err != nil {
		return confirmOutcome{}, false
	}
	return out, true
}

// parseConfirmed extracts the confirmer's boolean verdict. Unparseable,
// ambiguous, or negative answers all mean NOT confirmed (doubt survives).
// Signature and semantics kept verbatim from before tl1326 (batch A pin).
func parseConfirmed(answer string) bool {
	out, ok := parseConfirmOutcome(answer)
	return ok && out.Confirmed
}
