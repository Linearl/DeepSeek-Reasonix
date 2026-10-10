package main

// Task 539 phase-1 guided-message routing (design review §2.3): during the
// yielding window the desktop turn is the only live writer, so a remote
// guided message for a desktop-held session must be injected into the
// holding tab's controller (mid-turn steer with the durable fallback to a
// queued follow-up — nothing is lost). The serve-side inbox endpoints stay
// foreground-scoped and untouched; this gate only answers requests that
// explicitly name a session the desktop holds.

import (
	"encoding/json"
	"net/http"

	"reasonix/internal/servepool"
	"reasonix/internal/sessioninbox"
)

// installServePoolInboxGate wires the app-owned inbox gate into the gateway.
func (a *App) installServePoolInboxGate(gw *servepool.Gateway) {
	if gw == nil {
		return
	}
	gw.SetInboxGate(a.servePoolInboxGate)
}

// servePoolInboxGate answers POST /p/<id>/inbox/items when the named session
// is held by a desktop tab; anything else forwards to the serve unchanged
// (its fence answers honestly).
func (a *App) servePoolInboxGate(req servepool.InboxGateRequest) (bool, int, string, []byte) {
	path := a.resolveGatewayTakeoverSessionPath(req.ProjectRoot, req.SessionName)
	if path == "" {
		return false, 0, "", nil
	}
	key := sessionRuntimeKey(path)
	a.mu.Lock()
	tabs := make([]*WorkspaceTab, 0, 2)
	for _, tab := range a.tabs {
		if tab != nil && sessionRuntimeKey(tab.currentSessionPath()) == key {
			tabs = append(tabs, tab)
		}
	}
	a.mu.Unlock()
	if len(tabs) == 0 {
		// Not desktop-held (or the lease moved on): the serve's fence is the
		// honest answerer.
		return false, 0, "", nil
	}
	tab := tabs[0]
	var body struct {
		Input          string `json:"input"`
		Display        string `json:"display"`
		Intent         string `json:"intent"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil || body.Input == "" {
		return true, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("missing input\n")
	}
	intent := sessioninbox.IntentFollowup
	trySteer := false
	if req.Intent == "steer" {
		intent = sessioninbox.IntentSteer
		trySteer = true
	}
	ctrl, err := a.inboxCtrl(tab.ID)
	if err != nil {
		return true, http.StatusConflict, "text/plain; charset=utf-8", []byte(err.Error() + "\n")
	}
	idempotency := body.IdempotencyKey
	if idempotency == "" {
		idempotency = "gateway-inbox-" + servepool.RandomToken()
	}
	rec, err := a.enqueueInboxWithController(tab.ID, ctrl, intent, body.Display, body.Input, nil, idempotency, trySteer, "", "")
	if err != nil {
		return true, http.StatusConflict, "text/plain; charset=utf-8", []byte(err.Error() + "\n")
	}
	payload, err := json.Marshal(map[string]any{
		"itemID":      rec.ItemID,
		"disposition": rec.Disposition,
		"position":    rec.Position,
		"paused":      rec.Paused,
		"idempotent":  rec.Idempotent,
	})
	if err != nil {
		return true, http.StatusInternalServerError, "text/plain; charset=utf-8", []byte(err.Error() + "\n")
	}
	return true, http.StatusAccepted, "application/json", payload
}
