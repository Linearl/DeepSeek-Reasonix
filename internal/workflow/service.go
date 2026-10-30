package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/tool/builtin"
)

// 任务 934 —— 工作流服务：把 685 引擎接到 agent 工具面（builtin.WorkflowService
// seam，heartbeat SetHeartbeatManager 同款形态）与 686 面板真数据源。
//
// 依赖方向：workflow → agent → builtin → tool（无环；builtin 不能反向 import
// workflow，所以 seam 接口的视图类型定义在 builtin 侧、本包负责实现与转换）。
//
// 生命周期：Global() 是进程级单例——定义目录与运行登记跨会话共享（面板是
// 应用级视图）；子代理基底不进单例：boot 按「会话分支号 → 工厂」注册
// RunnerSource，workflow_run 执行时按调用方会话解析（jobs.SessionFromContext），
// 多标签各用各的基底，互不串线。事件水槽按次捕获：workflow_run 的调用上下文
// 携带发起会话的 sink，节点事件嵌进发起会话的事件流（面板由此点亮）。

// RunState 是一次运行的服务侧状态（工具与面板共读）。
type RunState string

const (
	RunRunning   RunState = "running"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
)

// RunRecord 是一次运行的登记行。nodes 由 recordingSink 在事件到达时更新
// （引擎异步发射，登记与事件同序）；report 在引擎返回后落袋。
type RunRecord struct {
	ID        string
	Workflow  string
	State     RunState
	StartedAt time.Time
	EndedAt   time.Time
	Err       string
	Report    *WorkflowReport

	mu        sync.Mutex
	nodes     map[string]*nodeRecord
	order     []string // 节点首见序（= 文档声明序）
	def       *Workflow
	cancel    context.CancelFunc
	cancelled bool
}

type nodeRecord struct {
	nodeType  string
	state     event.WorkflowNodeState
	round     int
	detail    string
	changedAt time.Time
}

// RunnerSource 按「调用方会话分支号」解析该会话的子代理基底。boot 注册；
// 返回 false = 该会话没有可用基底（task 工具未启用/会话未知）。
type RunnerSource func(sessionID string) (SubagentRunner, bool)

// Service 是进程级工作流服务。并发安全。
type Service struct {
	mu      sync.Mutex
	store   *store
	runs    map[string]*RunRecord
	order   []string // 启动序，最老在前
	sources []RunnerSource
	// dirty 通知面（686 面板真数据）：状态每次变化非阻塞投一个信号，
	// 消费方（desktop hub）自行拉快照。缓冲 1 = 合并风暴。
	dirty chan struct{}
	once  sync.Once
}

// NewService 构造服务；dir 为 config.WorkflowsDir()（空 = 内存目录）。
func NewService(dir string) *Service {
	return &Service{
		store: newStore(dir),
		runs:  map[string]*RunRecord{},
		dirty: make(chan struct{}, 1),
	}
}

// global 是进程级单例（惰性构造，boot 与桌面 hub 共用）。
var (
	globalOnce sync.Once
	globalSvc  *Service
)

// Global 返回进程级服务单例。目录在首个调用者处定死（boot 早于一切会话）。
func Global() *Service {
	globalOnce.Do(func() {
		globalSvc = NewService(config.WorkflowsDir())
	})
	return globalSvc
}

// AddRunnerSource 注册一个基底解析源（boot 每次装配调用一次；后注册者先查，
// 使重启后的新装配覆盖同会话旧条目）。
func (s *Service) AddRunnerSource(src RunnerSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = append([]RunnerSource{src}, s.sources...)
}

// Notify 返回变更信号的读端。desktop hub 由此驱动推送。
func (s *Service) Notify() <-chan struct{} {
	return s.dirty
}

