package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

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
	// Checkpoint（任务 1316）在每个节点终态（finish/abort/skip）被调用，
	// 携带 runState 的恢复投影。引擎不感知磁盘——落盘是宿主（Service）的
	// 职责；nil = 不关心（Run 的既有调用方零影响）。
	Checkpoint func(*CheckpointData)
	// ResultParseRetries（tl1432 B）：声明了 typed result 的 subagent 节点在
	// 输出无法解析为 JSON 对象时（容错提取也失败，见 parseResultJSON），向模
	// 型定向重发的次数上限——重发 prompt 带原 prompt 全文与「只输出 JSON 对
	// 象」指令，防报废整轮。0（零值）= 默认 1 次（默认即修复生效）；负数 =
	// 禁用重发；更大值按字面生效。只挂 subagent 节点：confirm 节点解析失败
	// 的语义是「未确认」（保留 findings 继续跑），不报废也不重发。
	ResultParseRetries int
	// Observe（tl1454）在单写者上接收逐节点观测：每次派发执行的用量、节点
	// 中止时的失败原因码、非致命契约提示。nil = 不关心（与 Checkpoint 同款
	// 宿主侧通道；686 六态事件契约不受影响）。
	Observe func(NodeObservation)
}

// DefaultResultParseRetries is the directed-retry budget when
// ResultParseRetries is left at its zero value (tl1432 B): exactly one
// re-ask — enough to recover a noisy answer, small enough that a model that
// cannot follow the JSON contract fails fast (two outputs max per node).
const DefaultResultParseRetries = 1

// resultParseRetries resolves the effective budget.
func (e *Engine) resultParseRetries() int {
	switch {
	case e == nil || e.ResultParseRetries < 0:
		return 0
	case e.ResultParseRetries == 0:
		return DefaultResultParseRetries
	default:
		return e.ResultParseRetries
	}
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

	phase      string // 工作流分区标签：只由 phase 节点在串行扫描段写（tl1326 G4：并发波次共享「最近进入的分区」，原义不变）
	failedNode string
	gateRound  int // current gate attempt, so a world-failure reports its round

	// meter is the tl1454 run budget (nil when the document declares none).
	// Set once at construction; the concurrent segment receives it captured.
	meter *budgetMeter
}

// Run executes the whole workflow sequentially in topological order. It
// ALWAYS returns a non-nil report once anything was emitted — a run that dies
// mid-graph hands back everything reported so far (point 5 落袋).
func (e *Engine) Run(ctx context.Context, wf *Workflow) (*WorkflowReport, error) {
	return e.RunWithID(ctx, wf, "")
}

// RunWithID is Run with an externally pinned run id (task 934): the run
// registry must know the id BEFORE the engine emits its first event so the
// workflow_run tool can hand it to the caller. An empty id falls back to the
// generated one, making Run's behavior byte-identical to before.
func (e *Engine) RunWithID(ctx context.Context, wf *Workflow, runID string) (*WorkflowReport, error) {
	if err := wf.Validate(); err != nil {
		return nil, err
	}
	s := newRunState(e, wf)
	if runID != "" {
		s.runID = runID
	}
	// runID 随 ctx 下行（tl1324 G6）：任务面板登记 id（wf:<runId>:<nodeID>）
	// 由 sub-agent runner 在节点执行时组装，外部钉定与自动生成两条路都覆盖。
	ctx = withRunID(ctx, s.runID)
	ctx, cancel := withWallBudget(ctx, wf.Budget)
	defer cancel()
	s.emitAll(event.WorkflowNodeQueued, 0, "")
	err := s.runAll(ctx)
	return s.buildReport(err), err
}

// ResumeWithID（任务 1316）从快照恢复调度：已完成/已跳过节点灌回并重放边
// 决策，其余节点重置 pending，runAll 从断点继续。调度器零改动——终态节点
// 不是 pending，扫描自然越过。恢复节点向事件流重放一次终态事件，续跑发起
// 会话的 feed 由此看到全图状态；skip 节点保持不发声（与原 run 的 queued-gray
// 一致）。
func (e *Engine) ResumeWithID(ctx context.Context, wf *Workflow, snap *runSnapshot) (*WorkflowReport, error) {
	if err := wf.Validate(); err != nil {
		return nil, err
	}
	s, err := runStateFromSnapshot(e, wf, snap)
	if err != nil {
		return nil, err
	}
	// runID 随 ctx 下行（与 RunWithID 同一注入语义，tl1316b）：续跑节点的任务
	// 面板登记 id 必须是 wf:<snap.RunID>:<nodeID>。漏注入时 runner 落 wf:norun:
	// 兜底——续跑行与 run 脱钩、跨 run 同名节点共用 jobID，并发重叠时 recorder
	// 配对表串线留僵尸 alive 行（persist_resume_monitor_test 钉 A/B）。
	ctx = withRunID(ctx, s.runID)
	ctx, cancel := withWallBudget(ctx, wf.Budget)
	defer cancel()
	s.replayTerminalEvents(snap)
	runErr := s.runAll(ctx)
	return s.buildReport(runErr), runErr
}

