package agent

import (
	"context"
	"sync"
)

// tl1434 —— 工作流节点详情「工作过程」只读回放的登记面（上游携带面）。
//
// 工作流引擎派发子代理时只拿回 final answer 字符串；子代理 transcript ref
// 在 RunProfileSpec 内部生成后只进进度事件流（任务440），引擎侧拿不到，
// nodeId→执行实例的关联因此无处落地（tl1434 探查节）。
//
// 本文件把 ref 沿既有「向上携带」先例（subagent_raw_answer.go：ctx 值只能
// 向下流，向上要靠调用方安装的可变载体）补一条通道：调用方（工作流的
// TaskSubagentRunner.runNode）在派发 ctx 上装 sink，RunProfileSpec 在 ref
// 生成处登记，调用方在 Execute 返回后读回。Execute 的 (string, error)
// 契约不动；不装 sink 的普通委派路径零成本（值为空即 no-op）。

// subagentRefKey is the ctx key for the ref sink. Unexported: the only door
// in is WithSubagentRefSink.
type subagentRefKey struct{}

// subagentRefSink is the mutable carrier. Writes and reads happen on the
// same dispatch goroutine today; the mutex keeps that an implementation
// detail rather than a correctness assumption.
type subagentRefSink struct {
	mu  sync.Mutex
	ref string
}

// WithSubagentRefSink returns ctx carrying an empty ref sink. Install it on
// the context handed to TaskTool.Execute / ReadOnlyTaskTool.Execute when the
// caller needs to learn which persisted sub-agent transcript the dispatch
// produced (workflow node trace, tl1434).
func WithSubagentRefSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentRefKey{}, &subagentRefSink{})
}

// RecordSubagentRef stores the persisted transcript ref minted for this run.
// A no-op when ctx carries no sink (ordinary delegation: nobody is listening)
// or when ref is empty (ephemeral runs never persist a transcript — there is
// nothing to replay, see EphemeralSubagentRun).
func RecordSubagentRef(ctx context.Context, ref string) {
	sink, ok := ctx.Value(subagentRefKey{}).(*subagentRefSink)
	if !ok || ref == "" {
		return
	}
	sink.mu.Lock()
	sink.ref = ref
	sink.mu.Unlock()
}

// SubagentRef returns the persisted transcript ref recorded during the run,
// or "" when no sink is present or the host recorded nothing (ephemeral runs
// and legacy paths must degrade, not fabricate an identity).
func SubagentRef(ctx context.Context) string {
	sink, ok := ctx.Value(subagentRefKey{}).(*subagentRefSink)
	if !ok {
		return ""
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.ref
}
