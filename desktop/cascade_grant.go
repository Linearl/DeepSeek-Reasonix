package main

import (
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// Task 225: contact-bound task-source grants. A dispatch from sourceContact
// hands targetContact work, so target's approval prompts may cascade to
// source's Ask channel while the grant is fresh. The registry is process-local
// by design: a restart ends the dispatched task's authorization window, and
// the fallback is the pre-225 local prompt — the safe side.

type cascadeGrant struct {
	source    string
	grantedAt time.Time
}

const cascadeGrantTTL = 24 * time.Hour

var (
	cascadeGrantsMu sync.Mutex
	cascadeGrants   = map[string]cascadeGrant{}
)

// cascadeApp is set at desktop startup; one App per process.
var cascadeApp *App

func registerCascadeGrant(target, source string) {
	target = strings.TrimSpace(target)
	source = strings.TrimSpace(source)
	if target == "" || source == "" || target == source {
		return
	}
	cascadeGrantsMu.Lock()
	cascadeGrants[target] = cascadeGrant{source: source, grantedAt: time.Now()}
	cascadeGrantsMu.Unlock()
}

// cascadeDelegateFor resolves the Ask channel of the task source for the
// session at selfPath (task 225). Any miss — no grant, an expired grant, the
// source runtime gone — returns ok=false and the caller keeps the prompt
// local: the exact pre-225 behavior. The source controller itself is the
// delegate: its own semantics decide (autopilot answers; otherwise its user
// is asked), which is the ruling's "delegate per the parent's policy".
func cascadeDelegateFor(selfPath string) (agent.Asker, string, bool) {
	if cascadeApp == nil {
		return nil, "", false
	}
	contact := strings.TrimSpace(agent.SessionContactID(selfPath))
	if contact == "" {
		return nil, "", false
	}
	cascadeGrantsMu.Lock()
	grant, has := cascadeGrants[contact]
	cascadeGrantsMu.Unlock()
	if !has || time.Since(grant.grantedAt) > cascadeGrantTTL {
		return nil, "", false
	}
	for _, t := range cascadeApp.sessionCollabLiveTargets(nil) {
		if t.contactID != grant.source || t.ctrl == nil {
			continue
		}
		if asker, ok := t.ctrl.(agent.Asker); ok {
			return asker, grant.source, true
		}
	}
	return nil, "", false
}

// cascadeSourceCapabilityFor (task 1022) reports what the desktop knows about
// a task source's ability to answer a forwarded ask — the tl 994 routeAsk
// screen's capability input. A heartbeat/守护线 source watches no panel, so it
// is never a legal cascade target even though it is a live runtime; the
// control layer keeps the prompt local fail-closed when this returns nil or
// an undeclared capability. Reachable only when cascadeDelegateFor already
// found a live runtime with that contact, so Liveness is true here.
func cascadeSourceCapabilityFor(source string) *control.CascadeSource {
	if cascadeApp == nil {
		return nil
	}
	contact := strings.TrimSpace(source)
	if contact == "" {
		return nil
	}
	for _, t := range cascadeApp.sessionCollabLiveTargets(nil) {
		if t.contactID != contact || t.ctrl == nil {
			continue
		}
		return &control.CascadeSource{
			Contact:       contact,
			Heartbeat:     cascadeApp.isHeartbeatTopicSession(t.topicID),
			AskResponder:  true,
			LatencyBudget: 0, // no declared latency yet; routeAsk keeps the configured wait
			Liveness:      true,
		}
	}
	return nil
}

// isHeartbeatTopicSession reports whether a tab/detached-runtime's bound topic
// was stamped by the automation scheduler (task 17 origin stamp). A heartbeat
// topic's controller is a live runtime but nobody watches its panel, so
// cascadeSourceCapabilityFor flags it as Heartbeat and the control layer's
// routeAsk screen keeps the child's prompt local instead of forwarding.
// Unknown/absent topicID is NOT a heartbeat — it is the "human by default"
// posture the pre-1022 code had, and the liveness/capability gates still
// guard it.
func (a *App) isHeartbeatTopicSession(topicID string) bool {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" || a == nil || a.topicState == nil {
		return false
	}
	raw, err := a.topicState.autoMetaRaw("", topicID)
	if err != nil || len(raw) == 0 {
		return false
	}
	return topicOriginIsHeartbeat(raw)
}
