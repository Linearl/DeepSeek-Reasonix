package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/permission"
	"reasonix/internal/sandbox"
	"reasonix/internal/skill"
	"reasonix/internal/subagentmailbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// listSubagentsViewLimit bounds the directory listing payload (task 447).
// Sub-agent transcripts are keyed per invocation, so a long-lived session can
// accumulate hundreds; the capsule panel only needs the recent tail.
const listSubagentsViewLimit = 200

// SubagentArtifactView is the whitelisted projection of agent.SubagentMeta
// for the frontend (task 447 capsule). Deliberately narrow: execution
// internals (system prompt hash, tool scope/schema hashes, context capsule)
// stay backend-only.
type SubagentArtifactView struct {
	Ref           string `json:"ref"`
	CreatedAt     int64  `json:"createdAt"` // unix milliseconds
	UpdatedAt     int64  `json:"updatedAt"` // unix milliseconds
	Status        string `json:"status"`    // running|completed|failed|interrupted
	Outcome       string `json:"outcome,omitempty"`
	Kind          string `json:"kind,omitempty"` // task|skill
	Name          string `json:"name,omitempty"`
	Model         string `json:"model,omitempty"`
	ParentSession string `json:"parentSession,omitempty"`
	// HasTranscript reports whether the <ref>.jsonl transcript exists on
	// disk. A meta can survive without its transcript after a crash; such
	// entries are listed but not openable.
	HasTranscript bool `json:"hasTranscript"`
	// PendingMail counts messages waiting in the sub-agent's mailbox
	// (任务616): persisted but not yet delivered to a running child. The
	// capsule shows it as an undelivered badge; delivery happens on the next
	// tool-round gap (running) or the next continue_from (parked).
	PendingMail int `json:"pendingMail"`
}

// ListSubagentsByParent lists the persisted sub-agent artifacts owned by the
// given parent session path (task 447 capsule panel "ended sub-agents"
// directory): newest first — createdAt desc, ref desc as the deterministic
// tie-break — capped at listSubagentsViewLimit. Read-only: a directory scan
// plus metadata reads, no store mutation and no lease acquisition. An empty
// session path yields an empty list, not an error: the panel calls this
// whenever the active tab has no bound session yet.
func (a *App) ListSubagentsByParent(sessionPath string) ([]SubagentArtifactView, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return []SubagentArtifactView{}, nil
	}
	dir, validated, err := a.sessionDirForPath(sessionPath)
	if err != nil {
		return nil, err
	}
	artifacts, err := agent.ListSubagentsByParent(dir, agent.BranchID(validated))
	if err != nil {
		return nil, err
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if !artifacts[i].Meta.CreatedAt.Equal(artifacts[j].Meta.CreatedAt) {
			return artifacts[i].Meta.CreatedAt.After(artifacts[j].Meta.CreatedAt)
		}
		return artifacts[i].Ref > artifacts[j].Ref
	})
	if len(artifacts) > listSubagentsViewLimit {
		artifacts = artifacts[:listSubagentsViewLimit]
	}
	out := make([]SubagentArtifactView, 0, len(artifacts))
	// 任务616: one hub re-used for every pending count; PendingCount is a
	// stat on a directory that usually does not exist, so the extra per-ref
	// cost is one failed ReadDir at most.
	mailHub := &subagentmailbox.Hub{Dir: filepath.Join(dir, "subagents")}
	for _, artifact := range artifacts {
		_, statErr := os.Stat(artifact.SessionPath)
		out = append(out, SubagentArtifactView{
			Ref:           artifact.Ref,
			CreatedAt:     artifact.Meta.CreatedAt.UnixMilli(),
			UpdatedAt:     artifact.Meta.UpdatedAt.UnixMilli(),
			Status:        string(artifact.Meta.Status),
			Outcome:       artifact.Meta.Outcome,
			Kind:          artifact.Meta.Kind,
			Name:          artifact.Meta.Name,
			Model:         artifact.Meta.Model,
			ParentSession: artifact.Meta.ParentSession,
			HasTranscript: statErr == nil,
			PendingMail:   mailHub.PendingCount(artifact.Ref),
		})
	}
	return out, nil
}

