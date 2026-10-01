package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// Task 349: the three channel tools (查看频道 / 获取消息 / 发送消息 — the
// 0928 拍板 tool set, names kept short per task 174). They share the channel
// entity over the mailbox root: sending PUBLISHES to the channel's SQLite log
// and expands to per-member single sends through the task-309 MailStore —
// there is no second delivery channel.

// channelSpawn kicks the background fan-out drain after a publish. It is a
// package seam so tests capture the job instead of racing the production
// goroutine (and its 2-5s member pacing).
var channelSpawn = func(job func()) { go job() }

// channelDrainTimeout bounds one background fan-out pass (a large channel at
// 2-5s per member stays well inside this).
const channelDrainTimeout = 10 * time.Minute

func channelStore(cfg SessionCollabConfig) (*collabchannel.Store, error) {
	mailDir := cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return nil, fmt.Errorf("collabchannel: no mail directory configured")
	}
	store, err := collabchannel.Open(mailDir)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// ── channel_list (查看频道) ────────────────────────────────────────────────

type channelListTool struct{ cfg SessionCollabConfig }

// NewChannelListTool builds the channel directory reader.
func NewChannelListTool(cfg SessionCollabConfig) tool.Tool { return channelListTool{cfg: cfg} }

func (channelListTool) Name() string { return "channel_list" }

func (channelListTool) Description() string {
	return "List the chat channels this session can see (task 349): name, topic, members and message count for each. Channels are virtual addresses — send with channel_send, read history with channel_read. Read-only. Experimental."
}

func (channelListTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","description":"Max channels to return (default 50, max 200)."}},"required":[]}`)
}

func (channelListTool) ReadOnly() bool { return true }

func (t channelListTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Limit int `json:"limit"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	store, err := channelStore(t.cfg)
	if err != nil {
		return "", err
	}
	defer store.Close()
	channels, err := store.ListChannels()
	if err != nil {
		return "", err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	truncated := len(channels) > limit
	if truncated {
		channels = channels[:limit]
	}
	out, _ := json.Marshal(map[string]any{
		"returned":  len(channels),
		"truncated": truncated,
		"channels":  channels,
	})
	return string(out), nil
}

// ── channel_read (获取消息) ────────────────────────────────────────────────

type channelReadTool struct{ cfg SessionCollabConfig }

// NewChannelReadTool builds the channel history reader (marks the caller's
// own per-recipient read rows as a side effect — consumption, not mutation
// of anyone else's state, hence ReadOnly=false like drain_inbox).
func NewChannelReadTool(cfg SessionCollabConfig) tool.Tool { return channelReadTool{cfg: cfg} }

func (channelReadTool) Name() string { return "channel_read" }

func (channelReadTool) Description() string {
	return "Read a chat channel's message history (task 349), chronological, limit default 50 / hard max 500 (optional since = ms epoch). Reading marks the returned messages as read FOR THE CALLING SESSION ONLY — read state is per member and never touches anyone else's. Channel messages are the pub/sub log; the same text is also fanned into each member's session mailbox as a followup. Experimental."
}

func (channelReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"channel":{"type":"string","description":"Channel name or id."},"limit":{"type":"integer","description":"Max messages (default 50, hard max 500)."},"since":{"type":"integer","description":"Only messages at/after this ms epoch."}},"required":["channel"]}`)
}

func (channelReadTool) ReadOnly() bool { return false }

func (t channelReadTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
		Since   int64  `json:"since"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Channel) == "" {
		return "", fmt.Errorf("channel_read: channel is required")
	}
	store, err := channelStore(t.cfg)
	if err != nil {
		return "", err
	}
	defer store.Close()
	msgs, err := store.Messages(p.Channel, p.Since, p.Limit)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	marked := 0
	if viewer := t.cfg.currentContactID(); viewer != "" && len(ids) > 0 {
		// Per-recipient read: rows keyed (message, member=viewer) — a member
		// who was not fanned this message simply marks nothing.
		if n, err := store.MarkRead(p.Channel, viewer, ids); err == nil {
			marked = n
		}
	}
	out, _ := json.Marshal(map[string]any{
		"channel":    p.Channel,
		"returned":   len(msgs),
		"markedRead": marked,
		"messages":   msgs,
	})
	return string(out), nil
}

// ── channel_send (发送消息) ────────────────────────────────────────────────

type channelSendTool struct{ cfg SessionCollabConfig }

// NewChannelSendTool builds the publish half (pub/sub write + queued fan-out).
func NewChannelSendTool(cfg SessionCollabConfig) tool.Tool { return channelSendTool{cfg: cfg} }

func (channelSendTool) Name() string { return "channel_send" }

func (channelSendTool) Description() string {
	return "Send a message to a chat channel (task 349): the message lands in the channel's SQLite history AND is queued for delivery to every other member as an individual followup through the session mailbox (task 309 — no separate broadcast channel), paced 2-5s apart under the channel's hourly message cap. Returns the message id and how many members it was queued for; delivery continues in the background. The sending session is never a recipient of its own message (it already has the line in channel history). Experimental."
}

func (channelSendTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"channel":{"type":"string","description":"Channel name or id."},"message":{"type":"string","description":"Message body."}},"required":["channel","message"]}`)
}

func (channelSendTool) ReadOnly() bool { return false }

func (t channelSendTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Channel string `json:"channel"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Channel) == "" || strings.TrimSpace(p.Message) == "" {
		return "", fmt.Errorf("channel_send: channel and message are required")
	}
	sender := t.cfg.currentContactID()
	if sender == "" {
		return "", fmt.Errorf("channel_send: no session identity — call from a registered session")
	}
	store, err := channelStore(t.cfg)
	if err != nil {
		return "", err
	}
	msg, err := store.Publish(p.Channel, sender, p.Message)
	if err != nil {
		_ = store.Close()
		return "", err
	}
	ch, chErr := store.Channel(p.Channel)
	_ = store.Close()
	if chErr != nil {
		return "", chErr
	}
	queued := 0
	for _, m := range ch.Members {
		if m != sender {
			queued++
		}
	}
	mailDir := t.cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	// Background fan-out: its own store instance (the tool's handle is closed
	// above), serialized with other drains by the instance mutex + SQLite.
	channelSpawn(func() {
		drain, err := collabchannel.Open(mailDir)
		if err != nil {
			return
		}
		defer drain.Close()
		ctx, cancel := context.WithTimeout(context.Background(), channelDrainTimeout)
		defer cancel()
		_, _ = drain.DrainFanout(ctx)
	})
	out, _ := json.Marshal(map[string]any{
		"channel":   p.Channel,
		"messageId": msg.ID,
		"queuedFor": queued,
		"at":        msg.At,
		"note":      "fan-out paced in the background (2-5s per member, delivery=followup, hourly cap enforced)",
	})
	return string(out), nil
}
