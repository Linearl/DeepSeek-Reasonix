package agent

// 任务 667（任务与子代理状态查询接口）：get_session_status 的可选参数
// include_tasks_and_subagents 依赖的唯一宿主回调与其行类型。
//
// 数据源纪律：这里的行与桌面「运行中面板」（任务 440，后台任务行）和
// 「胶囊计数」（任务 557，前台运行中子代理行）读的是同一运行时状态层——
// 宿主实现只是把两处 UI 已经在消费的行暴露成工具接口，不新造状态。
// 元数据纪律：行里只有标签/ref/时间戳/状态，没有提示词、推理与工具输出。

// TaskRuntimeRow is one background job row for the task 667 work-detail view.
// It mirrors what the desktop running panel (task 440) already renders from
// the session's job manager: label/kind/status/start time. Kind is "bash" or
// "task"; a "task" job owns its background sub-agent, which is represented by
// THIS row (never double-counted as a subagent row — the task 557 dedupe).
type TaskRuntimeRow struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"` // unix milliseconds
	// Tps is the sampled streaming rate carried by the job row (fork popover
	// heartbeat). Zero means "no sample yet" and is omitted from the tool view.
	Tps int `json:"tps,omitempty"`
}

// SubagentRuntimeRow is one RUNNING foreground sub-agent row for the task 667
// work-detail view — the same rows the task 557 capsule badge counts. Terminal
// children have left the runtime registry by the time they are observed here,
// so the row set's length IS the live active count (the zcode misjudgement
// this task fixes: count rows, don't guess from partially-returned results).
type SubagentRuntimeRow struct {
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Status    string `json:"status"`    // "running" for every row in this view
	StartedAt int64  `json:"startedAt"` // unix milliseconds
	// ParentToolCallID is the batch key: children dispatched by one parallel
	// call share it, so callers can group rows into dispatch batches.
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
}

// SessionWorkDetail is one session's runtime work detail: background job rows
// plus running-foreground-subagent rows. Empty slices mean "runtime visible,
// nothing running" — distinct from known=false, which means this process
// cannot see the runtime at all (never a guessed empty set).
type SessionWorkDetail struct {
	Tasks     []TaskRuntimeRow
	Subagents []SubagentRuntimeRow
}

// SessionWorkDetailFunc is the host probe behind get_session_status's
// include_tasks_and_subagents flag (task 667). known=false reports a runtime
// this process cannot see, mirroring the SessionStatus probe's rule.
type SessionWorkDetailFunc func(contactID string) (detail SessionWorkDetail, known bool)

// sessionWorkDetailSection renders one record's workDetail object. The shape
// is self-describing on purpose: the note states what each row set is and how
// to use the active count, so a model reading it once cannot misread it as a
// history of finished children.
func sessionWorkDetailSection(fn SessionWorkDetailFunc, contactID string, nowMS int64) map[string]any {
	if fn == nil {
		return map[string]any{
			"known": false,
			"hint":  "this host did not wire a runtime work-detail probe — task/subagent rows are not available from this process",
		}
	}
	detail, known := fn(contactID)
	if !known {
		return map[string]any{
			"known": false,
			"hint":  "this process cannot see the session's runtime (the same rule as state=unknown) — rows are omitted rather than a guessed empty set",
		}
	}
	tasks := make([]map[string]any, 0, len(detail.Tasks))
	for _, j := range detail.Tasks {
		row := map[string]any{
			"id":         j.ID,
			"kind":       j.Kind,
			"label":      j.Label,
			"status":     j.Status,
			"startedAt":  j.StartedAt,
			"durationMs": durationMS(j.StartedAt, nowMS),
		}
		if j.Tps > 0 {
			row["tps"] = j.Tps
		}
		tasks = append(tasks, row)
	}
	subs := make([]map[string]any, 0, len(detail.Subagents))
	for _, s := range detail.Subagents {
		row := map[string]any{
			"ref":        s.Ref,
			"name":       s.Name,
			"status":     s.Status,
			"startedAt":  s.StartedAt,
			"durationMs": durationMS(s.StartedAt, nowMS),
		}
		if s.ParentToolCallID != "" {
			row["batch"] = s.ParentToolCallID
		}
		subs = append(subs, row)
	}
	return map[string]any{
		"known":     true,
		"tasks":     tasks,
		"subagents": subs,
		"note":      "tasks = this session's background job rows (the desktop running panel's source); subagents = this session's RUNNING foreground sub-agents (the capsule badge's source). Terminal children have left this runtime view, so len(subagents) is the live active count — compare it against your dispatch expectation instead of inferring it from partially-returned results.",
	}
}

// durationMS clamps a start-timestamp-based duration to >= 0: a clock skew or
// a missing timestamp renders as 0, never as a negative duration.
func durationMS(startedAt, nowMS int64) int64 {
	if startedAt <= 0 || nowMS <= startedAt {
		return 0
	}
	return nowMS - startedAt
}
