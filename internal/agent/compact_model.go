package agent

import (
	"log/slog"
	"strings"

	"reasonix/internal/provider"
)

// 任务 707: the economic compaction model. Compaction digests are long
// outputs billed at the conversation model's rate; when the host arms
// Options.CompactModel (the lab switch + "provider/model" target, resolved
// boot-side by config.CompactModelLive), every summary request is served by
// that destination instead.
//
// Scope discipline (task book): only the *serving destination* switches. The
// summary request's safety caps, admission and chunking keep sizing against
// the conversation model's window (the fold plan is model-independent of who
// writes the digest), and the installed projection stays valid across later
// conversation-model switches — task 638 made projection validity a
// content-hash question (CoveredPrefixHash, model-independent) and demoted
// the promptCacheKey lineage to a pure cache namespace with rebind, so a
// digest written by an economic model under the conversation model's lineage
// key survives switching back and forth exactly as before.

// summaryDestination names the provider serving a summary request, the
// billable ref its usage events carry, and the pricing those events price
// against. The zero-prov shape never occurs: every constructor falls back to
// the conversation destination.
type summaryDestination struct {
	prov    provider.Provider
	ref     string
	pricing *provider.Pricing
}

// compactionDestination resolves the summary request's destination. With no
// compact model armed (the default, and the whole pre-707 surface) this is
// byte-for-byte the conversation destination. An armed ref resolves once
// through the same resolver seam the session model override uses (task 148);
// a successful resolution is cached for the agent's lifetime (a settings
// change rebuilds the agent, so no invalidation path is needed). Resolution
// failures are NOT cached: the next summary retries, and until one succeeds
// every summary falls back to the conversation model — compaction must never
// block on the economic model being misconfigured.
func (a *Agent) compactionDestination() summaryDestination {
	conversation := summaryDestination{
		prov:    a.providerForRequest(),
		ref:     a.destinationModelRef(),
		pricing: a.destinationPricing(),
	}
	ref := strings.TrimSpace(a.compactModel)
	if ref == "" {
		return conversation
	}
	if cached, ok := a.compactModelDestination.Load().(*summaryDestination); ok && cached != nil {
		return *cached
	}
	if a.svc.modelResolver == nil {
		slog.Warn("agent: compact model unavailable — resolver not wired; summary served by the conversation model",
			"ref", ref, "conversation_ref", conversation.ref)
		return conversation
	}
	prov, err := a.svc.modelResolver.Resolve(provider.Selection{Ref: ref})
	if err != nil {
		slog.Warn("agent: compact model unresolved — summary served by the conversation model",
			"ref", ref, "err", err.Error(), "conversation_ref", conversation.ref)
		return conversation
	}
	dest := &summaryDestination{prov: prov, ref: ref, pricing: a.compactModelPricing}
	a.compactModelDestination.Store(dest)
	slog.Info("agent: compaction summary served by the configured compact model", "ref", ref)
	return *dest
}

// summaryDestinationRef is the billable ref the NEXT summary request reaches:
// the armed compact ref when set (whether or not it currently resolves — the
// usage event follows the destination, and the 任务635 lane compares this ref
// across attempts so a mid-flight conversation-model switch cannot trick it
// into blindly resending a deterministic 400 the compact model produced),
// else the conversation ref. With the switch off this is exactly
// destinationModelRef(), byte-for-byte the pre-707 lane.
func (a *Agent) summaryDestinationRef() string {
	if ref := strings.TrimSpace(a.compactModel); ref != "" {
		return ref
	}
	return a.destinationModelRef()
}

// compactModelResolvedForTest exposes the cached resolution for assertions.
func (a *Agent) compactModelResolvedForTest() (string, bool) {
	if cached, ok := a.compactModelDestination.Load().(*summaryDestination); ok && cached != nil {
		return cached.ref, true
	}
	return "", false
}
