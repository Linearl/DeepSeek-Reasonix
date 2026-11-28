package control

import (
	"log/slog"
	"strings"

	"reasonix/internal/sessioninbox"
)

// InboxCancelResult is the authoritative receipt for a cancel+withdraw
// operation. Only IDs in DiscardedItemIDs are safe for a frontend to restore
// into its draft.
type InboxCancelResult struct {
	DiscardedItemIDs []string
	Warning          string
}

// logInboxCancelDecision emits one structured cancel record per discarded
// item (tl1747). Field contract matches the desktop delivery-decision lines
// (delivery_decision / decision_reason / session / topic) plus cancel_source
// for the who-cancelled distribution; session carries the item's CollabMailTo
// (empty for local queue entries). Observation only — the cancel semantics
// are untouched.
func logInboxCancelDecision(cancelSource string, discarded []sessioninbox.InboxItemMeta) {
	for _, item := range discarded {
		slog.Info("session-collab delivery decision",
			"delivery_decision", "cancelled",
			"decision_reason", "inbox_cancel",
			"session", item.CollabMailTo,
			"topic", "",
			"msg_id", item.ID,
			"cancel_source", cancelSource,
			"source", item.Source,
		)
	}
}

// CancelWithInboxItems stops the active turn and discards only the durable
// pending items explicitly owned by the cancelling frontend. Admission is
// paused around the batch deletion so TurnDone cannot race a cancelled item
// into a new provider turn. Unrelated inbox items remain intact.
func (c *Controller) CancelWithInboxItems(ids []string, source string) error {
	_, err := c.CancelWithInboxItemsResult(ids, source)
	return err
}

// CancelWithInboxItemsResult serializes withdrawal against every inbox
// admission path and returns exactly the durable messages that were removed.
// A consumed/running item is intentionally absent from the receipt.
func (c *Controller) CancelWithInboxItemsResult(ids []string, source string) (InboxCancelResult, error) {
	result := InboxCancelResult{DiscardedItemIDs: []string{}}
	c.inbox.admissionMu.Lock()
	defer c.inbox.admissionMu.Unlock()
	st, err := c.ensureInbox()
	if err != nil {
		c.Cancel()
		return result, err
	}
	wasPaused := st.Snapshot().Paused
	if err := st.SetPaused(true); err != nil {
		c.Cancel()
		return result, err
	}
	discarded, err := st.DiscardPendingItemsOwnedMetasResult(ids, strings.TrimSpace(source))
	if err != nil {
		// Keep the inbox paused for inspection if an item already crossed the
		// admission boundary. Cancellation still stops that in-flight turn.
		c.Cancel()
		return result, err
	}
	result.DiscardedItemIDs = sessioninbox.InboxItemIDs(discarded)
	// tl1747: one structured cancel record per discarded item (cancel_source
	// names the cancelling frontend).
	logInboxCancelDecision(strings.TrimSpace(source), discarded)
	c.Cancel()
	if !wasPaused {
		if err := st.SetPaused(false); err != nil {
			result.Warning = "The turn was stopped, but the message queue remains paused. Review it before resuming."
			return result, nil
		}
	}
	return result, nil
}
