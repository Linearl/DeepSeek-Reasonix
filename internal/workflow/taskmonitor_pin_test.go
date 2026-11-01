package workflow

// tl1324 G6 验收钉 6 条（任务书权威钉）：
//  1. 可见性：含 subagent 节点 run ⇒ store 出现对应条目；
//  2. 生命周期：节点失败也必须有 RecordDone 且状态为失败（不能只测成功路径）；
//  3. 不干扰：普通 jobs 后台任务 taskmonitor 行为不变（同一 recorder 实例上
//     jobs 风格登记照常工作）；
//  4. id 不冲突：工作流 wf 条目与 jobs jobID 条目同存 ⇒ 面板两条都在；
//  5. 租约泄漏：节点结束后 RuntimeLeaseUntil 清空 + heartbeat 不再续租
//     （startHeartbeat 只在 RecordDone 停，失败漏登记=goroutine 永续租=面板
//     僵尸 alive；panic recover 路径单钉）；
//  6. 归属：节点条目 SessionID == 发起会话 ID（jobs.SessionFromContext），
//     ListTasksForTab 精确匹配下可见（sessionID 留空=不可见）。
//
// 设施：renewCounterStore 包装 InMemoryStore 数续租；newPinMonitor 装配/卸载
// 包级 recorder；failingProvider 强制节点失败/配合预取消 ctx 走 Interrupted。

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
	"reasonix/internal/taskmonitor"
	"reasonix/internal/tool"
)

// ---- 设施 ----

// renewCounterStore 数 RenewRuntimeLease 的尝试/成功次数（钉 5）。SaveTask
// 补齐 FileStore 的 create-or-CAS 语义（InMemoryStore.SaveTask 只做 CAS 更新，
// 首写必须 UpsertTask——recorder.RecordStart 首写走 SaveTask）。
type renewCounterStore struct {
	*taskmonitor.InMemoryStore
	attempts atomic.Int64
	success  atomic.Int64
}

func (s *renewCounterStore) SaveTask(ctx context.Context, projectDir string, snap taskmonitor.TaskSnapshot) error {
	cur, err := s.InMemoryStore.GetTask(ctx, projectDir, snap.TaskID)
	if err == nil && cur == nil {
		return s.InMemoryStore.UpsertTask(projectDir, snap)
	}
	return s.InMemoryStore.SaveTask(ctx, projectDir, snap)
}

func (s *renewCounterStore) RenewRuntimeLease(ctx context.Context, projectDir, taskID, ownerID string, leaseUntil time.Time) (bool, error) {
	s.attempts.Add(1)
	ok, err := s.InMemoryStore.RenewRuntimeLease(ctx, projectDir, taskID, ownerID, leaseUntil)
	if ok {
		s.success.Add(1)
	}
	return ok, err
}

