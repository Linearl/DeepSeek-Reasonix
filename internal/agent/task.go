package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"

	"reasonix/internal/ablation"
	"reasonix/internal/checkpoint"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/imageinput"
	"reasonix/internal/jobs"
	"reasonix/internal/memory"
	"reasonix/internal/permission"
	"reasonix/internal/planmode"
	"reasonix/internal/provider"
	"reasonix/internal/sandbox"
	"reasonix/internal/subagentmailbox"
	"reasonix/internal/tool"
	"reasonix/internal/workspacelease"
)

// DefaultTaskSystemPrompt steers a sub-agent toward focused, terse delivery —
// it doesn't see the parent's conversation so it must self-contain.
const DefaultTaskSystemPrompt = `You are a sub-agent invoked by a parent coding agent to carry out one focused task.
Use the provided tools to investigate or act. For MCP, use the stable use_capability
proxy (list → inspect → call); do not expect direct mcp__* tool schemas. Return a
single final answer that is concise and self-contained — the parent will see only
that answer, not your tool calls or reasoning. If you need to ask for clarification,
fail with a precise question instead of guessing.`

// DefaultReadOnlyTaskSystemPrompt steers read-only sub-agents toward isolated
// research. They never receive writer tools, persisted transcript controls, or
// background process controls, so their final answer is the only handoff.
const DefaultReadOnlyTaskSystemPrompt = `You are a read-only research sub-agent invoked by a parent coding agent.
Use only the provided read-only tools to inspect code, docs, history, and safe shell output.
For MCP, use use_capability only for authorized tools that declare readOnly and are
not destructive; never treat missing readOnlyHint as permission to call. Do not
attempt to write files, install capabilities, mutate memory, control long-lived
processes, or delegate to writer-capable agents. If a read-only delegation tool is
available and genuinely useful, you may use it within the configured depth limit.
Return a concise, self-contained final answer with the evidence the parent needs.`

// maxConcurrentBackgroundTasks is the legacy writer-background fallback used
// only when a TaskTool has no session scheduler (tests). Production boots
// inject MaxParallelWriters via SubagentScheduler.
const maxConcurrentBackgroundTasks = DefaultMaxParallelWriters

func (b foregroundOnlyBash) Name() string { return "bash" }

func (b foregroundOnlyBash) Description() string {
	desc := strings.TrimSpace(b.inner.Description())
	if desc == "" {
		desc = "Execute a command in the shell and return combined stdout/stderr."
	}
	desc = strings.Replace(desc, "Execute a command in the shell", "Execute a foreground command in the shell", 1)
	return desc + " Background execution is unavailable inside subagents."
}

func (foregroundOnlyBash) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute in the foreground"}},"required":["command"]}`)
}

func (b foregroundOnlyBash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		RunInBackground bool `json:"run_in_background"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.RunInBackground {
		return "", tool.Blocked("blocked: background bash is unavailable in subagents; run a foreground command or ask the parent agent to start a background job")
	}
	return b.inner.Execute(ctx, args)
}

func (b foregroundOnlyBash) ReadOnly() bool { return b.inner.ReadOnly() }

func (b readOnlyBash) Name() string { return "bash" }

func (b readOnlyBash) Description() string {
	desc := strings.TrimSpace(b.inner.Description())
	if desc == "" {
		desc = "Execute a command in the shell and return combined stdout/stderr."
	}
	desc = strings.Replace(desc, "Execute a command in the shell", "Execute a foreground read-only command in the shell", 1)
	return desc + " Only permission-classified read-only commands are allowed; shell operators, background execution, process preservation, and write-capable arguments are blocked."
}

func (readOnlyBash) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Read-only shell command to execute in the foreground. Must match the permission-layer read-only command policy."}},"required":["command"]}`)
}

func (b readOnlyBash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if !permission.BashCommandIsReadOnly(args) {
		return "", tool.Blocked("blocked: read-only subagents can run only permission-classified foreground read-only commands")
	}
	return b.inner.Execute(ctx, args)
}

func (readOnlyBash) ReadOnly() bool { return true }

// TaskTool spawns a sub-agent in its own session for a focused sub-task. The
// sub-agent runs with a filtered tool whitelist and the same step budget shape
// as the parent (see Execute); its tool calls are forwarded to the parent's
// event stream nested under this call, while only its final assistant message is
// returned to the parent model. Use cases: keep noisy tool sequences (multi-file
// exploration, repeated grep / read_file) out of the parent's context budget, or
// parallel research across independent areas (the parallel-dispatch path picks
// these up only when readOnly, which task is not).
type TaskTool struct {
	imageInput                    *imageinput.Config
	prov                          provider.Provider
	pricing                       *provider.Pricing
	quoteContext                  *event.QuoteContext
	parentReg                     *tool.Registry
	maxSteps                      int
	reviewMaxSteps                int
	defaultSteps                  int
	contextWindow                 int
	compactRatio                  float64
	recentKeep                    int
	temperature                   float64
	archiveDir                    string
	keepPolicy                    KeepPolicy
	sysPrompt                     string
	gate                          Gate
	subagentModel, subagentEffort string
	resolveProvider               func(modelRef, effort string) (provider.Provider, *provider.Pricing, int, error)
	transcripts                   *SubagentStore
	workspaceRoot                 string
	baseModel                     string
	baseEffort                    string
	identityProfile               func(modelRef, effort string) (string, string)
	maxSubagentDepth              int
	ablation                      ablation.Set
	workspaceLease                *workspacelease.Owner
	// scheduler is the session-scoped concurrency + write-claim controller.
	// nil falls back to the legacy jobs.ReserveStart cap for background tasks.
	scheduler *SubagentScheduler
	// profileLookup resolves profile= names from the live Skill store without
	// embedding the name list in the tool schema (cache stability).
	profileLookup ProfileLookup
	// profileConfigModel/Effort look up persistent per-profile overrides
	// (agent.subagent_models / subagent_efforts).
	profileConfigModel  func(profile string) string
	profileConfigEffort func(profile string) string
	// bashSandboxEnforced reports whether OS sandbox can honour write roots
	// for bash inside path-bound writer sub-agents.
	bashSandboxEnforced func() bool
	// parallelWriterReadOnlyBash is the task-573 experimental fallback: when
	// BindWritePaths drops bash from a path-bound writer's registry (OS sandbox
	// cannot confine bash writes to the claim roots), re-admit the parent bash
	// wrapped in the read-only command classifier (readOnlyBash) so the child
	// regains read-only self-verification (timestamps, inspections) without any
	// write capability. Off (default) keeps the exact current behavior.
	parallelWriterReadOnlyBash bool
	// Task 265 audit-3 M2: the fork-only agent behaviours children inherit.
	compactionParallel bool
	contextBudget      bool
	researchBudget     bool

	// mailboxes is the session's running-subagent message channel (task 616):
	// persisted per-ref mailboxes under the subagents dir plus the process
	// steer registry. nil (switch off, or no persisted transcripts) keeps
	// send_message unregistered, publishes no handles, and drains nothing.
	mailboxes *subagentmailbox.Hub

	// mutationObserver is shared with spawned sub-agents for checkpoint capture.
	mutationObserver *checkpoint.MutationObserver
	// recoveryGate is the shared Auto Guard boundary for
	// this session (root + sub-agents). nil disables recovery in children.
	recoveryGate RecoveryGate
	writeRoots   *sandbox.WritableRootSet
	// capabilityRuntime is the session-shared MCP Host/specs substrate. Each
	// sub-agent gets its own use_capability frontend so ledger state stays
	// isolated while connections reuse the parent Host.
	capabilityRuntime *MCPCapabilityRuntime
}

