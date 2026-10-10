package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/capability"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// hostProfileFor mirrors hostProfile for UseCapabilityTool, which holds its
// own host reference shared with the runtime.
func (t *UseCapabilityTool) hostProfileFor() plugin.HostProfile {
	if t == nil || t.host == nil {
		return plugin.HostProfileCore
	}
	return t.host.Profile()
}

// UseCapabilityTool is the stable MCP capability proxy for Delivery, the
// two-model Planner, and task/fleet sub-agents. It lists, inspects, calls, or
// declines catalog capabilities without adding dynamic MCP tools to the
// provider-visible registry — subsequent calls keep using this stable schema.
// Multiple frontends may share one MCPCapabilityRuntime (Host + connection
// state) while keeping independent ledger/audit.
type UseCapabilityTool struct {
	host *plugin.Host
	// lifeCtx is the session-scoped context that owns on-demand MCP child
	// processes (mirrors lazySpawn.ctx): a proxied server must outlive the tool
	// call that started it and die with the session, not with a resolve-phase
	// timeout. nil falls back to context.Background() for direct/test use.
	lifeCtx context.Context
	// specs are the boot-converted plugin specs (env expansion, workspace
	// overrides and timeouts). The proxy never rebuilds
	// specs from raw config entries — that would fork the conversion logic.
	specs    []plugin.Spec
	runtime  *MCPCapabilityRuntime
	registry *tool.Registry // live registry for already-exposed MCP tools
	ledger   *capability.Ledger
	audit    *capability.Audit
	catalog  func() capability.Catalog
	// toolResultSession is bound by Agent.New; clones leave it empty so parent transcripts cannot
	// leak into planner or child frontends before their Agent binds them.
	toolResultMu      sync.RWMutex
	toolResultSession func() *Session
	readStrategyState func() *incompleteReadState
	mcpListMu         sync.RWMutex
	mcpListObserver   func(mcpListObservation)
	// state is session-shared connection observation when built via
	// MCPCapabilityRuntime; nil falls back to a private map for tests.
	state *mcpProxySharedState
}

// NewUseCapabilityTool builds a standalone capability proxy (tests and simple
// boots). Prefer MCPCapabilityRuntime.NewFrontend when multiple agents share
// one session Host.
func NewUseCapabilityTool(lifeCtx context.Context, host *plugin.Host, specs []plugin.Spec, reg *tool.Registry, ledger *capability.Ledger, audit *capability.Audit, catalog func() capability.Catalog) *UseCapabilityTool {
	return &UseCapabilityTool{
		host:     host,
		lifeCtx:  lifeCtx,
		specs:    append([]plugin.Spec(nil), specs...),
		registry: reg,
		ledger:   ledger,
		audit:    audit,
		catalog:  catalog,
		state:    &mcpProxySharedState{connected: map[string]bool{}},
	}
}

// CloneForAgent returns a new frontend sharing Host/specs/connection state but
// with independent ledger and audit (nil unless provided).
func (t *UseCapabilityTool) CloneForAgent(ledger *capability.Ledger, audit *capability.Audit) *UseCapabilityTool {
	if t == nil {
		return nil
	}
	state := t.state
	if state == nil {
		state = &mcpProxySharedState{connected: map[string]bool{}}
	}
	clone := &UseCapabilityTool{
		host:     t.host,
		lifeCtx:  t.lifeCtx,
		specs:    t.specs,
		runtime:  t.runtime,
		registry: t.registry,
		ledger:   ledger,
		audit:    audit,
		catalog:  t.catalog,
		state:    state,
	}
	return clone
}

func (*UseCapabilityTool) Name() string { return tool.HostUseCapability }

func (*UseCapabilityTool) Description() string {
	return "Fixed-schema capability proxy. Prefer search(query, limit<=8), then inspect one exact capability, then call it. list is a compact diagnostic inventory only. Supports stable ids such as tool:grep, skill:review, mcp-tool:server/tool, task:subagent, workflow:name, and web:/lsp:/session:/memory: namespaces. memory:remember saves facts (description+body required; activation=\"relevant\" on create; omit activation on update; \"pinned\" only if user asks); memory:forget(name); tool:memory(operation=search|read|list). decline records a reason for a prefer capability. Independent list/search/inspect calls are read-only and may be issued together. Calls keep the provider-visible schema fixed; real writers still pass permission, plan mode, sandbox, write-path, and workspace-lease checks. ARGUMENT PACKING (common mistake): action and capability_id are TOP-LEVEL parameters of this tool. For action=call, the `arguments` value must be the TARGET TOOL'S OWN argument object exactly (e.g. {\"description\":..., \"body\":...} for memory:remember) — never wrap another {arguments:..., capability_id:...} envelope inside it. Error self-diagnosis: \"capability_id is required\" means the top-level capability_id was missing; \"argument validation failed for <tool>: required ...\" means the arguments value is not the target tool's own parameter object (check for a double-wrapped envelope)."
}

// declineCapabilityIDs merges the single id and the batch list into a stable,
// deduplicated order (single id first), so one call can dismiss many
// candidates the capability route listed for the turn.
func declineCapabilityIDs(id string, batch []string) []string {
	// Hint deliberately drops the +1: len(batch)+1 is an overflow-prone sum
	// for the allocator on 32-bit builds (codeql
	// go/allocation-size-overflow); the single-id prepend costs at most one
	// amortized growth.
	candidates := make([]string, 0, len(batch))
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		candidates = append(candidates, trimmed)
	}
	candidates = append(candidates, batch...)
	seen := make(map[string]struct{}, len(candidates))
	out := candidates[:0]
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