func (s *renewCounterStore) listAll(t *testing.T) []taskmonitor.TaskSnapshot {
	t.Helper()
	tasks, err := s.InMemoryStore.ListTasks(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	return tasks
}

// newPinMonitor 装配包级 recorder（复用同实例语义），测试结束卸载。
func newPinMonitor(t *testing.T) (*taskmonitor.TaskRecorder, *renewCounterStore) {
	t.Helper()
	st := &renewCounterStore{InMemoryStore: taskmonitor.NewInMemoryStore()}
	rec := taskmonitor.NewTaskRecorder(st, "", nil)
	SetTaskMonitorRecorder(rec)
	t.Cleanup(func() { SetTaskMonitorRecorder(nil) })
	return rec, st
}

// newPinTaskTool 造一个脚本化 provider 驱动的真实 TaskTool（钉里所有节点
// 执行都走生产 TaskSubagentRunner 路径）。
func newPinTaskTool(t *testing.T, prov provider.Provider) *agent.TaskTool {
	t.Helper()
	parentReg := tool.NewRegistry()
	parentReg.Add(dummyReadOnlyTool{})
	return agent.NewTaskToolWithOptions(agent.TaskToolOptions{
		Provider:       prov,
		ParentRegistry: parentReg,
		MaxSteps:       20,
		SysPrompt:      "pin sub-agent",
	}).WithTranscripts(agent.NewSubagentStore(t.TempDir()), t.TempDir(), "base-model", "base-effort")
}

// okProvider 恒答 "ok"（无 result schema 的节点不解析 JSON）。
var okProvider = &answeringProvider{routes: []route{{match: "", answer: "ok"}}}

// failingProvider 的 Stream 直接报错 ⇒ TaskTool.Execute 返回失败。
type failingProvider struct{ err error }

func (p *failingProvider) Name() string { return "pin-fail" }

func (p *failingProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	return nil, p.err
}

// pinMiniDoc 是单 subagent 节点的最小工作流。
const pinMiniDoc = `{
  "version": 1,
  "name": "pin-mini",
  "nodes": [
    {"id":"worker","type":"subagent","prompt":"do work"}
  ],
  "edges": []
}`

// runPinMini 经 engine 全链路跑一次 pinMiniDoc（runID 由 RunWithID 生成并随
// ctx 下行），返回本次 run 的 store 条目与 run 错误（失败节点 fail-fast 是
// 引擎语义，登记在错误冒泡前已由包装层落袋——调用方按需断言）。
func runPinMini(t *testing.T, ctx context.Context, prov provider.Provider) ([]taskmonitor.TaskSnapshot, error) {
	t.Helper()
	_, st := newPinMonitor(t) // 每次独立 store，条目无歧义
	runner := NewTaskSubagentRunner(newPinTaskTool(t, prov))
	eng := newTestEngine(runner, nil, nil)
	_, runErr := eng.Run(ctx, mustParse(t, pinMiniDoc))
	return st.listAll(t), runErr
}

// wfEntry 在条目列表里找节点对应的 wf 条目（runID 由引擎生成 ⇒ 按节点后缀匹配）。
func wfEntry(t *testing.T, tasks []taskmonitor.TaskSnapshot, nodeID string) *taskmonitor.TaskSnapshot {
	return wfEntryForRun(t, tasks, nodeID)
}

// ---- 钉 1：可见性（e2e 全链路，真实 TaskTool，含 fixer）----

func TestPin1_WorkflowRunLandsEntriesInTaskStore(t *testing.T) {
	_, st := newPinMonitor(t)
	h := newE2EHarness(t)
	ctx := jobs.WithSession(context.Background(), "sess-e2e-1")
	if _, err := h.eng.Run(ctx, mustParse(t, e2eWorkflowDoc)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tasks := st.listAll(t)
	// e2e 工作流的四个子代理节点：hunt-a / hunt-b / conf-a（confirm 走
	// ReadOnly 路径）/ fix-unit（gate loop 第 1 轮修一次后过）。
	for _, node := range []string{"hunt-a", "hunt-b", "conf-a", "fix-unit"} {
		entry := wfEntryForRun(t, tasks, node)
		if entry.State != taskmonitor.TaskStateSucceeded || entry.RuntimeState != taskmonitor.RuntimeStateExited {
			t.Fatalf("node %q: state=%s runtime=%s, want succeeded/exited", node, entry.State, entry.RuntimeState)
		}
		if entry.SessionID != "sess-e2e-1" {
			t.Fatalf("node %q: SessionID=%q, want sess-e2e-1", node, entry.SessionID)
		}
		// nodeID 可查：JobID 形如 wf:<runId>:<nodeID>（runId 非钉定 ⇒ 不比前缀）。
		if len(entry.TaskID) == 0 || len(entry.JobID) == 0 {
			t.Fatalf("node %q: empty identity: task=%q job=%q", node, entry.TaskID, entry.JobID)
		}
	}
	t.Logf("钉1成立：%d 条登记（hunt-a/hunt-b/conf-a/fix-unit 各一），SessionID 全部归属发起会话", len(tasks))
}

// wfEntryForRun 按节点后缀匹配（runID 由引擎生成，不预知）。
func wfEntryForRun(t *testing.T, tasks []taskmonitor.TaskSnapshot, nodeID string) *taskmonitor.TaskSnapshot {
	t.Helper()
	for i := range tasks {
		if suffix, ok := trimWfPrefix(tasks[i].JobID); ok && suffixEndsWithNode(suffix, nodeID) {
			return &tasks[i]
		}
	}
	t.Fatalf("no wf entry for node %q in %+v", nodeID, tasks)
	return nil
}

func trimWfPrefix(jobID string) (string, bool) {
	const p = "wf:"
	if len(jobID) <= len(p) || jobID[:len(p)] != p {
		return "", false
	}
	return jobID[len(p):], true
}

func suffixEndsWithNode(suffix, nodeID string) bool {
	// suffix = <runId>:<nodeID>[#<occ>]
	i := indexColon(suffix)
	if i < 0 {
		return false
	}
	node := suffix[i+1:]
	if j := indexHash(node); j >= 0 {
		node = node[:j]
	}
	return node == nodeID
}

func indexColon(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

func indexHash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' {
			return i
		}
	}
	return -1
}

