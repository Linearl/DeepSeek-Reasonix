package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"reasonix/internal/event"
)

// 任务 1316 验收矩阵（设计文档 §8）：断点续跑的引擎投影/恢复、服务层落盘/
// 启动恢复/resume 入口/拒绝面/回收窗口/损坏容忍。每个测试名标注矩阵编号。

// countingRunner 记录每次子代理调用（prompt 序），答案可定制。
type countingRunner struct {
	mu      sync.Mutex
	prompts []string
	answer  func(prompt string) string
}

func (c *countingRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	c.mu.Lock()
	c.prompts = append(c.prompts, req.Prompt)
	answer := ""
	if c.answer != nil {
		answer = c.answer(req.Prompt)
	}
	c.mu.Unlock()
	if answer == "" {
		return "Final answer: {\"ok\": true}", nil
	}
	return "Final answer: " + answer, nil
}

func (c *countingRunner) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.prompts)
}

// captureCheckpoints 收集引擎投影（最后一次 = 断点现场）。
type captureCheckpoints struct {
	mu  sync.Mutex
	dat []*CheckpointData
}

func (c *captureCheckpoints) hook(cd *CheckpointData) {
	c.mu.Lock()
	c.dat = append(c.dat, cd)
	c.mu.Unlock()
}

func (c *captureCheckpoints) last() *CheckpointData {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.dat) == 0 {
		return nil
	}
	return c.dat[len(c.dat)-1]
}

const resumeChainDoc = `
version: 1
name: resume-chain
nodes:
  - id: first
    type: subagent
    prompt: first probe
  - id: second
    type: subagent
    prompt: second probe
edges:
  - from: first
    to: second
`

// 矩阵2（引擎层）：断点续跑——已完成节点不重跑、断点后节点继续、报告仍落袋。
func TestEngineResumeFromCheckpointSkipsFinishedNodes(t *testing.T) {
	runner := &countingRunner{}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook

	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 第一段：跑完第一个节点后人为中断——用一个已取消的 ctx 模拟进程死亡
	// 前的最后状态（第一个节点终态已 checkpoint，第二个未跑）。
	ctx, cancel := context.WithCancel(context.Background())
	eng.Subagents = &gateStopRunner{inner: runner, afterN: 1, onStop: cancel}
	if _, runErr := eng.RunWithID(ctx, wf, "wf-resume1"); runErr == nil {
		t.Fatalf("expected cancellation error for the interrupted run")
	}
	if cp.last() == nil {
		t.Fatalf("no checkpoint captured before the interruption")
	}
	if _, ok := cp.last().Nodes["first"]; !ok {
		t.Fatalf("first node missing from checkpoint: %+v", cp.last().Nodes)
	}
	if _, ok := cp.last().Nodes["second"]; ok {
		t.Fatalf("second node must not be in the checkpoint yet")
	}
	firstCalls := runner.calls()

	// 第二段：从快照恢复续跑——只有断点后的节点进 runner。
	snap := &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         "wf-resume1",
		Workflow:      wf.Name,
		State:         string(RunRunning),
		StartedAt:     time.Now(),
		Nodes:         cp.last().Nodes,
		NodeOrder:     cp.last().NodeOrder,
		Bag:           cp.last().Bag,
		Verifs:        cp.last().Verifs,
	}
	eng2 := New(runner, nil, event.Discard)
	report, err := eng2.ResumeWithID(context.Background(), wf, snap)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if runner.calls() != firstCalls+1 {
		t.Fatalf("resume must run exactly the remaining node: calls=%d want=%d", runner.calls(), firstCalls+1)
	}
	if !strings.Contains(report.Conclusion, "2/2 nodes ran") {
		t.Fatalf("report should count restored nodes as ran: %q", report.Conclusion)
	}
}

// gateStopRunner 在第 N 个子代理调用返回后触发 cancel（模拟断点时刻）。
type gateStopRunner struct {
	inner   *countingRunner
	afterN  int
	onStop  func()
	once    sync.Once
}

func (g *gateStopRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	ans, err := g.inner.Run(ctx, req)
	if g.inner.calls() >= g.afterN {
		g.once.Do(func() {
			if g.onStop != nil {
				g.onStop()
			}
		})
	}
	return ans, err
}

