package workflow

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
)

// tl1335 主动暂停（pause/resume 语义补齐）引擎面钉。语义边界：
//
//	pause  = 节点边界优雅挂起——当前节点（含 gate 修复环）完整跑完、
//	         checkpoint 落盘后，不再启动任何新节点，以 errWorkflowPaused 收场；
//	cancel = 立即砍掉运行中执行（context cancel，语义不变，tl1447 对照钉在
//	         tl1447_stop_resume_test.go）。
//
// 挂起点即恢复点：checkpoint 在盘，ResumeWithID 从断点继续（服务面钉在
// tl1335_pause_resume_test.go）。

// ① 节点边界挂起：PauseCheck 生效后 first 完成且 checkpoint 落袋、second 未
// 启动、errWorkflowPaused 返回、报告非 nil（salvage 落袋语义不变）。
func TestTL1335EnginePauseAtNodeBoundary(t *testing.T) {
	runner := &countingRunner{}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook
	eng.PauseCheck = func() bool { return true } // 第一轮循环头即挂起

	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	report, runErr := eng.RunWithID(context.Background(), wf, "wf-pause1")
	if !errors.Is(runErr, errWorkflowPaused) {
		t.Fatalf("pause must surface the sentinel error: %v", runErr)
	}
	if report == nil {
		t.Fatalf("pause must still hand back the salvage report")
	}
	if got := runner.calls(); got != 0 {
		t.Fatalf("pause at the first boundary must dispatch no node: %d", got)
	}
	if cp.last() == nil {
		t.Fatalf("pause must checkpoint at the boundary (the resume point)")
	}
}

// ② nil PauseCheck 零影响（对照面）：不请求暂停的 run 照常完成，既有调用方
// 路径逐字节不变。
func TestTL1335EngineNilPauseCheckRunsToCompletion(t *testing.T) {
	runner := &countingRunner{}
	eng := New(runner, nil, event.Discard) // PauseCheck 零值 nil

	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, runErr := eng.RunWithID(context.Background(), wf, "wf-pause2"); runErr != nil {
		t.Fatalf("run without a pause request must complete: %v", runErr)
	}
	if got := runner.calls(); got != 2 {
		t.Fatalf("both nodes must run: %d", got)
	}
}

// ③ 波次内节点不被砍：暂停请求落在波次执行中途（runner 回调里置位），已在
// 波次中的节点全部完成、聚合、checkpoint 之后才在下一边界挂起——温和挂起
// 的核心承诺（对比 cancel 立即砍掉运行中执行）。
func TestTL1335EnginePauseLetsInFlightWaveFinish(t *testing.T) {
	var pauseRequested atomic.Bool
	runner := &countingRunner{
		answer: func(prompt string) string {
			pauseRequested.Store(true) // 请求落在节点执行中途
			return ""
		},
	}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook
	eng.PauseCheck = func() bool { return pauseRequested.Load() }

	// 两条根节点无依赖 → 同一波次并发派发（tl1326 G4）。
	wf, err := Parse([]byte(`
version: 1
name: pause-wave
nodes:
  - id: wave-a
    type: subagent
    prompt: wave a probe
  - id: wave-b
    type: subagent
    prompt: wave b probe
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	report, runErr := eng.RunWithID(context.Background(), wf, "wf-pause3")
	if !errors.Is(runErr, errWorkflowPaused) {
		t.Fatalf("run must land paused at the NEXT boundary: %v", runErr)
	}
	if got := runner.calls(); got != 2 {
		t.Fatalf("in-flight wave nodes must all complete before pausing: %d", got)
	}
	if cp.last() == nil {
		t.Fatalf("pause must checkpoint after the wave aggregates")
	}
	for _, id := range []string{"wave-a", "wave-b"} {
		if _, ok := cp.last().Nodes[id]; !ok {
			t.Fatalf("completed wave node %q must be in the checkpoint", id)
		}
	}
	// 报告走 buildReport 的停止分支：暂停如实呈现为「停止前已完成的发现
	// 保留在下」——pause 不是完成态，不虚报 n/N。
	if !strings.Contains(report.Conclusion, "run paused by request") {
		t.Fatalf("salvage report must name the pause honestly: %q", report.Conclusion)
	}
}

// ④ 挂起后从 checkpoint 续跑（引擎面闭环）：挂起现场灌回 ResumeWithID，
// 已完成节点不重跑、剩余节点继续、run 完成——pause/resume 状态机的引擎半边。
func TestTL1335EngineResumeAfterPause(t *testing.T) {
	runner := &countingRunner{}
	cp := &captureCheckpoints{}
	eng := New(runner, nil, event.Discard)
	eng.Checkpoint = cp.hook
	eng.PauseCheck = func() bool { return runner.calls() >= 1 } // first 跑完后挂起

	wf, err := Parse([]byte(resumeChainDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, runErr := eng.RunWithID(context.Background(), wf, "wf-pause4"); !errors.Is(runErr, errWorkflowPaused) {
		t.Fatalf("expected pause after first: %v", runErr)
	}
	if _, ok := cp.last().Nodes["first"]; !ok {
		t.Fatalf("first must be checkpointed at the pause point")
	}
	firstCalls := runner.calls()

	// 解除暂停请求，从挂起现场续跑。
	eng2 := New(runner, nil, event.Discard)
	eng2.PauseCheck = func() bool { return false }
	snap := &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         "wf-pause4",
		Workflow:      wf.Name,
		// 恢复调度面不读 State（服务层状态机在 tl1335_pause_resume_test 钉），
		// 与 persist_resume_test 的中断快照同形态。
		State:     string(RunRunning),
		StartedAt: time.Now(),
		Nodes:     cp.last().Nodes,
		NodeOrder: cp.last().NodeOrder,
		Bag:       cp.last().Bag,
		Verifs:    cp.last().Verifs,
	}
	report, err := eng2.ResumeWithID(context.Background(), wf, snap)
	if err != nil {
		t.Fatalf("resume after pause: %v", err)
	}
	if runner.calls() != firstCalls+1 {
		t.Fatalf("resume must run only the remaining node: calls=%d want=%d", runner.calls(), firstCalls+1)
	}
	if !strings.Contains(report.Conclusion, "2/2 nodes ran") {
		t.Fatalf("resumed run must complete: %q", report.Conclusion)
	}
}
