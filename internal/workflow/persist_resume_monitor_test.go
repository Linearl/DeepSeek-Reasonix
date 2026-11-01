package workflow

// tl1316 续跑 × tl1324 任务面板登记——组合回归钉（tl1316b）。
//
// 缺陷：runID 只在 RunWithID 随 ctx 下行，ResumeWithID 漏注入 ⇒ runner 读到
// 空 runID，续跑节点的登记 id 落 wf:norun:<nodeID> 兜底：
//   - 续跑行与所属 run 脱钩（面板看不出这一行属于哪个 run）；
//   - 跨 run 同名节点共用一个 jobID，而 recorder 的 start→done 配对表按 jobID
//     记——两次执行时间重叠时后写覆盖前写，先起跑的那一行永远收不到
//     RecordDone，heartbeat 永续租 = 面板僵尸 alive（tl1324 ⑥ 要防的正是它）。
//
// 覆盖缺口：1324 六钉全走 eng.Run（RunWithID 路径），且 wfEntryForRun 只按节点
// 后缀匹配、不比 runId 前缀——续跑 × 登记组合此前零覆盖。下面两钉闭掉它：
// 钉 A 走生产 TaskSubagentRunner 全链路断言精确 JobID；钉 B 用重叠屏障
// 确定性复现两个续跑并发时的配对串线（修复前必出僵尸行）。

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/jobs"
	"reasonix/internal/taskmonitor"
)

// resumeSnapAfterFirst 构造「first 已完成、second 未跑」的断点快照
// （resumeChainDoc：first → second）。engine 层不校验定义指纹（那是服务层
// prepareResumeLocked 的职责），手工快照即可驱动 ResumeWithID。
func resumeSnapAfterFirst(wf *Workflow, runID string) *runSnapshot {
	return &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         runID,
		Workflow:      wf.Name,
		State:         string(RunRunning),
		StartedAt:     time.Now(),
		NodeOrder:     []string{"first"},
		Nodes: map[string]nodeSnapshot{
			"first": {NodeType: "subagent", Status: "done", Terminal: "passed", Output: "first probe done"},
		},
	}
}

// 钉 A：续跑节点的任务面板登记 id 必须是 wf:<snap.RunID>:<nodeID>。走生产
// 链路 engine.ResumeWithID → TaskSubagentRunner（真实 TaskTool）→ recorder →
// store，精确比对 JobID（不复用只比节点后缀的 wfEntryForRun——它抓不到本缺陷）。
func TestResumeRegistersTaskPanelEntryUnderSnapshotRunID(t *testing.T) {
	_, st := newPinMonitor(t)
	wf := mustParse(t, resumeChainDoc)
	const runID = "wf-resume-monitor-a"
	const sid = "sess-resume-a"

	eng := newTestEngine(NewTaskSubagentRunner(newPinTaskTool(t, okProvider)), nil, nil)
	ctx := jobs.WithSession(context.Background(), sid)
	if _, err := eng.ResumeWithID(ctx, wf, resumeSnapAfterFirst(wf, runID)); err != nil {
		t.Fatalf("ResumeWithID: %v", err)
	}

	tasks := st.listAll(t)
	if len(tasks) != 1 {
		t.Fatalf("续跑只应登记断点后的节点 second（first 已恢复不重跑），实际 %d 条：%+v", len(tasks), tasks)
	}
	entry := tasks[0]
	if want := "wf:" + runID + ":second"; entry.JobID != want {
		t.Errorf("续跑登记 id 未带快照 runID：JobID=%q, want %q（wf:norun: 前缀 = runID 没随 ctx 下行）", entry.JobID, want)
	}
	if entry.SessionID != sid {
		t.Errorf("续跑登记归属被破坏：SessionID=%q, want %q", entry.SessionID, sid)
	}
	if entry.State != taskmonitor.TaskStateSucceeded || entry.RuntimeState != taskmonitor.RuntimeStateExited || !entry.RuntimeLeaseUntil.IsZero() {
		t.Errorf("续跑节点应终态退租（succeeded/exited/lease 清零），实际 state=%s runtime=%s lease=%v",
			entry.State, entry.RuntimeState, entry.RuntimeLeaseUntil)
	}
	t.Logf("钉A：续跑登记 JobID=%s TaskID=%s", entry.JobID, entry.TaskID)
}

