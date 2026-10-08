package boot

import (
	"strings"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// collabInboxSettledProbe builds the Task 263 settled probe for the session
// inbox recovery chain: it reports whether an inbox row's collab source
// message was already handed over before the restart, so recovery drops the
// row as applied residue instead of replaying it onto the guidance shelf.
//
// Task 641 root cause fixed here. The previous closure had two independent
// coordinate mismatches that kept it false forever (so the 263/300 drop never
// fired and consumed cross-session messages replayed onto the shelf after
// every install restart):
//
//  1. Contact: the pump acks a delivered mail under the RECIPIENT's cursor
//     file (<to>.seen.json); the closure looked in the SENDER's (from).
//  2. Key: since task 309 the row's idempotency key defaults to the content
//     hash "collab:<from>:<thread>:<hash8>" — never the mail id the cursor
//     and the receipt store are keyed by.
//
// Both are fixed by using the task-309 delivery record the pump stamps on
// every row (meta.CollabMsgID = the real mail id, meta.CollabMailTo = the
// recipient contact):
//
//   - The task-570 delivery receipt first: the pump's own "this message was
//     handed over" authority (task 585's re-delivery skip consults the same
//     store), written atomically per message — it survives the
//     update-restart window that can lose a cursor ack.
//   - The recipient's seen cursor second, covering a receipt write lost to
//     the same window after the ack survived.
//
// Rows without a usable mail id answer false: no positive evidence, no drop —
// the row survives honestly (at-least-once; a pump redelivery dedupes onto
// the row's idempotency key). The probe is invoked under the Store's
// transaction lock, so it stays lock-free file reads only.
func collabInboxSettledProbe(mailDir string) func(sessioninbox.InboxItemMeta) bool {
	return func(meta sessioninbox.InboxItemMeta) bool {
		if !strings.HasPrefix(meta.Source, "collab:") {
			return false // only collab-sourced rows carry a mailbox delivery record
		}
		msgID := meta.CollabMsgID
		if msgID == "" {
			// Legacy per-message rows (pre-309) carried "collab:<msgID>" as
			// the idempotency key. Content keys contain colons — they are not
			// mail ids, and looking them up can only answer false.
			candidate := strings.TrimPrefix(meta.Idempotency, "collab:")
			if candidate != "" && !strings.Contains(candidate, ":") {
				msgID = candidate
			}
		}
		if msgID == "" {
			return false
		}
		mail := sessioncollab.NewMailStore(mailDir)
		if r, ok := mail.DeliveryReceipt(msgID); ok && sessioncollab.DeliveryReceiptSettled(r.Outcome) {
			return true
		}
		if meta.CollabMailTo == "" {
			return false // no recipient coordinate: the cursor cannot be consulted
		}
		return mail.Settled(meta.CollabMailTo, msgID)
	}
}
