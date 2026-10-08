package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// toolFloodTurnLimit is the per-turn tool-call cap (task 243 A1): MiMo's
// TOOLCALL_FLOODING_LIMIT borrowed as a RULE, not its AI SDK middleware — a
// batch whose admission would cross this line is cancelled whole with a
// structured continuation reminder. Consulted only while Agent.toolFloodLimit
// (experimental_tool_flood_limit) is on; storm breaker remains the separate
// cross-turn repeat-failure guard.
const toolFloodTurnLimit = 16

// mutationBarrierCause is an immutable, argument-free description of the
// first durable-state write that failed or was blocked in a tool batch.
type mutationBarrierCause struct {
	evidenceOnly          bool
	callID                string
	toolName              string
	stateMutation         bool
	workspaceMutation     bool
	contentMutation       bool
	repositoryMutation    bool
	classificationKnown   bool
	reason, blockingPhase string
}

func (c *mutationBarrierCause) message() string {
	if c == nil {
		return "blocked: skipped because an earlier modification failed or was blocked in this tool batch. " +
			"Fix or re-run the failed change first; verification was not executed."
	}
	reason := c.reason
	if reason == "" {
		reason = "state mutation whose effects cannot be proven read-only"
	}
	action := "failed"
	if c.blockingPhase == "blocked" {
		action = "was blocked"
	}
	return "blocked: skipped because an earlier modification (" + reason + ") " + action + " in this tool batch. " +
		"Fix or re-run the failed change first; verification was not executed."
}

// toolOutcome is one tool call's result. output is the first-visible bounded
// form the model sees; rawOutput is the full original when truncation applied
// (empty when identical so we avoid double storage). images ride outside text.
type toolOutcome struct {
	runState                   provider.ToolRunState
	visionSummary              *provider.VisionSummary
	output                     string
	rawOutput                  string // full original when different from output
	images                     []string
	blocked                    bool
	errMsg                     string
	truncated                  bool
	truncMsg                   string
	resolved                   bool
	resolvedName               string
	capabilityID               string
	resolvedReadOnly, executed bool
	workspaceMutation          *event.WorkspaceMutation
	effective                  workspaceEffectiveCall
	// execution is local shell metadata (optional). Provider messages strip it
	// via ModelMessages; UI/event sinks surface it on ToolResult cards.
	execution *tool.ShellExecution
	// mcpApp is the optional MCP Apps presentation; provider-excluded like
	// execution, persisted for Desktop cards.
	mcpApp *provider.MCPAppPresentation
	// recoveryGeneration is the gate generation captured before execution so
	// ObserveResult can ignore stale results after a mode switch.
	recoveryGeneration uint64
	// recoveryStopTurn is set when Auto Episode budgets are exhausted.
	recoveryStopTurn   bool
	recoveryStopReason string
	readTaskID         string
	readEnvelope       *tool.ReadResultEnvelope
	diagnostic         *tool.OperationDiagnostic
	evidenceSource     tool.EvidenceTargetInfo
	finalReadEnvelope  *tool.ReadResultEnvelope
	readReference      *readDelivery
	readActiveMillis   int64
	incompleteRead     *incompleteReadDeferred
	subagentOutcome    *SubagentOutcome
}

// batchExecution is the result of one provider tool-call batch.
type batchExecution struct {
	results            []string
	outcomes           []toolOutcome
	images             [][]string
	executions         []*tool.ShellExecution
	err                error
	recoveryStopTurn   bool
	recoveryStopReason string
}