// ReadSubagentSession returns one sub-agent transcript owned by the given
// parent session, rendered through the same preview pipeline the history
// drawer uses (previewSessionMessages), so the capsule history view shows
// exactly what PreviewSession would for that file. The ref must appear in
// the parent's own artifact list — membership is proven before any path is
// built, so a traversal-shaped ref can only fail with "not found".
func (a *App) ReadSubagentSession(sessionPath, ref string) ([]HistoryMessage, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	ref = strings.TrimSpace(ref)
	if sessionPath == "" {
		return nil, fmt.Errorf("empty session path")
	}
	if ref == "" {
		return nil, fmt.Errorf("empty subagent reference")
	}
	dir, validated, err := a.sessionDirForPath(sessionPath)
	if err != nil {
		return nil, err
	}
	artifacts, err := agent.ListSubagentsByParent(dir, agent.BranchID(validated))
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		if artifact.Ref != ref {
			continue
		}
		if _, err := os.Stat(artifact.SessionPath); err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("subagent transcript %q has not been saved yet", ref)
			}
			return nil, err
		}
		return previewSessionMessages(dir, artifact.SessionPath)
	}
	return nil, fmt.Errorf("subagent %q does not belong to this session", ref)
}

// DeleteSubagentRecord permanently removes one ended sub-agent artifact owned
// by the given parent session — transcript, sidecar files (event log, event
// index, …), and metadata. Task 558 capsule "delete record": the record
// disappears from the ended directory and its files are gone, not merely
// hidden. Delegation and ownership live in agent.DeleteSubagentArtifact: the
// ref must appear in the session's own artifact list, and a still-running
// record is refused (the ended directory never lists one anyway).
func (a *App) DeleteSubagentRecord(sessionPath, ref string) error {
	sessionPath = strings.TrimSpace(sessionPath)
	ref = strings.TrimSpace(ref)
	if sessionPath == "" {
		return fmt.Errorf("empty session path")
	}
	if ref == "" {
		return fmt.Errorf("empty subagent reference")
	}
	dir, validated, err := a.sessionDirForPath(sessionPath)
	if err != nil {
		return err
	}
	return agent.DeleteSubagentArtifact(dir, agent.BranchID(validated), ref)
}

// ClearEndedSubagents permanently removes every ended (non-running) sub-agent
// artifact owned by the given parent session and returns how many records
// were removed. Task 558 capsule "clear ended": running invocations are left
// untouched, and a rerun after a partial failure converges on the empty
// directory instead of erroring on what a previous pass already deleted.
func (a *App) ClearEndedSubagents(sessionPath string) (int, error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return 0, fmt.Errorf("empty session path")
	}
	dir, validated, err := a.sessionDirForPath(sessionPath)
	if err != nil {
		return 0, err
	}
	return agent.DeleteEndedSubagents(dir, agent.BranchID(validated))
}