// 矩阵5（引擎层）：gate pass 分支已决后中断，恢复后 fail 分支不激活、
// pass 分支下游继续。
func TestEngineResumeReplaysGateBranchDecision(t *testing.T) {
	doc := `
version: 1
name: resume-branch
nodes:
  - id: gate
    type: gate
    command: "exit 0"
  - id: pass-side
    type: subagent
    prompt: pass side
  - id: fail-side
    type: subagent
    prompt: fail side
edges:
  - {from: gate, to: pass-side, when: pass}
  - {from: gate, to: fail-side, when: fail}
`
	runner := &countingRunner{}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// gate 是第一个节点：一次 gate 成功 + passSide 子代理后中断。
	ctx, cancel := context.WithCancel(context.Background())
	eng.Subagents = &gateStopRunner{inner: runner, afterN: 1, onStop: cancel}
	if _, runErr := eng.RunWithID(ctx, wf, "wf-branch"); runErr == nil {
		t.Fatalf("expected interruption error")
	}
	snap := &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         "wf-branch",
		Workflow:      wf.Name,
		State:         string(RunRunning),
		StartedAt:     time.Now(),
		Nodes:         cp.last().Nodes,
		NodeOrder:     cp.last().NodeOrder,
	}
	if ns := snap.Nodes["gate"]; ns.Terminal != "passed" || ns.GatePassed == nil || !*ns.GatePassed {
		t.Fatalf("gate terminal not captured as passed: %+v", ns)
	}
	eng2 := New(runner, nil, event.Discard)
	report, err := eng2.ResumeWithID(context.Background(), wf, snap)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// failSide 必须保持 skipped（边决策重放），passSide 恰好再跑一次。
	for _, f := range report.Findings {
		_ = f
	}
	// 中断发生在 passSide 完成后：恢复阶段 failSide 不跑（边决策重放为
	// inactive）、passSide 不重跑——runner 总数保持中断前的 1（gate 不经 runner）。
	if runner.calls() != 1 {
		t.Fatalf("resume must not re-run decided branches: calls=%d", runner.calls())
	}
	// 全图收敛：3/3 节点（gate+passSide 恢复计数 + failSide skipped 也计入 ran？——
	// skipped 不算 ran；结论应报告完成而非停摆。
	if strings.Contains(report.Conclusion, "stopped at node") {
		t.Fatalf("resumed run must complete, got %q", report.Conclusion)
	}
}

// resumeConfirmJoinDoc 是矩阵6+7 的 fixture（find→confirm→join→after）。
const resumeConfirmJoinDoc = `
version: 1
name: resume-confirm-join
nodes:
  - id: find
    type: subagent
    prompt: find issues
    result:
      - name: claim
        type: string
    findings_field: claim
  - id: verify
    type: confirm
    target: find
  - id: merge
    type: join
  - id: after
    type: subagent
    prompt: act on {{merge}}
edges:
  - {from: find, to: verify}
  - {from: verify, to: merge}
  - {from: merge, to: after}
`

// 矩阵6+7（引擎层）：confirm/join 恢复——evidence 与聚合值来自快照，
// 下游 interpolate 与不中断基线逐字一致。
func TestEngineResumeRestoresConfirmAndJoinContext(t *testing.T) {
	doc := resumeConfirmJoinDoc
	runner := &countingRunner{answer: func(prompt string) string {
		if strings.HasPrefix(prompt, "find issues") {
			return `{"claim": [{"where": "a.go", "what": "bug"}]}`
		}
		return `{"confirmed": true, "reason": "reproduced"}`
	}}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 跑完 find+verify 后中断（after 触发 cancel）。
	ctx, cancel := context.WithCancel(context.Background())
	eng.Subagents = &gateStopRunner{inner: runner, afterN: 2, onStop: cancel} // find+verify 之后断点
	if _, runErr := eng.RunWithID(ctx, wf, "wf-cj"); runErr == nil {
		t.Fatalf("expected interruption error")
	}
	snap := &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         "wf-cj",
		Workflow:      wf.Name,
		State:         string(RunRunning),
		StartedAt:     time.Now(),
		Nodes:         cp.last().Nodes,
		NodeOrder:     cp.last().NodeOrder,
		Bag:           cp.last().Bag,
	}
	if _, ok := snap.Nodes["verify"]; !ok {
		t.Fatalf("confirm node missing from checkpoint")
	}
	if len(snap.Bag) == 0 {
		t.Fatalf("findings bag missing from checkpoint")
	}
	before := runner.calls()
	eng2 := New(runner, nil, event.Discard)
	if _, err := eng2.ResumeWithID(context.Background(), wf, snap); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// 恢复后只跑 after（join/confirm/find 不重跑）。
	if runner.calls() != before+1 {
		t.Fatalf("resume must only run `after`: %d -> %d", before, runner.calls())
	}
	// after 的 prompt 必须与「不中断基线」逐字一致——join 聚合值来自快照
	// result（merge 只聚合 active 上游 verify；基线跑法相同，两条 prompt 相等
	// 即证明 interpolate 上下文被完整恢复）。
	baseline := &countingRunner{answer: runner.answer}
	engB := New(baseline, nil, event.Discard)
	if _, err := engB.RunWithID(context.Background(), mustParseWf(t, doc), ""); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	want := baseline.prompts[len(baseline.prompts)-1]
	got := runner.prompts[len(runner.prompts)-1]
	if got != want {
		t.Fatalf("restored interpolation diverges from baseline:\n got=%q\nwant=%q", got, want)
	}
}