// executeBatch dispatches one model turn's tool calls. ToolDispatch events are
// emitted up front in call order; contiguous known ReadOnly calls fan out
// across goroutines while unknown and writer calls run serially so write/read
// ordering stays provider-ordered. Each completed serial call (or read-only
// group) is checkpointed before the next group starts.
func (a *Agent) executeBatch(ctx context.Context, turn *turnRuntime, calls []provider.ToolCall) batchExecution {
	turn.evidenceBlocked.clearChecks()
	defer turn.evidenceBlocked.clearChecks()
	// The assistant message already stored this slice in Session. Keep execution
	// state separate so refreshing a dependent preview never mutates shared
	// session memory outside Session's lock.
	calls = append([]provider.ToolCall(nil), calls...)
	if err := a.prepareToolBatch(ctx, calls); err != nil {
		return batchExecution{err: err}
	}
	// Task 243 A1 (MiMo #2463's flood rule, borrowed as a rule without the AI
	// SDK middleware): a single-turn tool-call cap. When admitting this batch
	// would push the turn past toolFloodTurnLimit calls, cancel the WHOLE
	// batch before anything executes — a cancel never releases a partial
	// batch — and hand the model a structured reminder to continue in smaller
	// steps. The count is turn-cumulative ("the 17th call cancels the batch"),
	// while storm breaker stays the separate cross-turn repeat-failure guard.
	// The cancelled calls never ran: no side effect exists, so nothing enters
	// writeRecovery/unknownRecovery and recovery never replays the batch —
	// the reminder is all the model receives.
	if a.toolFloodLimit && turn.toolCallsThisTurn+len(calls) > toolFloodTurnLimit {
		total := turn.toolCallsThisTurn + len(calls)
		turn.toolCallsThisTurn = total // keep counting so the cap keeps holding
		reminder := fmt.Sprintf("tool_flood_cancelled: this batch of %d calls would bring the turn to %d tool calls (limit %d). Nothing was executed, and this batch will NOT be replayed — continue with smaller batches (prefer 1-3 tool calls per step).", len(calls), total, toolFloodTurnLimit)
		results := make([]string, len(calls))
		outcomes := make([]toolOutcome, len(calls))
		images := make([][]string, len(calls))
		executions := make([]*tool.ShellExecution, len(calls))
		for i := range calls {
			outcomes[i] = toolOutcome{output: reminder, blocked: true, errMsg: reminder}
			results[i] = reminder
		}
		slog.Warn("agent: tool flood cap cancelled batch (task 243 A1)",
			"calls_in_batch", len(calls), "turn_total", total, "limit", toolFloodTurnLimit)
		return batchExecution{
			results:    results,
			outcomes:   outcomes,
			images:     images,
			executions: executions,
		}
	}
	turn.toolCallsThisTurn += len(calls)
	if a.task.ledger != nil {
		ctx = withObservationBoundary(ctx, a.task.ledger.ObservationBoundary())
	}

	slots := newBatchSlots(calls)
	// Evidence is evaluated once for the whole batch, before anything runs: a
	// call whose writer cannot prove what it replaces never starts, and a read
	// from this same batch can never satisfy it.
	evidenceBlocked := a.preflightEvidenceBatch(ctx, calls)
	results, outcomes, durations, startedAt := slots.results, slots.outcomes, slots.durations, slots.startedAt
	ranParallel := make([]bool, len(calls))
	batchStart := time.Now()
	// Snapshot the receipt count before the batch runs: if a loop guard fires
	// for this batch, successes recorded during it (a mixed batch where only one
	// call was guard-blocked) must already count as progress against the pass.
	receiptMark := 0
	if a.task.ledger != nil {
		receiptMark = a.task.ledger.Len()
	}
	// Full dispatches used the batch's initial file state. After a writer runs
	// (even a failed one — disk may have mutated), refresh dependent writer
	// previews. The first writer stays on the single-preview fast path.
	earlierWriterRan := false
	surfaceWriters := slots.surfaceWriters
	var batchErr error
	var batchErrOnce sync.Once
	run := func(s *batchSlots, i int) {
		if pre, blocked := evidenceBlocked[i]; blocked {
			s.outcomes[i] = pre
			s.results[i] = pre.output
			return
		}
		t, _, ambiguous := a.svc.tools.ResolveCall(s.calls[i].Name)
		known := t != nil && len(ambiguous) == 0
		writer := known && !t.ReadOnly()
		s.surfaceWriters[i] = writer
		if earlierWriterRan && writer {
			if refreshed, changed := refreshCurrentFileDiff(ctx, t, s.calls[i]); changed {
				s.calls[i] = refreshed
				a.sess.conversation.UpdateToolCallPreview(refreshed)
				if err := a.emitFullToolDispatch(ctx, refreshed, true); err != nil {
					wrapped := fmt.Errorf("persist refreshed tool dispatch %s: %w", refreshed.ID, err)
					batchErrOnce.Do(func() { batchErr = wrapped })
					s.outcomes[i] = toolOutcome{output: "cancelled: tool dispatch was not durable", errMsg: wrapped.Error()}
					s.results[i] = s.outcomes[i].output
					return
				}
			}
		}
		start := time.Now()
		s.startedAt[i] = start.UnixMilli()
		s.outcomes[i] = a.executeOneWithForce(ctx, turn, s.calls[i])
		// Task 227 phase 1: fold the outcome into the per-tool error counters
		// (observe only — no behavior change, no prompt injection).
		a.recordToolErrorStats(s.calls[i].Name, s.outcomes[i])
		recordWorkspaceMutation(a.svc.sink, s.outcomes[i].workspaceMutation)
		if s.outcomes[i].executed {
			s.surfaceWriters[i] = s.outcomes[i].workspaceMutation != nil
		}
		if s.outcomes[i].resolved {
			readOnly := s.outcomes[i].resolvedReadOnly
			s.calls[i].ResolvedName = s.outcomes[i].resolvedName
			s.calls[i].CapabilityID = s.outcomes[i].capabilityID
			s.calls[i].ResolvedReadOnly = &readOnly
			s.surfaceWriters[i] = !readOnly
		}
		s.durations[i] = time.Since(start).Milliseconds()
		s.results[i] = s.outcomes[i].output
	}
	committed := make([]bool, len(calls))
	finalize := func(i int) {
		if committed[i] {
			return
		}
		committed[i] = true
		a.finalizeIncompleteReadOutcome(ctx, outcomes[i].incompleteRead, &outcomes[i])
		a.finalizeReadDelivery(ctx, calls[i], &outcomes[i])
		results[i] = outcomes[i].output
		a.commitBatchCallResolution(calls[i])
		a.finishToolRecovery(ctx, calls[i], outcomes[i])
		a.storeBatchToolResult(ctx, calls[i], outcomes[i])
		if err := a.emitBatchToolResult(calls[i], outcomes[i], durations[i], startedAt[i], ranParallel[i], batchStart); err != nil {
			batchErrOnce.Do(func() { batchErr = fmt.Errorf("persist tool result %s: %w", calls[i].ID, err) })
		}
		if surfaceWriters[i] || (outcomes[i].resolved && !outcomes[i].resolvedReadOnly) {
			earlierWriterRan = true
		}
	}
	cancelled := false
	markCancelled := func(start int) {
		errMsg := context.Canceled.Error()
		if err := ctx.Err(); err != nil {
			errMsg = err.Error()
		}
		output := "cancelled: context cancelled before execution"
		for j := start; j < len(calls); j++ {
			results[j] = output
			outcomes[j] = toolOutcome{output: output, errMsg: errMsg}
		}
		cancelled = true
	}

	// recoveryBatchStop blocks remaining tools after Episode budgets are
	// exhausted so tool-call / result pairs stay complete for the provider.
	recoveryBatchStop := false
	recoveryStopReason := ""
	markRecoveryStopped := func(start int, reason string) {
		msg := "blocked: Auto recovery paused this turn; do not call more tools. Summarize completed work for the user."
		for j := start; j < len(calls); j++ {
			if results[j] != "" {
				continue
			}
			results[j] = msg
			outcomes[j] = toolOutcome{
				output:             msg,
				blocked:            true,
				errMsg:             firstLine(msg),
				recoveryStopTurn:   true,
				recoveryStopReason: reason,
			}
		}
		recoveryBatchStop = true
		if reason != "" {
			recoveryStopReason = reason
		}
	}

	// Deterministic dependency barrier: after a mutating call fails or is
	// blocked, later mutations/verifications in the batch are skipped; read-only
	// diagnosis still runs. executeOne re-checks after proxy resolution.
	mutationBatchStop := false
	a.mutationDependencyBarrier.Store(nil)
	markDependencySkipped := func(start int, cause *mutationBarrierCause) {
		a.markDependencySkipped(calls, outcomes, results, durations, start, cause)
		mutationBatchStop = true
	}

	for _, batch := range a.toolCallBatches(calls) {
		if ctx.Err() != nil || batchErr != nil {
			markCancelled(batch.start)
			break
		}
		if recoveryBatchStop {
			markRecoveryStopped(batch.start, recoveryStopReason)
			break
		}
		if batch.parallel && batch.end-batch.start > 1 {
			// Parallel segments are read-only by construction; no mutation barrier.
			private := slots.fork()
			ranUntil, finished := runParallel(ctx, batch.start, batch.end, func(i int) {
				a.stragglers.enter()
				defer a.stragglers.leave()
				run(private, i)
			})
			for i := batch.start; i < ranUntil; i++ {
				if finished[i] {
					slots.adopt(private, i)
				} else {
					slots.abandon(i)
				}
				ranParallel[i] = true
				finalize(i)
			}
			// After parallel execution completes, check if context was cancelled.
			// The individual tool executions should have detected ctx.Done(), but
			// we verify here to ensure we don't continue to subsequent batches.
			if ctx.Err() != nil {
				markCancelled(ranUntil)
				break
			}
			for i := batch.start; i < batch.end; i++ {
				if outcomes[i].recoveryStopTurn {
					recoveryBatchStop = true
					recoveryStopReason = outcomes[i].recoveryStopReason
					markRecoveryStopped(batch.end, recoveryStopReason)
					break
				}
			}
			if recoveryBatchStop {
				break
			}
			continue
		}
		for i := batch.start; i < batch.end; i++ {
			// Before executing the next tool, check if context was cancelled.
			// This prevents starting new tools when a previous tool's execution
			// triggered cancellation.
			if ctx.Err() != nil || batchErr != nil {
				markCancelled(i)
				break
			}
			if recoveryBatchStop {
				markRecoveryStopped(i, recoveryStopReason)
				break
			}
			if mutationBatchStop {
				// Fill dependency skips for remaining mutating/verify calls, then
				// allow any residual read-only diagnosis to run individually.
				if results[i] != "" {
					finalize(i)
					continue
				}
				if batchCallStaticallySkippable(a, calls[i]) {
					markDependencySkipped(i, nil)
					// markDependencySkipped fills this index; move on.
					if results[i] != "" {
						finalize(i)
						continue
					}
				}
			}
			if results[i] != "" {
				// Pre-filled dependency skip.
				finalize(i)
				continue
			}
			run(slots, i)
			finalize(i)
			if outcomes[i].recoveryStopTurn {
				recoveryBatchStop = true
				recoveryStopReason = outcomes[i].recoveryStopReason
				markRecoveryStopped(i+1, recoveryStopReason)
				break
			}
			// Mutation/verification failure barrier for the rest of this batch.
			if cause := batchCallMutationFailureCause(a, calls[i], outcomes[i]); cause != nil {
				mutationBatchStop = true
				markDependencySkipped(i+1, cause)
			}
			// After each tool execution, also check if the context was cancelled.
			// If so, stop executing remaining tools and return immediately so
			// the agent loop can detect the cancellation and exit.
			if ctx.Err() != nil {
				markCancelled(i + 1)
				break
			}
		}
		if cancelled || recoveryBatchStop {
			break
		}
	}

	for i := range calls {
		finalize(i)
	}
	a.applyBatchGuards(ctx, cancelled, calls, outcomes, results, receiptMark)
	a.storeBatchGuardResults(calls, results)
	images := make([][]string, len(calls))
	executions := make([]*tool.ShellExecution, len(calls))
	for i := range outcomes {
		images[i] = outcomes[i].images
		executions[i] = outcomes[i].execution
		if outcomes[i].recoveryStopTurn {
			recoveryBatchStop = true
			if outcomes[i].recoveryStopReason != "" {
				recoveryStopReason = outcomes[i].recoveryStopReason
			}
		}
	}
	return batchExecution{
		results:            results,
		outcomes:           outcomes,
		images:             images,
		executions:         executions,
		err:                batchErr,
		recoveryStopTurn:   recoveryBatchStop,
		recoveryStopReason: recoveryStopReason,
	}
}