// NewTaskToolWithOptions is the internal standard constructor for TaskTool.
// An empty SysPrompt still resolves to DefaultTaskSystemPrompt. No extra
// validation or default overrides are applied beyond the historical NewTaskTool
// behavior.
func NewTaskToolWithOptions(opts TaskToolOptions) *TaskTool {
	sysPrompt := opts.SysPrompt
	if sysPrompt == "" {
		sysPrompt = DefaultTaskSystemPrompt
	}
	return &TaskTool{
		imageInput:            opts.ImageInput,
		prov:                  opts.Provider,
		pricing:               opts.Pricing,
		quoteContext:          opts.QuoteContext,
		parentReg:             opts.ParentRegistry,
		maxSteps:              opts.MaxSteps,
		reviewMaxSteps:        opts.ReviewMaxSteps,
		defaultSteps:          opts.SubagentDefaultSteps,
		contextWindow:         opts.ContextWindow,
		recentKeep:            opts.RecentKeep,
		compactRatio:          opts.CompactRatio,
		temperature:           opts.Temperature,
		archiveDir:            opts.ArchiveDir,
		keepPolicy:            opts.KeepPolicy,
		sysPrompt:             sysPrompt,
		gate:                  opts.Gate,
		subagentModel:         opts.SubagentModel,
		subagentEffort:        opts.SubagentEffort,
		resolveProvider:       opts.ResolveProvider,
		maxSubagentDepth:      DefaultMaxSubagentDepth,
		compactionParallel:    opts.CompactionParallel,
		contextBudget:         opts.ContextBudget,
		researchBudget:        opts.ResearchBudget,
	}
}

// NewTaskTool wires a task tool to the parent agent's environment so its
// sub-agents can use the same provider and tools. sysPrompt is the system
// prompt every sub-agent starts with; pass "" for DefaultTaskSystemPrompt. gate
// is the permission gate sub-agents inherit — pass the headless variant so
// deny rules still bite while autonomous sub-agents are never blocked on an
// interactive prompt (there is no UI to answer one).
//
// Compatibility wrapper: new call sites should prefer NewTaskToolWithOptions.
// The positional form is kept for at least one full iteration cycle.
func NewTaskTool(prov provider.Provider, pricing *provider.Pricing, parentReg *tool.Registry,
	maxSteps, contextWindow, recentKeep int, softCompactRatio, toolResultSnipRatio, compactRatio, compactForceRatio, temperature float64, archiveDir, sysPrompt string, gate Gate,
	keepPolicy KeepPolicy, subagentModel, subagentEffort string, resolveProvider func(string, string) (provider.Provider, *provider.Pricing, int, error)) *TaskTool {
	return NewTaskToolWithOptions(TaskToolOptions{
		Provider:            prov,
		Pricing:             pricing,
		ParentRegistry:      parentReg,
		MaxSteps:            maxSteps,
		ContextWindow:       contextWindow,
		RecentKeep:          recentKeep,
		SoftCompactRatio:    softCompactRatio,
		ToolResultSnipRatio: toolResultSnipRatio,
		CompactRatio:        compactRatio,
		CompactForceRatio:   compactForceRatio,
		Temperature:         temperature,
		ArchiveDir:          archiveDir,
		SysPrompt:           sysPrompt,
		Gate:                gate,
		KeepPolicy:          keepPolicy,
		SubagentModel:       subagentModel,
		SubagentEffort:      subagentEffort,
		ResolveProvider:     resolveProvider,
	})
}

// WithTranscripts enables persisted sub-agent transcript continuation for this
// task tool. The base model/effort are the parent provider identity used when no
// subagent override is configured.
func (t *TaskTool) WithTranscripts(store *SubagentStore, workspaceRoot, baseModel, baseEffort string) *TaskTool {
	t.transcripts = store
	t.workspaceRoot = strings.TrimSpace(workspaceRoot)
	t.baseModel = strings.TrimSpace(baseModel)
	t.baseEffort = strings.TrimSpace(baseEffort)
	return t
}

func (t *TaskTool) WithTranscriptIdentityResolver(resolve func(modelRef, effort string) (string, string)) *TaskTool {
	t.identityProfile = resolve
	return t
}

func (t *TaskTool) WithMaxSubagentDepth(depth int) *TaskTool {
	t.maxSubagentDepth = NormalizeMaxSubagentDepth(depth)
	return t
}

// WithAblation propagates the parent's benchmark arm so a sub-agent runs with
// the same subsystems switched off.
func (t *TaskTool) WithAblation(set ablation.Set) *TaskTool {
	t.ablation = set
	return t
}

// WithWorkspaceLease shares the parent's workspace-wide delivery write lease
// with every spawned sub-agent. A shared owner is required: independent owners
// in one session would deadlock when a child tries to write while its parent
// already retains the lease.
func (t *TaskTool) WithWorkspaceLease(owner *workspacelease.Owner) *TaskTool {
	t.workspaceLease = owner
	return t
}

// WithScheduler attaches the session-scoped concurrency and write-claim
// controller used by task, fleet, parallel_tasks, and profile skill runners.
func (t *TaskTool) WithScheduler(s *SubagentScheduler) *TaskTool {
	t.scheduler = s
	return t
}

// Scheduler returns the attached session scheduler (may be nil in unit tests).
func (t *TaskTool) Scheduler() *SubagentScheduler {
	if t == nil {
		return nil
	}
	return t.scheduler
}

// WithProfileLookup enables task/fleet profile= resolution from the Skill store.
func (t *TaskTool) WithProfileLookup(lookup ProfileLookup) *TaskTool {
	t.profileLookup = lookup
	return t
}

// WithProfileConfigResolvers supplies persistent per-profile model/effort
// overrides (agent.subagent_models / subagent_efforts).
func (t *TaskTool) WithProfileConfigResolvers(model, effort func(profile string) string) *TaskTool {
	t.profileConfigModel = model
	t.profileConfigEffort = effort
	return t
}

// WithBashSandboxEnforced tells path-bound writer runs whether bash can keep
// the same write roots under the OS sandbox.
func (t *TaskTool) WithBashSandboxEnforced(fn func() bool) *TaskTool {
	t.bashSandboxEnforced = fn
	return t
}

// WithParallelWriterReadOnlyBash arms the task-573 experimental fallback: a
// path-bound (parallel) writer whose bash was dropped by BindWritePaths gets
// the parent bash re-admitted behind the readOnlyBash classifier. Off keeps
// bash absent exactly as before.
func (t *TaskTool) WithParallelWriterReadOnlyBash(on bool) *TaskTool {
	if t != nil {
		t.parallelWriterReadOnlyBash = on
	}
	return t
}

// WithCapabilityRuntime attaches the session-shared MCP runtime so ordinary and
// read-only sub-agents receive a stable use_capability frontend without
// inheriting dynamic mcp__* schemas.
func (t *TaskTool) WithCapabilityRuntime(rt *MCPCapabilityRuntime) *TaskTool {
	if t != nil {
		t.capabilityRuntime = rt
	}
	return t
}

// WithSubagentMailbox attaches the session's running-subagent message channel
// (task 616). nil (switch off / headless builds without persisted transcripts)
// keeps every pre-616 behavior: send_message stays unregistered, subagent
// runs publish no steer handles, and continue_from drains no mailboxes.
func (t *TaskTool) WithSubagentMailbox(hub *subagentmailbox.Hub) *TaskTool {
	if t != nil {
		t.mailboxes = hub
	}
	return t
}

