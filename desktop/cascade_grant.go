package main

import (
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
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
