package sessioncollab

import (
	"context"
	"strings"
)

// SendReadReceipt delivers the task-309 read receipt for one collab mail: a
// system follow-up back to the original sender, naming the mail that just
// entered this session's context (turn injection / drain consumption). The
// receipt itself never requests a receipt — that would make a receipt storm
// self-sustaining — and it starts a fresh thread so it cannot be mistaken for
// a conversational reply. Best-effort: callers log-and-continue on error, and
// a missing mailbox/sender degrades to a no-op.
func SendReadReceipt(mailDir, originalSender, recipientContact, originalMsgID string) error {
	if strings.TrimSpace(mailDir) == "" || strings.TrimSpace(originalSender) == "" || strings.TrimSpace(recipientContact) == "" {
		return nil // nothing to answer to — best-effort by contract
	}
	_, err := NewMailStore(mailDir).Deliver(context.Background(), MailMessage{
		From:     recipientContact,
		To:       originalSender,
		Body:     "已读回执：你的消息 " + originalMsgID + " 已进入本会话上下文（task 309 read receipt）。本条为系统回执，无需回复。",
		Delivery: string(DeliveryFollowup),
		// ReceiptRequested deliberately false: receipts never chain.
		// Task 320: platform-generated mail stamps its bucket at creation, so
		// the system bucket never depends on a body-text sniff.
		Kind: "system",
	})
	return err
}