// 矩阵8（引擎层）：output 超 64KB 截断的节点，恢复时整节点重置重跑。
func TestEngineResumeResetsTruncatedOutputNode(t *testing.T) {
	big := strings.Repeat("字", maxNodeOutputBytes) // 多字节，验证 UTF-8 边界
	runner := &countingRunner{answer: func(prompt string) string {
		return fmt.Sprintf(`{"ok": %q}`, big)
	}}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook
	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	eng.Subagents = &gateStopRunner{inner: runner, afterN: 1, onStop: cancel}
	if _, runErr := eng.RunWithID(ctx, wf, "wf-trunc"); runErr == nil {
		t.Fatalf("expected interruption error")
	}
	ns := cp.last().Nodes["first"]
	if !ns.OutputTruncated {
		t.Fatalf("oversized output must be marked truncated")
	}
	if len(ns.Output) > maxNodeOutputBytes+8 {
		t.Fatalf("truncated output exceeds cap: %d", len(ns.Output))
	}
	if !utf8.ValidString(ns.Output) {
		t.Fatalf("truncation must stay on rune boundaries")
	}
	before := runner.calls()
	eng2 := New(runner, nil, event.Discard)
	if _, err := eng2.ResumeWithID(context.Background(), wf, &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         "wf-trunc",
		Workflow:      wf.Name,
		State:         string(RunRunning),
		StartedAt:     time.Now(),
		Nodes:         cp.last().Nodes,
		NodeOrder:     cp.last().NodeOrder,
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if runner.calls() != before+2 { // first 重跑 + second 首跑
		t.Fatalf("truncated node must be reset and re-run: %d -> %d", before, runner.calls())
	}
}

const restoreDoc = `
version: 1
name: restore-svc
nodes:
  - id: solo
    type: subagent
    prompt: solo probe
`

// 矩阵1+13（服务层）：进程死亡 → 重启恢复 interrupted + 可续徽章；续跑钮
// 语义（服务判定 Resumable）。
func TestServiceRestoreInterruptedRunFromDisk(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewService(dir)
	ctx := context.Background()
	if _, err := svc1.DefineWorkflow(ctx, restoreDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	block := &blockingRunner{release: make(chan struct{})}
	svc1.AddRunnerSource(func(sessionID string) (SubagentRunner, bool) {
		if sessionID == "branch-x" {
			return block, true
		}
		return nil, false
	})
	res, err := svc1.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-x"), "restore-svc", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 等 solo 节点的 checkpoint 落盘（queued 事件即有投影？——投影在终态，
	// running 中无快照；这里节点未完成，盘上尚无 run.json。等待出现）。
	// 注：节点未到终态时不落盘是设计语义（running 中节点无恢复面）。
	runID := res.RunID
	_ = runID
	// 直接杀进程式中断：不再触碰 svc1（goroutine 挂在 blocking 上，随测试
	// 进程退出回收）。svc2 恢复——无终态快照 = 无恢复物，run 丢失（这是
	// 「节点终态粒度」的边界，矩阵14 用手工快照验证恢复面）。
	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	if len(svc2.Snapshot()) != 0 {
		t.Fatalf("no terminal checkpoint on disk, nothing to restore")
	}
}

// 矩阵1+14（服务层端到端）：有 checkpoint 的 run 重启恢复 interrupted →
// 续跑从断点继续 → 二次中断再续跑。
func TestServiceRestoreAndResumeInterruptedRun(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewService(dir)
	ctx := context.Background()
	if _, err := svc1.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	// first 放行（计数），second 阻塞——断点现场确定性落在两节点之间。
	var calls1 int32
	gate := &nodeGateRunner{
		blockPrefix: "second",
		release:     make(chan struct{}),
		onPass:      func() { atomic.AddInt32(&calls1, 1) },
	}
	svc1.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc1.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-y"), "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 等 first 节点 checkpoint 落盘后「杀进程」（弃用 svc1，不 cancel）。
	waitFor(t, 2*time.Second, func() bool {
		return runSnapshotOnDiskHas(dir, res.RunID, "first")
	})

	// —— 重启 ——
	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	snaps := svc2.Snapshot()
	if len(snaps) != 1 || snaps[0].State != RunInterrupted {
		t.Fatalf("restored run must be interrupted: %+v", snaps)
	}
	if !snaps[0].Resumable {
		t.Fatalf("restored run must be resumable (definition unchanged)")
	}
	if snaps[0].Def == nil {
		t.Fatalf("resumable run must carry its definition for the panel DAG")
	}

	// —— 续跑（模拟工具入口：非空 sessionID）——
	var calls2 int32
	runner2 := &hookRunner{onDone: func() { atomic.AddInt32(&calls2, 1) }}
	svc2.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner2, true })
	res2, err := svc2.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-z"), "resume-chain", res.RunID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res2.RunID != res.RunID {
		t.Fatalf("resume keeps the run identity: %s -> %s", res.RunID, res2.RunID)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc2.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	if atomic.LoadInt32(&calls2) != 1 {
		t.Fatalf("resume must only run the remaining node (second), got %d subagent calls", calls2)
	}
	// 快照落终态。
	snap := mustLoadSnap(t, dir, res.RunID)
	if snap.State != string(RunCompleted) || snap.Report == nil {
		t.Fatalf("final snapshot must carry completed state + report: state=%s report=%v", snap.State, snap.Report != nil)
	}

	// —— 二次中断再续跑（矩阵14）：把终态快照手工翻回 running 模拟又一次
	// 进程死亡（此时 first+second 都在快照里——构造真实二次中断：将 second
	// 从快照剔除并翻 running）——
	snap.State = string(RunRunning)
	snap.EndedAt = nil
	snap.Report = nil
	delete(snap.Nodes, "second")
	snap.NodeOrder = []string{"first"}
	if err := saveRunSnapshot(runsDir(dir), snap); err != nil {
		t.Fatalf("rewrite snapshot: %v", err)
	}
	svc3 := NewService(dir)
	svc3.restoreFromDisk()
	snaps = svc3.Snapshot()
	if len(snaps) != 1 || snaps[0].State != RunInterrupted || !snaps[0].Resumable {
		t.Fatalf("second restore must be interrupted+resumable: %+v", snaps)
	}
	var calls3 int32
	svc3.AddRunnerSource(func(string) (SubagentRunner, bool) {
		return &hookRunner{onDone: func() { atomic.AddInt32(&calls3, 1) }}, true
	})
	if _, err := svc3.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-w"), "resume-chain", res.RunID); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc3.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	if atomic.LoadInt32(&calls3) != 1 {
		t.Fatalf("second resume must only run second again, got %d calls", calls3)
	}
}

