package agent

import "context"

// tl1435(附) —— 工作流派发的只读子代理持久化标记（装机缺陷回填）。
//
// 缺陷机制：confirm 节点走 ReadOnlyTaskTool.Execute，它为了 read_only_task
// 「无持久副作用」的交互契约把 spec.Context.Ephemeral 钉死为 true——
// ContextRequest.parentSession 因此返回空，prepareTranscriptRunWithPrompt
// 落到 EphemeralSubagentRun：无 sidecar meta、无 transcript、无 ref。工作流
// 节点的派发发生在 turn 之外（无会话转录卡、无 turn 域登记），于是 scan/
// triage（task 路径，PrepareFresh 落盘）在右侧栏已结束区可见，而 confirm 的
// 复核子代理两条区都不落——已结束区漏显的本体。
//
// 修复走「派发方声明」：谁在 turn 外派发、谁比 read_only_task 更清楚这次
// 派发必须落盘。工作流 runner 在派发 ctx 上装本标记（与 WithSubagentRefSink
// 同一布线点），ReadOnlyTaskTool.Execute 见标记即不再强制 Ephemeral；标记
// 会随 ctx 流入子代理自己的工具调用，即 confirm 子代理再派生的只读孙代理
// 也持久化——与 scan/triage 子代理同水位，属预期语义。交互式 read_only_task
// 不装标记，契约逐字节不变（spawn_boundary 的 ephemeral 契约测试原样红不了）。

// subagentPersistRequestKey is the ctx key. Unexported: the only door in is
// WithSubagentTranscriptPersistence.
type subagentPersistRequestKey struct{}

// WithSubagentTranscriptPersistence marks this dispatch as one that must
// persist its sub-agent transcript. Install it on the context handed to
// TaskTool.Execute / ReadOnlyTaskTool.Execute when the dispatch happens
// outside any turn (workflow node execution): there is no session-transcript
// card to carry the child's presence, so the persisted sidecar directory is
// the only source the right-dock panel can see it through.
func WithSubagentTranscriptPersistence(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentPersistRequestKey{}, true)
}

// SubagentTranscriptPersistenceRequested reports whether the dispatching
// caller asked for persistence. False keeps every existing entry point on
// their pre-marker behavior (interactive read_only_task stays ephemeral).
func SubagentTranscriptPersistenceRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(subagentPersistRequestKey{}).(bool)
	return requested
}
