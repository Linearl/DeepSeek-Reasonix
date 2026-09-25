package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/tool"
)

// Task 284: subscribe_session — register a watch on another session's
// anomalies so its state changes push into THIS conversation (steer grade,
// 221 merge) instead of the manager hand-polling every 5 minutes.
//
// Lifecycle: one action-routed tool (subscribe | unsubscribe | list), the
// pattern the collaboration family already converged on (task 174). The
// service loop is process-wide and starts on first registration; with zero
// subscriptions it blocks on its wake channel — no polling surface (dispatch
// acceptance).
//
// Gate: registered only while experimental_event_trigger is on (task 230's
// switch — 284 is the persistent form of the same engine family, so one
// switch governs both, no new switch).
type subscribeSessionTool struct {
	cfg    SessionCollabConfig
	svc    *SubscribeService
	resolve func(to string) (contactID, sessionPath string, archived bool, err error)
}

// NewSubscribeSessionTool builds the subscription tool. resolve is OPTIONAL:
// production passes nil and the tool resolves through the same
// scanAddressable+ResolveTarget path talk_to_session uses (one identity
// definition per package); tests inject a stub to avoid building a session
// directory.
func NewSubscribeSessionTool(cfg SessionCollabConfig, svc *SubscribeService, resolve func(to string) (contactID, sessionPath string, archived bool, err error)) tool.Tool {
	return subscribeSessionTool{cfg: cfg, svc: svc, resolve: resolve}
}

// resolveTarget funnels subscribe and unsubscribe through one identity
// definition (task 174 pattern): injected stub when present, otherwise the
// production directory scan.
func (t subscribeSessionTool) resolveTarget(to string) (contactID string, archived bool, err error) {
	if t.resolve != nil {
		id, _, arch, rerr := t.resolve(to)
		return id, arch, rerr
	}
	ids := scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot)
	ident, rerr := ResolveTarget(ids, to)
	if rerr != nil {
		return "", false, rerr
	}
	return ident.ContactID, ident.Archived, nil
}

func (subscribeSessionTool) Name() string { return "subscribe_session" }

func (subscribeSessionTool) Description() string {
	return fmt.Sprintf("Watch another session and get its anomalies pushed into your conversation mid-turn (task 284). Actions: subscribe (register/renew a watch — same target renews, never double-pushes), unsubscribe (drop a watch; idempotent), list (active watches with remaining TTL). Events whitelist: %s. Pushes arrive as [订阅推送] steer messages through the same channel talk_to_session uses (steer degrades to followup when the panel switch is off). TTL expires silently — re-subscribe to renew. One background loop serves every subscription and idles completely while none exist. Requires the experimental_event_trigger switch. Read-only against peers: it never sends to the watched session.",
		strings.Join(SubscribeEventKinds(), ", "))
}

func (subscribeSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["subscribe","unsubscribe","list"],"description":"What to do. Required."},"target":{"type":"string","description":"Session to watch: contact_id, topic_id, or exact title. Required for subscribe/unsubscribe."},"events":{"type":"array","items":{"type":"string"},"description":"Event kinds to push (default [state_change]). Whitelist enforced server-side."},"ttl_s":{"type":"integer","description":"Subscription lifetime in seconds (default 3600, clamped 30..604800). Expires silently; re-subscribe to renew."},"interval_s":{"type":"integer","description":"Seconds between evaluations (default 30, clamped 5..300)."},"stuck_after_s":{"type":"integer","description":"For the stuck event: how long a target may run without turn progress before firing (default 600, clamped 60..86400)."}},"required":["action"]}`)
}

func (subscribeSessionTool) ReadOnly() bool { return true }

func (t subscribeSessionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action      string   `json:"action"`
		Target      string   `json:"target"`
		Events      []string `json:"events"`
		TTLS        int      `json:"ttl_s"`
		IntervalS   int      `json:"interval_s"`
		StuckAfterS int      `json:"stuck_after_s"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	switch action {
	case "list":
		return t.listResult()
	case "subscribe":
		if strings.TrimSpace(p.Target) == "" {
			return "", fmt.Errorf("target is required for subscribe (name the session to watch)")
		}
		return t.subscribe(p)
	case "unsubscribe":
		if strings.TrimSpace(p.Target) == "" {
			return "", fmt.Errorf("target is required for unsubscribe (or pass its contact id)")
		}
		return t.unsubscribe(p.Target)
	default:
		return "", fmt.Errorf("action must be subscribe, unsubscribe or list (got %q)", p.Action)
	}
}

func (t subscribeSessionTool) subscribe(p struct {
	Action      string   `json:"action"`
	Target      string   `json:"target"`
	Events      []string `json:"events"`
	TTLS        int      `json:"ttl_s"`
	IntervalS   int      `json:"interval_s"`
	StuckAfterS int      `json:"stuck_after_s"`
}) (string, error) {
	contactID, archived, err := t.resolveTarget(p.Target)
	if err != nil {
		return "", err
	}
	if archived {
		return "", fmt.Errorf("session %q is archived and cannot be watched (restore it first)", p.Target)
	}
	if contactID == "" {
		return "", fmt.Errorf("cannot resolve %q to a contact id — message it once first (first contact mints the address)", p.Target)
	}
	sub, err := t.svc.Subscribe(
		contactID, p.Target, p.Events,
		time.Duration(p.TTLS)*time.Second,
		time.Duration(p.IntervalS)*time.Second,
		time.Duration(p.StuckAfterS)*time.Second,
	)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"subscribed": true,
		"id":         sub.ID,
		"target":     sub.Target,
		"events":     sub.Events,
		"expires_at": sub.ExpiresAt.Format(time.RFC3339),
		"every_s":    sub.EveryS,
		"note":       "state changes and listed anomalies will push into this conversation as [订阅推送] steer messages until expiry; re-subscribe to renew, unsubscribe to stop",
	})
	return string(out), nil
}

func (t subscribeSessionTool) unsubscribe(target string) (string, error) {
	// Unsubscribe accepts either a raw contact id or anything the directory
	// resolves — the registry key is the contact id.
	id := strings.TrimSpace(target)
	if contactID, _, err := t.resolveTarget(target); err == nil && contactID != "" {
		id = contactID
	}
	removed := t.svc.Unsubscribe(id)
	out, _ := json.Marshal(map[string]any{
		"unsubscribed": removed,
		"id":           id,
		"note":         map[bool]string{true: "watch removed; no further pushes", false: "no such subscription (idempotent no-op)"}[removed],
	})
	return string(out), nil
}

func (t subscribeSessionTool) listResult() (string, error) {
	subs := t.svc.List()
	out, _ := json.Marshal(map[string]any{
		"subscriptions": subs,
		"count":         len(subs),
	})
	return string(out), nil
}
