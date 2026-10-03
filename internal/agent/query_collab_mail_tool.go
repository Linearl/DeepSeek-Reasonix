package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/collabinbox"
	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// Task 320 b: query_collab_mail — the in-session, SQL-shaped READ of the
// cross-session mail history. Safety boundary (task 320 设计要点 3): read-only
// (ReadOnly()=true, no dismiss/decide verbs here), always limited (default 50,
// hard ceiling 500), fixed field whitelist (the Entry struct — no arbitrary
// expressions, no body dumps beyond a bounded preview). It reads the SAME
// unified table the panel consumes, and because the index only sees messages
// actually sitting in a recipient inbox, a queued send never shows up as
// delivered (contract ②).
type queryCollabMailTool struct{ cfg SessionCollabConfig }

// NewQueryCollabMailTool builds the history query against the shared mail dir.
func NewQueryCollabMailTool(cfg SessionCollabConfig) tool.Tool {
	return queryCollabMailTool{cfg: cfg}
}

// ScanCollabIdentityDirectory exports the contact-directory scan for hosts
// (task 320/348): the desktop inbox panel resolves sender identity types
// through it so heartbeat/system senders classify without a body sniff.
func ScanCollabIdentityDirectory(sessionDir, workspaceRoot string) []sessioncollab.Identity {
	return scanAddressable(sessionDir, workspaceRoot)
}

func (queryCollabMailTool) Name() string { return "query_collab_mail" }

func (queryCollabMailTool) Description() string {
	return "Query the cross-session mail history (task 320) — read-only, no cursor side effects (drain_inbox is the consuming half). Filter by from/to contact, bucket (approval|mention|automation|system — the five inbox views), thread_id, since/until (ms epoch), unread_only, and state (all|pendingMe|mine|decided for the approval sub-states). Returns the unified entry table with delivered/read status and decidedBy (who approved: \"human\" or a contact id), newest first, limit default 50 / hard max 500 with a truncated flag when more matched. Only messages that really landed in a recipient's inbox appear — queued sends never do. Strictly read-only."
}

func (queryCollabMailTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"from":{"type":"string","description":"Only messages sent BY this contact_id."},"to":{"type":"string","description":"Only messages sent TO this contact_id."},"bucket":{"type":"string","enum":["all","approval","mention","automation","system"],"description":"Five-bucket inbox view (default all)."},"thread_id":{"type":"string","description":"Only messages on this conversation thread."},"since":{"type":"integer","description":"Inclusive lower bound on at (ms epoch)."},"until":{"type":"integer","description":"Exclusive upper bound on at (ms epoch)."},"unread_only":{"type":"boolean","description":"Only messages the recipient has not consumed yet."},"state":{"type":"string","enum":["all","pendingMe","mine","decided"],"description":"Approval sub-state filter (pendingMe = waiting on MY verdict)."},"limit":{"type":"integer","description":"Max rows (default 50, hard max 500)."},"offset":{"type":"integer","description":"Pagination offset into the filtered set."},"order":{"type":"string","enum":["desc","asc"],"description":"Date order (default desc = newest first)."}},"required":[]}`)
}

func (queryCollabMailTool) ReadOnly() bool { return true }

func (t queryCollabMailTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		From       string `json:"from"`
		To         string `json:"to"`
		Bucket     string `json:"bucket"`
		ThreadID   string `json:"thread_id"`
		Since      int64  `json:"since"`
		Until      int64  `json:"until"`
		UnreadOnly bool   `json:"unread_only"`
		State      string `json:"state"`
		Limit      int    `json:"limit"`
		Offset     int    `json:"offset"`
		Order      string `json:"order"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if !collabinbox.ValidBucket(p.Bucket) {
		return "", fmt.Errorf("query_collab_mail: unknown bucket %q (want all|approval|mention|automation|system)", p.Bucket)
	}
	switch p.State {
	case "", collabinbox.StateAll, collabinbox.StatePendingMe, collabinbox.StateMine, collabinbox.StateDecided:
	default:
		return "", fmt.Errorf("query_collab_mail: unknown state %q (want all|pendingMe|mine|decided)", p.State)
	}
	if p.Order != "" && p.Order != "desc" && p.Order != "asc" {
		return "", fmt.Errorf("query_collab_mail: unknown order %q (want desc|asc)", p.Order)
	}
	if p.Since != 0 && p.Until != 0 && p.Since >= p.Until {
		return "", fmt.Errorf("query_collab_mail: since must precede until")
	}

	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return "", fmt.Errorf("query_collab_mail: no mail directory configured")
	}
	// Sender-identity enrichment (task 348): heartbeat/system senders bucket
	// correctly without a body sniff. One directory scan feeds a map — the
	// resolver must stay O(1), classification runs per entry.
	identityByContact := map[string]string{}
	for _, id := range scanAddressable(t.cfg.SessionDir, t.cfg.WorkspaceRoot) {
		if id.ContactID != "" && id.IdentityType != "" {
			identityByContact[id.ContactID] = id.IdentityType
		}
	}
	resolver := func(contact string) string { return identityByContact[contact] }
	store := collabinbox.New(mailDir, resolver)
	// task 461 P1: the request ctx rides all the way into the lock wait, so a
	// user stop cancels a contended acquire immediately instead of hanging the
	// tool for the whole 5s budget (or forever, before the fix).
	snap, err := store.List(ctx, collabinbox.Query{
		Bucket:   p.Bucket,
		From:     p.From,
		To:       p.To,
		ThreadID: p.ThreadID,
		Since:    p.Since,
		Until:    p.Until,
		Unread:   p.UnreadOnly,
		State:    p.State,
		Viewer:   t.cfg.currentContactID(),
		Limit:    p.Limit,
		Offset:   p.Offset,
		Order:    p.Order,
	}, false) // applyRetention=false: a read never mutates the transport layer
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"revision":  snap.Revision,
		"returned":  snap.Returned,
		"total":     snap.Total,
		"truncated": snap.Truncated,
		"limit":     snap.Returned,
		"retention": snap.Settings.Retention,
		"note":      "history only — delivered means it sits in the recipient inbox; drain_inbox is the consuming half",
		"entries":   snap.Entries,
	})
	return string(out), nil
}
