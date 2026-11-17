package main

// tl1658 现场①：批量清理必须有可见进度——逐项进度事件的后端钉。
//
// 「清理全组」在 181 项/3.4GB 的量级下是一条分钟级的阻塞桥调用（tl1603 起
// 就如此），此前全程零反馈=用户无从知道是否在跑。修复与 dag-migration 同
// 一形态：桥内逐项 emit pre-repair-cleanup:progress（每项 processing 起、
// trashed/staged/refused 落定各一条），前端订阅渲染「已处理 n/N」。本文件
// 钉事件序列与载荷形状（跨平台：进度机制与平台无关）；长路径专用分支的钉
// 在 pre_repair_cleanup_longpath_windows_test.go（MAX_PATH 是 Windows Shell
// 的封顶，别处不存在该分支）。

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// TestPreRepairBatchCleanupEmitsProgressPerItem——批内每项 emit「processing
// 起 + 落定态」两条事件：index 从 1 起、total 为批大小、label 用展示名、
// status 与该项结局一致（trashed/refused）。ctx 缺失时零事件（离屏/纯测试
// 形态不炸，emitDagMigrationProgress 同一纪律）。
func TestPreRepairBatchCleanupEmitsProgressPerItem(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	writeLiveOwner(t, sessions, "s1")
	b1 := writePreRepairTree(t, sessions, "s1", "20261108T010000", 100)
	writeLiveOwner(t, sessions, "sick")
	b2 := writePreRepairTree(t, sessions, "sick", "20261109T010000", 200)
	withPreRepairTrashSpy(t, nil)
	previousInspect := preRepairHealthInspect
	preRepairHealthInspect = func(path string, _ agent.HealthInspectOptions) agent.SessionHealthReport {
		// 只有 sick 的宿主不健康（第 2 项被拒）；s1 健康（第 1 项清走）——
		// 一 healthy 一 refused 才能钉满 processing→trashed 与 processing→
		// refused 两种事件序列。
		return agent.SessionHealthReport{Path: path, Healthy: !strings.Contains(path, "sick")}
	}
	t.Cleanup(func() { preRepairHealthInspect = previousInspect })

	type progressEvent struct {
		index, total int
		label        string
		status       string
	}
	var got []progressEvent
	previousEmit := runtimeEventsEmitFallback
	runtimeEventsEmitFallback = func(_ context.Context, name string, payload ...any) {
		if name != preRepairCleanupProgressEvent {
			return
		}
		if len(payload) != 1 {
			t.Fatalf("progress payload = %v, want exactly one map", payload)
		}
		m, ok := payload[0].(map[string]any)
		if !ok {
			t.Fatalf("progress payload = %T, want map[string]any", payload[0])
		}
		got = append(got, progressEvent{
			index:  m["index"].(int),
			total:  m["total"].(int),
			label:  m["label"].(string),
			status: m["status"].(string),
		})
	}
	t.Cleanup(func() { runtimeEventsEmitFallback = previousEmit })

	app := &App{ctx: context.Background()}
	run, err := app.DeletePreRepairBackups([]string{b1, b2})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if run.Trashed != 1 || run.Failed != 1 {
		t.Fatalf("run = trashed %d / failed %d, want 1/1", run.Trashed, run.Failed)
	}
	if len(got) != 4 {
		t.Fatalf("progress events = %d (%+v), want 4 (processing+final per item)", len(got), got)
	}
	// 序列：第 1 项（trashed）processing→trashed，第 2 项（被健康闸拒）
	// processing→refused；index 连续、total 恒等于批大小。
	want := []progressEvent{
		{index: 1, total: 2, label: "备份标题20261108T010000", status: "processing"},
		{index: 1, total: 2, label: "备份标题20261108T010000", status: "trashed"},
		{index: 2, total: 2, label: "备份标题20261109T010000", status: "processing"},
		{index: 2, total: 2, label: "备份标题20261109T010000", status: "refused"},
	}
	for i, w := range want {
		g := got[i]
		if g.index != w.index || g.total != w.total || g.status != w.status {
			t.Fatalf("event[%d] = %+v, want %+v", i, g, w)
		}
		if !strings.Contains(g.label, w.label) {
			t.Fatalf("event[%d].label = %q, want it to carry the display label %q", i, g.label, w.label)
		}
	}
}

// TestPreRepairBatchCleanupProgressWithoutCtxIsSilent——离屏/测试形态（ctx 为
// nil）零事件零 panic：进度是增强不是依赖，桥在无宿主环境照常工作。
func TestPreRepairBatchCleanupProgressWithoutCtxIsSilent(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup := writePreRepairTree(t, sessions, "lonely", "20261108T010000", 100)
	withPreRepairTrashSpy(t, nil)

	fired := false
	previousEmit := runtimeEventsEmitFallback
	runtimeEventsEmitFallback = func(_ context.Context, _ string, _ ...any) {
		fired = true
	}
	t.Cleanup(func() { runtimeEventsEmitFallback = previousEmit })

	app := &App{} // ctx 未设（nil）
	if _, err := app.DeletePreRepairBackups([]string{backup}); err != nil {
		t.Fatalf("batch without ctx: %v", err)
	}
	if fired {
		t.Fatal("no progress event may fire with a nil ctx (emit guard)")
	}
}
