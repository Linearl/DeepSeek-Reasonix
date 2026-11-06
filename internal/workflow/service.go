package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

// RunState 是一次运行的服务侧状态（工具与面板共读）。interrupted（任务
// 1316）= 进程重启后从磁盘恢复的未完 run，可续跑；由启动恢复标定，本进程
// 内不产生（本进程未完即 running）。
type RunState string

const (
	RunRunning     RunState = "running"
	RunCompleted   RunState = "completed"
	RunFailed      RunState = "failed"
	RunInterrupted RunState = "interrupted"
)

// workflowMaxFinishedRuns 是终态 run 的保留窗口（内存与磁盘同边界，任务
// 1316 §7 收口 service.go 无 delete 的无界增长）。running/interrupted 永不
// 逐出。量级对齐 heartbeat maxRunHistory=20 的先例：workflow run 是全局低频
// 重件（每条含报告），取一个量级宽。
const workflowMaxFinishedRuns = 100

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
	// sessionID 是发起会话的分支号（tl1434）：工作流节点详情按它定位子代理
	// transcript 归属（与 meta.ParentSession 同一身份）。resume-from-panel 的
	// 空会话/重启恢复的旧快照为空——查看面按「无关联」降级。
	sessionID string

	mu        sync.Mutex
	nodes     map[string]*nodeRecord
	order     []string // 节点首见序（= 文档声明序）
	def       *Workflow
	cancel    context.CancelFunc
	cancelled bool
	// lastCP 是引擎最后一次 checkpoint 投影（1316）；finishRun 用它落终态
	// 快照。持久化关闭（dir 空）时为 nil。
	lastCP *CheckpointData
	// defFingerprint 是装载定义的指纹（1316）：快照入盘与 resume 校验同源。
	defFingerprint string
	// resumableOK 是启动恢复时算好的可续判定（定义存在 + 指纹匹配）；resume
	// 时还会重算（定义可能中途被 overwrite）。本进程产生的 run 恒 false。
	resumableOK bool
}

type nodeRecord struct {
	nodeType  string
	state     event.WorkflowNodeState
	round     int
	detail    string
	changedAt time.Time
	// startedAt 是本轮 running 的起点（重入 running 即刷新，门重试轮次
	// 各自计起点）；终态事件到达时与 changedAt 相减即单节点耗时（任务 934
	// 块③指标面）。
	startedAt time.Time
	// subagentRefs / subagentRefParents 是 tl1434 的 nodeId→子代理执行实例
	// 登记（append-only）：每次派发（含门循环各轮）产生的持久化 transcript
	// ref 按执行序追加，parents 与 refs 一一对应（跨会话 resume 后新派发的
	// 归属可能与 run 级 sessionID 不同，所以按次登记）。空=无关联（老 run、
	// ephemeral 派发、gate/join 节点），查看面按此降级。
	subagentRefs       []string
	subagentRefParents []string
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
	// persistDir 是 run 快照目录收口（WorkflowsDir/runs；空 = 不落盘，
	// 持久化整体退化，行为与 1316 之前逐字节相同）。
	persistDir string
	// restored 标记启动恢复已跑完（异步）；完成前面板看不到历史 run。
	restored bool
}

// NewService 构造服务；dir 为 config.WorkflowsDir()（空 = 内存目录）。
func NewService(dir string) *Service {
	s := &Service{
		store:      newStore(dir),
		runs:       map[string]*RunRecord{},
		dirty:      make(chan struct{}, 1),
		persistDir: runsDir(dir),
	}
	return s
}

// global 是进程级单例（惰性构造，boot 与桌面 hub 共用）。
var (
	globalOnce sync.Once
	globalSvc  *Service
)

