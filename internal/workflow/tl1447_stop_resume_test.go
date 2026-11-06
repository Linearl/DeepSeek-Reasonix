package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// tl1447 缺陷③：停止不可续跑缺口。旧语义=停止落 failed（resume 仅对
// interrupted 开放）⇒停止后根本续不了，卡死处置只能整跑重来。新语义边界
// （failed≠interrupted，见 RunState 语义表）：
//
//	用户停止 → interrupted（可续态，断点在盘即点亮 resumable）
//	真实失败 → failed（断点不可信，重跑；resumable 恒 false）
//	legacy 盘上 failed+cancelled 快照（旧版停止）→ 保持 failed，不可续
//
// 每个测试对应一条边界；跨重启恢复单独一条（restoreFromDisk 的 interrupted
// 快照分支是本件新增）。

// promptCountingRunner 按 prompt 前缀记录调用次数（断点续跑「已完成节点不
// 重跑」的计数面）。
type promptCountingRunner struct {
	mu      sync.Mutex
	counts  map[string]int
	failOn  string
	failErr error
}

func newPromptCountingRunner() *promptCountingRunner {
	return &promptCountingRunner{counts: map[string]int{}}
}

func (p *promptCountingRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	p.mu.Lock()
	p.counts[req.Prompt]++
	fail := p.failErr != nil && strings.HasPrefix(req.Prompt, p.failOn)
	p.mu.Unlock()
	if fail {
		return "", p.failErr
	}
	return "Final answer: {\"ok\": true}", nil
}

func (p *promptCountingRunner) calls(prefix string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for prompt, c := range p.counts {
		if strings.HasPrefix(prompt, prefix) {
			n += c
		}
	}
	return n
}

// gateBlockRunner 阻塞指定前缀的调用（停止窗口的确定性构造）。
type gateBlockRunner struct {
	blockPrefix string
	release     chan struct{}
	inner       *promptCountingRunner
}

func (g *gateBlockRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	if strings.HasPrefix(req.Prompt, g.blockPrefix) {
		select {
		case <-g.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return g.inner.Run(ctx, req)
}

// ① 停止落可续态：cancelled run → interrupted（非 failed）+ Error 空 +
// Resumable 点亮（断点在盘）。failed≠interrupted 边界的正面。
func TestTL1447StopLandsResumableInterrupted(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	gate := &gateBlockRunner{blockPrefix: "second", release: make(chan struct{}), inner: inner}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// first 的 checkpoint 落盘后再停止（断点确定存在）。
	waitFor(t, 2*time.Second, func() bool { return runSnapshotOnDiskHas(dir, res.RunID, "first") })
	if !svc.CancelRun(res.RunID) {
		t.Fatal("cancel must hit the live run")
	}
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State != RunRunning
	})
	snaps := svc.Snapshot()
	if snaps[0].State != RunInterrupted {
		t.Fatalf("stopped run must land interrupted (not failed): %+v", snaps[0])
	}
	if snaps[0].Err != "" {
		t.Fatalf("stopped run carries no failure error: %q", snaps[0].Err)
	}
	if !snaps[0].Resumable {
		t.Fatal("stopped run with an on-disk checkpoint must be resumable")
	}
	snap := mustLoadSnap(t, dir, res.RunID)
	if snap.State != string(RunInterrupted) || !snap.Cancelled {
		t.Fatalf("final snapshot must be interrupted + cancelled provenance: state=%s cancelled=%v", snap.State, snap.Cancelled)
	}
	t.Cleanup(func() { close(gate.release) })
}

// ② 停止后续跑：resume_run_id 接通——已完成节点不重跑，断点后节点继续，
// run 沿用原 runID 并完成。
func TestTL1447StopThenResumeFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	gate := &gateBlockRunner{blockPrefix: "second", release: make(chan struct{}), inner: inner}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return runSnapshotOnDiskHas(dir, res.RunID, "first") })
	svc.CancelRun(res.RunID)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunInterrupted
	})
	if inner.calls("first") != 1 {
		t.Fatalf("first node must have run exactly once before the stop: %d", inner.calls("first"))
	}

	// 续跑：面板入口（空 sessionID 走 boot 回退源；测试直接注入 source）。
	close(gate.release)
	res2, err := svc.RunWorkflow(ctx, "resume-chain", res.RunID)
	if err != nil {
		t.Fatalf("resume after stop: %v", err)
	}
	if res2.RunID != res.RunID {
		t.Fatalf("resume keeps the run identity: %s -> %s", res.RunID, res2.RunID)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	if got := inner.calls("first"); got != 1 {
		t.Fatalf("resume must not re-run the finished node: first ran %d times", got)
	}
	if got := inner.calls("second"); got != 1 {
		t.Fatalf("resume must run the remaining node exactly once: second ran %d times", got)
	}
}