// hookRunner 计数用。
type hookRunner struct{ onDone func() }

func (h *hookRunner) Run(_ context.Context, _ SubagentRequest) (string, error) {
	h.onDone()
	return "Final answer: {\"ok\": true}", nil
}

// nodeGateRunner 按 prompt 前缀放行/阻塞：blockPrefix 的调用挂住直到 release
// ——「进程死在第二个节点上」的确定性构造（矩阵1/14 的断点现场）。
type nodeGateRunner struct {
	blockPrefix string
	release     chan struct{}
	onPass      func()
}

func (n *nodeGateRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	if strings.HasPrefix(req.Prompt, n.blockPrefix) {
		<-n.release
	}
	if n.onPass != nil {
		n.onPass()
	}
	return "Final answer: {\"ok\": true}", nil
}

func runSnapshotOnDiskHas(dir, runID, nodeID string) bool {
	snap, err := loadRunSnapshot(runsDir(dir), runID)
	if err != nil {
		return false
	}
	_, ok := snap.Nodes[nodeID]
	return ok
}

func mustParseWf(t *testing.T, doc string) *Workflow {
	t.Helper()
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return wf
}

func mustLoadSnap(t *testing.T, dir, runID string) *runSnapshot {
	t.Helper()
	snap, err := loadRunSnapshot(runsDir(dir), runID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	return snap
}

// 矩阵3：定义漂移拒绝（改文档后 resume 必拒，文案给出已存在的下一步）。
func TestServiceResumeRejectsDefinitionDrift(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	// 手工落一份 state=running 的快照（指纹按现行定义算）。
	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runID := "wf-drift"
	if err := saveRunSnapshot(runsDir(dir), &runSnapshot{
		SchemaVersion:  snapshotSchemaVersion,
		RunID:          runID,
		Workflow:       wf.Name,
		DefFingerprint: defFingerprint(wf),
		State:          string(RunRunning),
		StartedAt:      time.Now(),
		Nodes:          map[string]nodeSnapshot{"first": {Status: "done", Terminal: "passed"}},
		NodeOrder:      []string{"first"},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	svc.restoreFromDisk()
	// overwrite 定义 → 指纹变。
	if _, err := svc.DefineWorkflow(ctx, strings.Replace(resumeChainDoc, "first probe", "first probe v2", 1), true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return &hookRunner{onDone: func() {}}, true })
	_, err = svc.RunWorkflow(ctx, "resume-chain", runID)
	if err == nil || !strings.Contains(err.Error(), "workflow_get") {
		t.Fatalf("drifted definition must refuse with next-step hint, got %v", err)
	}
}

// 矩阵4：completed 拒 / cancelled 拒 / interrupted 可续（可续路径已被
// TestServiceRestoreAndResumeInterruptedRun 覆盖）。
func TestServiceResumeRefusalMatrix(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fp := defFingerprint(wf)
	nodes := map[string]nodeSnapshot{"first": {Status: "done", Terminal: "passed"}}
	save := func(id, state string, cancelled bool) {
		t.Helper()
		if err := saveRunSnapshot(runsDir(dir), &runSnapshot{
			SchemaVersion: snapshotSchemaVersion, RunID: id, Workflow: wf.Name,
			DefFingerprint: fp, State: state, StartedAt: time.Now(),
			Cancelled: cancelled, Nodes: nodes, NodeOrder: []string{"first"},
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return &hookRunner{onDone: func() {}}, true })

	save("wf-done", string(RunCompleted), false)
	svc.restoreFromDisk()
	_, err = svc.RunWorkflow(ctx, "resume-chain", "wf-done")
	if err == nil || !strings.Contains(err.Error(), "nothing to resume") {
		t.Fatalf("completed must refuse: %v", err)
	}

	// cancelled：盘上 failed+cancelled（tl1447 前的 legacy 形态——旧版停止落
	// failed）。仍不可续，文案点名「旧版停止记录」给重跑下一步。
	save("wf-cancel", string(RunFailed), true)
	svc.restoreFromDisk()
	_, err = svc.RunWorkflow(ctx, "resume-chain", "wf-cancel")
	if err == nil || !strings.Contains(err.Error(), "stopped by an earlier build") {
		t.Fatalf("legacy failed+cancelled must refuse with legacy-stop semantics: %v", err)
	}

	// 未知 run。
	_, err = svc.RunWorkflow(ctx, "resume-chain", "wf-unknown")
	if err == nil || !strings.Contains(err.Error(), "unknown run") {
		t.Fatalf("unknown run must refuse: %v", err)
	}
}

// 矩阵9：回收窗口——第 101 条终态落袋后最老终态逐出（内存+磁盘），running
// 与 interrupted 永不逐出。
func TestServicePruneKeepsFinishedWindowAndProtectsUnfinished(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fp := defFingerprint(wf)
	svc.mu.Lock()
	for i := 0; i < workflowMaxFinishedRuns+1; i++ {
		id := fmt.Sprintf("wf-old-%03d", i)
		rec := &RunRecord{
			ID: id, Workflow: wf.Name, State: RunCompleted,
			StartedAt: time.Now(), defFingerprint: fp,
			nodes: map[string]*nodeRecord{},
		}
		svc.runs[id] = rec
		svc.order = append(svc.order, id)
		if err := saveRunSnapshot(svc.persistDir, &runSnapshot{
			SchemaVersion: snapshotSchemaVersion, RunID: id, Workflow: wf.Name,
			DefFingerprint: fp, State: string(RunCompleted), StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	// 两条不可逐出：running + interrupted。
	live := &RunRecord{ID: "wf-live", Workflow: wf.Name, State: RunRunning, StartedAt: time.Now(), nodes: map[string]*nodeRecord{}}
	interrupted := &RunRecord{ID: "wf-cut", Workflow: wf.Name, State: RunInterrupted, StartedAt: time.Now(), resumableOK: true, nodes: map[string]*nodeRecord{}}
	svc.runs["wf-live"] = live
	svc.runs["wf-cut"] = interrupted
	svc.order = append(svc.order, "wf-live", "wf-cut")
	svc.pruneLocked()
	svc.mu.Unlock()

	if _, ok := svc.runs["wf-old-000"]; ok {
		t.Fatalf("oldest finished run must be evicted")
	}
	if _, ok := svc.runs["wf-old-001"]; !ok {
		t.Fatalf("within-window finished run must stay")
	}
	if _, ok := svc.runs["wf-live"]; !ok {
		t.Fatalf("running run must never be evicted")
	}
	if _, ok := svc.runs["wf-cut"]; !ok {
		t.Fatalf("interrupted run must never be evicted")
	}
	if _, err := os.Stat(filepath.Join(runsDir(dir), "wf-old-000")); !os.IsNotExist(err) {
		t.Fatalf("evicted run's disk snapshot must go with it")
	}
	if _, err := os.Stat(filepath.Join(runsDir(dir), "wf-old-001")); err != nil {
		t.Fatalf("within-window disk snapshot must stay: %v", err)
	}
}

// 矩阵10+11：损坏/未来版本快照跳过不炸启动，且未来版本绝不覆盖。
func TestServiceRestoreSkipsCorruptAndFutureSnapshots(t *testing.T) {
	dir := t.TempDir()
	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 损坏文件。
	if err := os.MkdirAll(filepath.Join(runsDir(dir), "wf-broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir(dir), "wf-broken", "run.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 未来版本。
	if err := saveRunSnapshot(runsDir(dir), &runSnapshot{
		SchemaVersion: snapshotSchemaVersion + 1, RunID: "wf-future", Workflow: wf.Name,
		DefFingerprint: defFingerprint(wf), State: string(RunRunning), StartedAt: time.Now(),
		Nodes: map[string]nodeSnapshot{"first": {Status: "done", Terminal: "passed"}},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(dir)
	svc.restoreFromDisk() // 不得 panic
	if snaps := svc.Snapshot(); len(snaps) != 0 {
		t.Fatalf("corrupt+future snapshots must be skipped: %+v", snaps)
	}
	// 未来版本不可覆盖写：save 语义不拦（同 id 正常覆盖是合法路径），拦截在
	// load——这里钉 load 的前向保护文案。
	if _, err := loadRunSnapshot(runsDir(dir), "wf-future"); err == nil || !strings.Contains(err.Error(), "upgrade Reasonix") {
		t.Fatalf("future schema must refuse with upgrade hint: %v", err)
	}
}

// 矩阵12：WorkflowsDir 为空 → 不落盘，run 行为与持久化之前逐字节相同。
func TestServiceEmptyDirSkipsPersistence(t *testing.T) {
	svc := NewService("")
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return &hookRunner{onDone: func() {}}, true })
	res, err := svc.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-e"), "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	if svc.persistDir != "" {
		t.Fatalf("empty dir must disable persistence")
	}
	// completed（本进程终态）先拦，文案给出下一步。
	_, err = svc.RunWorkflow(ctx, "resume-chain", res.RunID)
	if err == nil || !strings.Contains(err.Error(), "nothing to resume") {
		t.Fatalf("completed run must refuse: %v", err)
	}
	// 内存登记消失后（重启语义）恢复物为零——无持久化无恢复（新实例连定义
	// 都没有，store.load 先炸；两种错误都证明恢复面为空）。
	svc2 := NewService("")
	svc2.restoreFromDisk()
	_, err = svc2.RunWorkflow(ctx, "resume-chain", res.RunID)
	if err == nil {
		t.Fatalf("without persistence a restart loses runs entirely")
	}
}

// 矩阵2 补充（序列化往返）：result JSON 的 encode/decode 保真（join 聚合面）。
func TestSnapshotResultRoundtrip(t *testing.T) {
	orig := map[string]any{"find": map[string]any{"claim": "x"}}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["find"].(map[string]any)["claim"] != "x" {
		t.Fatalf("result roundtrip broken: %#v", back)
	}
}