// runStateFromSnapshot 把快照灌回一个新 runState（engine.go 顶部 runState 的
// 恢复对应面）。快照里 output 截断超限的节点整节点重置——它的下游
// interpolation 会拼出残缺 prompt，宁可重跑不可续错。
func runStateFromSnapshot(e *Engine, wf *Workflow, snap *runSnapshot) (*runState, error) {
	s := newRunState(e, wf)
	s.runID = snap.RunID
	s.phase = snap.Phase
	s.failedNode = snap.FailedNode
	s.gateRound = snap.GateRound
	for _, f := range snap.Bag {
		// add 原样接受 verified 状态（幂等去重按 where/what）——快照里的
		// findings 状态是引擎指派的权威值，直接灌回。
		s.bag.add(f)
	}
	s.verifs = append(s.verifs, snap.Verifs...)

	restored := map[string]bool{}
	for id, ns := range snap.Nodes {
		if _, ok := s.index[id]; !ok {
			return nil, fmt.Errorf("workflow: snapshot %s: node %q not in definition %q", snap.RunID, id, wf.Name)
		}
		if ns.OutputTruncated {
			continue // 整节点重置重跑
		}
		switch ns.Status {
		case "done":
			s.status[id] = stDone
		case "skipped":
			s.status[id] = stSkipped
		default:
			continue // aborted/pending 等一律重跑
		}
		switch ns.Terminal {
		case "passed":
			s.term[id] = termPassed
		case "gate_failed":
			s.term[id] = termGateFailed
		default:
			s.term[id] = termSkipped
		}
		if ns.GatePassed != nil {
			s.gatePassed[id] = *ns.GatePassed
		}
		s.output[id] = ns.Output
		if len(ns.Result) > 0 {
			var v any
			if err := json.Unmarshal(ns.Result, &v); err != nil {
				return nil, fmt.Errorf("workflow: snapshot %s: node %q result: %w", snap.RunID, id, err)
			}
			s.result[id] = v
		}
		if len(ns.Findings) > 0 {
			s.findings[id] = append([]Finding(nil), ns.Findings...)
		}
		restored[id] = true
	}
	// 边决策重放：resolveOutEdges 是 (terminal, gatePassed) 的纯函数，全部
	// 节点状态灌完后再统一重放，避免顺序依赖。
	for id := range restored {
		s.resolveOutEdges(id, s.term[id])
	}
	return s, nil
}

// replayTerminalEvents 给恢复的终态节点补发一次六态事件（新会话 feed 的全图
// 状态重建）。优先用快照记录的六态（unconfirmed 等协议态引擎态推导不出）；
// 无记录时按引擎态推导。skipped 节点不发（skip 本就无事件，面板保持
// queued-gray）。
func (s *runState) replayTerminalEvents(snap *runSnapshot) {
	for i := range s.wf.Nodes {
		n := &s.wf.Nodes[i]
		if s.status[n.ID] != stDone {
			continue // skipped 无事件：skip 语义保持（面板 queued-gray）
		}
		state := event.WorkflowNodeCompleted
		if n.Type == NodeGate && s.gatePassed[n.ID] {
			state = event.WorkflowNodeGatePassed
		}
		round := 0
		detail := "restored from checkpoint"
		if ns, ok := snap.Nodes[n.ID]; ok {
			if ns.LastEventState != "" {
				state = event.WorkflowNodeState(ns.LastEventState)
			}
			if ns.Detail != "" {
				detail = ns.Detail
			}
			round = ns.Round
		}
		s.emit(n, state, round, detail)
	}
}

// checkpoint 把当前恢复投影递给宿主回调（nil 回调零开销）。
func (s *runState) checkpoint() {
	if s.eng == nil || s.eng.Checkpoint == nil {
		return
	}
	data := &CheckpointData{
		Nodes:      map[string]nodeSnapshot{},
		NodeOrder:  append([]string(nil), s.declaredOrder()...),
		Bag:        s.bag.snapshot(),
		Verifs:     append([]string(nil), s.verifs...),
		Phase:      s.phase,
		FailedNode: s.failedNode,
		GateRound:  s.gateRound,
	}
	for i := range s.wf.Nodes {
		n := &s.wf.Nodes[i]
		ns, ok := s.nodeSnapshot(n)
		if ok {
			data.Nodes[n.ID] = ns
		}
	}
	s.eng.Checkpoint(data)
}