// Global 返回进程级服务单例。目录在首个调用者处定死（boot 早于一切会话）。
// 启动恢复（任务 1316）异步执行：扫 runs/ 把终态历史与 interrupted run 载入
// 只读视图（磁盘为权威、内存为缓存），完成后 touch 一次刷新面板。
func Global() *Service {
	globalOnce.Do(func() {
		globalSvc = NewService(config.WorkflowsDir())
		if globalSvc.persistDir != "" {
			go globalSvc.restoreFromDisk()
		}
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
// resumeRunID 非空（任务 1316）= 续跑该 interrupted run：断点后的节点继续，
// 已完成节点不重跑；run 沿用原 runID，快照覆盖更新。
func (s *Service) RunWorkflow(ctx context.Context, name string, resumeRunID string) (builtin.WorkflowRunResult, error) {
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

	resumeSnap := (*runSnapshot)(nil)
	var rec *RunRecord
	runID := newRunID()
	if resumeRunID != "" {
		rec, resumeSnap, err = s.prepareResumeLocked(resumeRunID, wf)
		if err != nil {
			s.mu.Unlock()
			return builtin.WorkflowRunResult{}, err
		}
		runID = rec.ID
	} else {
		rec = &RunRecord{
			ID:             runID,
			Workflow:       wf.Name,
			State:          RunRunning,
			StartedAt:      time.Now(),
			def:            wf,
			defFingerprint: defFingerprint(wf),
			nodes:          map[string]*nodeRecord{},
			sessionID:      sessionID,
		}
		s.runs[runID] = rec
		s.order = append(s.order, runID)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	// 子代理转录按发起会话归属（父会话探活、垃圾回收、面板归属都吃它）。
	if sessionID != "" {
		runCtx = agent.WithParentSession(runCtx, sessionID)
		runCtx = jobs.WithSession(runCtx, sessionID)
	}
	// tl1434: 子代理 transcript ref 的 run 级登记口——runner 派发后经此闭包
	// 把 nodeId→ref 落进 RunRecord（append-only），面板「工作过程」由此回放。
	runCtx = withSubagentRefRecorder(runCtx, func(req SubagentRequest, ref, parentSession string) {
		s.recordSubagentRef(runID, req, ref, parentSession)
	})
	rec.mu.Lock()
	rec.cancel = cancel
	rec.cancelled = false
	rec.mu.Unlock()
	s.mu.Unlock()

	eng := New(runner, nil, &recordingSink{svc: s, runID: runID, inner: sink})
	if s.persistDir != "" {
		eng.Checkpoint = func(cd *CheckpointData) {
			s.persistCheckpoint(runID, cd)
		}
	}
	go func() {
		defer cancel()
		var report *WorkflowReport
		var runErr error
		if resumeSnap != nil {
			report, runErr = eng.ResumeWithID(runCtx, wf, resumeSnap)
		} else {
			report, runErr = eng.RunWithID(runCtx, wf, runID)
		}
		s.finishRun(runID, report, runErr)
	}()
	s.touch()
	hint := "poll workflow_status with this runId; node events stream to the conversation that started the run"
	if resumeSnap != nil {
		hint = "resumed from the interrupted checkpoint: finished nodes are preserved, remaining nodes continue; poll workflow_status with this runId"
	}
	return builtin.WorkflowRunResult{
		RunID:    runID,
		Workflow: wf.Name,
		State:    string(RunRunning),
		Hint:     hint,
	}, nil
}

// prepareResumeLocked 校验并翻新待续跑的 run（调用方持 s.mu）：只允许
// interrupted 且非用户主动取消的 run；定义指纹必须与快照匹配（拿旧断点配
// 新图是错误的续跑）。返回翻新后的登记行与盘上快照。
func (s *Service) prepareResumeLocked(runID string, wf *Workflow) (*RunRecord, *runSnapshot, error) {
	rec, ok := s.runs[runID]
	if !ok {
		return nil, nil, fmt.Errorf("unknown run %q; call workflow_status without run_id to list recent runs", runID)
	}
	rec.mu.Lock()
	state := rec.State
	cancelled := rec.cancelled
	rec.mu.Unlock()
	if state == RunRunning {
		return nil, nil, fmt.Errorf("run %q is still running; nothing to resume (poll workflow_status)", runID)
	}
	if state == RunCompleted {
		return nil, nil, fmt.Errorf("run %q already completed; there is nothing to resume", runID)
	}
	if state != RunInterrupted {
		// failed 终态：用户主动取消的不可续（对齐「user stopped on purpose」
		// 语义——重新 workflow_run 从头跑是明确的下一步），真实失败可续。
		if cancelled {
			return nil, nil, fmt.Errorf("run %q was cancelled on purpose; resuming it is not supported — start a fresh run with workflow_run instead", runID)
		}
		return nil, nil, fmt.Errorf("run %q is %s and holds no checkpoint to resume; start a fresh run with workflow_run instead", runID, state)
	}
	snap, err := loadRunSnapshot(s.persistDir, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("run %q: checkpoint unavailable: %w", runID, err)
	}
	if fp := defFingerprint(wf); fp == "" || fp != snap.DefFingerprint {
		return nil, nil, fmt.Errorf("workflow %q has changed since run %q checkpointed; resuming against a different definition is not supported — start a fresh run with workflow_run, or inspect the current document with workflow_get", wf.Name, runID)
	}
	// 翻新：interrupted → running；终态/错误面清零；节点六态记录保留（面板
	// 历史连续）。cancel 由调用方随后填（prepareResume 不持有新 ctx）。
	rec.mu.Lock()
	rec.State = RunRunning
	rec.EndedAt = time.Time{}
	rec.Err = ""
	rec.Report = nil
	rec.lastCP = nil
	rec.resumableOK = false
	rec.mu.Unlock()
	return rec, snap, nil
}

// persistCheckpoint 是引擎 checkpoint 回调的服务侧落地面：投影 + 服务侧六态
// 记录合并成 run.json（原子覆盖写）。写盘失败只降级 log——持久化是增强不是
// 门禁。
func (s *Service) persistCheckpoint(runID string, cd *CheckpointData) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	persistDir := s.persistDir
	s.mu.Unlock()
	if !ok || persistDir == "" {
		return
	}
	rec.mu.Lock()
	rec.lastCP = cd
	// 合并服务侧六态（LastEventState/轮次细节）：recordingSink 先于 checkpoint
	// 回调（引擎同 goroutine 先 emit 后投影），此处的 nodeRecord 已含本节点终态。
	merged := make(map[string]nodeSnapshot, len(cd.Nodes))
	for id, ns := range cd.Nodes {
		if nr := rec.nodes[id]; nr != nil {
			ns.LastEventState = string(nr.state)
			ns.ChangedAt = nr.changedAt // tl1336：与六态同源同刻落盘，恢复时回填
			if ns.Detail == "" {
				ns.Detail = nr.detail
			}
			if ns.Round == 0 {
				ns.Round = nr.round
			}
			// tl1434：执行实例关联随快照落盘（重启后面板仍可回放）。
			ns.SubagentRefs = nr.subagentRefs
			ns.SubagentRefParents = nr.subagentRefParents
		}
		merged[id] = ns
	}
	snap := s.buildSnapshotLocked(rec, merged)
	rec.mu.Unlock()
	if err := saveRunSnapshot(persistDir, snap); err != nil {
		log.Printf("[workflow] checkpoint persist failed for run %s: %v", runID, err)
	}
}

// buildSnapshotLocked 组装快照（调用方持 rec.mu）。
func (s *Service) buildSnapshotLocked(rec *RunRecord, nodes map[string]nodeSnapshot) *runSnapshot {
	snap := &runSnapshot{
		SchemaVersion:  snapshotSchemaVersion,
		RunID:          rec.ID,
		Workflow:       rec.Workflow,
		DefFingerprint: rec.defFingerprint,
		State:          string(rec.State),
		StartedAt:      rec.StartedAt,
		Cancelled:      rec.cancelled,
		Nodes:          nodes,
		SessionID:      rec.sessionID,
	}
	if rec.lastCP != nil {
		snap.Phase = rec.lastCP.Phase
		snap.FailedNode = rec.lastCP.FailedNode
		snap.GateRound = rec.lastCP.GateRound
		snap.Bag = rec.lastCP.Bag
		snap.Verifs = rec.lastCP.Verifs
	}
	if !rec.EndedAt.IsZero() {
		ended := rec.EndedAt
		snap.EndedAt = &ended
	}
	snap.Err = rec.Err
	snap.Report = rec.Report
	// NodeOrder：优先登记首见序，退化为 lastCP 的声明序。
	for _, id := range rec.order {
		if _, ok := nodes[id]; ok {
			snap.NodeOrder = append(snap.NodeOrder, id)
		}
	}
	if len(snap.NodeOrder) == 0 && rec.lastCP != nil {
		snap.NodeOrder = rec.lastCP.NodeOrder
	}
	return snap
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
	// 任务 1334：runs 契约=始终数组。Go nil 切片 JSON 序列化为 null，无 run
	// 时会回 {"runs":null}，与有 run 时的数组形态不一致；起手赋空切片修平。
	out := builtin.WorkflowStatusResult{Runs: []builtin.WorkflowRunView{}}
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

// ResumeRunFromPanel（任务 1316）是面板续跑钮的承接面：面板没有调用方会话
// 身份，sessionID 传空串，由 boot 注册的回退 RunnerSource（仅对空 sessionID
// 命中）解析最新装配的子代理基底——工具路径（workflow_run resume_run_id）
// 不受影响。事件流归属：空会话无事件流，节点六态不进任何会话 feed，面板
// 由 workflow:runs 推送照常点亮。
func (s *Service) ResumeRunFromPanel(ctx context.Context, runID string) (builtin.WorkflowRunResult, error) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	workflowName := ""
	if ok {
		workflowName = rec.Workflow
	}
	s.mu.Unlock()
	if !ok {
		return builtin.WorkflowRunResult{}, fmt.Errorf("unknown run %q; the run list is in the workflow panel", runID)
	}
	return s.RunWorkflow(ctx, workflowName, runID)
}

// recordSubagentRef 是 tl1434 的登记落点（runner 派发尾经 runCtx 闭包调
// 用，任意 goroutine）：append-only 追加该节点的执行实例关联，不触碰既有
// 六态/输出记录（并发安全口径与 recordNode 相同——rec.mu 串行化）。
//
// tl1434 追加：幂等去重——同节点尾部相同 ref 视为同一执行实例的重复上报
// （运行中 mint 即时登记 + Execute 返回兜底各报一次），不重复 append。
// occurrence 的语义=不同执行实例（transcript），不是不同次上报；ref 由
// PrepareFresh 每次 mint 唯一 id，同一节点合法地再现同 ref 只可能是上报
// 重复，故只比尾部即可。
func (s *Service) recordSubagentRef(runID string, req SubagentRequest, ref, parentSession string) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	s.mu.Unlock()
	if !ok {
		return
	}
	rec.mu.Lock()
	nr, seen := rec.nodes[req.NodeID]
	if !seen {
		nr = &nodeRecord{}
		rec.nodes[req.NodeID] = nr
		rec.order = append(rec.order, req.NodeID)
	}
	if n := len(nr.subagentRefs); n > 0 && nr.subagentRefs[n-1] == ref {
		rec.mu.Unlock()
		return
	}
	nr.subagentRefs = append(nr.subagentRefs, ref)
	nr.subagentRefParents = append(nr.subagentRefParents, parentSession)
	rec.mu.Unlock()
	s.touch()
}

// SubagentRefForNode 返回某节点第 occurrence 次（1-based）执行登记的
// transcript ref 与其归属父会话（desktop 查看桥的归属证明：ref 只能来自
// 登记表，不能任意指名读盘）。无关联（未知 run/节点、越界 occurrence、
// 空表）返回明确错误——查看面按错误降级，绝不猜。
func (s *Service) SubagentRefForNode(runID, nodeID string, occurrence int) (ref, parentSession string, err error) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	s.mu.Unlock()
	if !ok {
		return "", "", fmt.Errorf("unknown run %q; the run list is in the workflow panel", runID)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	nr := rec.nodes[nodeID]
	if nr == nil || len(nr.subagentRefs) == 0 {
		return "", "", fmt.Errorf("node %q has no recorded sub-agent execution trace (legacy run or non-persisted dispatch)", nodeID)
	}
	if occurrence < 1 || occurrence > len(nr.subagentRefs) {
		return "", "", fmt.Errorf("occurrence %d out of range: node %q has %d recorded executions", occurrence, nodeID, len(nr.subagentRefs))
	}
	return nr.subagentRefs[occurrence-1], nr.subagentRefParents[occurrence-1], nil
}

// RunNodeSnapshot 是一次运行中一个节点的锁外快照。
type RunNodeSnapshot struct {
	ID        string
	NodeType  string
	State     event.WorkflowNodeState
	Round     int
	Detail    string
	ChangedAt time.Time
	// StartedAt 是本轮 running 起点（零值 = 未见 running，如排队即跳过）。
	StartedAt time.Time
	// SubagentRefs / SubagentRefParents 是 tl1434 的执行实例关联快照
	// （append-only，执行序）。空=无关联，面板按降级显示。
	SubagentRefs       []string
	SubagentRefParents []string
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
	// Resumable（任务 1316）：面板「可续跑」徽章与续跑钮的判定（服务侧单一
	// 真相；前端不算指纹）。interrupted + 定义存在 + 指纹匹配。
	Resumable bool
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
			Resumable: rec.resumableOK && rec.State == RunInterrupted && !rec.cancelled,
		}
		for _, nid := range rec.order {
			nr := rec.nodes[nid]
			if nr == nil {
				continue
			}
			snap.Nodes = append(snap.Nodes, RunNodeSnapshot{
				ID:                 nid,
				NodeType:           nr.nodeType,
				State:              nr.state,
				Round:              nr.round,
				Detail:             nr.detail,
				ChangedAt:          nr.changedAt,
				StartedAt:          nr.startedAt,
				SubagentRefs:       append([]string(nil), nr.subagentRefs...),
				SubagentRefParents: append([]string(nil), nr.subagentRefParents...),
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
// 引擎保证——见 Engine.Run 注释）。终态同时入盘上快照（1316），随后惰性
// prune 终态保留窗口。
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
	// 终态快照：最后一次引擎投影 + 服务侧六态 + 终态字段。lastCP 为 nil
	// （持久化关闭，或运行零节点终态就死了）则跳过盘面——登记仍是权威。
	if s.persistDir != "" && rec.lastCP != nil {
		merged := make(map[string]nodeSnapshot, len(rec.lastCP.Nodes))
		for id, ns := range rec.lastCP.Nodes {
			if nr := rec.nodes[id]; nr != nil {
				ns.LastEventState = string(nr.state)
				ns.ChangedAt = nr.changedAt // tl1336：同 persistCheckpoint
				if ns.Detail == "" {
					ns.Detail = nr.detail
				}
				if ns.Round == 0 {
					ns.Round = nr.round
				}
				// tl1434：同 persistCheckpoint，终态快照也带执行实例关联。
				ns.SubagentRefs = nr.subagentRefs
				ns.SubagentRefParents = nr.subagentRefParents
			}
			merged[id] = ns
		}
		snap := s.buildSnapshotLocked(rec, merged)
		if err := saveRunSnapshot(s.persistDir, snap); err != nil {
			log.Printf("[workflow] final snapshot persist failed for run %s: %v", runID, err)
		}
	}
	rec.mu.Unlock()
	s.pruneLocked()
	s.mu.Unlock()
	s.touch()
}

// restoreFromDisk（任务 1316）启动恢复：扫 runs/ 目录把快照载入只读视图。
// state=running 的快照（本进程没有它的 finishRun）标 interrupted；终态原样。
// 定义存在且指纹匹配的 interrupted run 标可续；损坏/未来版本跳过（log 提示，
// 不炸启动）。完成后 prune 终态窗口并 touch 一次面板。
func (s *Service) restoreFromDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range listRunSnapshotIDs(s.persistDir) {
		snap, err := loadRunSnapshot(s.persistDir, id)
		if err != nil {
			log.Printf("[workflow] skipping unreadable run snapshot %s: %v", id, err)
			continue
		}
		if _, dup := s.runs[snap.RunID]; dup {
			continue // 本进程已产出同 id（newRunID 冲突，概率忽略）；内存权威
		}
		rec := &RunRecord{
			ID:             snap.RunID,
			Workflow:       snap.Workflow,
			StartedAt:      snap.StartedAt,
			defFingerprint: snap.DefFingerprint,
			nodes:          map[string]*nodeRecord{},
			Err:            snap.Err,
			sessionID:      snap.SessionID, // tl1434：回填发起会话，恢复后仍可回放
		}
		for _, nid := range snap.NodeOrder {
			ns, ok := snap.Nodes[nid]
			if !ok {
				continue
			}
			nr := &nodeRecord{
				nodeType: ns.NodeType,
				state:    event.WorkflowNodeState(ns.LastEventState),
				round:    ns.Round,
				detail:   ns.Detail,
				// tl1336：回填落盘时的六态变迁时刻（旧快照/无事件节点为零值，
				// 视图侧按「未知」省略）。startedAt 刻意不回填：续跑重放终态事件
				// 会把 changedAt 刷成重放时刻，配旧起点会把停机时长算进单节点耗时。
				changedAt: ns.ChangedAt,
				// tl1434：执行实例关联随快照回填（旧快照无字段=无关联，降级）。
				subagentRefs:       ns.SubagentRefs,
				subagentRefParents: ns.SubagentRefParents,
			}
			if nr.state == "" {
				// skip 等无事件节点：六态缺省 queued（面板 queued-gray 语义）。
				nr.state = event.WorkflowNodeQueued
			}
			rec.nodes[nid] = nr
			rec.order = append(rec.order, nid)
		}
		switch snap.State {
		case string(RunRunning):
			rec.State = RunInterrupted
		case string(RunCompleted):
			rec.State = RunCompleted
		case string(RunFailed):
			rec.State = RunFailed
			rec.cancelled = snap.Cancelled
		default:
			log.Printf("[workflow] run snapshot %s has unknown state %q; skipping", id, snap.State)
			continue
		}
		if snap.EndedAt != nil {
			rec.EndedAt = *snap.EndedAt
		}
		rec.Report = snap.Report
		if rec.State == RunInterrupted {
			if wf, err := s.store.load(snap.Workflow); err == nil && defFingerprint(wf) == snap.DefFingerprint {
				rec.def = wf
				rec.resumableOK = true
			}
		}
		s.runs[rec.ID] = rec
		s.order = append(s.order, rec.ID)
	}
	s.restored = true
	s.pruneLocked()
	s.touch()
}

// pruneLocked 收口终态保留窗口（任务 1316 §7；调用方持 s.mu）：只有
// completed/failed 可逐出，running/interrupted 永不逐出；内存行与盘上目录
// 同步消失（单一边界，两层真相会腐化）。
func (s *Service) pruneLocked() {
	var finished []string
	for _, id := range s.order {
		switch s.runs[id].State {
		case RunCompleted, RunFailed:
			finished = append(finished, id)
		}
	}
	if len(finished) <= workflowMaxFinishedRuns {
		return
	}
	evict := make(map[string]bool, len(finished)-workflowMaxFinishedRuns)
	for _, id := range finished[:len(finished)-workflowMaxFinishedRuns] {
		evict[id] = true
		removeRunSnapshot(s.persistDir, id)
	}
	order := s.order[:0]
	for _, id := range s.order {
		if !evict[id] {
			order = append(order, id)
		}
	}
	s.order = order
	for id := range evict {
		delete(s.runs, id)
	}
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
	if info.State == event.WorkflowNodeRunning && nr.state != event.WorkflowNodeRunning {
		nr.startedAt = now
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
		node := builtin.WorkflowRunNodeState{
			NodeID:   id,
			NodeType: nr.nodeType,
			State:    string(nr.state),
			Round:    nr.round,
			Detail:   nr.detail,
		}
		// 零值 = 时刻未知（tl1336 之前的旧快照恢复 / 无事件节点）：留 0 由
		// omitempty 省略，与上方 EndedAt 同口径，不外露 Go 零值毫秒数。
		if !nr.changedAt.IsZero() {
			node.ChangedAt = nr.changedAt.UnixMilli()
		}
		view.Nodes = append(view.Nodes, node)
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
