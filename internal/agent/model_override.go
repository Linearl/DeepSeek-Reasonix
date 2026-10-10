package agent

import (
	"log/slog"
	"strings"

	"reasonix/internal/nilutil"
	"reasonix/internal/provider"
)

// The session-scoped model override lets a frontend switch the tab's model
// without rebuilding the runtime (task 148, mirroring the task-334 effort
// override). provider.Request carries no model field — the wire model, its
// protocol flags, and its model info are construction-bound on the adapter —
// so a per-request model switch is a per-request *destination* switch: the
// override carries a fully constructed provider resolved from the session
// resolver, and request freezing captures it for the round.
//
// Task 602 lifts the task-148 same-family gate: every request-shaping surface
// (reasoning replay, projection, window mode, tool-call reasoning, native
// tool search) reads the destination through providerForRequest(), so a
// cross-family destination sees exactly the protocol behavior a rebuild would
// derive — the override IS a fully constructed adapter from the boot resolver.
// The same holds for the entry-derived scalars a rebuild would rebind:
// pricing, context window, output budget, the high-speed lane, and the
// billable model ref ride the override struct and are read through the
// effective* accessors. Protocol-scoped prompt text (the official
// DeepSeek-V4-Pro persona, baked into the session's system prompt) has no
// override seam — hosts gate that boundary themselves and keep their
// build+swap fallback for it.
//
// Targets the resolver cannot resolve, resolver-less hosts, and recovery-
// forked sessions still decline here and the caller keeps its build+swap
// fallback, which re-derives every surface exactly as before.

// ModelOverrideExtras carries the entry-derived scalars a rebuild would
// rebind alongside the provider (task 602). Zero fields keep the
// construction-time values — correct for same-family switches, conservative
// for cross-family callers that have no entry at hand.
type ModelOverrideExtras struct {
	// Pricing prices the destination's usage into money (task budget).
	Pricing *provider.Pricing
	// ContextWindow is the destination's token window (0 = keep).
	ContextWindow int
	// MaxOutputTokens is the destination's output cap (0 = keep).
	MaxOutputTokens int
	// HighSpeedModels is the destination's high-speed lane allowlist
	// (nil = keep; the boot gate ExperimentalHighSpeedModel already folded).
	HighSpeedModels []string
}

// sessionModelOverride is the effective request destination: a resolved
// provider, the canonical ref it serves, and the entry-derived scalars that
// follow the destination. Stored through atomic.Value — written rarely (an
// explicit switch), read on every request, following the
// sessionEffortOverride/responseLanguage precedent.
type sessionModelOverride struct {
	ref             string
	prov            provider.Provider
	pricing         *provider.Pricing
	contextWindow   int
	maxOutputTokens int
	highSpeedModels []string
}

// SetSessionModelOverride stores a session-scoped model override and reports
// whether the running agent can serve the next request from the new model
// without a rebuild. An empty ref clears the override and always succeeds. A
// non-empty ref is accepted when (a) the construction-time resolver seam
// is wired and (b) the ref resolves into a live provider — the family gate
// task 148 held is lifted by task 602 because every protocol-scoped read and
// every entry-derived scalar now follows the destination. Returning false
// tells the caller to fall back to the rebuild path instead of arming a
// destination the session cannot vouch for.
func (a *Agent) SetSessionModelOverride(ref string, extras ModelOverrideExtras) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		a.sessionModel.Store((*sessionModelOverride)(nil))
		return true
	}
	// A recovery-forked session keeps its reanchor semantics on the rebuild
	// path (forked file sealed, in-memory history anchored to a fresh branch):
	// an override that skips the rebuild would leave the forked file in active
	// use. Defer such sessions to the caller's fallback — same guard, and same
	// rationale, as the effort override.
	if path := strings.TrimSpace(a.sess.path); path != "" {
		if meta, ok, err := LoadBranchMeta(path); err == nil && ok && meta.Recovered {
			// Task 148: name why the fast path declined — documented fallback
			// cause, mirroring the effort override's recovery-fork line.
			slog.Info("agent: model override declined", "reason", "recovery-fork", "ref", ref)
			return false
		}
	}
	if a.svc.modelResolver == nil {
		// Task 148: host wired no resolver seam (CLI one-shots, sub-agents) —
		// documented fallback cause.
		slog.Info("agent: model override declined", "reason", "resolver-not-wired", "ref", ref)
		return false
	}
	// Carry the session's effort selection into the resolved entry so the new
	// provider's configured depth matches what the tab is running; the
	// per-request EffortOverride keeps riding every request on top of it.
	var effortSel *string
	if effort := strings.TrimSpace(a.sessionEffortOverrideValue()); effort != "" {
		effortSel = &effort
	}
	prov, err := a.svc.modelResolver.Resolve(provider.Selection{Ref: ref, Effort: effortSel})
	if err != nil {
		slog.Info("agent: model override declined", "reason", "resolve-failed", "ref", ref, "err", err.Error())
		return false
	}
	// Task 602: no family gate — the destination is a fully constructed
	// adapter from the boot resolver and every destination-scoped surface
	// (protocol shaping, wire, scalars) reads through providerForRequest and
	// the effective* accessors, matching what a rebuild would derive.
	a.sessionModel.Store(&sessionModelOverride{
		ref:             ref,
		prov:            prov,
		pricing:         extras.Pricing,
		contextWindow:   extras.ContextWindow,
		maxOutputTokens: extras.MaxOutputTokens,
		highSpeedModels: extras.HighSpeedModels,
	})
	slog.Info("agent: model override accepted", "ref", ref)
	return true
}

