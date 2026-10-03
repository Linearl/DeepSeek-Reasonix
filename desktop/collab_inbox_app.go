package main

import (
	"log/slog"

	"reasonix/internal/agent"
	"reasonix/internal/collabinbox"
	"reasonix/internal/config"
)

// Task 320 — the cross-session inbox panel's Wails surface.
//
// Every method answers a revision-stamped Snapshot (contract ①: two windows
// printing the same revision look at the same state), the panel's dismiss /
// decide / retention actions return the NEW snapshot directly so the caller
// never re-fetches to converge, and the state lives beside the mail directory
// (contract f: restart keeps entries, dismissals, decisions and settings).

// collabInboxStore opens the aggregate index over the shared mail dir. The
// sender-identity resolver (task 348) reads the same BranchMeta the directory
// scan uses, so heartbeat/system senders land in the right bucket; a session
// outside the scanned dirs simply falls back to the mention bucket.
func collabInboxStore() *collabinbox.Store {
	return collabinbox.New(config.SessionCollabMailDir(), func(contact string) string {
		for _, id := range agent.ScanCollabIdentityDirectory(config.SessionDir(), "") {
			if id.ContactID == contact {
				return id.IdentityType
			}
		}
		return ""
	})
}

// collabInboxViewer is the calling window's own contact id — it drives the
// approval sub-states (待我审 / 我发起的). No active session → "" and those
// sub-states simply stay false (honest absence, never a guess).
func (a *App) collabInboxViewer() string {
	if a == nil {
		return ""
	}
	if tab := a.activeTab(); tab != nil && tab.SessionPath != "" {
		return agent.SessionContactID(tab.SessionPath)
	}
	return ""
}

// ListCollabMail returns one revision-stamped page of the unified mail table.
// bucket: all|approval|mention|automation|system (empty = all); state:
// all|pendingMe|mine|decided; limit <= 0 uses the default (50, hard max 500);
// order: desc (newest first, the default) | asc (oldest first) — task 320 a's
// date sort, surfaced as a panel toggle (the index layer has always been
// dual-order; the agent query tool exposes the same field).
func (a *App) ListCollabMail(bucket, from, to, state string, limit int, includeDismissed bool, order string) (collabinbox.Snapshot, error) {
	snap, err := collabInboxStore().List(collabinbox.Query{
		Bucket:           bucket,
		From:             from,
		To:               to,
		State:            state,
		Viewer:           a.collabInboxViewer(),
		Order:            order,
		Limit:            limit,
		IncludeDismissed: includeDismissed,
	}, true) // panel calls may apply retention (the agent tool path never does)
	if err != nil {
		// The panel's frontend catch is intentionally silent (a closed gateway
		// must not crash it), which turns backend failures into an empty list
		// with no trace anywhere. Log it so a "N unread but empty panel"
		// report has a reachable cause.
		slog.Warn("collab inbox: ListCollabMail failed", "err", err,
			"bucket", bucket, "state", state, "limit", limit, "order", order)
	}
	return snap, err
}

// ListCollabMailChains returns the thread-grouped view (task 320 g): one row
// per conversation chain with its rounds expanded.
func (a *App) ListCollabMailChains(bucket string, limit int) (collabinbox.ChainSnapshot, error) {
	return collabInboxStore().Chains(collabinbox.Query{
		Bucket: bucket,
		Viewer: a.collabInboxViewer(),
		Limit:  limit,
	})
}

// CountUnreadCollabMail answers the icon-row badge (task 320 遗留 #1): how
// many unified-table entries are unread (the recipient's seen cursor does not
// cover them) and not dismissed — exactly what the panel's default view would
// list as awaiting attention. READ-ONLY by contract: applyRetention stays
// false, so a sidebar refresh never prunes the transport layer (that side
// effect belongs to the panel path alone), and no entries travel the wire —
// just the count.
func (a *App) CountUnreadCollabMail() (int64, error) {
	snap, err := collabInboxStore().List(collabinbox.Query{Unread: true, Limit: 1}, false)
	if err != nil {
		return 0, err
	}
	return int64(snap.Total), nil
}

// DismissCollabMail eliminates entries from the default view and returns the
// fresh snapshot (batch dismiss → new revision, e-①).
func (a *App) DismissCollabMail(ids []string) (collinboxSnapshot, error) {
	return collabInboxStore().Dismiss(ids)
}

// UndismissCollabMail restores previously eliminated entries.
func (a *App) UndismissCollabMail(ids []string) (collinboxSnapshot, error) {
	return collabInboxStore().Undismiss(ids)
}

// MarkCollabMailDecided records an approval verdict with its decider ("human"
// for a panel click; agent-driven decisions pass the contact id) — task 320 d
// (已裁决条目必须记录裁决者).
func (a *App) MarkCollabMailDecided(messageID, by string) (collinboxSnapshot, error) {
	if by == "" {
		by = "human"
	}
	return collabInboxStore().Decide(messageID, by)
}

// SetCollabMailRetention switches the retention window (7d|30d|90d|forever)
// and applies it immediately — task 320 c.
func (a *App) SetCollabMailRetention(retention string) (collinboxSnapshot, error) {
	return collabInboxStore().SetRetention(retention)
}

// collinboxSnapshot pins the wire type name for the Wails bindings.
type collinboxSnapshot = collabinbox.Snapshot