// ③ failed≠interrupted 边界（反事实）：真实失败落 failed，resumable 恒
// false，resume 入口拒绝并点名重跑下一步。
func TestTL1447FailedRunNotResumable(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	runner := &fakeRunner{fail: errors.New("node exploded")}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State != RunRunning
	})
	snaps := svc.Snapshot()
	if snaps[0].State != RunFailed {
		t.Fatalf("real failure must land failed: %+v", snaps[0])
	}
	if snaps[0].Resumable {
		t.Fatal("failed run must never be resumable (failed≠interrupted boundary)")
	}
	_, err = svc.RunWorkflow(ctx, "svc-test-wf", res.RunID)
	if err == nil || !strings.Contains(err.Error(), "failed and holds no trustworthy checkpoint") {
		t.Fatalf("resume of a failed run must refuse naming the fresh-rerun next step: %v", err)
	}
}

// ④ 停止 run 跨重启：interrupted+cancelled 快照被启动恢复接受（本件新增的
// restoreFromDisk 分支），恢复后仍可续跑。
func TestTL1447RestoredStoppedRunResumes(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewService(dir)
	ctx := context.Background()
	if _, err := svc1.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	gate := &gateBlockRunner{blockPrefix: "second", release: make(chan struct{}), inner: inner}
	svc1.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc1.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return runSnapshotOnDiskHas(dir, res.RunID, "first") })
	svc1.CancelRun(res.RunID)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc1.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunInterrupted
	})

	// —— 重启 ——
	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	snaps := svc2.Snapshot()
	if len(snaps) != 1 || snaps[0].State != RunInterrupted {
		t.Fatalf("restored stopped run must be interrupted: %+v", snaps)
	}
	if !snaps[0].Resumable {
		t.Fatal("restored stopped run must stay resumable across restart")
	}
	close(gate.release)
	runner2 := &hookRunner{onDone: func() {}}
	svc2.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner2, true })
	if _, err := svc2.RunWorkflow(ctx, "resume-chain", res.RunID); err != nil {
		t.Fatalf("resume after restart: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc2.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
}

// ⑤ 无断点的停止：零节点终态就停（lastCP 空）→ interrupted 但 resumable
// 不点亮，resume 入口拒绝并点名断点缺失——「可续」以断点在盘为准，不虚标。
func TestTL1447StopWithoutCheckpointNotResumable(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	runner := &blockingRunner{release: make(chan struct{})}
	t.Cleanup(func() { runner.once.Do(func() {}) })
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !svc.CancelRun(res.RunID) {
		t.Fatal("cancel must hit the live run")
	}
	close(runner.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State != RunRunning
	})
	snaps := svc.Snapshot()
	if snaps[0].State != RunInterrupted {
		t.Fatalf("stop still lands interrupted without a checkpoint: %+v", snaps[0])
	}
	if snaps[0].Resumable {
		t.Fatal("no checkpoint → no resumable badge (honest gating)")
	}
	_, err = svc.RunWorkflow(ctx, "svc-test-wf", res.RunID)
	if err == nil || !strings.Contains(err.Error(), "checkpoint unavailable") {
		t.Fatalf("resume without a checkpoint must refuse naming the gap: %v", err)
	}
}

// ⑥ 终态分类的直接钉（引擎公共路径上取消 armed 后统一以 context.Canceled
// 收场，并发组合只能在 finishRun 单元面钉）：cancelled 标记不淹没真实失败——
// 只有 context.Canceled 算停止（interrupted），其余错误照落 failed。
func TestTL1447FinishRunClassificationBoundary(t *testing.T) {
	svc := newTestService(t)
	svc.mu.Lock()
	rec := &RunRecord{
		ID:        "wf-classify",
		Workflow:  "wf",
		StartedAt: time.Now(),
		nodes:     map[string]*nodeRecord{},
	}
	svc.runs[rec.ID] = rec
	svc.order = append(svc.order, rec.ID)
	svc.mu.Unlock()

	rec.mu.Lock()
	rec.cancelled = true
	rec.mu.Unlock()
	// cancelled + 真实节点错误（非 context.Canceled）→ failed，错误原文保留。
	svc.finishRun(rec.ID, &WorkflowReport{Conclusion: "salvage"}, errors.New("node exploded during stop window"))
	rec.mu.Lock()
	state, runErr := rec.State, rec.Err
	rec.mu.Unlock()
	if state != RunFailed {
		t.Fatalf("cancelled + real failure must land failed: %+v", state)
	}
	if !strings.Contains(runErr, "node exploded during stop window") {
		t.Fatalf("failure detail must surface: %q", runErr)
	}

	// cancelled + context.Canceled → interrupted（可续分类的判定面）。
	rec2 := &RunRecord{
		ID:        "wf-classify-2",
		Workflow:  "wf",
		StartedAt: time.Now(),
		nodes:     map[string]*nodeRecord{},
	}
	svc.mu.Lock()
	svc.runs[rec2.ID] = rec2
	svc.order = append(svc.order, rec2.ID)
	svc.mu.Unlock()
	rec2.mu.Lock()
	rec2.cancelled = true
	rec2.mu.Unlock()
	svc.finishRun(rec2.ID, &WorkflowReport{Conclusion: "salvage"}, context.Canceled)
	rec2.mu.Lock()
	state2 := rec2.State
	rec2.mu.Unlock()
	if state2 != RunInterrupted {
		t.Fatalf("cancelled + context.Canceled must land interrupted: %+v", state2)
	}
}
