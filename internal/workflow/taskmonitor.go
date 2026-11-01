package workflow

import (
	"context"
	"sync"

	"reasonix/internal/taskmonitor"
)

// 任务面板生命周期上报（tl1324 G6）：workflow 子代理节点此前只经
// TaskTool.Execute 前台同步执行，不进 jobs.Manager，taskmonitor 只认
// jobs.Manager 上报的生命周期 ⇒ 任务/子代理面板看不到工作流子代理。
//
// 装配形态：包级 setter（builtin.WorkflowService 的 heartbeat
// SetHeartbeatManager 同款 seam）——controller 在 initializeTaskRecorder 里把
// 它已构造的同一个 *taskmonitor.TaskRecorder 注入进来。★必须复用同一实例，
// 禁止 workflow 侧 NewTaskRecorder 第二个：双实例 runtimeOwnerID 互斥守卫会
// 互拒对方条目（recorder.go RecordDone 的 owner 检查），heartbeat lease 也是
// per-instance——第二实例的 RecordStart 条目第一实例永远停不掉心跳。
//
// 依赖方向：workflow → taskmonitor → jobs（workflow 已引 jobs，无环）。

var (
	taskMonitorMu       sync.Mutex
	taskMonitorRecorder *taskmonitor.TaskRecorder
)

// SetTaskMonitorRecorder 注入任务面板生命周期上报器；nil 关闭上报。进程级
// 生效（工作流服务是惰性构造的单例，setter 不依赖构造时序——装配点在任何
// 时刻调用都作用于后续的每一次节点执行）。
func SetTaskMonitorRecorder(r *taskmonitor.TaskRecorder) {
	taskMonitorMu.Lock()
	taskMonitorRecorder = r
	taskMonitorMu.Unlock()
}

// currentTaskMonitorRecorder 返回当前上报器（无装配 = nil，节点执行照旧）。
// 调用方在一次节点执行内持有返回值：RecordStart 与 RecordDone 必须落在同一
// 实例上（monitor id → monitorTaskID 的内存映射 per-instance），否则延迟的
// RecordDone 查不到登记、心跳停不掉。
func currentTaskMonitorRecorder() *taskmonitor.TaskRecorder {
	taskMonitorMu.Lock()
	defer taskMonitorMu.Unlock()
	return taskMonitorRecorder
}

// runID 随 ctx 下行：登记 id（wf:<runId>:<nodeID>）由 runner 组装，引擎在
// RunWithID 解析出最终 runID（外部钉定或自动生成）、ResumeWithID 灌回快照
// runID 后注入——任一入口漏注入，该入口的登记就落 wf:norun: 兜底。
type runIDContextKey struct{}

func withRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, runID)
}

func runIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(runIDContextKey{}).(string)
	return id
}
