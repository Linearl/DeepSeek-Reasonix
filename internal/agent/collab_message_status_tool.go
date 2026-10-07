package agent

// 任务 570 (c1)：get_message_status —— 按 messageId 查询投递结局。
//
// 背景（task 568/570）：talk_to_session 的返回体恒为 status=queued，投递的真正
// 终态由宿主投递泵在之后的轮次里异步定局（约 4s 一轮），同步返回体永远给不出
// 终态。2026-10-07 心跳事故里，发送方发了 22 封唤醒、零回复、零失败信号——它
// 只能看到绿灯。本工具读 sessioncollab 的投递回执存储（desktop 泵在每次定局时
// 写入），把「已降级 / 被拒绝 / 注入成功 / 暂时失败」按消息 id 结构化地还给
// 发送方或接收方。
//
// 语义边界（不给假绿灯）：
//   - 回执存在 → known=true，status 即回执里的结局（failed_retrying 非终态，
//     会继续重试并被后续成功覆盖）；
//   - 回执不存在但消息在调用方自己的发件日志里 → status=pending（已入队，尚无
//     定局——泵未运行 / 目标始终无法 stand up 都落在这里），明确「未知」而非
//     「成功」；
//   - 回执不存在但消息在调用方自己的收件箱里 → status=in_my_inbox（调用方是
//     接收方），并给出泵游标（settled）状态；
//   - 都没有 → status=not_found，并说明只查询「自己发出 / 自己收到」的消息。
//
// 权限边界（task 570 必答 3）：回执只对消息双方（发送方 / 接收方）可见；其他
// 调用方得到显式权限拒绝，而不是结果。消息 id 是 128 位随机串，不可枚举。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// NewGetMessageStatusTool builds the per-message delivery-outcome query
// (task 570 c1).
func NewGetMessageStatusTool(cfg SessionCollabConfig) tool.Tool {
	return getMessageStatusTool{cfg: cfg}
}

type getMessageStatusTool struct{ cfg SessionCollabConfig }

func (getMessageStatusTool) Name() string { return "get_message_status" }

func (getMessageStatusTool) Description() string {
	return "Query the delivery outcome of one cross-session message by its messageId (task 570). talk_to_session only reports status=queued — the real outcome (injected mid-turn / degraded to queued follow-up / refused / temporarily failing with retries) is decided asynchronously by the host delivery pump, and this tool is how the sender sees it. Statuses: injected (steer entered the target's running turn); queued_followup (landed as a queued follow-up — normal for delivery=followup, a degradation when you sent delivery=steer); refused_hop / refused_provenance / refused_cross_wire (terminal refusals, the message was dropped); failed_retrying (target unavailable, will keep retrying — not terminal); pending (queued, no outcome recorded yet — NOT a success); in_my_inbox (you are the recipient; see the settled flag); not_found. Only your own sent and received messages are queryable — a message id you are not a party to is refused. Read-only."
}

