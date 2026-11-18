package collabinbox

import (
	"context"
	"strings"

	"reasonix/internal/sessioncollab"
)

// 派发四态（task 1696：跨会话收件箱人类可用——派发记录逐行 = 目标会话·角色·
// 四态·时间·跳转）。状态全部读持久审计层（*.sent.jsonl + receipts/*.json +
// 收件箱 threadId 回信链），零内存态——重启后四态与历史仍在（验收④）。
//
// 映射（判定顺序即优先级，一行只落一态）：
//   已拒绝 DispatchRefused  —— 回执 outcome 是终局拒绝（refused_* 三类 /
//     ignored_admission 准入终局否决 / open_retry_exhausted 打开重试耗尽）。
//   已回信 DispatchReplied  —— 收件库存在 threadId == 本消息 ID 的信（回信
//     链判定，任务 194 的 thread 语义）。回信优先于投递态：已注入但对方回了，
//     人类关心的是「回了」。
//   进行中 DispatchInFlight —— 回执 injected（对方上下文已收到、尚未回信）或
//     failed_retrying（投递失败仍在重试，非终局）。
//   等待   DispatchWaiting  —— 无回执（还没赶上第一次 pump）或
//     queued_followup（降级排队，尚未落地）。诚实缺席：没有回执就显示等待，
//     绝不猜成别的态。
const (
	DispatchWaiting  = "waiting"
	DispatchInFlight = "in_flight"
	DispatchReplied  = "replied"
	DispatchRefused  = "refused"
)

// ValidDispatchStatus reports whether v is one of the four states — the
// frontend renders exactly these four badges, so an unknown value would be a
// silent blank badge rather than an honest state.
func ValidDispatchStatus(v string) bool {
	switch v {
	case DispatchWaiting, DispatchInFlight, DispatchReplied, DispatchRefused:
		return true
	}
	return false
}

// dispatchRefusedOutcomes is the receipt-outcome vocabulary that settles a
// dispatch as refused. Everything else in the receipt vocabulary (injected,
// queued_followup, failed_retrying) is non-terminal and maps elsewhere; any
// UNKNOWN outcome fails safe to in_flight (it may still settle), never to
// refused — refusing is a claim about the other side, and a vocabulary typo
// must not manufacture one.
var dispatchRefusedOutcomes = map[string]bool{
	sessioncollab.ReceiptRefusedHop:         true,
	sessioncollab.ReceiptRefusedProvenance:  true,
	sessioncollab.ReceiptRefusedCrossWire:   true,
	sessioncollab.ReceiptIgnoredAdmission:   true,
	sessioncollab.ReceiptOpenRetryExhausted: true,
}

// SentDispatchRow is one outgoing message in the dispatch panel — the
// sender-side twin of Entry (which is the receiver-side row). Role rides the
// row so the panel renders 「目标会话·角色」 without a second lookup.
type SentDispatchRow struct {
	ID   string `json:"id"`
	From string `json:"fromContactId"`
	To   string `json:"toContactId"`
	// FromRole/ToRole are the five-value identity types (human|main|sub|
	// heartbeat|system) resolved at read time; empty = unknown identity.
	FromRole string `json:"fromRole,omitempty"`
	ToRole   string `json:"toRole,omitempty"`
	// ToTitle is the recipient title the sender recorded at send time
	// (RecordSent) — a stale snapshot by design (session renames are resolved
	// by the frontend directory, same as the inbox panel).
	ToTitle string `json:"toTitle,omitempty"`
	// Preview is a bounded body excerpt (the panel is a browser, not a
	// terminal — full text stays in the mail detail read).
	Preview      string `json:"preview"`
	Delivery     string `json:"delivery,omitempty"`
	RequireReply bool   `json:"requireReply,omitempty"`
	At           int64  `json:"at"`
	// Status is one of the four dispatch states above.
	Status string `json:"status"`
	// StatusAt is when the current status was recorded (ms epoch): the send
	// time for waiting, the receipt time otherwise. 0 = unknown.
	StatusAt int64 `json:"statusAt,omitempty"`
	// StatusDetail is the receipt's bounded cause for display (refusal text
	// class / delivery error). It never carries the message body.
	StatusDetail string `json:"statusDetail,omitempty"`
	// ReplyPreview is the first reply's preview when Status == replied — the
	// human reads what came back without opening the thread view.
	ReplyPreview string `json:"replyPreview,omitempty"`
	ReplyAt      int64  `json:"replyAt,omitempty"`
}

