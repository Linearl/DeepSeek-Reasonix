package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/tool"
)

// Task 235: drain_inbox — the agent-side pull. The model reads its own
// cross-session mailbox as a tool result and (optionally) settles what it
// takes. D1 (management ruling): pure pull into the tool result — this tool
// never starts a new round. Host-side dispatch stays on the existing
// maybeDispatchInbox / TryEnqueueAndSteer paths.
//
// Two modes share one verb:
//   - settle=false (Peek): read-only, nothing is acked — safe to poll.
//   - settle=true  (Claim+Ack / Drain): the batch is handed to the model and
//     the cursor advances, so InboxStatus.unread goes to zero. At-least-once:
//     a crash before Ack redelivers; the inbox idempotency key keeps a
//     redelivery from duplicating a turn.
//
// Tool-family relation (task 174): talk_to_session is the send half; this is
// the receive half. get_session_status / event_wait answer counters and idle
// predicates — they never surface message bodies. Folding drain into either
// would blur read-only and consuming semantics.
type drainInboxTool struct {
	cfg SessionCollabConfig
}

// NewDrainInboxTool builds the agent-side mailbox pull (task 235). It shares
// SessionCollabConfig with the other collaboration tools so identity, mail
// root, and hop ceiling resolve the same way at call time.
func NewDrainInboxTool(cfg SessionCollabConfig) tool.Tool {
	return drainInboxTool{cfg: cfg}
}

func (drainInboxTool) Name() string { return "drain_inbox" }

func (drainInboxTool) Description() string {
	return "Pull unread cross-session mail addressed to this session into the tool result and optionally settle it (task 235). settle=true (default) claims and acks the batch — after a successful call InboxStatus.unread drops to zero for those messages. settle=false peeks without advancing the cursor (safe to poll). Reply through talk_to_session using the fromContactId and threadId each message carries; never answer only inside your own transcript. This tool does not start a new turn — it is a pure pull. Registered only when experimental_collab_background_delivery is on (the host pump then skips delivery and this tool is the mailbox's sole consumer — Block2 M-a mutual exclusion). Experimental."
}