func (a *Agent) commitBatchCallResolution(call provider.ToolCall) {
	if call.ResolvedReadOnly == nil {
		return
	}
	a.sess.conversation.UpdateToolCallResolution(call)
	a.emitResolvedToolDispatch(call)
}

// batchCallMutationFailureCause returns a sanitized effect description when a
// durable-state mutation failed or was blocked. Verification failures alone do
// not open the dependency barrier.
// batchCallMutationFailureCause decides fail-cascade admission (task 244 B8;
// Reasonix's side of MiMo #2463's exemption face, sub-report 03-④B). The four
// rules, absorbed as classification rules rather than copied mechanics:
//
//  1. Only EFFECTIVE read/search failures exempt the cascade: the test is the
//     effects classification (non-StateMutation ⇒ nil), not the tool's name —
//     a read that failed for any reason never poisons the batch.
//  2. Every side-effect failure closes the batch, in both phases: "failed"
//     (executed, broke) and "blocked" (permission/evidence refused) — a
//     blocked write is still a write whose effect cannot be proven.
//  3. Read-only verification commands (bash && IsVerificationCommand, no
//     mutation) exempt like rule 1: they observe, they do not change state.
//  4. Unknown classification fails CLOSED: effects with Known=false ride the
//     writer side (readOnly defaults false), matching MiMo's "invalid calls
//     enter the cascade" direction — an unproven call never reads as safe.
//
// Pinned by TestBatchCascadeExemptionFace; evidenceOnly stays a further
// exemption layer for independent evidence writers (see
// applyMutationDependencyBarrier).
func batchCallMutationFailureCause(a *Agent, call provider.ToolCall, o toolOutcome) *mutationBarrierCause {
	if o.errMsg == "" && !o.blocked && outcomeRunState(o) != provider.ToolRunUnknown {
		return nil
	}
	readOnly := false
	toolName := call.Name
	toolArgs := json.RawMessage(call.Arguments)
	t, _, ambiguous := a.svc.tools.ResolveCall(call.Name)
	known := t != nil && len(ambiguous) == 0
	if known {
		readOnly = t.ReadOnly()
	}
	if call.ResolvedReadOnly != nil {
		readOnly = *call.ResolvedReadOnly
	}
	if o.resolved {
		readOnly = o.resolvedReadOnly
	}
	if o.effective.name != "" {
		toolName = o.effective.name
		toolArgs = o.effective.args
		readOnly = o.effective.readOnly
	}
	effects := evidence.ClassifyToolCall(toolName, toolArgs, readOnly)
	if toolName == "bash" && evidence.IsVerificationCommand(bashCommandFromArgs(toolArgs)) && !effects.StateMutation {
		return nil
	}
	if !effects.StateMutation {
		return nil
	}
	phase := "failed"
	if o.blocked {
		phase = "blocked"
	}
	return &mutationBarrierCause{
		evidenceOnly:        o.blocked && !o.executed && o.diagnostic != nil && o.diagnostic.Code == tool.WriteEvidenceMissing,
		callID:              call.ID,
		toolName:            toolName,
		stateMutation:       effects.StateMutation,
		workspaceMutation:   effects.WorkspaceMutation,
		contentMutation:     effects.ContentMutation,
		repositoryMutation:  effects.RepositoryMutation,
		classificationKnown: effects.Known && known,
		reason:              effects.Reason,
		blockingPhase:       phase,
	}
}