// SentDispatchSnapshot is the dispatch panel's read model (task 1696).
type SentDispatchSnapshot struct {
	Rows      []SentDispatchRow `json:"rows"`
	Total     int               `json:"total"`
	Returned  int               `json:"returned"`
	Truncated bool              `json:"truncated"`
	// Degraded marks a lock-busy read (same contract as Snapshot.Degraded):
	// the panel must show 「读取受限」, never an honest-looking empty list.
	Degraded bool `json:"degraded,omitempty"`
}

// dispatchPreview bounds a body to a single-line excerpt.
func dispatchPreview(body string) string {
	body = strings.ReplaceAll(body, "\r", " ")
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.TrimSpace(body)
	const max = 160
	r := []rune(body)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return body
}

// resolveRole maps a contact to its identity type; empty resolver or unknown
// contact → "" (the row renders without a role badge — honest absence).
func (s *Store) resolveRole(contact string) string {
	if s.resolver == nil {
		return ""
	}
	role := strings.TrimSpace(s.resolver(contact))
	// The resolver's return value is the five-value identity vocabulary;
	// anything else is treated as unknown rather than rendered as a role.
	if !validIdentityRole(role) {
		return ""
	}
	return role
}

func validIdentityRole(v string) bool {
	switch v {
	case sessioncollab.IdentityHuman, sessioncollab.IdentityMain,
		sessioncollab.IdentitySub, sessioncollab.IdentityHeartbeat,
		sessioncollab.IdentitySystem:
		return true
	}
	return false
}

// SentDispatch builds the dispatch panel view: every sent log row on this
// machine joined with its delivery receipt and reply-chain state. Read-only —
// no sweep, no retention, no dismissal writes (the panel is 只读展示面, a
// non-goal red line: no dispatch action entry here).
//
// limit <= 0 uses the default (50, hard max 500 — same page discipline as
// List). Rows sort newest-first (the freshest dispatch is the one to check).
func (s *Store) SentDispatch(ctx context.Context, limit int) (SentDispatchSnapshot, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	sent, degraded := s.mail.AllSent(ctx)
	if degraded {
		return SentDispatchSnapshot{Degraded: true}, nil
	}
	// One history pass builds the reply index: threadId == original message
	// ID means the original was answered. Also keeps the first reply's
	// preview for the row (the earliest reply by time).
	type replyInfo struct {
		preview string
		at      int64
	}
	replies := map[string]replyInfo{}
	rows, histDegraded := s.mail.History(ctx)
	if histDegraded {
		// Reply detection needs the inbox side; without it a "replied" state
		// would silently degrade to in_flight. Surface the degradation — the
		// panel shows 读取受限 rather than mis-stated statuses.
		return SentDispatchSnapshot{Degraded: true}, nil
	}
	for _, r := range rows {
		tid := strings.TrimSpace(r.Mail.ThreadID)
		if tid == "" {
			continue
		}
		if cur, ok := replies[tid]; !ok || r.Mail.At < cur.at {
			replies[tid] = replyInfo{preview: dispatchPreview(r.Mail.Body), at: r.Mail.At}
		}
	}
	snap := SentDispatchSnapshot{Total: len(sent)}
	if len(sent) > limit {
		sent = sent[:limit]
		snap.Truncated = true
	}
	for _, m := range sent {
		row := SentDispatchRow{
			ID:           m.ID,
			From:         m.From,
			To:           m.To,
			ToTitle:      strings.TrimSpace(m.ToTitle),
			Preview:      dispatchPreview(m.Body),
			Delivery:     m.Delivery,
			RequireReply: m.RequireReply,
			At:           m.At,
			Status:       DispatchWaiting,
			StatusAt:     m.At,
		}
		row.FromRole = s.resolveRole(m.From)
		row.ToRole = s.resolveRole(m.To)
		if receipt, ok := s.mail.DeliveryReceipt(m.ID); ok {
			row.StatusAt = receipt.At
			row.StatusDetail = receipt.Detail
			switch {
			case dispatchRefusedOutcomes[receipt.Outcome]:
				row.Status = DispatchRefused
			case receipt.Outcome == sessioncollab.ReceiptInjected ||
				receipt.Outcome == sessioncollab.ReceiptFailedRetrying:
				row.Status = DispatchInFlight
			default:
				// queued_followup and any unknown outcome keep waiting: the
				// message has not landed yet. Fail-safe away from refusal.
				row.Status = DispatchWaiting
			}
		}
		if reply, ok := replies[m.ID]; ok {
			row.Status = DispatchReplied
			row.StatusAt = reply.at
			row.ReplyPreview = reply.preview
			row.ReplyAt = reply.at
		}
		snap.Rows = append(snap.Rows, row)
	}
	snap.Returned = len(snap.Rows)
	return snap, nil
}
