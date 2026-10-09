package sessioncollab

// 任务 570 (c1)：投递回执（delivery receipt）。
//
// talk_to_session 只能把消息写进持久信箱，投递的终态（是否注入当轮 / 降级排队 /
// 被拒绝 / 暂时失败）由宿主投递泵（desktop pump）在之后的轮次里异步定局。在这份
// 回执存储出现之前，定局结果只存在于两处：进程日志和发给发送方的自由文本系统
// 通知——没有任何按 messageId 可查询的结构化状态，发送方永远只能看到「已入队」
// 绿灯。这里的职责只有一个：让泵把每条消息的定局结果落到磁盘，供
// get_message_status（internal/agent）与 peek_own_inbox 按消息 id 查询。
//
// 存储形态：每条消息一个文件 <mailDir>/receipts/<messageId>.json，原子写
// （tmp+rename），最后一次写入胜出。选择按消息分文件而不是单一大文件/单文件
// map，是因为查询键就是消息 id（O(1) 定位），且失败重试的每次定局更新不放大
// 重写成本；回执只含元数据（id/双方/结局/原因短句/时间），不含消息正文。
// 保留策略（随任务 320 retention 过期清理）留待后续任务，不在本切片。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 投递结局词汇表。与 desktop 泵的通知分类一一对应：
//   - injected：steer 真正注入了目标当轮（disposition started/steer_accepted）
//   - queued_followup：落为排队 follow-up——delivery=followup 时这是正常落点，
//     delivery=steer 时这是降级（发送方需要知道的正是这个区别）
//   - refused_hop：协作链达到 hop 上限，消息被拒绝并丢弃
//   - refused_provenance：thread 来源无法核实，消息被拒绝并丢弃
//   - refused_cross_wire：thread 串线到另一条链，消息被拒绝并丢弃
//   - failed_retrying：目标暂不可用，消息未 ack、将自动重试（非终态）
//   - open_retry_exhausted（任务579）：消息已入目标收件箱队列，但空闲开轮尝试
//     用尽重试预算（3 次）——消息保留在队列未丢弃；空闲桥在队首变化或会话恢复
//     活动后会重新获得预算，人工重试同样有效
const (
	ReceiptInjected           = "injected"
	ReceiptQueuedFollowup     = "queued_followup"
	ReceiptRefusedHop         = "refused_hop"
	ReceiptRefusedProvenance  = "refused_provenance"
	ReceiptRefusedCrossWire   = "refused_cross_wire"
	ReceiptFailedRetrying     = "failed_retrying"
	ReceiptOpenRetryExhausted = "open_retry_exhausted"
)

// 任务 599：投递泵的建议轮询节奏（毫秒），单一事实源。
//
// desktop 泵的 ticker（约 4 秒一轮，投递定局的节奏）与 talk_to_session 返回体
// 的 retryAfterMs 字段共用此常量：泵侧用 time.Duration(DeliveryPumpIntervalMS)
// * time.Millisecond 推导 ticker 周期，工具侧直接写进返回体，调用方据此安排
// get_message_status 的复查时机。放在本包是因为 desktop（main 包）与
// internal/agent 双方都已依赖 sessioncollab，而两者互相不可依赖——这里唯一定义，
// 任何一侧改节奏都会在编译期把另一侧一起带走，杜绝「字段说 4 秒、泵实际 5 秒」
// 的漂移误读（本字段的存在意义就是降低误读率，值错了比没有更糟）。
const DeliveryPumpIntervalMS = 4000

// ValidDeliveryReceiptOutcome reports whether s is part of the outcome
// vocabulary above. The store refuses to persist anything else, so the query
// tool never has to guess what an unknown outcome means.
func ValidDeliveryReceiptOutcome(s string) bool {
	switch s {
	case ReceiptInjected, ReceiptQueuedFollowup, ReceiptRefusedHop,
		ReceiptRefusedProvenance, ReceiptRefusedCrossWire, ReceiptFailedRetrying,
		ReceiptOpenRetryExhausted:
		return true
	default:
		return false
	}
}

// DeliveryReceiptSettled reports whether an outcome is terminal in the sense of
// 任务585: the delivery pass finished handling the message (injected, queued
// into the target's durable inbox, refused for good, or exhausted its idle-turn
// budget with the message kept queued). Only failed_retrying is not settled —
// that outcome names a pass that did NOT hand the message over, so the pump
// must retry it. The pump uses this to skip re-claiming a message whose mail
// cursor lost its ack in an update-restart window: a settled receipt proves a
// prior pass handed the message over, so re-delivering it would re-inject an
// already-handled message into the target.
func DeliveryReceiptSettled(outcome string) bool {
	return outcome != ReceiptFailedRetrying && ValidDeliveryReceiptOutcome(outcome)
}

