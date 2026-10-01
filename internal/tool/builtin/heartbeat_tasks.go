package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"reasonix/internal/tool"
)

// Heartbeat management tools (task 201): expose the desktop heartbeat
// scheduler's admin surface (list / upsert / enable-disable) to the agent so
// scheduled tasks can be created and maintained through the engine itself
// instead of hand-editing heartbeat-tasks.json.
//
// Architecture: the engine lives in the desktop module (package main), which
// imports this module — never the reverse. The tools therefore talk to a
// HeartbeatManager seam injected by the desktop at startup. Until a desktop
// process injects one (CLI runs, tests without a desktop), Execute fails with
// a dedicated message instead of guessing at the config file. This keeps the
// field contract single-sourced: the engine's own parsers validate every edit,
// and writes go through the engine's CAS save path so every persisted change
// bumps revision and reaches the running scheduler without a restart.

// HeartbeatTaskView is the read model one task is reported with. Engine-owned
// state (topicID, lastRunAt, createdAt, runCount) is included read-only; the
// next-run projection is computed by the engine's own schedule semantics.
type HeartbeatTaskView struct {
	ID                     string `json:"id"`
	Title                  string `json:"title"`
	Prompt                 string `json:"prompt"`
	Interval               string `json:"interval"`
	Enabled                bool   `json:"enabled"`
	Scope                  string `json:"scope,omitempty"`
	WorkspaceRoot          string `json:"workspaceRoot,omitempty"`
	ApprovalMode           string `json:"approvalMode,omitempty"`
	TimeWindowStart        string `json:"timeWindowStart,omitempty"`
	TimeWindowEnd          string `json:"timeWindowEnd,omitempty"`
	NotifyChannels         *bool  `json:"notifyChannels,omitempty"`
	NewConversationEachRun bool   `json:"newConversationEachRun,omitempty"`
	ReuseSession           bool   `json:"reuseSession,omitempty"`
	Provider               string `json:"provider,omitempty"`
	Model                  string `json:"model,omitempty"`
	GoalMode               bool   `json:"goalMode,omitempty"`
	GoalText               string `json:"goalText,omitempty"`
	// MaxRuns caps the number of runs (task 327); 0 = unlimited (default),
	// 1 = single run. Writable through heartbeat_task_upsert.
	MaxRuns int `json:"maxRuns,omitempty"`
	// Engine-owned read-only state.
	TopicID   string `json:"topicId,omitempty"`
	LastRunAt int64  `json:"lastRunAt,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	RunCount  int    `json:"runCount,omitempty"`
	// RunsUsed is how much of MaxRuns has been charged (task 327), read-only:
	// k of the panel's k/N badge. Reset only by an explicit re-enable.
	RunsUsed  int   `json:"runsUsed,omitempty"`
	NextRunAt int64 `json:"nextRunAt"` // unix millis; 0 = never (see NextRunHint)
	// NextRunHint: "", "disabled", "budget-exhausted", "invalid-interval",
	// "due-now", "overdue".
	NextRunHint string `json:"nextRunHint,omitempty"`
}

// HeartbeatListView is the list result plus the CAS token every mutating call
// must echo back (expected_revision).
type HeartbeatListView struct {
	Revision uint64              `json:"revision"`
	ETag     string              `json:"etag,omitempty"`
	Tasks    []HeartbeatTaskView `json:"tasks"`
}

// HeartbeatTaskPatch is an upsert request. Provided names which fields the
// caller actually sent, so the engine applies merge semantics (absent fields
// keep their current value; a present empty string clears a string field)
// instead of fragile full-row replacement.
type HeartbeatTaskPatch struct {
	Provided map[string]bool

	ID                     string
	Title                  string
	Prompt                 string
	Interval               string
	Enabled                *bool
	Scope                  string
	WorkspaceRoot          string
	ApprovalMode           string
	TimeWindowStart        string
	TimeWindowEnd          string
	NotifyChannels         *bool
	NewConversationEachRun *bool
	ReuseSession           *bool
	MaxRuns                *int
	Provider               string
	Model                  string
	GoalMode               *bool
	GoalText               string
}

// HeartbeatUpsertRequest is one upsert call. ExpectedRevision is required:
// the engine compares it against the on-disk revision before writing and
// fails with the current revision on mismatch instead of overwriting.
type HeartbeatUpsertRequest struct {
	Patch            HeartbeatTaskPatch
	ExpectedRevision uint64
}

// HeartbeatUpsertResult echoes what the caller needs to verify the edit:
// the task id, the parsed-interval receipt, and the post-write revision.
type HeartbeatUpsertResult struct {
	TaskID         string `json:"taskId"`
	Created        bool   `json:"created"`
	Revision       uint64 `json:"revision"`
	ParsedInterval string `json:"parsedInterval,omitempty"`
	IntervalKind   string `json:"intervalKind"`
	NextRunAt      int64  `json:"nextRunAt"`
	NextRunHint    string `json:"nextRunHint,omitempty"`
}

// HeartbeatSetEnabledRequest toggles one task without touching its other
// fields. ExpectedRevision semantics match the upsert path.
type HeartbeatSetEnabledRequest struct {
	ID               string
	Enabled          bool
	ExpectedRevision uint64
}

// HeartbeatSetEnabledResult reports the toggle outcome.
type HeartbeatSetEnabledResult struct {
	TaskID      string `json:"taskId"`
	Enabled     bool   `json:"enabled"`
	Revision    uint64 `json:"revision"`
	NextRunAt   int64  `json:"nextRunAt"`
	NextRunHint string `json:"nextRunHint,omitempty"`
}

// HeartbeatManager is the seam the desktop injects. All methods must be safe
// for concurrent use; implementations own validation, CAS, and persistence.
type HeartbeatManager interface {
	ListTasks() (HeartbeatListView, error)
	UpsertTask(req HeartbeatUpsertRequest) (HeartbeatUpsertResult, error)
	SetEnabled(req HeartbeatSetEnabledRequest) (HeartbeatSetEnabledResult, error)
}

var heartbeatManager atomic.Pointer[HeartbeatManager]

// SetHeartbeatManager wires the desktop implementation in at startup. Passing
// nil clears a previous registration (used by tests).
func SetHeartbeatManager(m HeartbeatManager) {
	if m == nil {
		heartbeatManager.Store(nil)
		return
	}
	heartbeatManager.Store(&m)
}

// heartbeatManagerFromCtx returns the injected manager, or a descriptive
// error for sessions running without the desktop engine (CLI, headless).
func heartbeatManagerFromCtx(ctx context.Context) (HeartbeatManager, error) {
	if p := heartbeatManager.Load(); p != nil {
		return *p, nil
	}
	return nil, fmt.Errorf("heartbeat management tools require the Reasonix desktop app with its scheduler engine running; this session has no heartbeat engine attached (CLI/serve sessions cannot manage scheduled tasks)")
}

func init() {
	tool.RegisterBuiltin(heartbeatTaskList{})
	tool.RegisterBuiltin(heartbeatTaskUpsert{})
	tool.RegisterBuiltin(heartbeatTaskEnable{})
}

// heartbeatContractDoc is the authoritative field contract shared by the
// mutating tools' descriptions. Kept in one place so the contract cannot
// drift between tools.
const heartbeatContractDoc = `Field contract (authoritative):
- id: optional; omit to create (an id is generated). A provided id must already exist — the tool refuses to silently create a mistyped duplicate. Engine-scoped.
- title (required for new tasks): user-visible label.
- prompt (required for new tasks): the prompt submitted to the topic each run.
- interval (required for new tasks): one of "Ns"/"Nm"/"Nh" duration ("30m", "24h"), a 5-field cron expression ("0 9 * * 1-5"), or a named schedule "kind:rule@HH:MM" optionally prefixed "fallback|" (daily, weekly:mon,wed, biweekly:fri, monthly:1, yearly:3-21; e.g. "168h|weekly:fri@18:00").
- enabled: boolean, default true for new tasks.
- scope: "" or "global" (default) for the global workspace, "project" — then workspaceRoot is required and must be a real project root.
- workspaceRoot: project root path; required iff scope="project".
- approvalMode: "ask", "auto", or "yolo"; empty defaults to "yolo" (tasks run without permission prompts). Invalid values are rejected, not silently normalized.
- timeWindowStart/timeWindowEnd: optional "HH:MM" bounds (start inclusive, end exclusive) for interval tasks; cross-midnight windows like 22:00-06:00 are supported.
- notifyChannels: boolean; true forwards the run's output to connected bot channels. Omit/false = no forwarding.
- newConversationEachRun: boolean; true creates a fresh topic per run (topicId then always points at the latest conversation).
- maxRuns: integer budget on how many times the task may run (task 327). 0/omitted = unlimited (today's repeating behavior); 1 = single run; N = stop after N. Counting is per trigger — a failed attempt spends a unit too, so a bounded task always stops — while a skipped tick (not due, lease held, busy conversation) spends nothing. Reaching the budget flips enabled to false and the task shows runsUsed == maxRuns; re-enabling it (heartbeat_task_enable or upsert with enabled=true) resets runsUsed to zero, so each re-enable grants a fresh budget of maxRuns. runsUsed itself is engine-owned and rejected on write.
- reuseSession: boolean; true resumes the conversation bound at topicId — each run appends the prompt there (context stays continuous, no new session per run). The first run creates and binds the topic. A run whose target conversation is busy (a turn is running) is skipped and retried next tick, never injected mid-turn. Takes precedence over newConversationEachRun.
- provider/model: optional per-run model override; accepts "provider/model", a provider name, or a bare model name. Both empty keeps the topic's current model.
- goalMode/goalText: goal-mode runs continue until the goal is met instead of stopping after one turn; empty goalText falls back to prompt.
- Engine-owned, read-only for tools (writing them is rejected): topicId, lastRunAt, createdAt, runHistory, runsUsed — the scheduler maintains them.
Revision contract (authoritative): the engine increments revision on every persisted write (a new file starts at 1); schemaVersion is code-owned and never written by tools. Every mutating call must pass expected_revision taken from the latest heartbeat_task_list result; on mismatch the call fails with the current revision instead of overwriting the concurrent writer (read again, re-apply, retry). This replaces the legacy hand-edit rule "bump revision yourself after changing path-like fields" — engine writes make it automatic.`

// heartbeat_task_list ----------------------------------------------------------------

type heartbeatTaskList struct{}

func (heartbeatTaskList) Name() string { return "heartbeat_task_list" }

func (heartbeatTaskList) Description() string {
	return "List the desktop app's scheduled heartbeat tasks with each task's parsed next run time, plus the config revision used as expected_revision by heartbeat_task_upsert / heartbeat_task_enable. Read-only; call it before any mutation. " + heartbeatContractDoc
}

func (heartbeatTaskList) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (heartbeatTaskList) ReadOnly() bool { return true }

func (heartbeatTaskList) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	mgr, err := heartbeatManagerFromCtx(ctx)
	if err != nil {
		return "", err
	}
	view, err := mgr.ListTasks()
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// heartbeat_task_upsert ----------------------------------------------------------------

type heartbeatTaskUpsert struct{}

func (heartbeatTaskUpsert) Name() string { return "heartbeat_task_upsert" }

func (heartbeatTaskUpsert) Description() string {
	return "Create or update one scheduled heartbeat task on the desktop app. Merge semantics: fields you send overwrite, fields you omit keep their current value; send an empty string to clear a string field. Invalid field names, engine-owned fields, or an unparsable interval/approvalMode/scope pair fail immediately with the offending field named — nothing is written silently. Requires expected_revision from the latest heartbeat_task_list; on mismatch the call fails with the current revision (read again, re-apply, retry). Success returns the parsed-interval receipt and the next run time. " + heartbeatContractDoc
}

func (heartbeatTaskUpsert) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "expected_revision":{"type":"integer","description":"Revision from the latest heartbeat_task_list result. Required CAS guard."},
  "id":{"type":"string","description":"Omit to create a new task; an existing id updates that task."},
  "title":{"type":"string"},
  "prompt":{"type":"string"},
  "interval":{"type":"string","description":"\"30m\"/\"24h\" duration, 5-field cron, or named schedule like \"168h|weekly:fri@18:00\"."},
  "enabled":{"type":"boolean"},
  "scope":{"type":"string","enum":["","global","project"]},
  "workspaceRoot":{"type":"string","description":"Required when scope=project."},
  "approvalMode":{"type":"string","enum":["ask","auto","yolo"]},
  "timeWindowStart":{"type":"string","description":"HH:MM inclusive bound."},
  "timeWindowEnd":{"type":"string","description":"HH:MM exclusive bound."},
  "notifyChannels":{"type":"boolean"},
  "newConversationEachRun":{"type":"boolean"},
  "reuseSession":{"type":"boolean"},
  "maxRuns":{"type":"integer","minimum":0,"description":"Run-count budget: 0 = unlimited (default), 1 = single run, N = stop after N runs. Charged per trigger (failures count); reaching it auto-disables the task. Re-enabling resets the count."},
  "provider":{"type":"string"},
  "model":{"type":"string"},
  "goalMode":{"type":"boolean"},
  "goalText":{"type":"string"}
},"required":["expected_revision"]}`)
}