// SubagentProfileInput is the desktop-bound shape for authoring a subagent
// profile. Named SubagentProfile* rather than bare Subagent* to stay distinct
// from internal/agent's Subagent* run-transcript types: this is a saved
// authoring profile (a skill file), not a runtime record of one execution.
//
// A profile is always written with runAs=subagent and invocation=manual — it
// stays invocable by name (/<name> <task>, run_skill) but never enters the pinned
// Skills index the model scans for candidates to call on its own initiative
// (see internal/skill/index.go). This is deliberate: a profile authored
// through a settings form has no triggers/auto-use tuning, so nothing about
// it signals the model should discover it unprompted.
type SubagentProfileInput struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"systemPrompt"`
	Color        string   `json:"color"`
	Model        string   `json:"model"`
	Effort       string   `json:"effort"`
	AllowedTools []string `json:"allowedTools"`
	// ReadOnly, when true, writes frontmatter read-only: true. Omitted/false
	// keeps the legacy writable default for older profiles.
	ReadOnly bool `json:"readOnly"`
	// Scope is "project" or "global" (empty defaults to global on create).
	Scope string `json:"scope"`
}

func createSubagentProfileScope(raw string) (skill.Scope, error) {
	if strings.TrimSpace(raw) == "" {
		return skill.ScopeGlobal, nil
	}
	return editableSubagentProfileScope(raw)
}

func editableSubagentProfileScope(raw string) (skill.Scope, error) {
	switch strings.TrimSpace(raw) {
	case "project":
		return skill.ScopeProject, nil
	case "global":
		return skill.ScopeGlobal, nil
	default:
		return "", fmt.Errorf("unsupported subagent profile scope %q — manage custom-path skills from the Skills page", raw)
	}
}

// CreateSubagentProfile writes a new user-authored subagent profile and
// returns its file path. Refuses a name that collides with a built-in
// subagent skill (explore/research/review/security-review) — Store.List's
// dedup rules let a same-named user file silently shadow the built-in
// everywhere, including the dedicated top-level explore/review tools, so this
// must be caught here rather than left to the generic CreateWithContent
// same-scope-only overwrite check.
func (a *App) CreateSubagentProfile(input SubagentProfileInput) (string, error) {
	name := strings.TrimSpace(input.Name)
	desc := strings.TrimSpace(input.Description)
	if desc == "" {
		return "", fmt.Errorf("description is required")
	}
	prompt := strings.TrimSpace(input.SystemPrompt)
	if prompt == "" {
		return "", fmt.Errorf("system prompt is required")
	}
	scope, err := createSubagentProfileScope(input.Scope)
	if err != nil {
		return "", err
	}

	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return "", fmt.Errorf("no active session")
	}
	// Refuse before writing anything: the post-save RefreshSkills rebuild is
	// rejected while the controller has a running turn, pending prompt, or
	// background jobs, and a profile file already written by then would strand
	// the UI — the save reports failure, the list never refreshes, and a retry
	// hits "already exists". Same precheck order as applyConfigChange.
	if err := a.ensureActiveTabRebuildAllowed("subagents"); err != nil {
		return "", err
	}
	occupied := make([]string, 0)
	for _, existing := range ctrl.AllSkills() {
		occupied = append(occupied, existing.Name, existing.SlashName())
	}
	for _, command := range ctrl.Commands() {
		occupied = append(occupied, command.Name)
	}
	if host := ctrl.Host(); host != nil {
		for _, prompt := range host.Prompts() {
			occupied = append(occupied, prompt.Name)
		}
	}
	if err := skill.ValidateSubagentProfileName(name, occupied); err != nil {
		return "", err
	}

	content := skill.RenderSkillFile(skill.SkillFileOptions{
		Name:         name,
		Description:  desc,
		Body:         prompt,
		RunAs:        skill.RunSubagent,
		Model:        strings.TrimSpace(input.Model),
		Effort:       strings.TrimSpace(input.Effort),
		AllowedTools: input.AllowedTools,
		ReadOnly:     input.ReadOnly,
		Color:        strings.TrimSpace(input.Color),
		Invocation:   "manual",
	})
	path, err := ctrl.CreateSkill(name, scope, content)
	if err != nil {
		return "", err
	}
	// Mirrors RefreshSkills/SetSkillEnabled: degrade a lease-held rebuild to a
	// deferred warning (the file is already saved), fail hard on a real error.
	if err := a.RefreshSkills(); err != nil {
		return "", err
	}
	return path, nil
}

// UpdateSubagentProfile overwrites an existing user-authored subagent
// profile's content in place. name and scope are the profile's identity and
// are not editable through this call — the frontend keeps them read-only in
// the edit form, since renaming or re-scoping would mean moving the file
// (delete-then-create), a separate operation this repo doesn't support yet.
// input.Name/input.Scope are ignored in favor of the name/scope params.
//
// Only profiles this page could have written are editable — see
// editableSubagentProfile. This is the backend enforcement of the same rule
// the frontend applies by filtering its list to invocation=manual.
func (a *App) UpdateSubagentProfile(name, scope string, input SubagentProfileInput) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	desc := strings.TrimSpace(input.Description)
	if desc == "" {
		return fmt.Errorf("description is required")
	}
	prompt := strings.TrimSpace(input.SystemPrompt)
	if prompt == "" {
		return fmt.Errorf("system prompt is required")
	}
	targetScope, err := editableSubagentProfileScope(scope)
	if err != nil {
		return err
	}

	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	// See CreateSubagentProfile: refuse before writing so a busy-rejected
	// rebuild can't leave the file changed while the UI reports failure.
	if err := a.ensureActiveTabRebuildAllowed("subagents"); err != nil {
		return err
	}
	found := false
	for _, sk := range ctrl.AllSkills() {
		if config.SkillNameKey(sk.Name) != config.SkillNameKey(name) {
			continue
		}
		found = true
		if sk.Scope != targetScope {
			return fmt.Errorf("%q scope mismatch: requested %q, current scope is %q", name, targetScope, sk.Scope)
		}
		if err := skill.ValidateEditableSubagentProfile(sk); err != nil {
			return err
		}
		break
	}
	if !found {
		return fmt.Errorf("%q not found", name)
	}

	content := skill.RenderSkillFile(skill.SkillFileOptions{
		Name:         name,
		Description:  desc,
		Body:         prompt,
		RunAs:        skill.RunSubagent,
		Model:        strings.TrimSpace(input.Model),
		Effort:       strings.TrimSpace(input.Effort),
		AllowedTools: input.AllowedTools,
		ReadOnly:     input.ReadOnly,
		Color:        strings.TrimSpace(input.Color),
		Invocation:   "manual",
	})
	if err := ctrl.UpdateSkill(name, targetScope, content); err != nil {
		return err
	}
	if err := a.RefreshSkills(); err != nil {
		return err
	}
	return nil
}

// DeleteSubagentProfile removes a user-authored subagent profile. scope must
// match what the caller most recently saw for this name (SkillView.Scope) —
// Store.Delete refuses a scope mismatch rather than guessing, so a stale
// client-side scope fails safely instead of deleting the wrong file.
func (a *App) DeleteSubagentProfile(name, scope string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	targetScope, err := editableSubagentProfileScope(scope)
	if err != nil {
		return err
	}
	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	// See CreateSubagentProfile: refuse before deleting so a busy-rejected
	// rebuild can't remove the file while the UI reports failure and keeps
	// listing the profile.
	if err := a.ensureActiveTabRebuildAllowed("subagents"); err != nil {
		return err
	}
	// Re-resolve the target and apply the full profile-identity check before
	// deleting: the generic DeleteSkill removes any user skill matching
	// name+scope, so a stale UI list (the file changed after load) or a direct
	// bridge call could otherwise delete an unrelated hand-authored skill this
	// page never owned.
	found := false
	for _, sk := range ctrl.AllSkills() {
		if config.SkillNameKey(sk.Name) != config.SkillNameKey(name) {
			continue
		}
		found = true
		if sk.Scope != targetScope {
			return fmt.Errorf("%q scope mismatch: requested %q, current scope is %q", name, targetScope, sk.Scope)
		}
		if err := skill.ValidateEditableSubagentProfile(sk); err != nil {
			return err
		}
		break
	}
	if !found {
		return fmt.Errorf("%q not found", name)
	}
	if err := ctrl.DeleteSkill(name, targetScope); err != nil {
		return err
	}
	if err := a.RefreshSkills(); err != nil {
		return err
	}
	return nil
}

// TrySubagentProfile runs a subagent profile once, synchronously, fully
// isolated from any live session — it builds its own provider and tool
// registry straight from config, like the standalone `reasonix review` CLI
// command (internal/cli/review.go), and never touches Controller.RunSkill or
// any part of the Chat Runtime critical path. Because it needs nothing saved
// to disk, it runs directly against the caller's current form values (input),
// so a profile can be tried before Save.
//
// A try run is deliberately READ-ONLY regardless of the profile's tool scope:
// it is a settings-page preview, not a real work session, and it has no UI to
// answer approval prompts. ReadOnlySubagentToolRegistry strips writer tools
// and wraps bash in the permission-classified read-only command policy; the confined reader/
// search/fetch instances below enforce the same workspace boundaries the real
// boot path installs (boot.go addBuiltins), and the headless permission gate
// applies the user's configured deny rules.
func (a *App) TrySubagentProfile(input SubagentProfileInput, task string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	prompt := strings.TrimSpace(input.SystemPrompt)
	if prompt == "" {
		return "", fmt.Errorf("system prompt is required")
	}

	// One try run at a time, cancellable from the settings page and aborted
	// with the app context on shutdown — a runaway model loop must not burn
	// through all 12 steps with no way to stop it.
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	runCtx, cancel := context.WithCancel(base)
	a.tryRunMu.Lock()
	if a.tryRunCancel != nil {
		a.tryRunMu.Unlock()
		cancel()
		return "", fmt.Errorf("another try run is still in progress — cancel it or wait for it to finish")
	}
	a.tryRunCancel = cancel
	a.tryRunMu.Unlock()
	defer func() {
		a.tryRunMu.Lock()
		a.tryRunCancel = nil
		a.tryRunMu.Unlock()
		cancel()
	}()

	// Resolve config against the active tab's workspace, not the desktop
	// process's CWD — project-level reasonix.toml (sandbox roots, permissions)
	// must apply to the try run exactly as it would to a real session there.
	// Snapshot under the lock: WorkspaceRoot is rewritten under a.mu (spelling
	// normalization, session-binding redirects) and must not be read bare.
	root := ""
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil {
		root = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return "", err
	}
	modelRef := strings.TrimSpace(input.Model)
	if modelRef == "" {
		modelRef = strings.TrimSpace(cfg.Agent.SubagentModel)
	}
	if modelRef == "" {
		modelRef = cfg.DefaultModel
	}
	entry, ok := cfg.ResolveModel(modelRef)
	if !ok {
		return "", fmt.Errorf("unknown model %q", modelRef)
	}
	me := *entry
	if effort := strings.TrimSpace(input.Effort); effort != "" {
		normalized, err := config.NormalizeEffort(&me, effort)
		if err != nil {
			return "", err
		}
		me.Effort = normalized
		if me.Kind == "anthropic" && me.Effort != "" && strings.TrimSpace(me.Thinking) == "" {
			me.Thinking = "adaptive"
		}
	}
	prov, err := boot.NewProviderWithProxy(&me, cfg.NetworkProxySpec())
	if err != nil {
		return "", err
	}

	reg := trySubagentToolRegistry(cfg, root, input.AllowedTools)

	// The headless gate enforces the user's configured permission rules. A
	// subagent has no UI to answer an Ask decision, so deny and ask both block.
	policy := permission.New(cfg.Permissions.Mode, cfg.Permissions.Allow, cfg.Permissions.Ask, cfg.Permissions.Deny).
		WithAllowDynamicBashFallback(cfg.Permissions.AllowDynamicBash)

	result, err := agent.RunReadOnlySubAgentWithSession(runCtx, prov, reg, agent.NewSession(prompt), task, agent.Options{
		MaxSteps:      12,
		Temperature:   cfg.Agent.Temperature,
		Pricing:       me.Price,
		ContextWindow: me.ContextWindow,
		Gate:          trySubagentPermissionGate(policy),
	}, event.Discard)
	if err != nil {
		return "", err
	}
	return result, nil
}

// trySubagentPermissionGate pins the settings-page try runner to an explicit
// non-interactive Ask posture. Unlike the legacy bootstrap gate, this fails
// closed when a configured rule or writer fallback needs approval: the try
// runner has no approval UI that could answer such a request.
func trySubagentPermissionGate(policy permission.Policy) agent.Gate {
	return control.BuildHeadlessApprovalGate(policy, control.ToolApprovalAsk)
}

// CancelTrySubagentProfile aborts the in-flight settings-page try run, if
// any. The pending TrySubagentProfile call returns its context error.
func (a *App) CancelTrySubagentProfile() {
	a.tryRunMu.Lock()
	cancel := a.tryRunCancel
	a.tryRunMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// trySubagentToolRegistry builds the read-only, workspace-rooted tool set a
// try run may use. The parent set comes from builtin.Workspace — the same
// per-workspace assembly boot uses for desktop tabs — so every tool both
// enforces the configured confinement AND resolves relative paths against the
// active tab's root, not the desktop process CWD (which, in a multi-workspace
// session, would let a try run read or search a different project than the
// one on screen). Writers are stripped by the read-only registry — Workspace
// wiring them anyway keeps this byte-comparable with boot's assembly and safe
// if the read-only posture is ever relaxed.
func trySubagentToolRegistry(cfg *config.Config, root string, allowedTools []string) *tool.Registry {
	writeRoots := cfg.WriteRootsForRoot(root)
	forbidReadRoots := boot.RuntimeForbidReadRoots(cfg, root)
	bashSpec := sandbox.Spec{
		Mode:            cfg.BashMode(),
		WriteRoots:      writeRoots,
		ForbidReadRoots: forbidReadRoots,
		Network:         cfg.Sandbox.Network,
	}
	ws := builtin.Workspace{
		Dir:             root,
		WriteRoots:      writeRoots,
		ForbidReadRoots: forbidReadRoots,
		Bash:            bashSpec,
		BashTimeout:     time.Duration(cfg.BashTimeoutSeconds()) * time.Second,
		Search:          builtin.ResolveSearch(cfg.Tools.Search.Engine, cfg.Tools.Search.RgPath, io.Discard),
		ProxySpec:       cfg.NetworkProxySpec(),
		ReadPaths:       builtin.NewPathResolver(),
		SessionGuard:    builtin.NewSessionDataGuard(config.MemoryUserDir(), cfg.AllowWriteRoots()),
		ManagedConfig:   builtin.NewManagedConfigPaths(config.ReasonixManagedConfigPaths()),
	}
	parentReg := tool.NewRegistry()
	for _, tl := range ws.Tools() {
		parentReg.Add(tl)
	}
	// ReadOnlySubagentToolRegistry treats an empty allowedTools as "all" (the
	// "default all permissions" tool-scope option) and then keeps only
	// read-only tools plus policy-wrapped bash.
	return agent.ReadOnlySubagentToolRegistry(parentReg, allowedTools)
}