// ---- 钉 2：生命周期（失败/取消/panic 三条退出路径都必 RecordDone）----

func TestPin2_FailedNodeRecordsDoneAsFailed(t *testing.T) {
	tasks, runErr := runPinMini(t, context.Background(), &failingProvider{err: errors.New("provider exploded")})
	if runErr == nil {
		t.Fatalf("失败节点应使 run fail-fast")
	}
	entry := wfEntry(t, tasks, "worker")
	if entry.State != taskmonitor.TaskStateFailed {
		t.Fatalf("失败节点应登记为 failed，实际 %s", entry.State)
	}
	if entry.RuntimeState != taskmonitor.RuntimeStateExited || !entry.RuntimeLeaseUntil.IsZero() || entry.RuntimeOwnerID != "" {
		t.Fatalf("失败节点应退租（exited/lease 清零/owner 清空），实际 runtime=%s lease=%v owner=%q",
			entry.RuntimeState, entry.RuntimeLeaseUntil, entry.RuntimeOwnerID)
	}
	t.Logf("钉2成立：节点失败 ⇒ RecordDone(failed) 且租约退净")
}

func TestPin2_CancelledNodeRecordsDoneAsCancelled(t *testing.T) {
	rec, st := newPinMonitor(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预取消 ⇒ ctx.Err() != nil ⇒ Interrupted 分支
	// 注：经 engine 的取消发生在节点边界（runAll 先查 ctx，未起跑的节点不
	// 登记），节点内取消由本钉直打包装层的终态映射。
	exec := func(context.Context, SubagentRequest) (string, error) {
		return "", context.Canceled
	}
	func() {
		defer func() { _ = recover() }() // 语义同失败路径：无 panic 预期，仅隔离
		if _, err := runNodeWithMonitor(ctx, SubagentRequest{NodeID: "worker"}, rec, exec); err == nil {
			t.Fatalf("取消的执行应报错")
		}
	}()
	entry := wfEntry(t, st.listAll(t), "worker")
	if entry.State != taskmonitor.TaskStateCancelled {
		t.Fatalf("取消节点应登记为 cancelled，实际 %s", entry.State)
	}
	if !entry.RuntimeLeaseUntil.IsZero() {
		t.Fatalf("取消节点应退租，实际 lease=%v", entry.RuntimeLeaseUntil)
	}
	t.Logf("钉2补钉：取消路径 RecordDone(interrupted→cancelled) 生效")
}

func TestPin2_PanicPathRecordsDoneBeforeRepanicking(t *testing.T) {
	rec, st := newPinMonitor(t)
	called := 0
	exec := func(context.Context, SubagentRequest) (string, error) {
		called++
		panic("subagent substrate exploded")
	}
	ctx := jobs.WithSession(context.Background(), "sess-panic")

	func() {
		defer func() {
			if p := recover(); p == nil {
				t.Fatalf("panic 应被包装 recover 记录后重抛，实际未重抛")
			} else if p != "subagent substrate exploded" {
				t.Fatalf("重抛的原 panic 值被改写：%v", p)
			}
		}()
		_, _ = runNodeWithMonitor(ctx, SubagentRequest{NodeID: "worker"}, rec, exec)
	}()
	if called != 1 {
		t.Fatalf("exec 应执行恰一次，实际 %d", called)
	}
	tasks := st.listAll(t)
	entry := wfEntry(t, tasks, "worker")
	if entry.State != taskmonitor.TaskStateFailed {
		t.Fatalf("panic 路径应登记 failed，实际 %s", entry.State)
	}
	if !entry.RuntimeLeaseUntil.IsZero() || entry.RuntimeState != taskmonitor.RuntimeStateExited {
		t.Fatalf("panic 路径应退租，实际 lease=%v runtime=%s", entry.RuntimeLeaseUntil, entry.RuntimeState)
	}
	t.Logf("钉2补钉：panic(recover→RecordDone→repanic) 全路径退租生效")
}

// ---- 钉 3+4：不干扰 + id 不冲突（同一 recorder 实例上两种来源共存）----

func TestPin3Pin4_JobsStyleEntriesUnaffectedAndCoexist(t *testing.T) {
	rec, st := newPinMonitor(t)
	runner := NewTaskSubagentRunner(newPinTaskTool(t, okProvider))
	eng := newTestEngine(runner, nil, nil)

	// jobs 风格登记（模拟普通后台任务在共享实例上的既有行为）。
	rec.RecordStart("task-1", "task", "manual background job")
	rec.RecordDone("task-1", jobs.Done, nil)

	// 工作流节点登记走同一实例、同一 store。
	if _, err := eng.Run(jobs.WithSession(context.Background(), "sess-x"), mustParse(t, pinMiniDoc)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tasks := st.listAll(t)
	var jobsEntry, wfTask *taskmonitor.TaskSnapshot
	for i := range tasks {
		switch {
		case tasks[i].JobID == "task-1":
			jobsEntry = &tasks[i]
		case len(tasks[i].JobID) > 3 && tasks[i].JobID[:3] == "wf:" && wfTask == nil:
			wfTask = &tasks[i]
		}
	}
	if jobsEntry == nil || wfTask == nil {
		t.Fatalf("两条来源都应在场：jobs=%v wf=%v（all=%+v）", jobsEntry != nil, wfTask != nil, tasks)
	}
	if jobsEntry.TaskID == wfTask.TaskID {
		t.Fatalf("id 不冲突被破坏：TaskID 撞车 %q", jobsEntry.TaskID)
	}
	if jobsEntry.State != taskmonitor.TaskStateSucceeded {
		t.Fatalf("钉3：jobs 条目行为应不变（succeeded），实际 %s", jobsEntry.State)
	}
	if wfTask.State != taskmonitor.TaskStateSucceeded {
		t.Fatalf("钉4：wf 条目应 succeeded，实际 %s", wfTask.State)
	}
	t.Logf("钉3+4成立：jobs 条目照常 succeeded；wf 条目（%s）与 jobs 条目（%s）同存不同 id", wfTask.TaskID, jobsEntry.TaskID)
}

// ---- 钉 5：租约泄漏（终态清租 + heartbeat 不再续租）----

func TestPin5_LeaseClearedAndHeartbeatStopsAfterDone(t *testing.T) {
	_, st := newPinMonitor(t)
	runner := NewTaskSubagentRunner(newPinTaskTool(t, okProvider))
	eng := newTestEngine(runner, nil, nil)
	if _, err := eng.Run(jobs.WithSession(context.Background(), "sess-lease"), mustParse(t, pinMiniDoc)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entry := wfEntry(t, st.listAll(t), "worker")
	if !entry.RuntimeLeaseUntil.IsZero() || entry.RuntimeState != taskmonitor.RuntimeStateExited || entry.RuntimeOwnerID != "" {
		t.Fatalf("节点结束后租约必须清空（面板 alive 僵尸的直接判据），实际 lease=%v runtime=%s owner=%q",
			entry.RuntimeLeaseUntil, entry.RuntimeState, entry.RuntimeOwnerID)
	}

	// heartbeat 退出：成功续租数不得再涨（续租守卫在终态上必拒，
	// renewHeartbeat false ⇒ goroutine 自行退出）。heartbeat 周期 5s
	// （taskmonitor.runtimeHeartbeatEvery）：第一个窗口 >一整周期，第二个
	// 窗口再过一周期比对稳定性 —— 若心跳泄漏，第二窗口必出现新续租。
	w1 := st.success.Load()
	time.Sleep(6 * time.Second)
	w2 := st.success.Load()
	time.Sleep(5400 * time.Millisecond)
	w3 := st.success.Load()
	if w2 != w1 || w3 != w2 {
		t.Fatalf("heartbeat 泄漏：RecordDone 后续租仍在增长 %d→%d→%d", w1, w2, w3)
	}
	t.Logf("钉5成立：租约清空 + 两个窗口（6s/5.4s）续租数稳定在 %d ⇒ heartbeat 已退出，无僵尸 alive", w3)
}

// ---- 钉 6：归属（SessionID == 发起会话 ID）----

func TestPin6_EntryOwnedByInitiatingSession(t *testing.T) {
	const sid = "sess-owner-pin-42"
	tasks, runErr := runPinMini(t, jobs.WithSession(context.Background(), sid), okProvider)
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	entry := wfEntry(t, tasks, "worker")
	if entry.SessionID != sid {
		t.Fatalf("归属被破坏：SessionID=%q, want %q（发起会话）", entry.SessionID, sid)
	}
	// ListTasksForTab 精确匹配语义（desktop filterTasksBySession:
	// task.SessionID == sessionID）：发起会话 tab 可见，无关 tab 不可见。
	visibleForOwner, visibleForOther := 0, 0
	for i := range tasks {
		switch tasks[i].SessionID {
		case sid:
			visibleForOwner++
		case "some-other-tab":
			visibleForOther++
		}
	}
	if visibleForOwner != 1 || visibleForOther != 0 {
		t.Fatalf("面板可见性：owner tab 应见 1 条（实际 %d），other tab 应见 0 条（实际 %d）",
			visibleForOwner, visibleForOther)
	}
	t.Logf("钉6成立：条目 SessionID=%q 精确匹配发起会话 tab", sid)
}

// ---- fixer occurrence：同 id 不可复用（钉 2 的机制面，⑤）----

func TestPinOccurrence_FixerRerunsGetDistinctIds(t *testing.T) {
	_, st := newPinMonitor(t)
	gate := newFakeGate()
	gate.byCmd["go test ./..."] = []GateResult{
		{ExitCode: 1, Output: "FAIL r1"},
		{ExitCode: 1, Output: "FAIL r2"},
		{ExitCode: 0},
	}
	doc := `{
  "version": 1,
  "name": "pin-fixer-occ",
  "nodes": [
    {"id":"check","type":"gate","command":"go test ./...",
     "loop":{"fixer":"fix-it","max_rounds":4}},
    {"id":"fix-it","type":"subagent","prompt":"fix it"}
  ],
  "edges": []
}`
	runner := NewTaskSubagentRunner(newPinTaskTool(t, okProvider))
	eng := newTestEngine(runner, gate.run, nil)
	if _, err := eng.Run(jobs.WithSession(context.Background(), "sess-occ"), mustParse(t, doc)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tasks := st.listAll(t)
	var ids []string
	for i := range tasks {
		ids = append(ids, tasks[i].JobID)
	}
	// fixer 第 1 轮 = wf:<run>:fix-it，第 2 轮 = wf:<run>:fix-it#2 —— 同 id
	// 不可复用，两条登记独立成行、各自终态。
	round1, round2 := 0, 0
	for _, id := range ids {
		if suffix, ok := trimWfPrefix(id); ok {
			node := suffix[indexColon(suffix)+1:]
			switch node {
			case "fix-it":
				round1++
			case "fix-it#2":
				round2++
			}
		}
	}
	if round1 != 1 || round2 != 1 {
		t.Fatalf("fixer 两轮应各得一条独立登记（round1=%d round2=%d），ids=%v", round1, round2, ids)
	}
	for i := range tasks {
		if tasks[i].State != taskmonitor.TaskStateSucceeded {
			t.Fatalf("每条登记都应各自终态 succeeded，实际 %s（%s）", tasks[i].State, tasks[i].JobID)
		}
	}
	t.Logf("occurrence 钉成立：fixer 两轮登记为 %v，无同 id 复用", ids)
}