func (heartbeatTaskUpsert) ReadOnly() bool { return false }

func (heartbeatTaskUpsert) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	mgr, err := heartbeatManagerFromCtx(ctx)
	if err != nil {
		return "", err
	}
	patch, expected, err := decodeHeartbeatUpsertArgs(args)
	if err != nil {
		return "", err
	}
	result, err := mgr.UpsertTask(HeartbeatUpsertRequest{Patch: patch, ExpectedRevision: expected})
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// heartbeatUpsertArgOrder lists the accepted argument names in a stable order
// so error messages can point at every rejected key at once.
var heartbeatUpsertArgOrder = []string{
	"id", "title", "prompt", "interval", "enabled", "scope", "workspaceRoot",
	"approvalMode", "timeWindowStart", "timeWindowEnd", "notifyChannels",
	"newConversationEachRun", "reuseSession", "maxRuns", "provider", "model", "goalMode", "goalText",
}

// heartbeatEngineOwnedFields are scheduler-maintained; an upsert that names
// them is rejected instead of silently overwriting run state.
var heartbeatEngineOwnedFields = map[string]bool{
	"topicId": true, "lastRunAt": true, "createdAt": true, "runHistory": true,
	// Task 327: the spent budget is charged by the scheduler; only the budget
	// itself (maxRuns) is writable, and even that is reset on re-enable.
	"runsUsed": true,
}