func (getMessageStatusTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"string","description":"The messageId returned by talk_to_session (msg_...)."}},"required":["message_id"]}`)
}

func (getMessageStatusTool) ReadOnly() bool { return true }

// receiptStatusSemantics maps a receipt outcome to the caller-facing
// explanation. Kept as a pure function so the wording is pinned by tests.
func receiptStatusSemantics(outcome string) string {
	switch outcome {
	case sessioncollab.ReceiptInjected:
		return "已注入目标会话当轮（steer 生效）——这是真正的「已送达并生效」。"
	case sessioncollab.ReceiptQueuedFollowup:
		return "已落为排队 follow-up：delivery=followup 时这是正常落点；delivery=steer 时这是降级（未能注入目标当轮）。目标会在其下一轮处理。"
	case sessioncollab.ReceiptRefusedHop:
		return "被拒绝并丢弃：协作链达到 hop 上限。新起一条链（hop=0、不带旧 thread_id）重发。"
	case sessioncollab.ReceiptRefusedProvenance:
		return "被拒绝并丢弃：thread 来源无法核实。回信时把收到的 thread_id 原样传回，或省略 thread_id 新起链。"
	case sessioncollab.ReceiptRefusedCrossWire:
		return "被拒绝并丢弃：thread_id 串线到另一条链。改用你收到的那条入向消息的 thread_id。"
	case sessioncollab.ReceiptFailedRetrying:
		return "投递暂时失败（目标不可用），消息仍在队列中将自动重试——非终态，后续成功会覆盖本状态。"
	default:
		return ""
	}
}

func (t getMessageStatusTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	p.MessageID = strings.TrimSpace(p.MessageID)
	if p.MessageID == "" {
		return "", fmt.Errorf("message_id is required")
	}
	me := t.cfg.currentContactID()
	if me == "" {
		return "", fmt.Errorf("get_message_status: no session path — the caller's contact_id cannot be resolved (call from a registered session)")
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return "", fmt.Errorf("get_message_status: no mail directory configured")
	}
	store := sessioncollab.NewMailStoreWithHopLimit(mailDir, t.cfg.hopLimit())

	// 1) A recorded outcome is authoritative — for either party of the message.
	if r, ok := store.DeliveryReceipt(p.MessageID); ok {
		if me != r.From && me != r.To {
			return "", fmt.Errorf("get_message_status: %q 的投递回执仅消息双方（发送方/接收方）可查（task 570 权限边界）", p.MessageID)
		}
		out, _ := json.Marshal(map[string]any{
			"messageId": r.MessageID,
			"status":    r.Outcome,
			"known":     true,
			"semantics": receiptStatusSemantics(r.Outcome),
			"receipt":   r,
			"note":      "receipt recorded by the host delivery pump; failed_retrying is not terminal and is overwritten by a later success",
		})
		return string(out), nil
	}

	// 2) No outcome yet: is it a message I sent? (pending — the pump has not
	// settled it, which is NOT a success.)
	for _, m := range store.ListSent(me, 0) {
		if m.ID == p.MessageID {
			out, _ := json.Marshal(map[string]any{
				"messageId": p.MessageID,
				"status":    "pending",
				"known":     false,
				"semantics": "已入队（在你自己的发件日志里），尚无投递定局——投递泵可能尚未运行，或目标始终无法就绪。这不是成功回执；稍后可用同一 messageId 复查。",
				"note":      "no delivery receipt exists yet; pending means unknown, never delivered",
			})
			return string(out), nil
		}
	}

	// 3) Am I the recipient? Report the mailbox state plus the pump cursor —
	// legacy messages sent before receipts existed still answer here.
	all, err := store.InboxMessages(me)
	if err == nil {
		for _, m := range all {
			if m.ID != p.MessageID {
				continue
			}
			settled := store.Settled(me, m.ID)
			status, semantics := "in_my_inbox_unclaimed", "在你的收件箱里，投递泵尚未把它投进你的会话（会话内收件箱/对话流）。"
			if settled {
				status = "in_my_inbox"
				semantics = "已在你的收件箱且投递泵已定局（已进入你会话的会话内收件箱）。"
			}
			out, _ := json.Marshal(map[string]any{
				"messageId": p.MessageID,
				"status":    status,
				"known":     true,
				"settled":   settled,
				"from":      m.From,
				"delivery":  m.Delivery,
				"semantics": semantics,
				"note":      "you are the recipient; no delivery receipt exists (message may predate task 570)",
			})
			return string(out), nil
		}
	}

	// 4) Nothing anywhere the caller may look.
	out, _ := json.Marshal(map[string]any{
		"messageId": p.MessageID,
		"status":    "not_found",
		"known":     false,
		"semantics": "在你的发件日志、你自己的收件箱和投递回执里都找不到这个 id。本工具只查询你自己发出或收到的消息（task 570 权限边界）。",
		"note":      "not_found is also the answer for a message between two OTHER sessions — that is by design",
	})
	return string(out), nil
}