// MailboxHub returns the attached message channel (nil when off).
func (t *TaskTool) MailboxHub() *subagentmailbox.Hub {
	if t == nil {
		return nil
	}
	return t.mailboxes
}

// mailboxForRun returns the mailbox for one prepared run: nil for ephemeral
// runs (empty ref), a disabled channel, or a nil run.
func (t *TaskTool) mailboxForRun(run *SubagentRun) *subagentmailbox.Mailbox {
	if t == nil || t.mailboxes == nil || run == nil || run.Ref == "" {
		return nil
	}
	return t.mailboxes.MailboxFor(run.Ref)
}

func (t *TaskTool) Name() string { return tool.HostTask }

// boundarySummary is the subagent tool-boundary summary embedded in the task
// tool description and schema. Task 572 (484-a): it must match the registry a
// sub-agent actually receives — when the host cannot enforce bash write roots
// (nil wiring fails closed the same way), BindWritePaths drops bash from
// explicit write_paths sub-agents, so the no-sandbox clause is appended;
// sandbox-enforcing hosts keep the historical text byte-identical.
func (t *TaskTool) boundarySummary() string {
	if t != nil && t.bashCanEnforceWriteRoots() {
		return subagentToolBoundarySummary
	}
	return subagentToolBoundarySummary + subagentToolBoundaryNoSandboxClause
}

func (t *TaskTool) Description() string {
	return "Spawn a sub-agent for a focused sub-task. Optional profile selects a runAs=subagent Skill whose body becomes the full system prompt (no implicit concise default). Optional write_paths declare non-overlapping write targets so background writers may run in parallel; omitting write_paths on a writer claims the whole workspace and serializes writers. The sub-agent runs in its own session with a filtered tool list (defaults to every parent tool, then applies the subagent boundary: " + t.boundarySummary() + "). Only its final answer is returned."
}

func (t *TaskTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "prompt":{"type":"string","description":"What the sub-agent should accomplish. Be specific about the deliverable — the sub-agent does not see this conversation."},
  "description":{"type":"string","description":"Short label for the sub-task (3-7 words). Surfaced in the dispatch line so the user sees what's running."},
  "profile":{"type":"string","description":"Optional runAs=subagent profile name. Resolved at runtime from the Skill store; explicit names may invoke invocation=manual profiles. The profile body becomes the full system prompt."},
  "write_paths":{"type":"array","items":{"type":"string"},"description":"Optional workspace-relative or absolute file/directory paths this writer may modify. Globs and workspace escapes are rejected. Writers without write_paths claim the whole workspace (serializing against every other writer claim). Non-overlapping paths allow parallel writers up to max_parallel_writers. In fleet, multiple whole-workspace claims fail preflight before any task starts."},
  "tools":{"type":"array","items":{"type":"string"},"description":"Optional tool whitelist. When profile sets allowed-tools, this list is intersected (call args cannot expand profile permissions). ` + t.boundarySummary() + `"},
  "max_steps":{"type":"integer","description":"Optional cap on tool-call rounds. Defaults to two-thirds of the parent's cap (min 12); a configured subagent_default_steps override wins.","minimum":1},
  "run_in_background":{"type":"boolean","description":"Run the sub-agent asynchronously: returns a job id immediately and keeps working across turns. Collect its final answer with wait, and you'll be notified when it finishes. Use for long, independent sub-tasks you don't need to block on right now."},
  "model":{"type":"string","description":"Optional model override for the sub-agent (a configured provider/model name). Precedence: persistent profile config, this argument, profile frontmatter, global subagent default, parent model."},
  "effort":{"type":"string","description":"Optional reasoning effort for the sub-agent (e.g. high, max). Same precedence as model."},
  "continue_from":{"type":"string","description":"Continue a prior compatible subagent transcript in the current conversation context. Pass only the 'sa_...' value from the prior result's 'Subagent reference: ...' line. If the ref belongs to an ancestor conversation, the framework continues a current-conversation copy."}
},
"required":["prompt"]
}`)
}

// ReadOnly is false: a sub-agent can invoke any whitelisted tool, including
// writers. Conservative classification keeps the parallel-dispatch path from
// running two sub-agents at once and letting their writes race.
func (t *TaskTool) ReadOnly() bool { return false }

// ResolveProfile extracts model/effort from task args (and optional profile
// overrides) for dispatch-line display. Runtime execution re-resolves with the
// full precedence chain.
func (t *TaskTool) ResolveProfile(args json.RawMessage) *event.Profile {
	var p struct {
		Model   string `json:"model"`
		Effort  string `json:"effort"`
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil
	}
	profileModel, profileEffort := "", ""
	configModel, configEffort := "", ""
	if name := strings.TrimSpace(p.Profile); name != "" {
		if def, err := ResolveProfileDefinition(t.profileLookup, name); err == nil {
			profileModel, profileEffort = def.Model, def.Effort
		}
		if t.profileConfigModel != nil {
			configModel = t.profileConfigModel(name)
		}
		if t.profileConfigEffort != nil {
			configEffort = t.profileConfigEffort(name)
		}
	}
	model, effort := ResolveModelEffort(
		configModel, configEffort,
		p.Model, p.Effort,
		profileModel, profileEffort,
		t.subagentModel, t.subagentEffort,
	)
	if model == "" && effort == "" {
		return nil
	}
	return &event.Profile{Model: model, Effort: effort}
}

// ReadOnlyTaskTool runs an isolated sub-agent with a strictly read-only tool
// registry. It intentionally omits transcript continuation/fork controls so the
// call has no durable host side effects.
//
// Task 118 (opt-in): with the read-only-background experiment on, the schema
// also offers run_in_background. Read-only dispatch was the last path that could
// only block the main chain: across the local session corpus 12/12 read-only
// dispatches ran in the foreground while 34/38 writer tasks already ran in the
// background, so research - the most delegable work - was the part that
// serialized. Off by default, because a background run is a durable host side
// effect and the tool's contract is that it has none.
type ReadOnlyTaskTool struct {
	task *TaskTool
	// allowBackground is written once during boot and read-only afterwards.
	allowBackground bool
}

func NewReadOnlyTaskTool(task *TaskTool) *ReadOnlyTaskTool {
	return &ReadOnlyTaskTool{task: task}
}

// WithBackgroundExecution exposes the optional run_in_background argument on the
// read-only path (task 118). Returns the tool so boot can chain it.
func (r *ReadOnlyTaskTool) WithBackgroundExecution(allow bool) *ReadOnlyTaskTool {
	if r != nil {
		r.allowBackground = allow
	}
	return r
}

func (*ReadOnlyTaskTool) Name() string { return tool.HostReadOnlyTask }

func (*ReadOnlyTaskTool) Description() string {
	return "Spawn a read-only research sub-agent for a focused investigation. The sub-agent runs in an isolated, ephemeral session with read-only tools only; bash is wrapped to allow only permission-classified foreground read-only commands. It cannot write files, install capabilities, mutate memory, run background jobs, continue/fork transcripts, or delegate to writer-capable agents. Read-only nested delegation may be available until max_subagent_depth is reached. Only its final answer is returned."
}

func (r *ReadOnlyTaskTool) Schema() json.RawMessage {
	background := ""
	if r != nil && r.allowBackground {
		background = `,
  "run_in_background":{"type":"boolean","description":"Run the read-only sub-agent asynchronously: returns a job id immediately and keeps researching across turns. Collect its final answer with wait, and you'll be notified when it finishes. Use for long investigations you don't need to block on right now — read-only research is the safest thing to run in the background."}`
	}
	return json.RawMessage(`{
"type":"object",
"properties":{
  "prompt":{"type":"string","description":"What the read-only sub-agent should investigate. Be specific about the evidence or summary to return — the sub-agent does not see this conversation."},
  "description":{"type":"string","description":"Short label for the read-only sub-task (3-7 words). Surfaced in the dispatch line so the user sees what's running."},
  "tools":{"type":"array","items":{"type":"string"},"description":"Optional read-only tool whitelist. Writer, installer, memory mutation, background job, and delegation tools are never exposed."},
  "max_steps":{"type":"integer","description":"Optional cap on tool-call rounds. Defaults to two-thirds of the parent's cap (min 12); a configured subagent_default_steps override wins.","minimum":1},
  "model":{"type":"string","description":"Optional model override for the sub-agent (a configured provider/model name)."},
  "effort":{"type":"string","description":"Optional reasoning effort for the sub-agent (e.g. high, max)."}` + background + `
},
"required":["prompt"]
}`)
}

func (*ReadOnlyTaskTool) ReadOnly() bool { return true }

// PlanModeSafe reports true: read_only_task spawns a strictly read-only research
// sub-agent (no writers, installers, memory mutation, background jobs, or
// delegation), so it is safe to run while planning.
func (*ReadOnlyTaskTool) PlanModeSafe() bool { return true }

func (r *ReadOnlyTaskTool) ResolveProfile(args json.RawMessage) *event.Profile {
	if r == nil || r.task == nil {
		return nil
	}
	return r.task.ResolveProfile(args)
}

func (r *ReadOnlyTaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if r == nil || r.task == nil {
		return "", fmt.Errorf("read_only_task is not configured")
	}
	var p struct {
		Prompt          string   `json:"prompt"`
		Description     string   `json:"description"`
		Tools           []string `json:"tools"`
		MaxSteps        int      `json:"max_steps"`
		Model           string   `json:"model"`
		Effort          string   `json:"effort"`
		RunInBackground bool     `json:"run_in_background"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	// Every entry point compiles to a spec and runs through RunProfileSpec, so a
	// boundary added there cannot be missed by one caller. read_only_task keeps
	// its own promise of no durable side effects through Ephemeral.
	// Task 118: run_in_background is honored only while the experiment is on, so
	// the default schema and behaviour stay exactly as they were.
	background := p.RunInBackground && r.allowBackground
	spec, err := r.task.buildTaskSpec(ctx, p.Prompt, p.Description, "", nil, p.Tools, p.MaxSteps, p.Model, p.Effort, "", "", background, true)
	if err != nil {
		return "", err
	}
	spec.Worker.SystemPrompt = DefaultReadOnlyTaskSystemPrompt
	spec.Context.Ephemeral = true
	return r.task.RunProfileSpec(ctx, spec)
}