func (s *Service) touch() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// runnerFor 解析调用方会话的子代理基底（调用方须持 s.mu；source 探测是
// 纯读闭包，锁内执行安全）。
func (s *Service) runnerForLocked(sessionID string) (SubagentRunner, error) {
	for _, src := range s.sources {
		if r, ok := src(sessionID); ok {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no workflow subagent substrate for session %q; workflow_run needs a session booted with the task tool enabled (CLI sessions without a task tool cannot run workflows)", sessionID)
}

// Define 实现 builtin.WorkflowService：校验 + 落盘。
func (s *Service) DefineWorkflow(_ context.Context, doc string, overwrite bool) (builtin.WorkflowDefineResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	created, wf, err := s.store.save([]byte(doc), overwrite)
	if err != nil {
		return builtin.WorkflowDefineResult{}, err
	}
	return defineResult(wf, created), nil
}

// List 实现 builtin.WorkflowService。
func (s *Service) ListWorkflows(_ context.Context) (builtin.WorkflowListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	docs := s.store.list()
	out := builtin.WorkflowListResult{Workflows: make([]builtin.WorkflowSummary, 0, len(docs))}
	for _, raw := range docs {
		var wf Workflow
		if err := json.Unmarshal(raw, &wf); err != nil {
			continue
		}
		out.Workflows = append(out.Workflows, summarize(wf))
	}
	return out, nil
}

// Get 实现 builtin.WorkflowService。
func (s *Service) GetWorkflow(_ context.Context, name string) (builtin.WorkflowGetResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, err := s.store.load(name)
	if err != nil {
		return builtin.WorkflowGetResult{}, err
	}
	raw, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return builtin.WorkflowGetResult{}, err
	}
	return builtin.WorkflowGetResult{Name: wf.Name, Document: string(raw)}, nil
}

// Run 实现 builtin.WorkflowService：解析基底与水槽，登记运行，后台起引擎。
// 运行存活于触发 turn 之外（自有 context），事件仍嵌进发起会话的事件流。
func (s *Service) RunWorkflow(ctx context.Context, name string) (builtin.WorkflowRunResult, error) {
	sessionID := jobs.SessionFromContext(ctx)
	s.mu.Lock()
	wf, err := s.store.load(name)
	if err != nil {
		s.mu.Unlock()
		return builtin.WorkflowRunResult{}, err
	}
	runner, err := s.runnerForLocked(sessionID)
	if err != nil {
		s.mu.Unlock()
		return builtin.WorkflowRunResult{}, err
	}
	_, sink, _, ok := agent.CallContext(ctx)
	if !ok || sink == nil {
		sink = event.Discard
	}
	runID := newRunID()
	runCtx, cancel := context.WithCancel(context.Background())
	// 子代理转录按发起会话归属（父会话探活、垃圾回收、面板归属都吃它）。
	if sessionID != "" {
		runCtx = agent.WithParentSession(runCtx, sessionID)
		runCtx = jobs.WithSession(runCtx, sessionID)
	}
	rec := &RunRecord{
		ID:        runID,
		Workflow:  wf.Name,
		State:     RunRunning,
		StartedAt: time.Now(),
		def:       wf,
		cancel:    cancel,
		nodes:     map[string]*nodeRecord{},
	}
	s.runs[runID] = rec
	s.order = append(s.order, runID)
	s.mu.Unlock()

	eng := New(runner, nil, &recordingSink{svc: s, runID: runID, inner: sink})
	go func() {
		defer cancel()
		report, runErr := eng.RunWithID(runCtx, wf, runID)
		s.finishRun(runID, report, runErr)
	}()
	s.touch()
	return builtin.WorkflowRunResult{
		RunID:    runID,
		Workflow: wf.Name,
		State:    string(RunRunning),
		Hint:     "poll workflow_status with this runId; node events stream to the conversation that started the run",
	}, nil
}

// Status 实现 builtin.WorkflowService：runID 空 = 最近运行（新→旧，截 20 条）。
func (s *Service) WorkflowStatus(_ context.Context, runID string) (builtin.WorkflowStatusResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.order
	if strings.TrimSpace(runID) != "" {
		rec, ok := s.runs[runID]
		if !ok {
			return builtin.WorkflowStatusResult{}, fmt.Errorf("unknown run %q; call workflow_status without run_id to list recent runs", runID)
		}
		ids = []string{rec.ID}
	}
	out := builtin.WorkflowStatusResult{}
	for i := len(ids) - 1; i >= 0 && len(out.Runs) < 20; i-- {
		out.Runs = append(out.Runs, s.runViewLocked(s.runs[ids[i]]))
	}
	return out, nil
}

// CancelRun 中止一次运行（面板停止钮的承接面；引擎在节点边界响应取消）。
// 返回是否命中（未知名或已终态 = false）。
func (s *Service) CancelRun(runID string) bool {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	rec.mu.Lock()
	if rec.State != RunRunning {
		rec.mu.Unlock()
		return false
	}
	rec.cancelled = true
	cancel := rec.cancel
	rec.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// RunNodeSnapshot 是一次运行中一个节点的锁外快照。
type RunNodeSnapshot struct {
	ID        string
	NodeType  string
	State     event.WorkflowNodeState
	Round     int
	Detail    string
	ChangedAt time.Time
}

// RunSnapshot 是一次运行的锁外快照（desktop hub 组装面板 wire 用）。
// Def 随行——面板要用定义渲染 DAG。
type RunSnapshot struct {
	ID        string
	Workflow  string
	State     RunState
	StartedAt time.Time
	EndedAt   time.Time
	Err       string
	Report    *WorkflowReport
	Def       *Workflow
	Nodes     []RunNodeSnapshot
}

// Snapshot 返回全部运行（启动序）的快照。
func (s *Service) Snapshot() []RunSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunSnapshot, 0, len(s.order))
	for _, id := range s.order {
		rec := s.runs[id]
		rec.mu.Lock()
		snap := RunSnapshot{
			ID:        rec.ID,
			Workflow:  rec.Workflow,
			State:     rec.State,
			StartedAt: rec.StartedAt,
			EndedAt:   rec.EndedAt,
			Err:       rec.Err,
			Report:    rec.Report,
			Def:       rec.def,
		}
		for _, nid := range rec.order {
			nr := rec.nodes[nid]
			if nr == nil {
				continue
			}
			snap.Nodes = append(snap.Nodes, RunNodeSnapshot{
				ID:        nid,
				NodeType:  nr.nodeType,
				State:     nr.state,
				Round:     nr.round,
				Detail:    nr.detail,
				ChangedAt: nr.changedAt,
			})
		}
		rec.mu.Unlock()
		out = append(out, snap)
	}
	return out
}