// declineResultText keeps the single-id wording byte-compatible with the
// pre-batch tool result and summarizes batches compactly.
func declineResultText(ids []string, reason string) string {
	if len(ids) == 1 {
		return fmt.Sprintf("declined capability %s: %s", ids[0], reason)
	}
	return fmt.Sprintf("declined %d capabilities (%s): %s", len(ids), strings.Join(ids, ", "), reason)
}

func (*UseCapabilityTool) ReadOnly() bool { return true }

func (*UseCapabilityTool) Schema() json.RawMessage {
	// Stable schema — must not change across turns or when MCP connects.
	// capability_id is optional only for action=list; inspect/call/decline still
	// require it at resolve time. One intentional prefix upgrade; thereafter
	// install/connect churn does not change this schema.
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","enum":["list","search","inspect","call","decline"],"description":"Use search for discovery, inspect one exact result, then call. list is diagnostic only."},
			"capability_id":{"type":"string","description":"Capability id such as skill:review, mcp-server:github, or mcp-tool:github/search_issues. Not required for action=list."},
			"capability_ids":{"type":"array","items":{"type":"string"},"description":"Fork: action=decline only. Decline several capabilities in one call instead of one call per id."},
			"query":{"type":"string","description":"Local catalog query required for action=search. No process or network is started."},
			"limit":{"type":"integer","minimum":1,"maximum":8,"default":5,"description":"Maximum search results; defaults to 5."},
			"arguments":{"type":"object","description":"For action=call: the TARGET TOOL'S OWN argument object, passed through unchanged (e.g. {\"description\":..., \"body\":...} for memory:remember). Do NOT wrap capability_id, action, or another arguments envelope inside it — those are top-level parameters of this tool, not part of the target arguments."},
			"reason":{"type":"string","description":"Required non-empty reason when action=decline"}
		},
		"required":["action"],
		"additionalProperties":false
	}`)
}

// ResolveCall implements tool.CallResolver so the agent can run permission,
// hooks, and evidence against the real MCP target before execution.
func (t *UseCapabilityTool) ResolveCall(ctx context.Context, args json.RawMessage) (tool.ResolvedCall, error) {
	p, action, id, err := parseUseCapabilityArgs(args)
	if err != nil {
		return tool.ResolvedCall{}, &capabilityInputError{err}
	}
	base := tool.ResolvedCall{
		DisplayName:  "use_capability",
		ProxyAction:  action,
		CapabilityID: id,
		Args:         p.Arguments,
	}
	switch action {
	case "list", "search", "inspect":
		return t.resolveDiscovery(ctx, p, action, id, base)
	case "decline":
		ids := declineCapabilityIDs(id, p.CapabilityIDs)
		if len(ids) == 0 {
			return tool.ResolvedCall{}, capabilityInputErrorf("capability_id or capability_ids is required for action=decline")
		}
		reason := strings.TrimSpace(p.Reason)
		if reason == "" {
			return tool.ResolvedCall{}, capabilityInputErrorf("reason is required for action=decline")
		}
		// Decline must not skip require. The mutation itself is delayed until the
		// agent has applied its post-resolution host boundary.
		if t.ledger != nil {
			for _, candidate := range ids {
				if e, ok := t.ledger.Get(candidate); ok && e.Policy == capability.AutoUseRequire {
					return tool.ResolvedCall{}, fmt.Errorf("cannot decline a require capability %q", candidate)
				}
			}
		}
		base.SkipExecute = true
		base.Result = declineResultText(ids, reason)
		base.ReadOnly = true
		base.Commit = func() error {
			if t.ledger != nil {
				for _, candidate := range ids {
					if err := t.ledger.MarkDeclined(candidate, reason); err != nil {
						return err
					}
				}
			}
			if t.audit != nil {
				for range ids {
					t.audit.RecordDecline()
				}
			}
			return nil
		}
		return base, nil
	case "call":
		if id == "" {
			// Task 728: the model may nest capability_id inside the arguments
			// object (58 of the 59 observed call failures). Promote it once;
			// the promoted form dispatches only when it satisfies the target
			// schema, and otherwise the original missing-id error stands so
			// the receipt stays precise (task 457 self-heal discipline).
			if nestedID, inner, ok := promoteNestedCapabilityID(p.Arguments); ok {
				healedBase := base
				healedBase.CapabilityID = nestedID
				if healed, herr := t.resolveCall(ctx, nestedID, inner, healedBase); herr == nil &&
					targetAcceptsArguments(healed.Target, inner) {
					t.noteArgumentSelfHeal(nestedID, "promoted a capability_id nested inside the arguments object to the envelope top level")
					return healed, nil
				}
			}
			return tool.ResolvedCall{}, capabilityInputErrorf("capability_id is required for action=call")
		}
		if id == sessionToolResultCapabilityID || id == sessionReadStrategyReceiptCapabilityID {
			return t.resolveSessionCapability(id, p.Arguments, base)
		}
		resolved, rerr := t.resolveCall(ctx, id, p.Arguments, base)
		if rerr != nil {
			return tool.ResolvedCall{}, rerr
		}
		// Task 212: object-shaped double envelopes resolve fine (the id is
		// valid) but then fail the dispatch gate on the wrapped shape. When
		// the received arguments cannot satisfy the target schema but the
		// inner envelope arguments do, retry once with the unwrapped value.
		// If the inner value also fails, keep the original resolution so the
		// gate reports the arguments exactly as they were received.
		if inner, ok := detectDoubleEnvelopedArguments(p.Arguments); ok &&
			!targetAcceptsArguments(resolved.Target, resolved.Args) {
			if healed, herr := t.resolveCall(ctx, id, inner, base); herr == nil &&
				targetAcceptsArguments(healed.Target, inner) {
				t.noteArgumentSelfHeal(id, "unwrapped a double-enveloped arguments value one level before dispatch")
				return healed, nil
			}
		}
		if p.healedDoubleEnvelope {
			// parseUseCapabilityArgs already unwrapped a stringified
			// envelope for an MCP target; record it once it dispatched.
			t.noteArgumentSelfHeal(id, "unwrapped a stringified call envelope one level before dispatch")
		}
		if p.healedStringified {
			// Task 457: parseUseCapabilityArgs already parsed a JSON-string
			// arguments value into an object for an MCP target.
			t.noteArgumentSelfHeal(id, "parsed a JSON-string arguments value into an object before dispatch")
		}
		// Task 457: parameters spread across the envelope top level
		// (message+to etc. with no arguments object) reach the target as
		// empty arguments. When the received form cannot satisfy the target
		// schema but merging the spread parameters into one object can,
		// retry once with the merged object; otherwise keep the original
		// resolution so the gate reports the arguments exactly as received.
		if merged, keys, ok := p.mergedSpreadArguments(); ok &&
			!targetAcceptsArguments(resolved.Target, resolved.Args) {
			if healed, herr := t.resolveCall(ctx, id, merged, base); herr == nil &&
				targetAcceptsArguments(healed.Target, merged) {
				t.noteArgumentSelfHeal(id, fmt.Sprintf("merged top-level parameters [%s] into the arguments object before dispatch", strings.Join(keys, ", ")))
				return healed, nil
			}
		}
		return resolved, nil
	default:
		return tool.ResolvedCall{}, capabilityInputErrorf("unknown action %q; use list, search, inspect, call, or decline", p.Action)
	}
}

func (t *UseCapabilityTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	resolved, err := t.ResolveCall(ctx, args)
	if err != nil {
		return "", err
	}
	if resolved.SkipExecute {
		if resolved.Commit != nil {
			if err := resolved.Commit(); err != nil {
				return "", err
			}
		}
		if resolved.ProxyAction == "call" && !resolved.Unavailable {
			if t.ledger != nil {
				t.ledger.MarkSucceeded(resolved.CapabilityID)
			}
			if t.audit != nil {
				t.audit.RecordMCPProxy(false, true, false)
			}
		}
		return resolved.Result, nil
	}
	if resolved.Unavailable {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(resolved.CapabilityID, resolved.UnavailableReason)
		}
		return "", fmt.Errorf("capability unavailable: %s", resolved.UnavailableReason)
	}
	if resolved.Target == nil {
		return "", fmt.Errorf("no target tool resolved for %s", resolved.CapabilityID)
	}
	if t.ledger != nil {
		t.ledger.MarkInvoked(resolved.CapabilityID)
	}
	if t.audit != nil {
		t.audit.RecordMCPProxy(false, true, false)
	}
	out, err := resolved.Target.Execute(ctx, resolved.Args)
	if err != nil {
		if t.ledger != nil {
			t.ledger.MarkFailed(resolved.CapabilityID, err.Error())
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return out, err
	}
	if t.ledger != nil {
		t.ledger.MarkSucceeded(resolved.CapabilityID)
	}
	return out, nil
}

func (t *UseCapabilityTool) resolveCall(ctx context.Context, id string, args json.RawMessage, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	// Registry-backed tools and skills share the unified proxy. Real writers
	// still pass permission/plan/sandbox/lease checks via ResolvedCall.Target.
	if name, ok := strings.CutPrefix(id, "tool:"); ok {
		return t.resolveRegistryTool(ctx, strings.TrimSpace(name), id, args, base)
	}
	if name, ok := strings.CutPrefix(id, "skill:"); ok {
		return t.resolveSkillCall(strings.TrimSpace(name), id, args, base)
	}
	if name, ok := strings.CutPrefix(id, "task:"); ok {
		return t.resolveRegistryTool(ctx, taskToolName(name), id, args, base)
	}
	if name, ok := strings.CutPrefix(id, "workflow:"); ok {
		return t.resolveRegistryTool(ctx, strings.TrimSpace(name), "tool:"+strings.TrimSpace(name), args, base)
	}
	for _, prefix := range []string{"web:", "lsp:", "session:", "memory:"} {
		if rest, ok := strings.CutPrefix(id, prefix); ok {
			return t.resolveRegistryTool(ctx, strings.TrimSpace(rest), id, args, base)
		}
	}
	// Server-level call is the first-discovery path for servers with no
	// schema cache: it resolves to a gated connect-and-list target so the
	// model can learn tool names without inspect ever starting a process.
	if server, ok := parseMCPServerCapabilityID(id); ok {
		return t.resolveServerConnect(ctx, server, base)
	}
	server, raw, err := parseMCPCapabilityID(id)
	if err != nil {
		return tool.ResolvedCall{}, err
	}
	if !t.serverEnabled(server) {
		return t.resolveUnavailable(base, id, plugin.ModelToolName(server, raw), t.serverUnavailableReason(server)), nil
	}
	var runtimeSpec plugin.Spec
	releaseRuntime := func() {}
	if t.runtime != nil {
		spec, unlock, lockErr := t.lockAuthorizedRuntimeServer(ctx, server)
		if lockErr != nil {
			return t.resolveUnavailable(base, id, plugin.ModelToolName(server, raw), lockErr.Error()), nil
		}
		releaseRuntime = unlock
		defer func() { releaseRuntime() }()
		runtimeSpec = spec
	}
	// Prefer already-exposed registry tool (auto-started MCP). The model name
	// MUST come from the plugin layer's canonical constructor: it appends a
	// collision hash for sanitised raw names, and permission/hook rules are
	// written against that executed name — a proxy-local normalization would
	// let them silently miss.
	modelName := plugin.ModelToolName(server, raw)
	if t.registry != nil {
		if tl, ok := t.registry.Get(modelName); ok {
			if t.runtime != nil && !plugin.MCPToolMatchesSpec(tl, runtimeSpec) {
				return t.resolveUnavailable(base, id, modelName, fmt.Sprintf("connected MCP server %q identity does not match the current runtime configuration", server)), nil
			}
			base.TargetName = modelName
			base.Target = t.bindRuntimeMCP(runtimeSpec, tl)
			base.ReadOnly = tl.ReadOnly()
			if len(args) == 0 {
				base.Args = json.RawMessage(`{}`)
			} else {
				base.Args = args
			}
			return base, nil
		}
	}
	// Server already connected (auto-started, a previous proxy call, or a
	// sibling tab sharing this host): resolving against live tools is
	// side-effect-free. serverTools also refreshes the catalog snapshot so a
	// cross-tab connect still yields routable mcp-tool entries here.
	if t.host != nil && t.host.HasClient(server) {
		var tools []tool.Tool
		if t.runtime != nil {
			tools, err = t.serverToolsForSpec(ctx, server, runtimeSpec)
		} else {
			tools, err = t.serverTools(ctx, server)
		}
		if err != nil {
			return t.resolveUnavailable(base, id, modelName, err.Error()), nil
		}
		target := findMCPTool(tools, raw, modelName)
		if target == nil {
			return t.resolveUnavailable(base, id, modelName, fmt.Sprintf("MCP tool %q not found on server %q", raw, server)), nil
		}
		base.Target = t.bindRuntimeMCP(runtimeSpec, target)
		base.TargetName = target.Name()
		base.ReadOnly = target.ReadOnly()
		if len(args) == 0 {
			base.Args = json.RawMessage(`{}`)
		} else {
			base.Args = args
		}
		return base, nil
	}
	// Unconnected server: resolution must stay pure — no subprocess, no network.
	// Return a deferred target that connects in Execute, after the permission
	// gate and PreToolUse hooks have approved the real target name/arguments.
	spec := runtimeSpec
	if t.runtime == nil {
		var ok bool
		spec, ok = t.specFor(server)
		if !ok {
			return t.resolveUnavailable(base, id, modelName, mcpServerUnregisteredMessage(server)), nil
		}
		spec = plugin.ResolveStoredAuthorization(ctx, spec)
	}
	// Execute rechecks the immutable spec snapshot. Release dispatchMu because
	// Boot's catalog callback takes its own runtime snapshot.
	releaseRuntime()
	releaseRuntime = func() {}
	return t.resolveUnconnectedMCPCall(id, args, base, spec, server, raw, modelName), nil
}

// resolveSkillCall routes skill:<name> through run_skill / read_only_skill /
// read_skill when present, preserving the real skill tool name for evidence.
func (t *UseCapabilityTool) resolveSkillCall(skillName, id string, args json.RawMessage, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return tool.ResolvedCall{}, fmt.Errorf("capability id %q is missing a skill name", id)
	}
	if t.registry == nil {
		return t.resolveUnavailable(base, id, skillName, "tool registry is unavailable"), nil
	}
	// Prefer full run_skill; fall back to read-only variants when the session
	// only exposes them (planner / plan mode).
	for _, toolName := range []string{"run_skill", "read_only_skill", "read_skill"} {
		if tl, ok := t.registry.Get(toolName); ok {
			payload := args
			if len(payload) == 0 || string(payload) == "null" {
				payload = json.RawMessage(fmt.Sprintf(`{"name":%q}`, skillName))
			} else {
				var m map[string]any
				if json.Unmarshal(payload, &m) == nil {
					if _, has := m["name"]; !has {
						m["name"] = skillName
						if b, err := json.Marshal(m); err == nil {
							payload = b
						}
					}
				}
			}
			base.Target = tl
			base.TargetName = toolName
			base.ReadOnly = tl.ReadOnly()
			base.Args = payload
			return base, nil
		}
	}
	return t.resolveUnavailable(base, id, skillName, fmt.Sprintf("skill tools are not available for %q", skillName)), nil
}

func taskToolName(name string) string {
	name = strings.TrimSpace(name)
	switch name {
	case "subagent", "task":
		return "task"
	case "read_only_subagent", "read_only_task", "research":
		return "read_only_task"
	case "parallel", "parallel_tasks":
		return "parallel_tasks"
	case "fleet":
		return "fleet"
	default:
		return name
	}
}

// resolveUnavailable fills the host-proven unavailable shape shared by the
// side-effect-free resolution failures (missing config, unknown tool).
func (t *UseCapabilityTool) resolveUnavailable(base tool.ResolvedCall, id, modelName, reason string) tool.ResolvedCall {
	base.Unavailable = true
	base.UnavailableReason = reason
	base.SkipExecute = true
	base.Result = "capability unavailable: " + reason
	base.TargetName = modelName
	base.ReadOnly = false
	base.Commit = func() error {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(id, reason)
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return nil
	}
	return base
}

// findMCPTool matches a server's tool list by raw MCP name or by the
// canonical namespaced model-visible name (plugin.ModelToolName).
func findMCPTool(tools []tool.Tool, raw, modelName string) tool.Tool {
	for _, tl := range tools {
		if m, ok := tl.(tool.MCPMetadata); ok && m.MCPRawToolName() == raw {
			return tl
		}
		if tl.Name() == modelName {
			return tl
		}
	}
	return nil
}

// onDemandMCPTool defers MCP server startup to Execute so permission and hook
// gates always run before any subprocess or network side effect. Before the live
// handshake it remains write-capable until the resolved MCP tool is classified.
type onDemandMCPTool struct {
	proxy     *UseCapabilityTool
	spec      plugin.Spec
	server    string
	raw       string
	modelName string
	// destructive comes from the schema cache when available. A live promotion
	// is detected in Execute so a retry re-enters the current Plan/read-only
	// execution boundary.
	destructive bool
	readOnly    bool
	schema      json.RawMessage
}

func (o *onDemandMCPTool) Name() string { return o.modelName }

func (o *onDemandMCPTool) Description() string {
	return "on-demand MCP tool " + o.server + "/" + o.raw + " (connects when first used)"
}

func (o *onDemandMCPTool) Schema() json.RawMessage {
	if len(o.schema) > 0 {
		return o.schema
	}
	return json.RawMessage(`{"type":"object"}`)
}

func (o *onDemandMCPTool) ReadOnly() bool {
	return o.readOnly
}

func (o *onDemandMCPTool) ReadOnlyExecutionHostMutation() bool { return true }

func (o *onDemandMCPTool) MCPServerAuthorized() bool {
	// Spec.Authorized is the single runtime authorization result. Boot/install
	// and ResolveStoredAuthorization set it; this path never invents trust.
	return o.spec.ServerAuthorized()
}

func (o *onDemandMCPTool) ReadOnlyExecutionBlockReason() string {
	return "connect this MCP capability from a parent session first"
}

// MCPServerName/MCPRawToolName expose the deferred target for audit and
// diagnostics (tool.MCPMetadata).
func (o *onDemandMCPTool) MCPServerName() string  { return o.server }
func (o *onDemandMCPTool) MCPRawToolName() string { return o.raw }
func (o *onDemandMCPTool) MCPDestructiveHint() bool {
	return o.destructive
}

func (o *onDemandMCPTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	text, _, err := o.executeWithImages(ctx, args)
	return text, err
}

// ExecuteWithImages preserves structured MCP image results on the first call,
// when the deferred target must connect the server before dispatch. Keeping the
// resolution and safety checks in executeWithImages ensures text-only and image
// callers share the same authorization and runtime-identity boundary.
func (o *onDemandMCPTool) ExecuteWithImages(ctx context.Context, args json.RawMessage) (string, []string, error) {
	return o.executeWithImages(ctx, args)
}

func (o *onDemandMCPTool) executeWithImages(ctx context.Context, args json.RawMessage) (string, []string, error) {
	// Final runtime-bound authorization and identity check before any
	// process/network start. The read lock also linearizes this dispatch against
	// disable, uninstall, and same-name hot replacement.
	spec, unlock, err := o.proxy.lockAuthorizedRuntimeServer(ctx, o.server)
	if err != nil {
		msg := err.Error()
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, msg)
		}
		return "", nil, err
	}
	defer unlock()
	if !plugin.MCPRuntimeSpecMatches(spec, o.spec) {
		return "", nil, fmt.Errorf("MCP server %q runtime identity changed after resolution; retry so Reasonix can bind the current configuration", o.server)
	}
	tools, err := o.proxy.ensureServerToolsForSpec(ctx, o.server, spec)
	if err != nil {
		// Audit for the call path is recorded once by the agent loop
		// (noteCapabilityInvocation); only the ledger outcome lands here.
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, err.Error())
		}
		return "", nil, err
	}
	target := findMCPTool(tools, o.raw, o.modelName)
	if target == nil {
		msg := fmt.Sprintf("MCP tool %q not found on server %q", o.raw, o.server)
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, msg)
		}
		return "", nil, fmt.Errorf("%s", msg)
	}
	if !plugin.MCPToolMatchesSpec(target, spec) {
		return "", nil, fmt.Errorf("connected MCP server %q identity does not match the current runtime configuration; reconnect this server before retrying", o.server)
	}
	if _, err := plugin.ReconcileCachedToolSafety(o.server, o.raw, plugin.CachedToolSafety{
		ReadOnly:    o.readOnly,
		Destructive: o.destructive,
	}, target); err != nil {
		return "", nil, err
	}
	// Planner non-destructive lane and reader lane: re-check live metadata
	// before tools/call even when Reconcile did not see a cache promotion.
	if tool.HasNonDestructiveMCPExecutionIntent(ctx) {
		if !mcpServerAuthorized(target) || mcpDestructiveHint(target) {
			return "", nil, fmt.Errorf("MCP server %q changed the authorization or destructive classification for tool %q; the call was blocked before dispatch — retry so Reasonix can re-apply the current Planner MCP safety boundary", o.server, o.raw)
		}
	}
	if blocked, msg := hostValidateBeforeDispatch(target, args, "mcp-tool:"+o.server+"/"+o.raw); blocked {
		return "", nil, fmt.Errorf("%s", msg)
	}
	if imageTool, ok := target.(tool.ImageTool); ok {
		return imageTool.ExecuteWithImages(ctx, args)
	}
	text, err := target.Execute(ctx, args)
	return text, nil, err
}

func (t *UseCapabilityTool) ensureServerToolsForSpec(ctx context.Context, server string, spec plugin.Spec) ([]tool.Tool, error) {
	// Reuse shared host if already connected (including auto-started).
	if t.host.HasClient(server) {
		return t.serverToolsForSpec(ctx, server, spec)
	}
	// On-demand connect: the child and handshake belong to the session. This
	// tool call waits briefly, but a slow healthy server continues in the
	// background instead of being killed and restarted on every retry. Tools
	// stay off the main provider-visible registry.
	life := t.lifeCtx
	if life == nil {
		life = context.Background()
	}
	started := time.Now()
	result := t.host.EnsureConnectedInBackground(life, spec)
	waitBudget := plugin.DefaultStartupWaitBudget()
	timer := time.NewTimer(waitBudget)
	defer timer.Stop()
	var tools []tool.Tool
	var err error
	select {
	case connected := <-result:
		tools, err = connected.Tools, connected.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("MCP server %q is still initializing after %s; startup continues in background (limit %s) — retry on a later turn",
			server, waitBudget, spec.ResolvedStartupTimeout())
	}
	if err != nil {
		if plugin.IsServerAlreadyConnected(err) {
			return t.serverToolsForSpec(ctx, server, spec)
		}
		t.host.RecordFailure(spec, err)
		return nil, fmt.Errorf("connect %q: %w", server, err)
	}
	t.ensureState().markConnected(server)
	schemaBytes := 0
	for _, target := range tools {
		schemaBytes += len(target.Schema())
	}
	durationMs := time.Since(started).Milliseconds()
	t.capabilityAudit().RecordMCPList("remote", "connect", durationMs, len(tools), schemaBytes)
	t.observeMCPList(mcpListObservation{
		Server: server, Source: "remote", Trigger: "connect", DurationMs: durationMs,
		ToolCount: len(tools), SchemaBytes: schemaBytes, NetworkCall: true,
	})
	// Intentionally do NOT add tools to t.registry — provider schema stays stable.
	_ = tools
	return t.serverToolsForSpec(ctx, server, spec)
}

// serverTools fetches the live tools for a connected server and refreshes the
// shared catalog snapshot so mcp-tool entries stay routable once the server
// is StatusReady (its tools are absent from the provider-visible registry).
func (t *UseCapabilityTool) serverTools(ctx context.Context, server string) ([]tool.Tool, error) {
	spec, unlock, err := t.lockAuthorizedRuntimeServer(ctx, server)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return t.serverToolsForSpec(ctx, server, spec)
}

func (t *UseCapabilityTool) serverToolsForSpec(ctx context.Context, server string, spec plugin.Spec) ([]tool.Tool, error) {
	tools, err := t.host.ToolsForSpec(ctx, spec)
	if err != nil {
		return nil, err
	}
	t.ensureState().setLiveTools(server, snapshotMCPTools(tools))
	return tools, nil
}

// ConnectedProxyTools returns raw tool metadata for servers connected through
// any frontend sharing this proxy state, keyed by server name. Catalog builders
// consume it so concrete mcp-tool capabilities survive an on-demand connect
// without ever touching the provider-visible registry.
func (t *UseCapabilityTool) ConnectedProxyTools() map[string][]plugin.CachedTool {
	if t == nil {
		return nil
	}
	return t.ensureState().snapshotLiveTools()
}

func (t *UseCapabilityTool) ensureState() *mcpProxySharedState {
	if t.state == nil {
		t.state = &mcpProxySharedState{connected: map[string]bool{}}
	}
	return t.state
}

// specFor looks up the boot-converted spec for server. The proxy deliberately
// holds []plugin.Spec, not raw config entries: env expansion, workspace
// overrides, call timeouts, and read-only tool names all live in the
// shared conversion and must not be re-derived here.
func (t *UseCapabilityTool) specFor(server string) (plugin.Spec, bool) {
	if t.runtime != nil {
		return t.runtime.enabledSpec(server)
	}
	for _, s := range t.specs {
		if s.Name == server {
			return s, true
		}
	}
	return plugin.Spec{}, false
}

// lockAuthorizedRuntimeServer acquires the shared runtime dispatch read lock
// and returns the current enabled, authorized spec. The caller must keep the
// returned lock until the identity-bound Host operation or tools/call has
// crossed its dispatch boundary; lifecycle mutations take the write lock.
func (t *UseCapabilityTool) lockAuthorizedRuntimeServer(ctx context.Context, server string) (plugin.Spec, func(), error) {
	if t.runtime == nil {
		spec, ok := t.specFor(server)
		if !ok {
			return plugin.Spec{}, func() {}, mcpServerUnregisteredError(server)
		}
		spec = plugin.ResolveStoredAuthorization(ctx, spec)
		if !spec.ServerAuthorized() {
			return plugin.Spec{}, func() {}, fmt.Errorf("MCP server %q is not authorized; install it or complete project identity approval before connecting", server)
		}
		return spec, func() {}, nil
	}

	t.runtime.dispatchMu.RLock()
	unlock := t.runtime.dispatchMu.RUnlock
	t.runtime.mu.RLock()
	configured, ok := t.runtime.servers[strings.TrimSpace(server)]
	t.runtime.mu.RUnlock()
	if !ok {
		unlock()
		return plugin.Spec{}, func() {}, mcpServerUnregisteredError(server)
	}
	if !configured.enabled {
		unlock()
		return plugin.Spec{}, func() {}, mcpServerDisabledError(server)
	}
	spec := plugin.ResolveStoredAuthorization(ctx, cloneMCPSpec(configured.spec))
	if !spec.ServerAuthorized() {
		unlock()
		return plugin.Spec{}, func() {}, fmt.Errorf("MCP server %q is not authorized; install it or complete project identity approval before connecting", server)
	}
	return spec, unlock, nil
}

func (t *UseCapabilityTool) bindRuntimeMCP(spec plugin.Spec, target tool.Tool) tool.Tool {
	if t.runtime == nil || target == nil {
		return target
	}
	return &runtimeBoundMCPTool{
		proxy:      t,
		target:     target,
		server:     spec.Name,
		authorized: spec.ServerAuthorized(),
	}
}

func (t *UseCapabilityTool) withRuntimeBoundMCP(ctx context.Context, server string, target tool.Tool, execute func() error) error {
	if t.runtime == nil {
		return execute()
	}
	spec, unlock, err := t.lockAuthorizedRuntimeServer(ctx, server)
	if err != nil {
		return err
	}
	defer unlock()
	if !plugin.MCPToolMatchesSpec(target, spec) {
		return fmt.Errorf("connected MCP server %q identity does not match the current runtime configuration; reconnect this server before retrying", server)
	}
	return t.runtime.withServerGate(ctx, server, execute)
}

func (t *UseCapabilityTool) serverEnabled(server string) bool {
	if t.runtime != nil {
		return t.runtime.serverEnabled(server)
	}
	// Standalone proxies predate the authoritative runtime and may resolve
	// already-registered MCP tools without carrying a duplicate spec slice.
	return true
}

func (t *UseCapabilityTool) configuredServers() []mcpRuntimeServer {
	if t.runtime != nil {
		return t.runtime.configuredServers()
	}
	servers := make([]mcpRuntimeServer, 0, len(t.specs))
	seen := map[string]bool{}
	for _, raw := range t.specs {
		spec := cloneMCPSpec(raw)
		name := strings.TrimSpace(spec.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		servers = append(servers, mcpRuntimeServer{
			entry:   config.PluginEntry{Name: name},
			spec:    spec,
			enabled: true,
		})
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].spec.Name < servers[j].spec.Name })
	return servers
}

func (t *UseCapabilityTool) currentCatalog() capability.Catalog {
	if t.catalog != nil {
		return t.catalog()
	}
	return capability.Catalog{}
}

// parseMCPServerCapabilityID extracts the server name from an mcp-server id.
func parseMCPServerCapabilityID(id string) (string, bool) {
	if !strings.HasPrefix(id, "mcp-server:") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(id, "mcp-server:"))
	return name, name != ""
}

// resolveServerConnect resolves action=call on an mcp-server id. A connected
// server lists its tools immediately (side-effect free); an unconnected one
// resolves to a deferred connect target that runs only after the permission
// gate and PreToolUse hooks approve it. Stored project authorization is applied
// at resolve time so unauthorized project MCP never reaches process startup.
func (t *UseCapabilityTool) resolveServerConnect(ctx context.Context, server string, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	id := "mcp-server:" + server
	if !t.serverEnabled(server) {
		return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), t.serverUnavailableReason(server)), nil
	}
	if t.host != nil && t.host.HasClient(server) {
		out, err := t.listServerTools(ctx, server)
		if err != nil {
			return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), err.Error()), nil
		}
		base.SkipExecute = true
		base.HostCompleted = true
		base.Result = out
		base.ReadOnly = true
		return base, nil
	}
	spec, unlock, err := t.lockAuthorizedRuntimeServer(ctx, server)
	if err != nil {
		return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), err.Error()), nil
	}
	unlock()
	connect := &onDemandMCPConnect{proxy: t, spec: spec, server: server}
	base.Target = connect
	// A dedicated exact identity names the connect for permission and hook
	// rules. It cannot collide with a real mcp__ tool, and rules do not need to
	// rely on unsupported tool-name glob matching.
	base.TargetName = connect.Name()
	// Connecting spawns a subprocess, so it is never a read-only fast path for
	// ordinary Plan/strict agents. PlannerMCPExecution may allow authorized
	// connects; unauthorized specs are blocked before process/network start.
	base.ReadOnly = false
	base.Args = json.RawMessage(`{}`)
	return base, nil
}

// onDemandMCPConnect is the deferred first-discovery target: it connects the
// server post-approval and returns the live tool directory.
type onDemandMCPConnect struct {
	proxy  *UseCapabilityTool
	spec   plugin.Spec
	server string
}

func (o *onDemandMCPConnect) Name() string { return plugin.MCPConnectPermissionName(o.server) }

func (o *onDemandMCPConnect) Description() string {
	return "connect MCP server " + o.server + " on demand and list its tools"
}

func (o *onDemandMCPConnect) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (o *onDemandMCPConnect) ReadOnly() bool { return false }

// MCPLifecycleConnect marks this target as an MCP connect-and-list lifecycle
// action for Planner authorization (not a remote tools/call).
func (o *onDemandMCPConnect) MCPLifecycleConnect() bool { return true }

func (o *onDemandMCPConnect) MCPServerAuthorized() bool {
	return o.spec.ServerAuthorized()
}

func (o *onDemandMCPConnect) MCPServerName() string { return o.server }

func (o *onDemandMCPConnect) ReadOnlyExecutionHostMutation() bool { return true }

func (o *onDemandMCPConnect) ReadOnlyExecutionBlockReason() string {
	if !o.spec.ServerAuthorized() {
		return "start an unauthorized MCP server (install it or complete project identity approval first)"
	}
	return "connect this MCP server from a parent session first"
}

func (o *onDemandMCPConnect) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	// Zero process/network start when authorization, enable state, or exact
	// runtime identity changed after resolve.
	spec, unlock, err := o.proxy.lockAuthorizedRuntimeServer(ctx, o.server)
	if err != nil {
		msg := err.Error()
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-server:"+o.server, msg)
		}
		return "", err
	}
	defer unlock()
	if !plugin.MCPRuntimeSpecMatches(spec, o.spec) {
		return "", fmt.Errorf("MCP server %q runtime identity changed after resolution; retry so Reasonix can bind the current configuration", o.server)
	}
	if _, err := o.proxy.ensureServerToolsForSpec(ctx, o.server, spec); err != nil {
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-server:"+o.server, err.Error())
		}
		return "", err
	}
	return o.proxy.listServerToolsForSpec(ctx, o.server, spec)
}

// listServerTools renders the live tool directory of a connected server and
// refreshes the proxy snapshot on the way (via serverTools).
func (t *UseCapabilityTool) listServerTools(ctx context.Context, server string) (string, error) {
	tools, err := t.serverTools(ctx, server)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("connected MCP server %q; %d tools:\n%s", server, len(tools), inspectToolListJSON(server, tools)), nil
}

func (t *UseCapabilityTool) listServerToolsForSpec(ctx context.Context, server string, spec plugin.Spec) (string, error) {
	tools, err := t.serverToolsForSpec(ctx, server, spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("connected MCP server %q; %d tools:\n%s", server, len(tools), inspectToolListJSON(server, tools)), nil
}

func parseMCPCapabilityID(id string) (server, raw string, err error) {
	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, "mcp-tool:"):
		rest := strings.TrimPrefix(id, "mcp-tool:")
		server, raw, ok := strings.Cut(rest, "/")
		if !ok || server == "" || raw == "" {
			return "", "", fmt.Errorf("invalid mcp-tool id %q; want mcp-tool:<server>/<tool>", id)
		}
		return server, raw, nil
	case strings.HasPrefix(id, "mcp-server:"):
		return "", "", fmt.Errorf("%q is a server id; call it directly to connect and list tools, or use mcp-tool:<server>/<tool>", id)
	default:
		// Task 240, direction 2: this branch was a dead end — the soft-budget
		// nudge told the model to "call extend_research_budget", the model
		// routed it here with a bare name, and got this error with no way out.
		// Name the shapes the proxy accepts and the concrete id for the
		// nudge's tool so the next attempt lands.
		return "", "", fmt.Errorf("action=call needs a shaped capability id: local/builtin tools use tool:<name> (for the budget nudge: tool:extend_research_budget), MCP tools use mcp-tool:<server>/<tool>; action=list shows the catalog")
	}
}

// Ensure UseCapabilityTool satisfies the tool contracts used by the agent.
var (
	_ tool.Tool            = (*UseCapabilityTool)(nil)
	_ tool.CallResolver    = (*UseCapabilityTool)(nil)
	_ tool.BatchClassifier = (*UseCapabilityTool)(nil)
)

// EmitProxyAudit is a helper for frontends: returns a notice describing the
// proxy name and real target for user audit trails.
func EmitProxyAudit(sink event.Sink, resolved tool.ResolvedCall) {
	if sink == nil || resolved.TargetName == "" {
		return
	}
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Text:   capabilityProxyNoticeText(resolved.DisplayName, resolved.TargetName),
		Detail: resolved.CapabilityID,
	})
}