// DeliveryReceipt is one message's settle record, written by the host pump at
// the moment the message's outcome is decided (task 570 c1).
type DeliveryReceipt struct {
	MessageID string `json:"messageId"`
	// From/To are the sender and recipient contact ids. The query tool checks
	// the caller against both before answering: a receipt is visible to the
	// two parties of the message, to nobody else.
	From string `json:"fromContactId,omitempty"`
	To   string `json:"toContactId"`
	// Delivery is the delivery mode in force for this message
	// (followup | steer) — a queued_followup outcome only means "降级" when
	// the mode was steer, so the mode rides the receipt.
	Delivery string `json:"delivery,omitempty"`
	// Outcome is one of the Receipt* vocabulary values.
	Outcome string `json:"outcome"`
	// Detail is a short, bounded cause (the refusal text class or the delivery
	// error), for display. It must never carry the message body.
	Detail string `json:"detail,omitempty"`
	// At is when this outcome was recorded (ms epoch). A retry that later
	// succeeds overwrites the receipt, so At always names the LATEST outcome.
	At int64 `json:"at"`
	// Attempts counts failed delivery passes recorded for this message
	// (failed_retrying only; 1 on the first failure). A settled outcome
	// leaves the last failure count in place for diagnosis.
	Attempts int `json:"attempts,omitempty"`
}

// receiptPath is the per-message receipt file. The receipts/ subdirectory
// keeps the mail root flat for the existing `*.inbox.jsonl` /
// `*.sent.jsonl` scans (PendingContacts, History, CountSentFromToday) — none
// of them recurse, so a subdirectory cannot leak into those surfaces.
func (s *MailStore) receiptPath(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		messageID = "unknown"
	}
	return filepath.Join(s.root, "receipts", messageID+".json")
}

// RecordDeliveryReceipt persists one outcome for one message, overwriting any
// previous record (latest outcome wins — a retry that succeeds must replace
// the failed_retrying record, or the sender would keep seeing a stale
// failure). For failed_retrying with Attempts unset, the count continues from
// the previous record so consecutive failures accumulate across passes.
//
// The write takes the mail directory's cross-process lock: two desktop
// processes may both pump, and read-modify-write (attempts) must not
// interleave — the same discipline Deliver/Ack already follow.
func (s *MailStore) RecordDeliveryReceipt(ctx context.Context, r DeliveryReceipt) error {
	r.MessageID = strings.TrimSpace(r.MessageID)
	if r.MessageID == "" {
		return errors.New("sessioncollab: delivery receipt needs a message id")
	}
	if r.To == "" {
		return errors.New("sessioncollab: delivery receipt needs a recipient")
	}
	if !ValidDeliveryReceiptOutcome(r.Outcome) {
		return fmt.Errorf("sessioncollab: unknown delivery receipt outcome %q", r.Outcome)
	}
	if r.At == 0 {
		r.At = time.Now().UnixMilli()
	}
	if len(r.Detail) > 300 {
		r.Detail = r.Detail[:300]
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if r.Outcome == ReceiptFailedRetrying && r.Attempts <= 0 {
		// Continue the failure chain: the caller (the pump pass) does not know
		// how many failures came before it.
		if prior, ok := s.loadReceiptLocked(r.MessageID); ok {
			r.Attempts = prior.Attempts + 1
		} else {
			r.Attempts = 1
		}
	}
	return atomicWriteJSON(s.receiptPath(r.MessageID), r)
}

// DeliveryReceipt answers one message's latest recorded outcome. Read-only
// and lock-free: receipt files are written by atomic rename, so a reader
// sees either the old or the new file, never a partial one. ok=false means
// no outcome has been recorded (the message may still be waiting for its
// first pump pass — that is "pending", not "failed").
func (s *MailStore) DeliveryReceipt(messageID string) (DeliveryReceipt, bool) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return DeliveryReceipt{}, false
	}
	b, err := os.ReadFile(s.receiptPath(messageID))
	if err != nil {
		return DeliveryReceipt{}, false
	}
	var r DeliveryReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return DeliveryReceipt{}, false
	}
	return r, true
}

// loadReceiptLocked decodes one receipt file under the caller's lock
// (RecordDeliveryReceipt's attempts continuation). Same decode as
// DeliveryReceipt; kept separate so the locked path needs no second unlock.
func (s *MailStore) loadReceiptLocked(messageID string) (DeliveryReceipt, bool) {
	b, err := os.ReadFile(s.receiptPath(messageID))
	if err != nil {
		return DeliveryReceipt{}, false
	}
	var r DeliveryReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return DeliveryReceipt{}, false
	}
	return r, true
}