// batchCallStaticallySkippable reports whether a remaining call can be marked
// not_run/dependency without resolving a proxy. Proxies and unknown tools
// return false so executeOne can resolve the real target first.
func batchCallStaticallySkippable(a *Agent, call provider.ToolCall) bool {
	t, _, ambiguous := a.svc.tools.ResolveCall(call.Name)
	if t == nil || len(ambiguous) > 0 {
		// Unknown / ambiguous: fail closed via executeOne path.
		return false
	}
	// A proxy may resolve against a live capability whose result can change
	// between calls, so never resolve here just to pre-fill a skip: executeOne
	// resolves exactly once and classifies the real target before Commit.
	if _, ok := t.(tool.CallResolver); ok {
		return false
	}
	readOnly := t.ReadOnly()
	isVerification := call.Name == "bash" && evidence.IsVerificationCommand(bashCommandFromArgs(json.RawMessage(call.Arguments)))
	if isVerification {
		return true
	}
	return evidence.ClassifyToolCall(call.Name, json.RawMessage(call.Arguments), readOnly).StateMutation
}

type toolCallBatch struct {
	start    int
	end      int
	parallel bool
}

// toolCallBatches preserves read-only fan-out unless a tool hook can mutate
// the workspace (task 244 B6, the hook row of the admission table). Such hooks
// are covered by a whole-workspace claim, so every batch flattens to provider
// order instead of racing that claim against each other: the table above
// classifies tools, this layer decides whether classification still matters.
func (a *Agent) toolCallBatches(calls []provider.ToolCall) []toolCallBatch {
	batches := partitionToolCalls(a.svc.tools, calls)
	if !toolHooksMayMutateWorkspace(a.svc.hooks) {
		return batches
	}
	for i := range batches {
		batches[i].parallel = false
	}
	return batches
}