// declaredOrder 返回非 fixer 节点的声明序（首见序的上界近似；recordNode 的
// order 仍是面板权威，快照 NodeOrder 只是恢复兜底）。
func (s *runState) declaredOrder() []string {
	out := make([]string, 0, len(s.wf.Nodes))
	for i := range s.wf.Nodes {
		if !s.fixers[s.wf.Nodes[i].ID] {
			out = append(out, s.wf.Nodes[i].ID)
		}
	}
	return out
}

// nodeSnapshot 投影一个节点；只有恢复相关终态（done/skipped）返回 true。
func (s *runState) nodeSnapshot(n *Node) (nodeSnapshot, bool) {
	var ns nodeSnapshot
	ns.NodeType = n.Type
	switch s.status[n.ID] {
	case stDone:
		ns.Status = "done"
	case stSkipped:
		ns.Status = "skipped"
	default:
		return ns, false
	}
	switch s.term[n.ID] {
	case termPassed:
		ns.Terminal = "passed"
	case termGateFailed:
		ns.Terminal = "gate_failed"
	default:
		ns.Terminal = "skipped"
	}
	if n.Type == NodeGate {
		v := s.gatePassed[n.ID]
		ns.GatePassed = &v
		ns.Round = s.gateRound
	}
	if ns.Terminal == "gate_failed" {
		ns.Detail = fmt.Sprintf("exit ≠ 0 with a receiving branch (round %d)", s.gateRound)
	}
	out, truncated := truncateTail(s.output[n.ID], maxNodeOutputBytes)
	ns.Output = out
	ns.OutputTruncated = truncated
	if r, ok := s.result[n.ID]; ok {
		raw, err := json.Marshal(r)
		if err == nil {
			ns.Result = raw
		}
	}
	ns.Findings = s.findings[n.ID]
	return ns, true
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
		meter:      newBudgetMeter(wf.Budget),
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

// runAll is the topological scheduler (tl1326 G4 拓扑真并行). Each pass
// scans in declaration order and:
//   - resolves the skip cascade serially (a node whose in-edges all decided
//     inactive skips; skip propagates by deciding its out-edges inactive);
//   - dispatches EVERY ready subagent/confirm node's runner call concurrently
//     and aggregates the answers single-writer in wave order (= declaration
//     order);
//   - runs gates (with their fixer loop) and joins inline during the scan —
//     their whole body is runState mutation, so they stay on the single
//     writer by construction;
//   - treats a ready phase node as a wave barrier: dispatch whatever was
//     collected, run the phase first thing next pass. Phase nodes are section
//     markers (they only write s.phase); several real definitions place them
//     as roots between node groups, and the pre-G4 engine ran them in
//     declaration position — the barrier keeps that section chronology (the
//     events' Phase label) intact under parallelism.
//
// Concurrency contract (audit 05 variant A 并发段+单写者汇聚): the concurrent
// segment is ONLY "call the injected runner and take the answer" — prompts
// are interpolated and requests built in the serial scan, and every state
// write (status/output/result/findings/bag/verifs/edges/events/checkpoint)
// happens in the serial scan or the serial aggregation pass. runState stays
// lock-free without a data race; an in-flight wave never reorders topology
// because a node only joins a wave after all its in-edges decided. An
// inline-node error fails fast BEFORE the wave dispatches; a dispatched-node
// error fails fast at its aggregation position (already-aggregated earlier
// nodes keep their terminal state — salvage unchanged).
func (s *runState) runAll(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return stopCause(ctx)
		}
		// tl1454: a spent token/rounds budget stops the run before the next
		// node is dispatched (the node that crossed keeps its result).
		if over := s.meter.exceeded(); over != nil {
			return over
		}
		// ---- serial scan: skip cascade, inline nodes, wave collection ----
		var calls []*nodeCall
		var phaseGate *Node
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
				continue
			}
			decided, active := s.inState(n.ID)
			if !decided {
				continue
			}
			if !active {
				s.skip(n)
				progressed = true
				continue
			}
			if n.Type == NodePhase {
				// 波次屏障：已收集的节点先派发，phase 下一轮最先执行——
				// 分区标签时序与串行引擎一致（见函数注释）。
				phaseGate = n
				progressed = true
				break
			}
			progressed = true
			if n.Type == NodeSubagent || n.Type == NodeConfirm {
				calls = append(calls, s.beginNode(ctx, n))
				continue
			}
			// gate/join: serial inline — an error fails fast before this
			// wave's runner calls go out.
			if err := s.execute(ctx, n); err != nil {
				return err
			}
		}
		if !progressed {
			return nil
		}
		switch {
		case len(calls) > 0:
			if err := s.dispatchAndCollect(calls); err != nil {
				return err
			}
		case phaseGate != nil:
			if err := s.execute(ctx, phaseGate); err != nil {
				return err
			}
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
	s.checkpoint() // 1316：skip 也进快照（无事件但恢复需要它的边决策）
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
