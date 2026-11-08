package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// tl1335 主动暂停（pause/resume 语义补齐）服务面状态机钉。语义边界：
//
//	pause（PauseRun） → paused（可续态，「我还会回来」的显式挂起；在飞节点
//	                    跑完才生效，工作不丢）
//	stop （CancelRun） → interrupted（可续态，立即砍；tl1447 语义不变）
//	resume             → paused/interrupted 都放行（resumableState），从
//	                     checkpoint 断点继续，已完成节点不重跑
//	failed/completed   → 不可续；真实失败不被 pause 掩盖
//
// 每个测试对应一条边界；引擎半边（节点边界/波次保留）钉在
// tl1335_engine_pause_test.go。

// stagedRunner 是 pause 生效窗口的确定性构造：节点进入执行即发信号，等
// release 放行后按 fail 决定报真实错误还是正常回答。
type stagedRunner struct {
	entered chan struct{}
	release chan struct{}
	fail    error
	once    sync.Once
}

func (p *stagedRunner) Run(_ context.Context, _ SubagentRequest) (string, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	if p.fail != nil {
		return "", p.fail
	}
	return "Final answer: {\"ok\": true}", nil
}

// pauseGateRunner 让「PauseRun 落在节点在飞时」变成确定性时序：首个调用进
// 入执行即发信号并阻塞，放行后转发 inner 计数。没有它，PauseRun 可能抢在
// 首节点派发前生效（零执行挂起路径，钉①会退化成另一个语义）。
type pauseGateRunner struct {
	entered chan struct{}
	release chan struct{}
	inner   *promptCountingRunner
	once    sync.Once
}

func (p *pauseGateRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return p.inner.Run(ctx, req)
}

// ① pause 后状态保留：在飞节点跑完 → paused（非 interrupted/failed）、Err 空、
// Resumable 点亮、盘上快照 state=paused 且无 cancelled 来源标记、未启动的
// 节点不被派发。
func TestTL1335PauseLandsResumablePaused(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	// 阻塞 first 本身：PauseRun 在其在飞时确定性设下，release 后 first 完成
	// 并在下一节点边界挂起（second 不派发）——温和挂起的确定性构造。
	gate := &pauseGateRunner{entered: make(chan struct{}), release: make(chan struct{}), inner: inner}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first node never entered execution")
	}
	if !svc.PauseRun(res.RunID) {
		t.Fatal("pause must hit the live run")
	}
	close(gate.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunPaused
	})
	snaps := svc.Snapshot()
	if snaps[0].State != RunPaused {
		t.Fatalf("paused run must land paused: %+v", snaps[0])
	}
	if snaps[0].Err != "" {
		t.Fatalf("paused run carries no failure error: %q", snaps[0].Err)
	}
	if !snaps[0].Resumable {
		t.Fatal("paused run with an on-disk checkpoint must be resumable")
	}
	if inner.calls("second") != 0 {
		t.Fatalf("pause must not dispatch unstarted nodes: second ran %d times", inner.calls("second"))
	}
	snap := mustLoadSnap(t, dir, res.RunID)
	if snap.State != string(RunPaused) || snap.Cancelled {
		t.Fatalf("snapshot must be paused without cancelled provenance: state=%s cancelled=%v", snap.State, snap.Cancelled)
	}
}

// ② resume 后从挂起点继续：paused run 经 resume_run_id 续跑——runID 沿用、
// 已完成节点不重跑、断点后节点继续、run 完成。
func TestTL1335ResumePausedFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	gate := &pauseGateRunner{entered: make(chan struct{}), release: make(chan struct{}), inner: inner}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first node never entered execution")
	}
	if !svc.PauseRun(res.RunID) {
		t.Fatal("pause must hit the live run")
	}
	close(gate.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunPaused
	})
	if inner.calls("first") != 1 || inner.calls("second") != 0 {
		t.Fatalf("pause point must be exactly after first: first=%d second=%d", inner.calls("first"), inner.calls("second"))
	}

	// 续跑：面板入口（空 sessionID 走 boot 回退源；测试直接注入 source）。
	res2, err := svc.RunWorkflow(ctx, "resume-chain", res.RunID)
	if err != nil {
		t.Fatalf("resume paused run: %v", err)
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

// ③ cancel 语义不变（对照钉，tl1447 边界不翻案）：pause 请求已设的 running
// run 改点停止 → cancel 优先，落 interrupted（不是 paused），cancelled 来源
// 标记在盘——pause 不掩盖 stop。
func TestTL1335CancelBeatsPendingPause(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	gate := &pauseGateRunner{entered: make(chan struct{}), release: make(chan struct{}), inner: inner}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first node never entered execution")
	}
	if !svc.PauseRun(res.RunID) {
		t.Fatal("pause must hit the live run")
	}
	// 用户改主意立即停：cancel 砍掉在飞执行，优先于待生效的 pause 请求。
	if !svc.CancelRun(res.RunID) {
		t.Fatal("cancel must hit the live run")
	}
	close(gate.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State != RunRunning
	})
	snaps := svc.Snapshot()
	if snaps[0].State != RunInterrupted {
		t.Fatalf("cancel must land interrupted even with a pending pause request: %+v", snaps[0])
	}
	snap := mustLoadSnap(t, dir, res.RunID)
	if snap.State != string(RunInterrupted) || !snap.Cancelled {
		t.Fatalf("cancelled run keeps tl1447 semantics: state=%s cancelled=%v", snap.State, snap.Cancelled)
	}
}