func (t *TaskTool) effectiveProfile(model, effort string) (string, string) {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	if model == "" {
		model = strings.TrimSpace(t.subagentModel)
	}
	if effort == "" {
		effort = strings.TrimSpace(t.subagentEffort)
	}
	return model, effort
}

func (t *TaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Prompt          string   `json:"prompt"`
		Description     string   `json:"description"`
		Profile         string   `json:"profile"`
		WritePaths      []string `json:"write_paths"`
		Tools           []string `json:"tools"`
		MaxSteps        int      `json:"max_steps"`
		RunInBackground bool     `json:"run_in_background"`
		Model           string   `json:"model"`
		Effort          string   `json:"effort"`
		ContinueFrom    string   `json:"continue_from"`
		ForkFrom        string   `json:"fork_from"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}

	spec, err := t.buildTaskSpec(ctx, p.Prompt, p.Description, p.Profile, p.WritePaths, p.Tools, p.MaxSteps, p.Model, p.Effort, p.ContinueFrom, p.ForkFrom, p.RunInBackground, false)
	if err != nil {
		return "", err
	}
	return t.RunProfileSpec(ctx, spec)
}

// buildTaskSpec resolves profile, tools, model/effort, and write claims for a
// single task/fleet item. forceReadOnly forces the read-only registry.
func (t *TaskTool) buildTaskSpec(ctx context.Context, prompt, description, profile string, writePaths, tools []string, maxSteps int, model, effort, continueFrom, forkFrom string, background, forceReadOnly bool) (ProfileExecSpec, error) {
	spec := ProfileExecSpec{
		Task:    TaskSpec{Objective: prompt, Description: description},
		Worker:  WorkerSpec{Kind: "task", Name: "task", SystemPrompt: t.sysPrompt},
		Grant:   CapabilityGrant{CallTools: tools},
		Context: ContextRequest{ContinueFrom: strings.TrimSpace(continueFrom), ForkFrom: strings.TrimSpace(forkFrom)},
		Sched:   SchedulerPolicy{MaxSteps: maxSteps, RunInBackground: background, Nested: SubagentDepth(ctx) > 0},
	}
	profile = strings.TrimSpace(profile)
	readOnly := forceReadOnly
	var profileTools []string
	var profileModel, profileEffort string
	if profile != "" {
		def, err := ResolveProfileDefinition(t.profileLookup, profile)
		if err != nil {
			return ProfileExecSpec{}, err
		}
		spec.Worker.Profile = def.Name
		spec.Worker.Name = def.Name
		spec.Worker.Kind = "skill"
		spec.Worker.SystemPrompt = def.Body
		spec.Worker.UseProfilePrompt = true
		profileTools = def.AllowedTools
		profileModel, profileEffort = def.Model, def.Effort
		if def.ReadOnly {
			readOnly = true
		}
	}
	spec.Grant.ReadOnly = readOnly
	spec.Grant.ProfileTools = profileTools

	configModel, configEffort := "", ""
	if profile != "" {
		if t.profileConfigModel != nil {
			configModel = t.profileConfigModel(profile)
		}
		if t.profileConfigEffort != nil {
			configEffort = t.profileConfigEffort(profile)
		}
	}
	spec.Worker.Model, spec.Worker.Effort = ResolveModelEffort(
		configModel, configEffort,
		model, effort,
		profileModel, profileEffort,
		t.subagentModel, t.subagentEffort,
	)

	if !readOnly {
		// Every writer carries a claim. Omitting write_paths conservatively claims
		// the whole workspace, including foreground task calls, so they cannot
		// bypass an already-running background/fleet writer claim. Direct legacy
		// TaskTool constructions without a workspace/scheduler keep their old
		// no-claim behavior; production boot always configures both.
		requireClaim := t.scheduler != nil || strings.TrimSpace(t.workspaceRoot) != "" || background || len(writePaths) > 0
		claims, err := t.resolveWriterClaims(writePaths, requireClaim)
		if err != nil {
			return ProfileExecSpec{}, err
		}
		spec.Grant.WritePaths = claims
		if requireClaim && claims.Empty() {
			return ProfileExecSpec{}, fmt.Errorf("writer claim resolved empty")
		}
	} else if len(writePaths) > 0 {
		return ProfileExecSpec{}, fmt.Errorf("write_paths is not valid for read-only tasks")
	}
	return spec, nil
}

func (t *TaskTool) resolveWriterClaims(writePaths []string, requireClaim bool) (WritePathSet, error) {
	if len(writePaths) > 0 {
		return NormalizeWritePaths(t.workspaceRoot, writePaths)
	}
	if !requireClaim {
		return WritePathSet{}, nil
	}
	return WholeWorkspaceWriteClaim(t.workspaceRoot)
}

// RunProfileSpec executes a unified profile/task specification. Shared by task,
// fleet items, and boot-wired skill runners so prompt, tools, claims, and
// scheduling cannot drift across entry points.
func (t *TaskTool) RunProfileSpec(ctx context.Context, spec ProfileExecSpec) (result string, err error) {
	if t == nil {
		return "", fmt.Errorf("task tool is not configured")
	}
	// Per-child progress tracker: converts the child's reasoning/text/notice/
	// retrying into reserved ToolProgress previews and guarantees exactly one
	// terminal status (completed/cancelled/failed). The background job owns
	// finish after handoff; every other exit finishes here, including
	// validation errors and panics.
	trk := newSubagentProgressTracker(ctx, subSink(ctx))
	backgroundHandoff := false
	defer func() {
		if backgroundHandoff {
			return
		}
		if p := recover(); p != nil {
			trk.finish(nil, fmt.Errorf("panic: %v", p))
			panic(p)
		}
		trk.finish(ctx.Err(), err)
	}()
	if !spec.Sched.RunInBackground {
		trk.running()
	}
	if strings.TrimSpace(spec.Task.Objective) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(spec.Worker.SystemPrompt) == "" {
		if spec.Worker.UseProfilePrompt {
			return "", fmt.Errorf("profile system prompt is empty")
		}
		spec.Worker.SystemPrompt = t.sysPrompt
	}

	ctx, maxSteps := t.childMaxStepsForSpec(ctx, &spec)
	childDepth, err := t.nextSubagentDepth(ctx)
	if err != nil {
		return "", err
	}

	toolNames, err := IntersectToolLists(t.parentReg, spec.Grant.ProfileTools, spec.Grant.CallTools)
	if err != nil {
		return "", err
	}
	subReg, childWriteRoots, err := t.buildSubagentRegistry(spec, toolNames, childDepth)
	if err != nil {
		return "", err
	}

	modelRef, effortRef := spec.Worker.Model, spec.Worker.Effort
	usageModelRef := t.usageModelRef(modelRef, effortRef)
	parentID, parentSink, _, _ := CallContext(ctx)
	run, err := t.prepareTranscriptRunWithPrompt(ctx, subReg, modelRef, effortRef, spec.Context.parentSession(ctx), parentID, spec.Context.ContinueFrom, spec.Context.ForkFrom, spec.Worker.SystemPrompt, spec.Worker.Kind, spec.Worker.Name)
	if err != nil {
		return "", err
	}
	prov, pricing, ctxWin, err := t.resolveSubSessionRuntime(modelRef, effortRef)
	if err != nil {
		return t.failBeforeSubagentRelease(run, fmt.Errorf("sub-agent profile: %w", err))
	}
	// 任务616: the mailbox is keyed by this run's own ref. Ephemeral runs
	// (empty ref) get nil and publish nothing.
	mailbox := t.mailboxForRun(run)
	// 任务440: stamp the persisted transcript ref onto the progress preview
	// stream so the frontend can bind the live card to the on-disk transcript
	// (the dock live view reads through the ref). Empty for ephemeral runs.
	trk.setRef(run.Ref)
	lifecyclePhase := "child_created"
	if strings.TrimSpace(spec.Context.ContinueFrom) != "" || strings.TrimSpace(spec.Context.ForkFrom) != "" {
		lifecyclePhase = "child_resume"
	}
	// Task 557: the lifecycle payload carries background ownership so the
	// desktop foreground-subagent registry can skip job-owned children (they
	// are already represented by their job row). The flag comes from the spec
	// routing here, and from the background-job context marker for fleet
	// children running under a backgrounded fleet.
	emitSubagentLifecycle(parentSink, lifecyclePhase, parentID, spec.Worker.Name, usageModelRef, effortRef, run, nil, spec.Sched.RunInBackground || BackgroundOwnedLifecycle(ctx))

	isWriter := !spec.Grant.ReadOnly
	acquireReq := AcquireRequest{
		Writer:     isWriter,
		WritePaths: spec.Grant.WritePaths,
		Nested:     spec.Sched.Nested,
		Label:      firstNonEmpty(spec.Task.Description, spec.Worker.Name, "task"),
	}
	// Defensive fallback for callers that manually construct a background spec
	// instead of going through buildTaskSpec.
	if isWriter && spec.Grant.WritePaths.Empty() && spec.Sched.RunInBackground {
		whole, werr := WholeWorkspaceWriteClaim(t.workspaceRoot)
		if werr != nil {
			return t.failBeforeSubagentRelease(run, werr)
		}
		acquireReq.WritePaths = whole
		spec.Grant.WritePaths = whole
	}

	recoveryTaskID := subagentRecoveryTaskID(ctx, run.Ref)
	backgroundWriter := (spec.Sched.RunInBackground || spec.Sched.BackgroundWriter) && !spec.Grant.ReadOnly
	var mutationObserver *checkpoint.MutationObserver
	if t.mutationObserver != nil {
		turn := t.mutationObserver.OwnershipTurn()
		mutationObserver = t.mutationObserver.CloneForSubagent(recoveryTaskID, turn, backgroundWriter)
	}
	runSession := func(runCtx context.Context, sink event.Sink, writerAlreadyRegistered bool) (string, error) {
		if mutationObserver != nil && backgroundWriter && !writerAlreadyRegistered {
			turn := mutationObserver.OwnershipTurn()
			if err := mutationObserver.RegisterWriter(recoveryTaskID, "background_subagent", turn); err != nil {
				return "", err
			}
			defer mutationObserver.UnregisterWriter(recoveryTaskID)
		}
		prompt := composeChildTaskPrompt(spec)
		// 任务616: a continued transcript receives everything that arrived in
		// the source ref's mailbox while the sub-agent was not running. The
		// drain happens at run start so messages sent in the park window are
		// delivered exactly once and marked delivered on disk.
		if block := t.mailboxes.DrainForContinue(spec.Context.ContinueFrom); block != "" {
			prompt = block + "\n\n" + prompt
		}
		if spec.Grant.ReadOnly {
			return t.runReadOnlySubSession(runCtx, prompt, subReg, sink, maxSteps, prov, pricing, ctxWin, run.Session, childDepth, recoveryTaskID, usageModelRef, mutationObserver, mailbox)
		}
		return t.runSubSession(WithSubagentWriteClaim(runCtx, spec.Grant.WritePaths), prompt, subReg, sink, maxSteps, prov, pricing, ctxWin, run.Session, childDepth, recoveryTaskID, usageModelRef, mutationObserver, childWriteRoots, mailbox)
	}

	if spec.Sched.RunInBackground {
		result, runErr, handedOff := t.runBackgroundProfileSpec(ctx, spec, run, trk, parentID, parentSink, usageModelRef, effortRef, runSession, acquireReq, mutationObserver, backgroundWriter, recoveryTaskID)
		backgroundHandoff = handedOff
		return result, runErr
	}

	// Foreground: acquire a slot (queue if needed), then run synchronously.
	releaseSlot, claimID, err := t.acquireSlot(ctx, acquireReq)
	if err != nil {
		return t.failedSubagentResult(run, err)
	}
	defer releaseSlot()
	defer run.Release()
	ctx = WithSubagentClaimID(ctx, claimID)
	emitSubagentLifecycle(parentSink, "child_running", parentID, spec.Worker.Name, usageModelRef, effortRef, run, nil, BackgroundOwnedLifecycle(ctx))
	answer, err := runSession(ctx, trk.wrap(), false)
	if err != nil {
		result, runErr := t.resolveAmbiguousSubagentFailure(ctx, run, spec.Task.Objective, usageModelRef, parentSink, err)
		phase, outcome := terminalSubagentLifecycle(runErr)
		emitSubagentLifecycle(parentSink, phase, parentID, spec.Worker.Name, usageModelRef, effortRef, run, outcome, BackgroundOwnedLifecycle(ctx))
		return result, runErr
	}
	if t.transcripts != nil && run.Ref != "" {
		if err := t.transcripts.SaveCompleted(run); err != nil {
			result, runErr := t.failedSubagentResult(run, err)
			phase, outcome := terminalSubagentLifecycle(runErr)
			emitSubagentLifecycle(parentSink, phase, parentID, spec.Worker.Name, usageModelRef, effortRef, run, outcome, BackgroundOwnedLifecycle(ctx))
			return result, runErr
		}
		emitSubagentLifecycle(parentSink, "child_completed", parentID, spec.Worker.Name, usageModelRef, effortRef, run, &SubagentOutcome{Status: SubagentOutcomeCompleted, FinalAnswer: answer}, BackgroundOwnedLifecycle(ctx))
		return FormatSubagentRunResult(answer, run, false), nil
	}
	return GuardSubagentHostDecisionText(answer), nil
}

func (t *TaskTool) runBackgroundProfileSpec(ctx context.Context, spec ProfileExecSpec, run *SubagentRun, trk *subagentProgressTracker, parentID string, parentSink event.Sink, usageModelRef, effortRef string,
	runSession func(context.Context, event.Sink, bool) (string, error), acquireReq AcquireRequest, mutationObserver *checkpoint.MutationObserver, backgroundWriter bool, recoveryTaskID string,
) (string, error, bool) {
	jm, ok := jobs.FromContext(ctx)
	if !ok {
		result, err := t.failBeforeSubagentRelease(run, fmt.Errorf("background execution is not available in this context"))
		return result, err, false
	}
	var releaseStart func()
	if t.scheduler == nil {
		var running int
		var okReserve bool
		releaseStart, running, okReserve = jm.ReserveStartForSession(jobs.SessionFromContext(ctx), "task", maxConcurrentBackgroundTasks)
		if !okReserve {
			result, err := t.failBeforeSubagentRelease(run, fmt.Errorf("%d background tasks are already running for this session (limit %d); collect their results with wait — or run this sub-task in the foreground — before starting more", running, maxConcurrentBackgroundTasks))
			return result, err, false
		}
		defer releaseStart()
	} else {
		releaseStart = func() {}
	}
	label := firstNonEmpty(spec.Task.Description, spec.Worker.Name, "task")
	if t.transcripts != nil && run != nil && run.Ref != "" {
		if err := t.transcripts.MarkRunning(run); err != nil {
			releaseStart()
			result, saveErr := t.failBeforeSubagentRelease(run, err)
			return result, saveErr, false
		}
	}
	writerRegistered := false
	if mutationObserver != nil && backgroundWriter {
		turn := mutationObserver.OwnershipTurn()
		if err := mutationObserver.RegisterWriter(recoveryTaskID, "background_subagent", turn); err != nil {
			releaseStart()
			result, saveErr := t.failBeforeSubagentRelease(run, err)
			return result, saveErr, false
		}
		writerRegistered = true
	}
	parentSession := ParentSession(ctx)
	backgroundEvidence := evidence.NewLedger()
	slotReq := acquireReq
	trk.queued()
	job := jm.StartForSession(jobs.SessionFromContext(ctx), "task", label, func(jobCtx context.Context, _ io.Writer) (result string, err error) {
		if writerRegistered {
			defer mutationObserver.UnregisterWriter(recoveryTaskID)
		}
		jobCtx = WithParentSession(jobCtx, parentSession)
		jobCtx = withInheritedHostConstraints(ctx, jobCtx)
		jobCtx = evidence.WithLedger(jobCtx, backgroundEvidence)
		// Task 557: this child is job-owned; its lifecycle events must never
		// enter the desktop foreground-subagent registry (the job row already
		// represents it).
		jobCtx = withBackgroundOwnedLifecycle(jobCtx)
		defer run.Release()
		defer publishBackgroundEvidence(jobCtx, backgroundEvidence, t.workspaceRoot)
		defer func() {
			if r := recover(); r != nil {
				panicErr := fmt.Errorf("internal error: panic: %v\n%s", r, debug.Stack())
				result, err = t.failedSubagentResult(run, panicErr)
			}
			phase, outcome := terminalSubagentLifecycle(err)
			emitSubagentLifecycle(parentSink, phase, parentID, spec.Worker.Name, usageModelRef, effortRef, run, outcome, true)
			trk.finish(jobCtx.Err(), err)
		}()
		releaseSlot, claimID, slotErr := t.acquireSlot(jobCtx, slotReq)
		if slotErr != nil {
			return t.failedSubagentResult(run, slotErr)
		}
		defer releaseSlot()
		jobCtx = WithSubagentClaimID(jobCtx, claimID)
		trk.running()
		emitSubagentLifecycle(parentSink, "child_running", parentID, spec.Worker.Name, usageModelRef, effortRef, run, nil, true)
		answer, err := runSession(jobCtx, trk.wrap(), writerRegistered)
		if err != nil {
			return t.resolveAmbiguousSubagentFailure(jobCtx, run, spec.Task.Objective, usageModelRef, parentSink, err)
		}
		if err := t.transcripts.SaveCompleted(run); err != nil {
			return t.failedSubagentResult(run, err)
		}
		return FormatSubagentRunResult(answer, run, false), nil
	})
	releaseStart()
	queuedNote := ""
	if t.scheduler != nil {
		queuedNote = " It may wait in the session queue until a concurrency/write slot is free."
	}
	if run != nil && run.Ref != "" {
		return fmt.Sprintf("Started background task %q (%s).%s\n%s\nIt runs across turns; collect its final answer with wait (or wait will return it once done), and you'll be notified when it finishes.", job.ID, label, queuedNote, FormatSubagentReference(run)), nil, true
	}
	return fmt.Sprintf("Started background task %q (%s).%s It runs across turns; collect its final answer with wait (or wait will return it once done), and you'll be notified when it finishes.", job.ID, label, queuedNote), nil, true
}

func (t *TaskTool) acquireSlot(ctx context.Context, req AcquireRequest) (func(), int64, error) {
	noop := func() {}
	if t.scheduler == nil {
		return noop, 0, nil
	}
	return t.scheduler.AcquireWithID(ctx, req)
}

func (t *TaskTool) bashCanEnforceWriteRoots() bool {
	if t != nil && t.bashSandboxEnforced != nil {
		return t.bashSandboxEnforced()
	}
	return false
}

func (t *TaskTool) prepareTranscriptRunWithPrompt(ctx context.Context, subReg *tool.Registry, modelRef, effortRef, parentSession, parentID, continueFrom, legacyForkFrom, systemPrompt, kind, name string) (*SubagentRun, error) {
	continueFrom = strings.TrimSpace(continueFrom)
	legacyForkFrom = strings.TrimSpace(legacyForkFrom)
	parentSession = strings.TrimSpace(parentSession)
	if continueFrom != "" && legacyForkFrom != "" {
		return nil, fmt.Errorf("continue_from and fork_from are mutually exclusive; pass only continue_from")
	}
	if t.transcripts == nil {
		return nil, fmt.Errorf("subagent transcript store is required")
	}
	if systemPrompt == "" {
		systemPrompt = t.sysPrompt
	}
	if kind == "" {
		kind = "task"
	}
	if name == "" {
		name = "task"
	}
	if parentSession == "" {
		if continueFrom != "" || legacyForkFrom != "" {
			return nil, fmt.Errorf("subagent continuation requires a persisted session; none is active in this run")
		}
		return EphemeralSubagentRun(systemPrompt), nil
	}
	identityModel, identityEffort := t.effectiveIdentity(modelRef, effortRef)
	spec := SubagentSpec{
		Kind:             kind,
		Name:             name,
		WorkspaceRoot:    t.workspaceRoot,
		ParentSession:    parentSession,
		ParentToolCallID: parentID,
		SystemPrompt:     systemPrompt,
		Registry:         subReg,
		ToolContext:      childToolIdentityContext(ctx),
		Model:            identityModel,
		Effort:           identityEffort,
		ResumedFrom:      firstNonEmpty(continueFrom, legacyForkFrom),
	}
	if continueFrom != "" {
		return t.transcripts.PrepareContinue(continueFrom, spec)
	}
	if legacyForkFrom != "" {
		return t.transcripts.PrepareLegacyForkFrom(legacyForkFrom, spec)
	}
	return t.transcripts.PrepareFresh(spec)
}

func childToolIdentityContext(ctx context.Context) context.Context {
	ctx = tool.WithoutGoalTurnRecorder(ctx)
	ctx = memory.WithoutQueue(ctx)
	ctx = jobs.WithoutManager(ctx)
	return planmode.WithActive(ctx, PlanModeFromContext(ctx))
}

func (t *TaskTool) effectiveIdentity(modelRef, effort string) (string, string) {
	if t.identityProfile != nil {
		model, eff := t.identityProfile(modelRef, effort)
		return strings.TrimSpace(model), strings.TrimSpace(eff)
	}
	return t.effectiveModelIdentity(modelRef), t.effectiveEffortIdentity(effort)
}

// usageModelRef returns the canonical provider/model identity of the runtime
// selected for a child. The resolver expands aliases and supplies the parent
// model when no child override is configured.
func (t *TaskTool) usageModelRef(modelRef, effort string) string {
	model, _ := t.effectiveIdentity(modelRef, effort)
	if model != "" {
		return model
	}
	return firstNonEmpty(modelRef, t.baseModel, t.subagentModel)
}

func (t *TaskTool) effectiveModelIdentity(modelRef string) string {
	if strings.TrimSpace(modelRef) != "" {
		return strings.TrimSpace(modelRef)
	}
	return strings.TrimSpace(t.baseModel)
}

func (t *TaskTool) effectiveEffortIdentity(effort string) string {
	if strings.TrimSpace(effort) != "" {
		return strings.TrimSpace(effort)
	}
	return strings.TrimSpace(t.baseEffort)
}

// buildSubReg returns the sub-agent's tool set: the named whitelist (minus
// unavailable sub-agent tools), or every parent tool except those tools.
func (t *TaskTool) buildSubReg(names []string, childDepth int) *tool.Registry {
	return SubagentToolRegistryForDepthWithRuntime(t.parentReg, names, childDepth, t.maxDepth(), t.capabilityRuntime)
}

func (t *TaskTool) maxDepth() int {
	if t == nil {
		return DefaultMaxSubagentDepth
	}
	if t.maxSubagentDepth == 0 {
		return DefaultMaxSubagentDepth
	}
	return NormalizeMaxSubagentDepth(t.maxSubagentDepth)
}

func (t *TaskTool) nextSubagentDepth(ctx context.Context) (int, error) {
	current := SubagentDepth(ctx)
	next := current + 1
	maxDepth := t.maxDepth()
	if next > maxDepth {
		return 0, fmt.Errorf("subagent delegation depth limit reached (max_subagent_depth=%d)", maxDepth)
	}
	return next, nil
}

func (t *restrictedCapabilityProxy) ClassifyCall(args json.RawMessage) tool.CallClass {
	if t == nil || t.check(args) != nil {
		return tool.CallClass{}
	}
	classifier, ok := t.Tool.(tool.BatchClassifier)
	if !ok {
		return tool.CallClass{}
	}
	return classifier.ClassifyCall(args)
}

// Description is fixed: never embed dynamic capability IDs (they change with
// MCP install/tool-list and would break the stable provider tool prefix).
func (t *restrictedCapabilityProxy) Description() string {
	return t.Tool.Description()
}

func (t *restrictedCapabilityProxy) check(args json.RawMessage) error {
	var p struct {
		Action       string `json:"action"`
		CapabilityID string `json:"capability_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if action == "list" || action == "search" {
		return nil
	}
	id := strings.TrimSpace(p.CapabilityID)
	if id == "" {
		return fmt.Errorf("capability_id is required")
	}
	if id == sessionToolResultCapabilityID || id == sessionReadStrategyReceiptCapabilityID {
		return nil
	}
	if !t.allowed[id] {
		return fmt.Errorf("capability %q is outside this subagent's allowed-tools", id)
	}
	return nil
}

