package agent

import (
	"log/slog"
)

// steerCacheKeyProbe is the before-side snapshot of the provider-visible
// prompt fingerprint taken when a steer is about to be injected.
type steerCacheKeyProbe struct {
	key       string
	prefixLen int
}

// tl1608 observation pin (user directive 2026-11-15): these two slog lines
// bracket the steer injection and carry the provider-visible prompt
// fingerprint — the practical stand-in for the provider prompt cache key,
// which has no explicit wire field here (providers cache by prefix identity;
// the same fingerprint seeds context receipts). The hypothesis under test:
// a steer rewrites the prompt PREFIX → provider cache invalidated → full
// prefill (the reason a steer round can look like prefill_slow).
//
//   - changed=true  → the previously visible prefix was rewritten between the
//     probes: the provider cache is fully invalidated for the next request.
//     If this fires frequently, the follow-up belongs to the tl1602 cache
//     surface (or a dedicated fix task) — this pin is observation only and
//     deliberately changes no behavior.
//   - changed=false → the steer only appended after the old prefix: exactly
//     the one unavoidable suffix miss, not a prefix rewrite.
//
// Steers are user-initiated rare events, so hashing the full conversation
// twice per injection is acceptable.
func (a *Agent) logSteerCacheKeyBefore() steerCacheKeyProbe {
	msgs := modelInputMessages(a.sess.conversation.Messages)
	probe := steerCacheKeyProbe{prefixLen: len(msgs)}
	if len(msgs) > 0 {
		probe.key = providerVisibleFingerprint(msgs)
	}
	slog.Info("agent: steer_cache_key",
		"phase", "before",
		"cache_key", probe.key,
		"prefix_msgs", probe.prefixLen,
		"session", a.recoveryLogSessionName(),
	)
	return probe
}

// logSteerCacheKeyAfter re-fingerprints ONLY the prefix that existed at the
// before-probe (msgs[:before.prefixLen]). Comparing prefix-to-prefix is what
// makes the verdict meaningful: an appended steer message must NOT flip
// changed — that suffix miss is expected; only a rewrite of already-visible
// history (full cache invalidation) may.
func (a *Agent) logSteerCacheKeyAfter(before steerCacheKeyProbe) {
	msgs := modelInputMessages(a.sess.conversation.Messages)
	prefixLen := before.prefixLen
	if prefixLen > len(msgs) {
		prefixLen = len(msgs)
	}
	key := ""
	changed := false
	if prefixLen > 0 {
		key = providerVisibleFingerprint(msgs[:prefixLen])
		changed = key != before.key
	}
	slog.Info("agent: steer_cache_key",
		"phase", "after",
		"cache_key", key,
		"prefix_msgs", prefixLen,
		"msgs", len(msgs),
		"changed", changed,
		"session", a.recoveryLogSessionName(),
	)
}