// partitionToolCalls is the tool-dispatch admission table (task 244 B6; the
// Reasonix form of MiMo #2456's realpath concurrency classes). Three classes,
// decided per call, provider order preserved:
//
//   - parallel: Known + ReadOnly + ParallelSafe (tool.BatchClassifier), or a
//     plain ReadOnly tool without dynamic resolution; contiguous members share
//     one batch (maxParallel slots in runParallel).
//   - barrier: the evidence-ledger tools (complete_step, todo_write, wait,
//     bash_output, compress — registered ConcurrentSafe=false in
//     internal/tool's safety table, the task-426 single source); receipts must
//     land in provider order, so each is its own serial batch. use_capability
//     joins them because it may resolve to a real MCP writer (the
//     CallResolver short-circuit below).
//   - writer: anything not ReadOnly, task included; serial batches whose
//     exclusion is owned by write claims/coordination
//     (tool_write_coordination.go), not by this table.
//
// Unknown names are serial by default: deterministic errors, never fanned out.
//
// Nested dispatch needs no MiMo #2456-style deadlock exemption: task is a
// writer via ReadOnly()==false, so a nested call enters its child serially,
// and the child's own executor runs this same table. There is no FIFO queue
// a parent wait could deadlock against. Do not "add the exemption" without
// first adding a queue that would need it.
func partitionToolCalls(r *tool.Registry, calls []provider.ToolCall) []toolCallBatch {
	var batches []toolCallBatch
	for i := 0; i < len(calls); {
		if parallelisableCall(r, calls[i]) {
			start := i
			i++
			for i < len(calls) && parallelisableCall(r, calls[i]) {
				i++
			}
			batches = append(batches, toolCallBatch{start: start, end: i, parallel: true})
			continue
		}
		batches = append(batches, toolCallBatch{start: i, end: i + 1})
		i++
	}
	return batches
}