func (t *restrictedCapabilityProxy) ResolveCall(ctx context.Context, args json.RawMessage) (tool.ResolvedCall, error) {
	if err := t.check(args); err != nil {
		return tool.ResolvedCall{}, err
	}
	rc, err := t.resolver.ResolveCall(ctx, args)
	if err != nil {
		return rc, err
	}
	var p struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(args, &p)
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if rc.SkipExecute {
		switch action {
		case "list":
			rc.Result = filterCapabilityListResult(rc.Result, t.servers)
		case "search":
			rc.Result = filterCapabilitySearchResult(rc.Result, t.allowed)
		}
	}
	return rc, nil
}

func (t *restrictedCapabilityProxy) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := t.check(args); err != nil {
		return "", err
	}
	out, err := t.Tool.Execute(ctx, args)
	if err != nil {
		return out, err
	}
	var p struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(args, &p)
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "list":
		return filterCapabilityListResult(out, t.servers), nil
	case "search":
		return filterCapabilitySearchResult(out, t.allowed), nil
	}
	return out, nil
}

// forwardTurnAttachments opts the child in to the parent turn's resolved
// attachment candidates (task 551: the task-244 B9 rejection gate is removed —
// it judged the turn-level candidates instead of the subagent's own inputs and
// blocked every image-bearing dispatch with a misleading message). The child
// provider owns the final vision decision: text-only providers retain the
// attachment metadata but omit image parts during serialization. Because this
// opt-in rides an inherited ctx criterion, it is logged with structured fields
// — before task 551 nothing recorded who received which attachments, and the
// misattribution cost two wrong diagnoses. Standing rule: any branch that
// rejects or degrades on an inherited ctx criterion must slog.Warn with the
// same fields (criterion source, value, decision).
func forwardTurnAttachments(ctx context.Context, modelRef, session string) context.Context {
	candidates := SubagentImageCandidates(ctx)
	if len(candidates) == 0 {
		return ctx
	}
	slog.Info("subagent: forwarding turn attachments",
		"modelRef", modelRef,
		"count", len(candidates),
		"session", session)
	return WithUserImages(ctx, candidates)
}

