package agent

import (
	"strings"

	"reasonix/internal/provider"
)

// tl1562 observability helpers. The 20261112 incident (fork开发-新12: a
// session died on "无人值守自动续轮已停止" and the log could not close the
// loop single-threaded) named three gaps: projection invalidation carried no
// drift detail, provider empty responses carried no local reconciliation or
// session attribution, and output truncation reached the UI but never the
// log. The helpers in this file feed the structured lines that close those
// gaps; each log site stays grep-able and names the session.

// sessionLogRef names this conversation in a structured log line. The bound
// session path is stable across resume (desktop sessions); ephemeral and
// sub-agent sessions fall back to the transport identity, minted lazily on
// the same terms withProviderCacheSession uses, so a log line that fires
// before the first sampling request still carries attribution. Empty only
// when there is no conversation at all. Acceptance contract (tl1562 ①):
// every one of the three incident-form lines carries at least one of
// session/topic/tab — this is the "session" leg.
func (a *Agent) sessionLogRef() string {
	if a == nil {
		return ""
	}
	if path := strings.TrimSpace(a.SessionPath()); path != "" {
		return path
	}
	s := a.Session()
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cacheSessionID == "" {
		s.cacheSessionID = provider.NewCacheSessionID()
	}
	return s.cacheSessionID
}