func parallelisableCall(r *tool.Registry, call provider.ToolCall) bool {
	target, _, ambiguous := r.ResolveCall(call.Name)
	if target == nil || len(ambiguous) != 0 {
		return false
	}
	if classifier, ok := target.(tool.BatchClassifier); ok {
		class := classifier.ClassifyCall(json.RawMessage(call.Arguments))
		return class.Known && class.ReadOnly && class.ParallelSafe
	}
	if _, dynamic := target.(tool.CallResolver); dynamic {
		return false
	}
	// 任务 426 单一事实源：证据台账 barrier 名单（complete_step / todo_write /
	// wait / bash_output / compress）不再在此硬编码，改读 SafetySpec 的
	// ConcurrentSafe=false（名单本身由 internal/tool 的契约测试守卫并钉死与
	// 原名单等价）。表外动态工具的推导兜底与原 target.ReadOnly() 逐位一致。
	spec := tool.SafetyOf(target)
	return spec.ReadOnly && spec.ConcurrentSafe
}

// parallelStragglerGrace bounds how long a cancelled parallel segment waits for
// tools that have not returned. Tool owners kill their own processes within
// their WaitDelay; past this the batch reports the effect as unknown instead
// of keeping the whole turn wedged behind one call that ignores its context.
var parallelStragglerGrace = 15 * time.Second