func (t *TaskTool) resolveSubSessionRuntime(modelRef, effort string) (provider.Provider, *provider.Pricing, int, error) {
	prov, pricing, ctxWin := t.prov, t.pricing, t.contextWindow
	if t.resolveProvider != nil && (modelRef != "" || effort != "") {
		p, pr, cw, err := t.resolveProvider(modelRef, effort)
		if err != nil {
			return nil, nil, 0, err
		}
		prov, pricing, ctxWin = p, pr, cw
	}
	return prov, pricing, ctxWin, nil
}

func (t *TaskTool) runSubSession(ctx context.Context, prompt string, subReg *tool.Registry, sink event.Sink, maxSteps int, prov provider.Provider, pricing *provider.Pricing, ctxWin int, sess *Session, childDepth int, recoveryTaskID, modelRef string, mutationObserver *checkpoint.MutationObserver, writeRoots *sandbox.WritableRootSet, mailbox *subagentmailbox.Mailbox) (string, error) {
	opts := t.subagentOptions(ctx, maxSteps, pricing, ctxWin, childDepth, recoveryTaskID, mutationObserver)
	if writeRoots != nil {
		opts.WriteRoots = writeRoots
	}
	opts.SubagentMailbox = mailbox
	opts.ModelRef = modelRef
	// Capture the pristine task before host framing is prepended: delivery
	// intent classification must judge the task, not the wrapper.
	opts.ClassifierTaskText = prompt
	prompt = t.withWorkspaceContext(prompt) + "\n\n" + completeSubtaskContract
	// The child provider owns the final vision decision (task 551: the B9
	// rejection gate is gone). Text-only providers retain the attachment
	// metadata but omit image parts during serialization.
	ctx = forwardTurnAttachments(ctx, modelRef, subagentSessionName(sess, recoveryTaskID))
	return RunSubAgentWithSession(ctx, prov, subReg, sess, prompt, opts, sink)
}

