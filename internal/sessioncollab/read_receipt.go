package sessioncollab

import (
	"context"
	"strings"
)

// SelfSendIdentityNotice is 任务1000：发信方与收信方是同一个会话时，回执
//（已读回执）必须追加的身世提醒——身份认知错乱时模型可能给自己发信，若回执
// 与普通回执无异，发信方会继续在错误身份下工作而不自知。导出为包级常量，
// 测试与任何需要判定「本条回执是否带身世提醒」的读口共用同一份文案，避免
// 硬编码漂移。
const SelfSendIdentityNotice = "注意：这条消息是发给你自己的。如果这不符合意图，请检查自己的身份认知。"

// SendReadReceipt delivers the task-309 read receipt for one collab mail: a
// system follow-up back to the original sender, naming the mail that just
// entered this session's context (turn injection / drain consumption). The
// receipt itself never requests a receipt — that would make a receipt storm
// self-sustaining — and it starts a fresh thread so it cannot be mistaken for
// a conversational reply. Best-effort: callers log-and-continue on error, and
// a missing mailbox/sender degrades to a no-op.
//
// 任务1000：originalSender == recipientContact（发信方=收信方，自发信）时，
// 回执正文追加 SelfSendIdentityNotice——身份认知错乱的自救提示。定时器
// 注入路径（任务1001 session_remind）结构上不经过跨会话投递/回执面（见
// internal/control/reminder.go 的豁免注释），因此不会被本判定误伤。
func SendReadReceipt(mailDir, originalSender, recipientContact, originalMsgID string) error {
	if strings.TrimSpace(mailDir) == "" || strings.TrimSpace(originalSender) == "" || strings.TrimSpace(recipientContact) == "" {
		return nil // nothing to answer to — best-effort by contract
	}
	body := "已读回执：你的消息 " + originalMsgID + " 已进入本会话上下文（task 309 read receipt）。本条为系统回执，无需回复。"
	if originalSender == recipientContact {
		// 任务1000：自发信的身世提醒——发信方即收信方，回执提示检查身份认知。
		body += " " + SelfSendIdentityNotice
	}
	_, err := NewMailStore(mailDir).Deliver(context.Background(), MailMessage{
		From:     recipientContact,
		To:       originalSender,
		Body:     body,
		Delivery: string(DeliveryFollowup),
		// ReceiptRequested deliberately false: receipts never chain.
		// Task 320: platform-generated mail stamps its bucket at creation, so
		// the system bucket never depends on a body-text sniff.
		Kind: "system",
	})
	return err
}