// RunDefinition 返回一次运行装载的定义（面板渲染 DAG 用）。未知名 = nil。
func (s *Service) RunDefinition(runID string) *Workflow {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[runID]
	if !ok {
		return nil
	}
	return rec.def
}

// finishRun 落袋终态（成功/失败/取消统一走这里；report 永不为 nil 语义由
// 引擎保证——见 Engine.Run 注释）。
func (s *Service) finishRun(runID string, report *WorkflowReport, runErr error) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return
	}
	rec.mu.Lock()
	cancelled := rec.cancelled
	rec.EndedAt = time.Now()
	rec.Report = report
	switch {
	case runErr != nil && cancelled:
		rec.State = RunFailed
		rec.Err = "cancelled"
	case runErr != nil:
		rec.State = RunFailed
		rec.Err = runErr.Error()
	default:
		rec.State = RunCompleted
	}
	rec.mu.Unlock()
	s.mu.Unlock()
	s.touch()
}

// recordingSink 是运行的水槽包装：WorkflowNode 事件在转发前更新登记行，
// 让 workflow_status 与面板快照不依赖事件回放就能读到节点态。
type recordingSink struct {
	svc   *Service
	runID string
	inner event.Sink
}

func (r *recordingSink) Emit(e event.Event) {
	if e.Kind == event.WorkflowNode && e.Workflow != nil {
		r.svc.recordNode(r.runID, *e.Workflow)
	}
	if r.inner != nil {
		r.inner.Emit(e)
	}
}

func (s *Service) recordNode(runID string, info event.WorkflowNodeInfo) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	s.mu.Unlock()
	if !ok {
		return
	}
	now := time.Now()
	rec.mu.Lock()
	nr, seen := rec.nodes[info.NodeID]
	if !seen {
		nr = &nodeRecord{nodeType: info.NodeType}
		rec.nodes[info.NodeID] = nr
		rec.order = append(rec.order, info.NodeID)
	}
	nr.state = info.State
	nr.round = info.Round
	nr.detail = info.Detail
	nr.changedAt = now
	rec.mu.Unlock()
	s.touch()
}

// runViewLocked 组装单次运行的工具视图（调用方持 s.mu）。
func (s *Service) runViewLocked(rec *RunRecord) builtin.WorkflowRunView {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	view := builtin.WorkflowRunView{
		RunID:     rec.ID,
		Workflow:  rec.Workflow,
		State:     string(rec.State),
		StartedAt: rec.StartedAt.UnixMilli(),
	}
	if !rec.EndedAt.IsZero() {
		view.EndedAt = rec.EndedAt.UnixMilli()
	}
	view.Error = rec.Err
	for _, id := range rec.order {
		nr := rec.nodes[id]
		if nr == nil {
			continue
		}
		view.Nodes = append(view.Nodes, builtin.WorkflowRunNodeState{
			NodeID:    id,
			NodeType:  nr.nodeType,
			State:     string(nr.state),
			Round:     nr.round,
			Detail:    nr.detail,
			ChangedAt: nr.changedAt.UnixMilli(),
		})
	}
	if rec.Report != nil {
		view.Report = reportView(rec.Report)
	}
	return view
}

func reportView(r *WorkflowReport) *builtin.WorkflowReportView {
	out := &builtin.WorkflowReportView{
		Conclusion: r.Conclusion,
		Verified:   r.Verified,
		NotCovered: r.NotCovered,
		Findings:   make([]builtin.WorkflowFindingView, 0, len(r.Findings)),
	}
	for _, f := range r.Findings {
		out.Findings = append(out.Findings, builtin.WorkflowFindingView{
			Where: f.Where, What: f.What, Evidence: f.Evidence, Status: f.Status, Severity: f.Severity,
		})
	}
	return out
}

func summarize(wf Workflow) builtin.WorkflowSummary {
	sum := builtin.WorkflowSummary{
		Name:          wf.Name,
		Description:   wf.Description,
		SchemaVersion: wf.Version,
		Nodes:         len(wf.Nodes),
		Edges:         len(wf.Edges),
	}
	for i := range wf.Nodes {
		switch wf.Nodes[i].Type {
		case NodeSubagent:
			sum.Subagents++
		case NodeGate:
			sum.Gates++
			if wf.Nodes[i].Loop != nil {
				sum.LoopGates++
			}
		case NodeJoin:
			sum.Joins++
		case NodePhase:
			sum.Phases++
		case NodeConfirm:
			sum.Confirms++
		}
	}
	return sum
}

func defineResult(wf *Workflow, created bool) builtin.WorkflowDefineResult {
	sum := summarize(*wf)
	return builtin.WorkflowDefineResult{
		Name:      wf.Name,
		Created:   created,
		Nodes:     sum.Nodes,
		Edges:     sum.Edges,
		Gates:     sum.Gates,
		LoopGates: sum.LoopGates,
	}
}