// forcedToolOutput is the 任务461-P7 L3 abandon face, mirroring the parallel
// batch's abandon: the executor stopped waiting after the termination
// escalation fired, the call's effect is unknown, and the tool goroutine is
// quarantined as a straggler (recorded, bounded at the next turn's drain,
// never waited on here).
const forcedToolOutput = "force-stopped: the executor stopped waiting because the tool ignored termination; its effect is unknown"

// executeOneWithForce runs one tool call under the 任务461-P7 L3
// force-abandon watchdog. The call executes on its own goroutine so the
// executor can stop waiting the moment the host's force signal fires instead
// of wedging the turn behind a tool that ignores cancellation. L1 (the
// ordinary cancel) does NOT abandon: the tool keeps its graceful-exit window
// and the escalation policy decides when force comes. Hosts without the
// force half (WithStopForce absent) keep the pre-P7 wait-forever behavior
// byte for byte.
func (a *Agent) executeOneWithForce(ctx context.Context, turn *turnRuntime, call provider.ToolCall) toolOutcome {
	forceDone := stopForceDone(ctx)
	if forceDone == nil {
		return a.executeOne(ctx, turn, call)
	}
	type execResult struct{ outcome toolOutcome }
	done := make(chan execResult, 1)
	go func() {
		// The quarantined goroutine registers as a straggler for its whole
		// life: the next turn's drain bounds its overlap instead of the turn
		// (记录+隔离 — 执行器不等它，UI 立即释放).
		a.stragglers.enter()
		defer a.stragglers.leave()
		done <- execResult{a.executeOne(ctx, turn, call)}
	}()
	select {
	case r := <-done:
		return r.outcome
	default:
	}
	select {
	case r := <-done:
		return r.outcome
	case <-forceDone:
		slog.Warn("agent: forced stop abandoned a tool that ignored termination",
			"tool", call.Name, "call", call.ID, "session", a.recoveryLogSessionName())
		return toolOutcome{output: forcedToolOutput, errMsg: forcedToolOutput, executed: true}
	}
}

// runParallel returns the launched prefix and which of those calls finished.
// An unfinished index belongs to a straggler that still owns its private slot.
func runParallel(ctx context.Context, start, end int, run func(int)) (int, []bool) {
	const maxParallel = 8
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	completed := make(chan int, end-start)
	ranUntil := start
launch:
	for i := start; i < end; i++ {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break launch
		}
		if ctx.Err() != nil {
			<-sem
			break
		}

		wg.Add(1)
		ranUntil = i + 1
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			run(i)
			completed <- i
		}()
	}
	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()
	select {
	case <-allDone:
	case <-ctx.Done():
		select {
		case <-allDone:
		case <-time.After(parallelStragglerGrace):
		}
	}
	finished := make([]bool, end)
	for {
		select {
		case i := <-completed:
			finished[i] = true
		default:
			return ranUntil, finished
		}
	}
}

// batchSlots is one batch's per-call execution state. Parallel segments run
// against a fork so a tool that outlives cancellation writes only into slots
// the batch has already stopped reading.
type batchSlots struct {
	calls          []provider.ToolCall
	outcomes       []toolOutcome
	results        []string
	durations      []int64
	startedAt      []int64
	surfaceWriters []bool
}

func newBatchSlots(calls []provider.ToolCall) *batchSlots {
	n := len(calls)
	return &batchSlots{
		calls: calls, outcomes: make([]toolOutcome, n), results: make([]string, n),
		durations: make([]int64, n), startedAt: make([]int64, n), surfaceWriters: make([]bool, n),
	}
}

func (s *batchSlots) fork() *batchSlots {
	return newBatchSlots(append([]provider.ToolCall(nil), s.calls...))
}

func (s *batchSlots) adopt(from *batchSlots, i int) {
	s.calls[i], s.outcomes[i], s.results[i] = from.calls[i], from.outcomes[i], from.results[i]
	s.durations[i], s.startedAt[i], s.surfaceWriters[i] = from.durations[i], from.startedAt[i], from.surfaceWriters[i]
}

const abandonedToolOutput = "interrupted: the tool did not stop after cancellation; its effect is unknown"

func (s *batchSlots) abandon(i int) {
	s.outcomes[i] = toolOutcome{output: abandonedToolOutput, errMsg: abandonedToolOutput, executed: true}
	s.results[i] = abandonedToolOutput
}
