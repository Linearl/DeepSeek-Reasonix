package serve

import (
	"reasonix/internal/agent"
)

// HistoryJSONForFile renders a saved transcript file as the /history JSON
// payload — the exact shape the live GET /history endpoint returns — without
// touching any Server runtime state (no controller, no mirror map, no lease).
// It is the renderer behind the servepool gateway's inline virtual-project
// history (e.g. the desktop "global" session scope, which has no serve
// process of its own).
func HistoryJSONForFile(path string) (any, error) {
	session, err := agent.LoadSession(path)
	if err != nil {
		return nil, err
	}
	return historyMessages(session.Messages), nil
}