func (t *TaskTool) runReadOnlySubSession(ctx context.Context, prompt string, subReg *tool.Registry, sink event.Sink, maxSteps int, prov provider.Provider, pricing *provider.Pricing, ctxWin int, sess *Session, childDepth int, recoveryTaskID, modelRef string, mutationObserver *checkpoint.MutationObserver, mailbox *subagentmailbox.Mailbox) (string, error) {
	opts := t.subagentOptions(ctx, maxSteps, pricing, ctxWin, childDepth, recoveryTaskID, mutationObserver)
	opts.SubagentMailbox = mailbox
	opts.ModelRef = modelRef
	// Capture the pristine task before host framing is prepended: delivery
	// intent classification must judge the task, not the wrapper.
	opts.ClassifierTaskText = prompt
	prompt = t.withWorkspaceContext(prompt)
	ctx = forwardTurnAttachments(ctx, modelRef, subagentSessionName(sess, recoveryTaskID))
	return RunReadOnlySubAgentWithSession(ctx, prov, subReg, sess, prompt, opts, sink)
}

func subagentRecoveryTaskID(ctx context.Context, ref string) string {
	if ref = strings.TrimSpace(ref); ref != "" {
		return "subagent:" + ref
	}
	if callID, _, _, ok := CallContext(ctx); ok && strings.TrimSpace(callID) != "" {
		return "subagent:" + strings.TrimSpace(callID)
	}
	return "subagent"
}

