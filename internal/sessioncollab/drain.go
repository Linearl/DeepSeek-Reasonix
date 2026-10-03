package sessioncollab

import "context"

// Drain claims unread messages for contactID, hands the batch to fn, and acks
// the IDs fn returns as settled — all under a single file lock (B1 fix:
// same-lock Claim+Ack prevents two consumers from double-consuming). Refused
// (hop-ceiling) messages are always acked so they do not reappear on every
// pass. Messages fn does not settle stay queued for the next pass
// (at-least-once).
//
// Task 235: this is the batch settle primitive the drain_inbox tool wraps. It
// deliberately does not dispatch turns — D1 is pure pull into a tool result.
// ctx is the caller's request context; a user stop ends a contended lock wait
// immediately (task 461 P1).
// Host-side "pull then start a round" goes through the existing
// maybeDispatchInbox / TryEnqueueAndSteer paths, never through here.
func (s *MailStore) Drain(ctx context.Context, contactID string, fn func(pending, refused []MailMessage) []string) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	pending, refused, err := s.claimLocked(contactID)
	if err != nil {
		return err
	}
	if len(pending) == 0 && len(refused) == 0 {
		return nil
	}
	settled := fn(pending, refused)
	// Refused messages are gone for good (over the hop ceiling) — ack them
	// unconditionally so the caller does not see the same refusal forever.
	for _, m := range refused {
		settled = append(settled, m.ID)
	}
	return s.ackLocked(contactID, settled...)
}
