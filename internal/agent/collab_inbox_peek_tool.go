package agent

// 任务 570 (c2)：peek_own_inbox —— 会话读自己跨会话信箱的只读通道。
//
// 背景（task 568/570 调研核实）：投递的降级/拒绝回执以 kind=system 系统通知的
// 形式落进发送方自己的信箱，但桌面 OFF 链下 drain_inbox 未注册（且它读的是
// 会话内队列，不是跨会话 MailStore），bus-mcp 的 collab_inbox_read 又只服务
// zcode-* 合成联系人——发送方（心跳线）连自己的降级回执都看不到。本工具补上
// 这条只读通道：只读调用方自己的 <contactId>.inbox.jsonl，每行标注投递泵游标
// （settled）与投递回执结局（任务 570 c1）。
//
// 关键约束（单消费者设计，M-a）：MailStore 的 Claim→Ack 游标在任一时刻只允许
// 一个消费者（泵或 drain_inbox，由 boot 门互斥）。本工具 settle=false：不
// Claim、不 Ack、不写游标，纯粹读文件——泵的双阶段消费与 drain 的消费权利都
// 不受影响（双读场景由测试钉住：peek 前后游标字节不变、泵仍能 Claim 到全部
// 待投消息）。
//
// 权限边界：没有 target 参数，也不接受一个——读的永远是调用方自己的信箱
// （contact_id 在调用时解析），不存在越权读取他人信箱的入口。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// peekInboxMaxRows is the hard row ceiling for one peek call — the schema's
// documented max. A heartbeat's inbox can hold thousands of rows; the tool
// returns the newest slice plus honest counters, never the whole file.
const peekInboxMaxRows = 100

// NewPeekOwnInboxTool builds the read-only self-inbox peek (task 570 c2).
func NewPeekOwnInboxTool(cfg SessionCollabConfig) tool.Tool {
	return peekOwnInboxTool{cfg: cfg}
}

type peekOwnInboxTool struct{ cfg SessionCollabConfig }

func (peekOwnInboxTool) Name() string { return "peek_own_inbox" }

func (peekOwnInboxTool) Description() string {
	return "Read YOUR OWN cross-session mailbox (task 570) — the durable MailStore inbox, not the tab queue drain_inbox consumes. This is how a sender sees the platform status notes it could never read before: steer_degraded (your steer was downgraded to a queued follow-up), refused_* (your message was dropped: hop limit / thread provenance / cross-wired thread), delivery_failed (temporary, will retry). Rows carry settled (the delivery pump's cursor — false means the pump has not picked it up yet) and outcome (the task-570 delivery receipt, when recorded). Strictly read-only: no claim, no ack, no cursor write — the pump and drain_inbox single-consumer design is untouched, so peeking never consumes anything. Args filter/limit the newest rows; counters always cover the whole inbox. Call this right after a talk_to_session whose answer matters, to check for a system note."
}

func (peekOwnInboxTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max rows returned, newest first (default 20, max 100). Counters always reflect the whole inbox."},"only_system":{"type":"boolean","description":"Only platform status notes (kind=system): degraded/refused/failed receipts. Default false."},"hide_settled":{"type":"boolean","description":"Hide rows the delivery pump already settled into your session (default false = show everything with the settled flag)."}},"required":[]}`)
}

func (peekOwnInboxTool) ReadOnly() bool { return true }

// peekInboxRow is one rendered inbox row: message metadata plus the two
// visibility layers this task adds — the pump cursor (settled) and the
// delivery receipt outcome.
type peekInboxRow struct {
	ID       string `json:"id"`
	From     string `json:"from,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Delivery string `json:"delivery,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	At       int64  `json:"at"`
	Settled  bool   `json:"settled"`
	// Outcome is the recorded delivery receipt for this message, when the
	// pump wrote one (task 570 c1). Empty = no outcome recorded (message may
	// predate the receipt store, or is inbound mail the pump has not settled).
	Outcome string `json:"outcome,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// Preview is the first line of the body, rune-bounded — the degraded /
	// refused note text is one line, so triage never needs the full body.
	Preview string `json:"preview"`
}

// peekInboxPreview cuts the body's first line to a bounded rune count.
func peekInboxPreview(body string) string {
	body = strings.TrimSpace(body)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	runes := []rune(body)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return body
}

func (t peekOwnInboxTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Limit       int  `json:"limit"`
		OnlySystem  bool `json:"only_system"`
		HideSettled bool `json:"hide_settled"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	me := t.cfg.currentContactID()
	if me == "" {
		return "", fmt.Errorf("peek_own_inbox: no session path — your own inbox needs the caller's contact_id (call from a registered session)")
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return "", fmt.Errorf("peek_own_inbox: no mail directory configured")
	}
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.Limit > peekInboxMaxRows {
		p.Limit = peekInboxMaxRows
	}
	store := sessioncollab.NewMailStoreWithHopLimit(mailDir, t.cfg.hopLimit())
	// 一次锁定读拿齐全部行+游标（任务 570 c2）：不 Claim、不 Ack、不写游标，
	// 泵的单消费者设计不受影响。
	all, settledByID, err := store.InboxWithCursor(me)
	if err != nil {
		return "", err
	}

	rows := make([]peekInboxRow, 0, len(all))
	unreadSystem, unreadTotal := 0, 0
	// File order is oldest→newest; walk backwards so rows come out newest
	// first and the counter pass stays a single sweep.
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		settled := settledByID[m.ID]
		if !settled {
			unreadTotal++
			if m.Kind == "system" {
				unreadSystem++
			}
		}
		if p.OnlySystem && m.Kind != "system" {
			continue
		}
		if p.HideSettled && settled {
			continue
		}
		row := peekInboxRow{
			ID:       m.ID,
			From:     m.From,
			Kind:     m.Kind,
			Delivery: m.Delivery,
			ThreadID: m.ThreadID,
			At:       m.At,
			Settled:  settled,
			Preview:  peekInboxPreview(m.Body),
		}
		if r, ok := store.DeliveryReceipt(m.ID); ok {
			row.Outcome = r.Outcome
			row.Detail = r.Detail
		}
		rows = append(rows, row)
		if len(rows) >= p.Limit {
			break
		}
	}
	if rows == nil {
		rows = []peekInboxRow{}
	}
	out, _ := json.Marshal(map[string]any{
		"me":              me,
		"totalInInbox":    len(all),
		"unsettled":       unreadTotal,
		"unsettledSystem": unreadSystem,
		"returned":        len(rows),
		"limit":           p.Limit,
		"note":            "read-only peek at YOUR OWN mailbox — no claim/ack/cursor write, the pump's single-consumer delivery is untouched; kind=system rows are the degraded/refused receipts a sender previously could never read (task 570)",
		"rows":            rows,
	})
	return string(out), nil
}
