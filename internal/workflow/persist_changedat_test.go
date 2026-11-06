package workflow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// 任务 1336 —— 恢复快照 nodes.changedAt 回填。缺陷现场：宿主重启后
// workflow_status 返回的恢复 run，nodes[].changedAt 全是 Go time 零值的毫秒数
// （-62135596800000）——快照不落节点变迁时刻，恢复装载也无从回填。
// 钉三面：checkpoint 落盘→恢复（interrupted）、终态快照落盘→恢复（completed）、
// 无时刻可回填（tl1336 之前的旧快照 / 无事件节点）时视图省略而非外露零值。

// goZeroTimeUnixMilli 是缺陷读数：time.Time{}.UnixMilli()。
const goZeroTimeUnixMilli int64 = -62135596800000

// ctxGateRunner 放行非 blockPrefix 的调用；blockPrefix 的调用挂到 ctx 取消
// ——断点现场确定性落在其前，测试收尾 CancelRun 即回收，不遗留阻塞 goroutine。
type ctxGateRunner struct{ blockPrefix string }

func (r *ctxGateRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	if strings.HasPrefix(req.Prompt, r.blockPrefix) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "Final answer: {\"ok\": true}", nil
}

// nodeChangedAtFromSnapshot 取面板快照里某 run 某节点的 ChangedAt。
func nodeChangedAtFromSnapshot(t *testing.T, snaps []RunSnapshot, runID, nodeID string) time.Time {
	t.Helper()
	for _, s := range snaps {
		if s.ID != runID {
			continue
		}
		for _, n := range s.Nodes {
			if n.ID == nodeID {
				return n.ChangedAt
			}
		}
	}
	t.Fatalf("node %s of run %s not in snapshot: %+v", nodeID, runID, snaps)
	return time.Time{}
}

// nodeChangedAtFromStatus 取 workflow_status 视图里某节点的 changedAt（unix ms）。
func nodeChangedAtFromStatus(t *testing.T, svc *Service, runID, nodeID string) int64 {
	t.Helper()
	st, err := svc.WorkflowStatus(context.Background(), runID)
	if err != nil || len(st.Runs) != 1 {
		t.Fatalf("status %s: %v %+v", runID, err, st)
	}
	for _, n := range st.Runs[0].Nodes {
		if n.NodeID == nodeID {
			return n.ChangedAt
		}
	}
	t.Fatalf("node %s not in status view: %+v", nodeID, st.Runs[0].Nodes)
	return 0
}

// assertRestoredChangedAt 是验收断言：恢复后非零值且等于落盘时值（面板快照
// 精确到纳秒相等；workflow_status 视图等于落盘值的毫秒数，且不是零值读数）。
func assertRestoredChangedAt(t *testing.T, svc *Service, runID, nodeID string, persisted time.Time) {
	t.Helper()
	restored := nodeChangedAtFromSnapshot(t, svc.Snapshot(), runID, nodeID)
	if restored.IsZero() || !restored.Equal(persisted) {
		t.Fatalf("node %s: restored changedAt=%v, want the persisted %v", nodeID, restored, persisted)
	}
	ms := nodeChangedAtFromStatus(t, svc, runID, nodeID)
	if ms <= 0 || ms == goZeroTimeUnixMilli || ms != persisted.UnixMilli() {
		t.Fatalf("node %s: workflow_status changedAt=%d, want %d (persisted)", nodeID, ms, persisted.UnixMilli())
	}
}

// 恢复面①：节点终态 checkpoint 落盘 → 进程死亡 → 重启恢复 interrupted run，
// 已完成节点的 changedAt 等于落盘时值（续跑之前即正确，不靠续跑重放覆盖）。
func TestServiceRestoreBackfillsCheckpointChangedAt(t *testing.T) {
	if zero := (time.Time{}).UnixMilli(); zero != goZeroTimeUnixMilli {
		t.Fatalf("defect reading drifted: zero time = %d", zero)
	}
	dir := t.TempDir()
	svc1 := NewService(dir)
	ctx := context.Background()
	if _, err := svc1.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	svc1.AddRunnerSource(func(string) (SubagentRunner, bool) { return &ctxGateRunner{blockPrefix: "second"}, true })
	res, err := svc1.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-1336"), "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer func() {
		// 收尾：取消挂在 second 上的原 run 并等它落终态（落盘写完再交还 TempDir 清理）。
		svc1.CancelRun(res.RunID)
		waitFor(t, 5*time.Second, func() bool {
			st, err := svc1.WorkflowStatus(ctx, res.RunID)
			// tl1447：停止落 interrupted（可续态）而非 failed。
			return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunInterrupted)
		})
	}()
	waitFor(t, 5*time.Second, func() bool { return runSnapshotOnDiskHas(dir, res.RunID, "first") })

	// 落盘时值：盘上 first.changedAt 非零，且与原进程登记行同源同刻。
	persisted := mustLoadSnap(t, dir, res.RunID).Nodes["first"].ChangedAt
	if persisted.IsZero() {
		t.Fatalf("checkpoint must persist the node's changedAt")
	}
	if live := nodeChangedAtFromSnapshot(t, svc1.Snapshot(), res.RunID, "first"); !live.Equal(persisted) {
		t.Fatalf("persisted changedAt %v must equal the live record %v", persisted, live)
	}

	// —— 重启 ——
	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	snaps := svc2.Snapshot()
	if len(snaps) != 1 || snaps[0].State != RunInterrupted {
		t.Fatalf("restored run must be interrupted: %+v", snaps)
	}
	assertRestoredChangedAt(t, svc2, res.RunID, "first", persisted)
}