// sessionModelOverrideRef returns the armed override's canonical ref; empty
// when the configured model stands. Observability surface for hosts.
func (a *Agent) sessionModelOverrideRef() string {
	if o := a.sessionModelValue(); o != nil {
		return o.ref
	}
	return ""
}

// sessionModelValue returns the armed override; nil when unset.
func (a *Agent) sessionModelValue() *sessionModelOverride {
	if o, ok := a.sessionModel.Load().(*sessionModelOverride); ok {
		return o
	}
	return nil
}

// providerForRequest returns the provider serving the next request: the armed
// session override when present, else the construction provider. Read on every
// request build; the override flips between rounds, never mid-request.
func (a *Agent) providerForRequest() provider.Provider {
	if o := a.sessionModelValue(); o != nil && o.prov != nil {
		return o.prov
	}
	return a.svc.prov
}

// destinationModelRef returns the billable model ref serving the next request:
// the armed override's ref when set, else the construction ref (task 602).
// Usage events, prompt-cache keys, and budget state keys read through here so
// a hot switch attributes to the model the requests actually reach.
func (a *Agent) destinationModelRef() string {
	if o := a.sessionModelValue(); o != nil && o.ref != "" {
		return o.ref
	}
	return a.modelRef
}

// destinationPricing returns the destination's pricing (task 602); nil keeps
// the construction pricing (which may itself be nil — hosts without a schedule).
func (a *Agent) destinationPricing() *provider.Pricing {
	if o := a.sessionModelValue(); o != nil && o.pricing != nil {
		return o.pricing
	}
	return a.svc.pricing
}

// destinationContextWindow returns the destination's declared token window; 0
// keeps the construction window (0 meaning unknown — sizing falls back to
// learned state). The learned-window composition lives in
// effectiveContextWindow, which takes this as its configured input.
func (a *Agent) destinationContextWindow() int {
	if o := a.sessionModelValue(); o != nil && o.contextWindow > 0 {
		return o.contextWindow
	}
	return a.contextWindow
}

// destinationMaxOutputTokens returns the destination's output cap. An override
// cap wins; 0 keeps the construction cap — but only when the override stays on
// the construction provider's route (task 752): a cross-provider hot switch
// with an unconfigured destination must not ride the PREVIOUS provider's cap
// (deepseek's 384000 got a mimo-class gateway 400 because the "0 = keep"
// fallback leaked it onto a wire it no longer describes). Such requests omit
// MaxTokens instead — OpenAI-compatible gateways apply their server default
// ("0 = unset/omit" is the adapters' documented contract), and the Anthropic
// adapter fills its own mandatory fallback at construction.
func (a *Agent) destinationMaxOutputTokens() int {
	if o := a.sessionModelValue(); o != nil {
		if o.maxOutputTokens > 0 {
			return o.maxOutputTokens
		}
		if !sameProviderRoute(o.prov, a.svc.prov) {
			return 0
		}
	}
	return a.maxOutputTokens
}

// sameProviderRoute reports whether dst serves the same provider route the
// construction scalars were derived from. Names are route-scoped (the boot
// resolver builds one adapter per named entry), so name equality is the route
// identity; a nil construction provider shares its route with nothing.
func sameProviderRoute(dst, construction provider.Provider) bool {
	if nilutil.IsNil(dst) || nilutil.IsNil(construction) {
		return false
	}
	return strings.TrimSpace(dst.Name()) == strings.TrimSpace(construction.Name())
}

// destinationHighSpeedModels returns the destination's high-speed lane
// allowlist; nil keeps the construction allowlist.
func (a *Agent) destinationHighSpeedModels() []string {
	if o := a.sessionModelValue(); o != nil && o.highSpeedModels != nil {
		return o.highSpeedModels
	}
	return a.highSpeedModels
}