func (drainInboxTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"settle":{"type":"boolean","description":"true (default): claim and ack — messages are consumed. false: peek only, cursor does not advance."},"limit":{"type":"integer","description":"Max messages to return (default 20, clamped 1..100)."},"source":{"type":"string","description":"Only return messages whose fromContactId matches this value. Empty (default) returns all senders."},"layer":{"type":"string","enum":["mailbox","inbox","all"],"description":"Which layer to pull from. mailbox = MailStore (cross-session handoff). inbox = sessioninbox (followups already delivered to the session queue). all (default) = both layers merged."}}}`)
}

// ReadOnly is false when settle can advance the cursor; the registry uses this
// for approval classification, and a Peek-only call is still allowed through
// the same tool because the mode is an argument, not a separate verb.
func (drainInboxTool) ReadOnly() bool { return false }

// drainInboxMessage is one mailbox entry as the model sees it. The body is
// included raw plus a rendered block so the model can quote or parse.
type drainInboxMessage struct {
	ID           string `json:"id"`
	From         string `json:"fromContactId"`
	FromSession  string `json:"fromSession,omitempty"`
	Body         string `json:"body"`
	Delivery     string `json:"delivery,omitempty"`
	Hop          int    `json:"hop,omitempty"`
	CardID       string `json:"cardId,omitempty"`
	ThreadID     string `json:"threadId,omitempty"`
	RequireReply bool   `json:"requireReply,omitempty"`
	ReplyTo      string `json:"replyTo,omitempty"`
	At           int64  `json:"at"`
	Text         string `json:"text"`
}

type drainInboxPayload struct {
	Settled     bool                `json:"settled"`
	Took        int                 `json:"took"`
	Refused     int                 `json:"refused"`
	Unread      int                 `json:"unreadAfter"`
	Messages    []drainInboxMessage `json:"messages"`
	RefusedMsgs []drainInboxMessage `json:"refusedMessages,omitempty"`
	Query       string              `json:"query"`
}

// drainInboxRenderText builds the text block for one message. It mirrors
// desktop/session_collab.go sessionCollabDeliveryText closely enough that the
// model sees the same reply contract on both delivery paths, without the
// desktop package dependency.
func drainInboxRenderText(msg sessioncollab.MailMessage, effectiveHop int) string {
	var b strings.Builder
	b.WriteString("[跨会话消息]")
	fromLabel := msg.From
	if fromLabel == "" {
		fromLabel = "(未登记)"
	}
	toLabel := msg.To
	if toLabel == "" {
		toLabel = "(未知)"
	}
	b.WriteString(" 来自 contact_id=")
	b.WriteString(fromLabel)
	b.WriteString(" → 发至 contact_id=")
	b.WriteString(toLabel)
	if effectiveHop > 0 {
		b.WriteString(" (hop=")
		b.WriteString(strconv.Itoa(effectiveHop))
		b.WriteString(")")
	}
	b.WriteString("\n\n")
	b.WriteString(msg.Body)
	b.WriteString("\n\n---\n")
	if msg.CardID != "" {
		b.WriteString("关联任务卡片：" + msg.CardID + "\n")
	}
	if msg.ID != "" {
		b.WriteString("会话线程：threadId=" + msg.ID + "\n")
	}
	if msg.ReplyTo != "" {
		b.WriteString("回复方式：完成后用 talk_to_session 回信到 contact_id=" + msg.ReplyTo +
			"，hop 传 " + strconv.Itoa(effectiveHop+1))
		if msg.ID != "" {
			b.WriteString("，并把 thread_id 设为 " + msg.ID)
		}
		b.WriteString("。")
		if msg.RequireReply {
			b.WriteString("\n⚠ 发件人要求回信（require_reply）：完成本信的工作后，必须按上面的回复方式回信；无法完成也请回信说明，不要只在本会话里写下结论。")
		}
	} else {
		b.WriteString("这是一条单向通知：发送方未登记 contact_id，本消息无法回信。")
	}
	return b.String()
}

func drainInboxConvert(msg sessioncollab.MailMessage, hop int) drainInboxMessage {
	return drainInboxMessage{
		ID:           msg.ID,
		From:         msg.From,
		FromSession:  msg.FromSession,
		Body:         msg.Body,
		Delivery:     msg.Delivery,
		Hop:          msg.Hop,
		CardID:       msg.CardID,
		ThreadID:     msg.ThreadID,
		RequireReply: msg.RequireReply,
		ReplyTo:      msg.ReplyTo,
		At:           msg.At,
		Text:         drainInboxRenderText(msg, hop),
	}
}

// drainInboxFromSessionInbox pulls items from the sessioninbox layer (H1).
// These are followups already delivered to the session queue — the MailStore
// copy has been acked by the host pump, so this is the only place they remain.
// Settle uses ClaimItem+AckDequeue to consume; peek reads the snapshot only.
func drainInboxFromSessionInbox(sessionPath, sourceFilter string, settle bool, limit int) (msgs []drainInboxMessage, settledIDs []string) {
	if strings.TrimSpace(sessionPath) == "" {
		return nil, nil
	}
	store, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		return nil, nil
	}
	defer store.Close()
	snap := store.Snapshot()
	for _, item := range snap.Items {
		if item.State == sessioninbox.StateSteerConsumed || item.State == sessioninbox.StateRunning {
			continue
		}
		if strings.TrimSpace(sourceFilter) != "" && item.Source != sourceFilter {
			continue
		}
		if len(msgs) >= limit {
			break
		}
		_, env, rerr := store.ReadItem(item.ID)
		if rerr != nil {
			continue
		}
		m := drainInboxMessage{
			ID:   "inbox:" + item.ID,
			From: item.Source,
			Body: env.DisplayText,
			At:   item.CreatedAt.UnixMilli(),
			Text: "[sessioninbox] " + item.Preview,
		}
		msgs = append(msgs, m)
		if settle {
			if err := store.ClaimItem(item.ID); err == nil {
				if err := store.AckDequeue(item.ID); err == nil {
					settledIDs = append(settledIDs, item.ID)
				}
			}
		}
	}
	return msgs, settledIDs
}

func (t drainInboxTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Settle *bool  `json:"settle"`
		Limit  int    `json:"limit"`
		Source string `json:"source"`
		Layer  string `json:"layer"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	// D1: drain means consume by default; settle=false is the explicit peek.
	settle := true
	if p.Settle != nil {
		settle = *p.Settle
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	me := t.cfg.currentContactID()
	if me == "" {
		return "", fmt.Errorf("drain_inbox: no contact_id for the calling session (set a session path or register the session first)")
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	mail := sessioncollab.NewMailStoreWithHopLimit(mailDir, t.cfg.hopLimit())

	payload := drainInboxPayload{
		Settled: settle,
		Query:   "drain_inbox — mailbox pull for the calling session only",
	}

	// H1: determine which layers to pull from.
	layer := strings.ToLower(strings.TrimSpace(p.Layer))
	if layer == "" {
		layer = "all"
	}
	useMailbox := layer == "mailbox" || layer == "all"
	useInbox := layer == "inbox" || layer == "all"

	if useMailbox && settle {
		var taken []sessioncollab.MailMessage
		var refused []sessioncollab.MailMessage
		err := mail.Drain(me, func(pending, refusedBatch []sessioncollab.MailMessage) []string {
			refused = refusedBatch
			// H2: filter by source if specified; only return (and thus only Ack)
			// the IDs actually taken. Non-matching IDs stay queued (not acked).
			filtered := pending
			if strings.TrimSpace(p.Source) != "" {
				filtered = nil
				for _, m := range pending {
					if m.From == p.Source {
						filtered = append(filtered, m)
					}
				}
			}
			if len(filtered) > limit {
				taken = filtered[:limit]
			} else {
				taken = filtered
			}
			ids := make([]string, 0, len(taken))
			for _, m := range taken {
				ids = append(ids, m.ID)
			}
			return ids
		})
		if err != nil {
			return "", fmt.Errorf("drain_inbox: %w", err)
		}
		payload.Took = len(taken)
		payload.Refused = len(refused)
		for _, m := range taken {
			payload.Messages = append(payload.Messages, drainInboxConvert(m, m.Hop))
		}
		for _, m := range refused {
			payload.RefusedMsgs = append(payload.RefusedMsgs, drainInboxConvert(m, m.Hop))
		}
	} else if useMailbox {
		peeked, err := mail.Peek(me)
		if err != nil {
			return "", fmt.Errorf("drain_inbox: %w", err)
		}
		// H2: filter by source if specified.
		if strings.TrimSpace(p.Source) != "" {
			filtered := peeked[:0:0]
			for _, m := range peeked {
				if m.From == p.Source {
					filtered = append(filtered, m)
				}
			}
			peeked = filtered
		}
		if len(peeked) > limit {
			peeked = peeked[:limit]
		}
		payload.Took = len(peeked)
		for _, m := range peeked {
			payload.Messages = append(payload.Messages, drainInboxConvert(m, m.Hop))
		}
	}

	// H1: pull from sessioninbox layer.
	if useInbox {
		remaining := limit - payload.Took
		if remaining > 0 {
			inboxMsgs, _ := drainInboxFromSessionInbox(me, p.Source, settle, remaining)
			payload.Took += len(inboxMsgs)
			payload.Messages = append(payload.Messages, inboxMsgs...)
		}
	}

	if payload.Messages == nil {
		payload.Messages = []drainInboxMessage{}
	}
	unread, _ := mail.InboxStatus(me)
	payload.Unread = unread

	out, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
