package agent

import (
	"log/slog"
	"strings"
	"sync/atomic"
)

// The session-scoped effort override lets a frontend switch the reasoning
// depth without rebuilding the runtime: the override rides the per-request
// channel (provider.Request.EffortOverride), and adapters apply it only when
// the endpoint's effort vocabulary accepts it — see requestEffort in the
// openai adapter and the PerRequestEfforts probe it exposes.

// effortVarying is implemented by providers whose effort vocabulary is
// request-scoped. Optional on purpose: providers that cannot vary depth per
// call keep the boot-time rebuild path in the frontends.
type effortVarying interface {
	PerRequestEfforts() []string
}

// sessionEffortOverride holds a session-scoped reasoning-depth override —
// empty when the configured depth stands. atomic.Value follows the
// responseLanguage/reasoningLanguage precedent: written rarely (an explicit
// switch), read on every request.
type sessionEffortOverride struct{ atomic.Value }

// SetSessionEffortOverride stores a session-scoped effort override and
// reports whether the running provider honors per-request depth. An empty
// level clears the override and always succeeds. A non-empty level is
// accepted only when the provider lists it in its per-request vocabulary;
// returning false tells the caller to fall back to the rebuild path instead
// of writing an override that would silently degrade to the configured depth
// on the wire.
func (a *Agent) SetSessionEffortOverride(level string) bool {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		a.sessionEffort.Store("")
		return true
	}
	// tl1660: a recovery-forked session no longer declines the per-request
	// override. The old guard (task 1301) deferred it to the rebuild path on
	// the theory that skipping the rebuild leaves the forked file in active
	// use — but the 20261117 incident (tab_f7ee41fe, 14-30s per switch) proved
	// the two concerns orthogonal on every axis:
	//   1. The override only changes the wire's reasoning depth
	//      (provider.Request.EffortOverride); it never touches the session
	//      file, the branch identity, or the transcript, so there is nothing
	//      for the reanchor semantics to conflict with.
	//   2. On a settled recovery copy the rebuild's "reanchor" is a no-op: the
	//      20261117 effort rebuilds forked nothing, moved no path, and resumed
	//      the same recovery file (logs: two declined→rebuild cycles, zero
	//      "session move follow" lines, cache_state=warm on the same path).
	//   3. The recovery copy is already in active use after its resume — every
	//      turn saves to it — so "the forked file stays in use" describes the
	//      normal state, not something the override worsens.
	//   4. The override survives a later conflict fork: sessionEffort lives on
	//      the Agent, and recovery swaps the Session object, not the Agent.
	// The rebuild path itself keeps its task-1301 semantics untouched (a
	// snapshot conflict still forks and re-anchors there); declining is just no
	// longer forced by effort switches. The model override keeps its guard —
	// a model change rebuilds the provider anyway.
	if path := strings.TrimSpace(a.sess.path); path != "" {
		if meta, ok, err := LoadBranchMeta(path); err == nil && ok && meta.Recovered {
			// tl1660: leave a trace on the fast path instead of the old decline
			// line, so a recovery-copy switch is still attributable in the logs
			// (paired against the desktop's fast-per-request line).
			slog.Info("agent: effort override accepted on recovery fork", "path", "fast-per-request")
		}
	}
	// Task 148: probe the effective destination (session model override when
	// armed) so a post-switch depth change validates against the vocabulary
	// the request will actually reach.
	varying, ok := a.providerForRequest().(effortVarying)
	if !ok {
		// Task 334: provider exposes no per-request vocabulary probe — the
		// other documented fallback cause (e.g. non-openai adapters).
		slog.Info("agent: effort override declined", "reason", "provider-not-effort-varying", "path", "fallback")
		return false
	}
	available := varying.PerRequestEfforts()
	for _, l := range available {
		if strings.EqualFold(strings.TrimSpace(l), level) {
			a.sessionEffort.Store(level)
			return true
		}
	}
	// Task 1388: log the vocabulary the probe saw, so a decline is
	// distinguishable between "provider exposes no probe" (previous branch)
	// and "probe present but the level missed it" — the gateway entries fixed
	// by the protocol-scoped GLM gate surfaced as exactly the latter.
	slog.Info("agent: effort override declined", "reason", "level-not-in-vocabulary", "level", level, "vocabulary", strings.Join(available, "|"), "path", "fallback")
	return false
}

// sessionEffortOverrideValue returns the stored override; empty when unset,
// which lets the configured depth stand.
func (a *Agent) sessionEffortOverrideValue() string {
	if v, ok := a.sessionEffort.Load().(string); ok {
		return v
	}
	return ""
}

// effortOverrideForRequest picks the request-scoped depth: the governor's
// engaged override wins — a running guard must not be outbid by a
// session-level depth bump — then the session override, then the configured
// depth (empty).
func (a *Agent) effortOverrideForRequest() string {
	if gov := a.governorOverride(); gov != "" {
		return gov
	}
	return a.sessionEffortOverrideValue()
}