// WithRecoveryGate shares Auto Guard with spawned sub-agents.
func (t *TaskTool) WithWriteRoots(set *sandbox.WritableRootSet) *TaskTool {
	if t == nil {
		return nil
	}
	t.writeRoots = set
	return t
}

func (t *TaskTool) WithRecoveryGate(g RecoveryGate) *TaskTool {
	if t == nil {
		return nil
	}
	t.recoveryGate = g
	return t
}

// WithMutationObserver shares the host mutation observer with spawned sub-agents.
// Foreground children inherit the parent ownership turn; background children
// keep the turn that spawned them (set via OwnershipTurn at Begin).
func (t *TaskTool) WithMutationObserver(obs *checkpoint.MutationObserver) *TaskTool {
	if t == nil {
		return nil
	}
	t.mutationObserver = obs
	return t
}

func (t *TaskTool) withWorkspaceContext(prompt string) string {
	if t == nil {
		return prompt
	}
	ctx := subagentWorkspaceContext(t.workspaceRoot)
	if ctx == "" {
		return prompt
	}
	return ctx + "\n\n" + prompt
}

func subagentWorkspaceContext(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	// Wording note: avoid incidental action verbs ("resolve", "fix", …) in this
	// host framing — it is prepended to every sub-agent prompt and must never
	// read as task intent (see classifierTaskText, which also strips it).
	return `<workspace-context event="SubagentWorkspace">
Current workspace: ` + strconv.Quote(root) + `
File tools interpret relative paths against this workspace. For project inspection, prefer "." or relative paths unless the user explicitly named another absolute path.
</workspace-context>`
}

func FormatSubagentReference(run *SubagentRun) string {
	if run == nil || run.Ref == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Subagent reference: %s\n", run.Ref)
	if strings.TrimSpace(run.ForkedFrom) != "" {
		fmt.Fprintf(&b, "Forked from: %s\n", strings.TrimSpace(run.ForkedFrom))
		b.WriteString("The requested ref resolves to an ancestor conversation transcript, so the framework continues a copy owned by the current conversation. To continue this copied subagent transcript in a later call, pass ")
		b.WriteString(run.Ref)
		b.WriteString(" as `continue_from`. Start a fresh subagent when the next task is independent.")
		return b.String()
	}
	b.WriteString("To continue this same subagent transcript in a later call, pass this ref as `continue_from`. Start a fresh subagent when the next task is independent.")
	return b.String()
}

// GuardSubagentHostDecisionText appends a fixed boundary warning only when a
// child agent result appears to discuss host approval or user-owned decisions.
// The implementation lives in internal/tool so the skill tools share the exact
// same phrase list and notice.
func GuardSubagentHostDecisionText(answer string) string {
	return tool.GuardSubagentHostDecisionText(answer)
}

// EvidenceSummary exports this agent's turn-scoped receipts for parent merge.
func (a *Agent) EvidenceSummary() evidence.ChildEvidenceSummary {
	if a == nil || a.task.ledger == nil {
		return evidence.ChildEvidenceSummary{}
	}
	return a.task.ledger.Summary()
}

// NestedSink returns a sink that forwards a sub-agent's tool activity to the
// parent stream, nested under the tool call carried by ctx, so a frontend shows
// it beneath that call (the same nesting `task` uses). Falls back to the given
// sink when ctx carries no call context. Used by subagent skills.
func NestedSink(ctx context.Context, fallback event.Sink) event.Sink {
	parentID, parent, _, ok := CallContext(ctx)
	if !ok || parent == nil {
		return fallback
	}
	return subSinkFor(parentID, parent)
}

// subSink forwards a sub-agent's tool dispatch/result/progress events and
// billable usage to the parent's event stream. Only tool activity is nested
// visually; the sub-agent's text/reasoning stays isolated (progress previews
// travel as reserved ToolProgress channels, not as parent Text/Reasoning) and
// only its final answer is returned.
//
// The sub-agent's own turn/text/reasoning events are dropped — forwarding them
// would make the parent transcript noisy and could imply they belong to the
// parent model context, which they do not.
//
// Usage events are observability only, so forwarding them preserves billing
// totals without polluting the parent provider-visible prefix.
//
// Tool events are tagged with the parent task call's ID so a frontend nests them
// under it. The forwarded call IDs are namespaced with the parent ID so a
// sub-agent call can never collide with a parent call in the frontend's
// dispatch→result matching. ToolProgress covers both the sub-agent's real tool
// output and nested sub-agent progress previews, which ride the same sink so
// their IDs match the cards they belong to. Falls back to Discard when there's
// no parent stream (the headless run loop, or a direct Execute in tests).
func subSink(ctx context.Context) event.Sink {
	parentID, parent, _, ok := CallContext(ctx)
	if !ok || parent == nil {
		return event.Discard
	}
	return subSinkFor(parentID, parent)
}

// subSinkFor builds the nesting sink from an already-captured parent ID + stream,
// for the background path where the job runs under a context that no longer
// carries the call context. Falls back to Discard when there's no parent stream.
func subSinkFor(parentID string, parent event.Sink) event.Sink {
	if parent == nil {
		return event.Discard
	}
	return nestedSink{AuditForwarder: event.AuditForwarder{Inner: parent}, parentID: parentID, parent: parent}
}