// 钉 B：两个续跑 run 的同名节点执行时间重叠（现实场景：重启后在面板上对两条
// interrupted run 先后点续跑——面板路径无会话身份 = sessionless）。登记 id 必须
// 各带各的 runID，两行都走到终态、无僵尸 alive。修复前两者同为
// wf:norun:second：配对表后写覆盖前写，先起跑的那一行永不 RecordDone。
func TestConcurrentResumesKeepTaskPanelRowsApartAndTerminal(t *testing.T) {
	_, st := newPinMonitor(t)
	wf := mustParse(t, resumeChainDoc)
	runIDs := []string{"wf-resume-monitor-b1", "wf-resume-monitor-b2"}

	// 重叠屏障：两次执行都进入执行体（RecordStartSession 已落）后才一起放行，
	// 保证两条登记的 start 都先于任一 done——确定性构造重叠窗口。
	started := make(chan struct{}, 8)
	allIn := make(chan struct{})
	go func() {
		for range runIDs {
			<-started
		}
		close(allIn)
	}()
	exec := func(context.Context, SubagentRequest) (string, error) {
		started <- struct{}{}
		select {
		case <-allIn:
			return "ok", nil
		case <-time.After(10 * time.Second):
			return "", errors.New("overlap barrier timed out: the two resumed executions never overlapped")
		}
	}
	runner := monitoredExecRunner{exec: exec}

	errs := make(chan error, len(runIDs))
	for _, id := range runIDs {
		go func(id string) {
			eng := newTestEngine(runner, nil, nil)
			_, err := eng.ResumeWithID(context.Background(), wf, resumeSnapAfterFirst(wf, id))
			if err != nil {
				err = errors.Join(errors.New("resume "+id), err)
			}
			errs <- err
		}(id)
	}
	for range runIDs {
		if err := <-errs; err != nil {
			t.Fatalf("%v", err)
		}
	}

	tasks := st.listAll(t)
	if len(tasks) != len(runIDs) {
		t.Errorf("两个续跑应各得一条登记，实际 %d 条：%+v", len(tasks), tasks)
	}
	byJob := map[string]int{}
	for _, task := range tasks {
		byJob[task.JobID]++
		if task.RuntimeState == taskmonitor.RuntimeStateAlive || !task.RuntimeLeaseUntil.IsZero() {
			t.Errorf("僵尸行：JobID=%s TaskID=%s runtime=%s lease=%v（RecordDone 没落到这一行，heartbeat 仍在续租）",
				task.JobID, task.TaskID, task.RuntimeState, task.RuntimeLeaseUntil)
		}
	}
	for _, id := range runIDs {
		if want := "wf:" + id + ":second"; byJob[want] != 1 {
			t.Errorf("续跑 run %s 的登记缺失或撞车：want 恰 1 条 %q，实际 JobID 分布 %v", id, want, byJob)
		}
	}
}

// monitoredExecRunner 与生产 TaskSubagentRunner.Run 共用同一登记内核
// （currentTaskMonitorRecorder + runNodeWithMonitor），执行体可注入——重叠
// 屏障只能搭在执行体里（taskmonitor_pin_test 钉 2 的 cancel/panic 路径同款手法）。
type monitoredExecRunner struct {
	exec func(context.Context, SubagentRequest) (string, error)
}

func (m monitoredExecRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	return runNodeWithMonitor(ctx, req, currentTaskMonitorRecorder(), m.exec)
}