func decodeHeartbeatUpsertArgs(args json.RawMessage) (HeartbeatTaskPatch, uint64, error) {
	var raw map[string]json.RawMessage
	if len(args) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return HeartbeatTaskPatch{}, 0, fmt.Errorf("invalid args: %w", err)
		}
	}
	accepted := map[string]bool{"expected_revision": true}
	for _, k := range heartbeatUpsertArgOrder {
		accepted[k] = true
	}
	var unknown, engineOwned []string
	for k := range raw {
		if accepted[k] {
			continue
		}
		if heartbeatEngineOwnedFields[k] {
			engineOwned = append(engineOwned, k)
			continue
		}
		unknown = append(unknown, k)
	}
	sort.Strings(unknown)
	sort.Strings(engineOwned)
	if len(unknown) > 0 || len(engineOwned) > 0 {
		var b strings.Builder
		if len(unknown) > 0 {
			fmt.Fprintf(&b, "unknown field(s) %s — not part of the heartbeat task contract", strings.Join(unknown, ", "))
		}
		if len(engineOwned) > 0 {
			if b.Len() > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "field(s) %s are engine-owned (topicId/lastRunAt/createdAt/runHistory are maintained by the scheduler) and cannot be written by tools", strings.Join(engineOwned, ", "))
		}
		return HeartbeatTaskPatch{}, 0, fmt.Errorf("%s", b.String())
	}

	boolPtr := func(key string) (*bool, error) {
		v, ok := raw[key]
		if !ok {
			return nil, nil
		}
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return nil, fmt.Errorf("field %q must be a boolean", key)
		}
		return &b, nil
	}
	str := func(key string) (string, bool, error) {
		v, ok := raw[key]
		if !ok {
			return "", false, nil
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", true, fmt.Errorf("field %q must be a string", key)
		}
		return s, true, nil
	}
	// Task 327: maxRuns is an integer budget. Absent (or null) keeps the
	// current value; 0 is a real value meaning "unlimited".
	intPtr := func(key string) (*int, error) {
		v, ok := raw[key]
		if !ok {
			return nil, nil
		}
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return nil, fmt.Errorf("field %q must be an integer number of runs (0 = unlimited, 1 = single run)", key)
		}
		if n < 0 {
			return nil, fmt.Errorf("field %q must not be negative; use 0 for unlimited, 1 for a single run, or a positive N", key)
		}
		return &n, nil
	}

	patch := HeartbeatTaskPatch{Provided: map[string]bool{}}
	var err error
	for _, k := range heartbeatUpsertArgOrder {
		patch.Provided[k] = false
		if _, ok := raw[k]; ok {
			patch.Provided[k] = true
		}
	}
	if patch.Title, _, err = str("title"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.Prompt, _, err = str("prompt"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.Interval, _, err = str("interval"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.ID, _, err = str("id"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.Scope, _, err = str("scope"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.WorkspaceRoot, _, err = str("workspaceRoot"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.ApprovalMode, _, err = str("approvalMode"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.TimeWindowStart, _, err = str("timeWindowStart"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.TimeWindowEnd, _, err = str("timeWindowEnd"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.Provider, _, err = str("provider"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.Model, _, err = str("model"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.GoalText, _, err = str("goalText"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.Enabled, err = boolPtr("enabled"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.NotifyChannels, err = boolPtr("notifyChannels"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.NewConversationEachRun, err = boolPtr("newConversationEachRun"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.ReuseSession, err = boolPtr("reuseSession"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.MaxRuns, err = intPtr("maxRuns"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}
	if patch.GoalMode, err = boolPtr("goalMode"); err != nil {
		return HeartbeatTaskPatch{}, 0, err
	}

	var expected uint64
	if v, ok := raw["expected_revision"]; ok {
		if err := json.Unmarshal(v, &expected); err != nil {
			return HeartbeatTaskPatch{}, 0, fmt.Errorf("field \"expected_revision\" must be an integer revision from heartbeat_task_list")
		}
	} else {
		return HeartbeatTaskPatch{}, 0, fmt.Errorf("expected_revision is required: call heartbeat_task_list first and pass its revision back so a concurrent edit fails instead of being overwritten")
	}
	return patch, expected, nil
}

// heartbeat_task_enable ----------------------------------------------------------------

type heartbeatTaskEnable struct{}

func (heartbeatTaskEnable) Name() string { return "heartbeat_task_enable" }

func (heartbeatTaskEnable) Description() string {
	return "Enable or disable one scheduled heartbeat task by id. The toggle takes effect immediately in the running scheduler and is persisted. Requires expected_revision from the latest heartbeat_task_list; on mismatch the call fails with the current revision (read again, retry). Use heartbeat_task_upsert for any other field change. " + heartbeatContractDoc
}

func (heartbeatTaskEnable) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "id":{"type":"string","description":"Task id from heartbeat_task_list."},
  "enabled":{"type":"boolean","description":"true = enable, false = disable."},
  "expected_revision":{"type":"integer","description":"Revision from the latest heartbeat_task_list result. Required CAS guard."}
},"required":["id","enabled","expected_revision"]}`)
}

func (heartbeatTaskEnable) ReadOnly() bool { return false }

func (heartbeatTaskEnable) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	mgr, err := heartbeatManagerFromCtx(ctx)
	if err != nil {
		return "", err
	}
	var p struct {
		ID               string `json:"id"`
		Enabled          *bool  `json:"enabled"`
		ExpectedRevision uint64 `json:"expected_revision"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("id is required: the id of the task to toggle, from heartbeat_task_list")
	}
	if p.Enabled == nil {
		return "", fmt.Errorf("enabled is required and must be true or false")
	}
	result, err := mgr.SetEnabled(HeartbeatSetEnabledRequest{ID: p.ID, Enabled: *p.Enabled, ExpectedRevision: p.ExpectedRevision})
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
