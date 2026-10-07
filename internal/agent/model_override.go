package agent

import (
	"log/slog"
	"strings"

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
// Only same-family switches (provider.Name equal, e.g. mimo/a → mimo/b) are
// accepted. The request-shaping surfaces that stay read from the construction
// provider — reasoning replay policy, projection, window mode, tool-call
// reasoning — are protocol-scoped, and one provider entry family shares one
// endpoint protocol, so they remain valid across the family's models.
// Cross-family targets decline here and the caller keeps its build+swap
// fallback, which re-derives every surface exactly as before.

// sessionModelOverride is the effective request destination: a resolved
// provider plus the canonical ref it serves. Stored through atomic.Value —
// written rarely (an explicit switch), read on every request, following the
// sessionEffortOverride/responseLanguage precedent.
type sessionModelOverride struct {
	ref  string
	prov provider.Provider
}

// SetSessionModelOverride stores a session-scoped model override and reports
// whether the running agent can serve the next request from the new model
// without a rebuild. An empty ref clears the override and always succeeds. A
// non-empty ref is accepted only when (a) the construction-time resolver seam
// is wired, (b) the ref resolves into a live provider, and (c) the resolved
// provider stays in the running provider's family (same Name) — returning
// false tells the caller to fall back to the rebuild path instead of arming a
// destination the request-shaping surfaces cannot vouch for.
func (a *Agent) SetSessionModelOverride(ref string) bool {
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
	if a.svc.prov != nil && prov.Name() != a.svc.prov.Name() {
		// Task 148: cross-family targets keep the rebuild path, which re-derives
		// the protocol-bound shaping surfaces the fast path leaves in place.
		slog.Info("agent: model override declined", "reason", "provider-family-mismatch", "ref", ref, "running", a.svc.prov.Name(), "incoming", prov.Name())
		return false
	}
	a.sessionModel.Store(&sessionModelOverride{ref: ref, prov: prov})
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