// ④ paused run 跨重启：快照被启动恢复接受且保持 paused（不是 interrupted——
// 「我暂停的」身份不因重启丢失），恢复后仍可续跑。
func TestTL1335RestoredPausedRunResumes(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewService(dir)
	ctx := context.Background()
	if _, err := svc1.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	inner := newPromptCountingRunner()
	gate := &pauseGateRunner{entered: make(chan struct{}), release: make(chan struct{}), inner: inner}
	svc1.AddRunnerSource(func(string) (SubagentRunner, bool) { return gate, true })
	res, err := svc1.RunWorkflow(ctx, "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first node never entered execution")
	}
	if !svc1.PauseRun(res.RunID) {
		t.Fatal("pause must hit the live run")
	}
	close(gate.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc1.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunPaused
	})

	// —— 重启 ——
	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	snaps := svc2.Snapshot()
	if len(snaps) != 1 || snaps[0].State != RunPaused {
		t.Fatalf("restored paused run must stay paused: %+v", snaps)
	}
	if !snaps[0].Resumable {
		t.Fatal("restored paused run must stay resumable across restart")
	}
	runner2 := &hookRunner{onDone: func() {}}
	svc2.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner2, true })
	if _, err := svc2.RunWorkflow(ctx, "resume-chain", res.RunID); err != nil {
		t.Fatalf("resume paused run after restart: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc2.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
}

// ⑤ PauseRun 拒绝面：未知 run、非 running 态（已终态）一律 false——暂停只
// 对活着且未挂起的 run 有意义。
func TestTL1335PauseRunRejections(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	if svc.PauseRun("wf-unknown") {
		t.Fatal("pause of an unknown run must miss")
	}
	runner := &fakeRunner{}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State != RunRunning
	})
	if svc.PauseRun(res.RunID) {
		t.Fatal("pause of a finished run must miss")
	}
}

// ⑥ 真实失败不被 pause 掩盖：挂起请求落执行中途、在飞节点报真实错误 →
// failed（不是 paused）——pause 只在节点边界收场，失败语义（tl1447：断点
// 不可信）不受影响。
func TestTL1335RealFailureNotMaskedByPause(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	stage := &stagedRunner{entered: make(chan struct{}), release: make(chan struct{}), fail: errors.New("node exploded mid-pause")}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return stage, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-stage.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("node never entered execution")
	}
	if !svc.PauseRun(res.RunID) {
		t.Fatal("pause must hit the live run")
	}
	close(stage.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State != RunRunning
	})
	snaps := svc.Snapshot()
	if snaps[0].State != RunFailed {
		t.Fatalf("a real node failure must land failed even with a pending pause: %+v", snaps[0])
	}
	if !strings.Contains(snaps[0].Err, "exploded mid-pause") {
		t.Fatalf("failure detail must surface: %q", snaps[0].Err)
	}
}

// ⑦ 持久化关闭的 pause：落 paused 但 resumable 不点亮（checkpoint 不可能在
// 盘，诚实门控与 tl1447 钉⑤同口），resume 入口拒绝并点名断点缺失。
func TestTL1335PauseWithoutPersistenceNotResumable(t *testing.T) {
	svc := NewService("") // 内存目录：persistDir 为空
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	stage := &stagedRunner{entered: make(chan struct{}), release: make(chan struct{})}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return stage, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case <-stage.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("node never entered execution")
	}
	if !svc.PauseRun(res.RunID) {
		t.Fatal("pause must hit the live run")
	}
	close(stage.release)
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunPaused
	})
	snaps := svc.Snapshot()
	if snaps[0].Resumable {
		t.Fatal("paused run without persistence must not be resumable (honest gating)")
	}
	if _, err := svc.RunWorkflow(ctx, "svc-test-wf", res.RunID); err == nil || !strings.Contains(err.Error(), "checkpoint unavailable") {
		t.Fatalf("resume without a checkpoint must refuse naming the gap: %v", err)
	}
}