// 恢复面②：run 跑完终态快照落盘 → 重启恢复 completed run，全部节点的
// changedAt 等于落盘时值（finishRun 合并路径）。
func TestServiceRestoreBackfillsFinalSnapshotChangedAt(t *testing.T) {
	dir := t.TempDir()
	svc1 := NewService(dir)
	ctx := context.Background()
	if _, err := svc1.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	svc1.AddRunnerSource(func(string) (SubagentRunner, bool) { return &hookRunner{onDone: func() {}}, true })
	res, err := svc1.RunWorkflow(withCallSink(ctx, &captureSink{}, "branch-1336"), "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		st, err := svc1.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	disk := mustLoadSnap(t, dir, res.RunID)
	if disk.State != string(RunCompleted) {
		t.Fatalf("final snapshot must be completed: %s", disk.State)
	}
	persisted := map[string]time.Time{}
	for _, id := range []string{"first", "second"} {
		ns, ok := disk.Nodes[id]
		if !ok || ns.ChangedAt.IsZero() {
			t.Fatalf("final snapshot must persist %s changedAt: %+v", id, ns)
		}
		if live := nodeChangedAtFromSnapshot(t, svc1.Snapshot(), res.RunID, id); !live.Equal(ns.ChangedAt) {
			t.Fatalf("%s: persisted changedAt %v must equal the live record %v", id, ns.ChangedAt, live)
		}
		persisted[id] = ns.ChangedAt
	}

	// —— 重启 ——
	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	snaps := svc2.Snapshot()
	if len(snaps) != 1 || snaps[0].State != RunCompleted {
		t.Fatalf("restored run must stay completed: %+v", snaps)
	}
	for id, at := range persisted {
		assertRestoredChangedAt(t, svc2, res.RunID, id, at)
	}
}

// 恢复面③：无时刻可回填（tl1336 之前的旧快照不带 changedAt；续跑里被跳过的
// 节点无事件）——零值不落盘，视图按「未知」省略 changedAt，绝不外露零值读数。
func TestServiceRestoreWithoutChangedAtOmitsZeroReading(t *testing.T) {
	dir := t.TempDir()
	wf := mustParseWf(t, resumeChainDoc)
	if err := saveRunSnapshot(runsDir(dir), &runSnapshot{
		SchemaVersion:  snapshotSchemaVersion,
		RunID:          "wf-legacy",
		Workflow:       wf.Name,
		DefFingerprint: defFingerprint(wf),
		State:          string(RunRunning),
		StartedAt:      time.Now(),
		Nodes: map[string]nodeSnapshot{
			"first": {NodeType: NodeSubagent, LastEventState: "completed", Status: "done", Terminal: "passed"},
		},
		NodeOrder: []string{"first"},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	path, err := runSnapshotPath(runsDir(dir), "wf-legacy")
	if err != nil {
		t.Fatalf("snapshot path: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if strings.Contains(string(raw), "changedAt") {
		t.Fatalf("zero changedAt must not be persisted (same on-disk shape as pre-1336 snapshots): %s", raw)
	}

	svc := NewService(dir)
	svc.restoreFromDisk()
	if at := nodeChangedAtFromSnapshot(t, svc.Snapshot(), "wf-legacy", "first"); !at.IsZero() {
		t.Fatalf("no persisted changedAt means unknown, not fabricated: %v", at)
	}
	if ms := nodeChangedAtFromStatus(t, svc, "wf-legacy", "first"); ms != 0 {
		t.Fatalf("unknown changedAt must be omitted from workflow_status, got %d", ms)
	}
	st, err := svc.WorkflowStatus(context.Background(), "wf-legacy")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	wire, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("encode status: %v", err)
	}
	if strings.Contains(string(wire), "-62135596800000") || strings.Contains(string(wire), `"changedAt"`) {
		t.Fatalf("workflow_status must not leak the Go zero-time reading: %s", wire)
	}
}
