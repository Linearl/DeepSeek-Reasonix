package agent

import (
	"fmt"
	"strconv"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/sessioncollab"
)

// 任务 530（任务 173 的覆盖件）：turn 闭合回信提醒钩子。
//
// 173 交付的 require_reply 只有「随信投递」的文内提示（drain_inbox_tool /
// sessionCollabDeliveryText 里的 ⚠ 行）：信进入上下文时提一次，之后模型若把
// 这件事放掉了，跨会话的回信义务就被静默丢弃 —— 与任务 194 同一族问题。
// 本钩子在 turn 干净闭合时补一道检查：本会话邮箱里仍有「已读未回」的
// require_reply 信，就注入一轮用户可见的提醒（任务 6 契约：走 172 的
// HostGeneratedUserMessage 通道 + 协作状态流审计 + Notice 事件，
// 绝不用 hidden model message）。
//
// 幂等（参照 notifySenderOnce 的 MarkNotified 模式）：
//   - 同 thread 已回信不提醒 —— 已回 = 我方 sent 日志里存在 thread_id 指向
//     该信的消息（回信契约就是把收到的消息 id 原样传回 thread_id）；
//   - 同一封信最多提醒一次 —— MarkNotified(me, <msgID>:reply_nudge) 只在
//     第一次返回 true，先占键后注入，键占不到就不提，重启也不复活；
//   - 未进入上下文的信不提醒 —— Settled（已 ack）才算「看到过」，没投递
//     到轮次的信提了模型也看不见。
//
// 防循环：每轮最多一次提醒（terminal.replyNudges 硬上限 1），提醒轮自己
// 闭合时键已占用，不会二次触发；MarkNotified 写失败按「已提醒」处理
// （fail-closed），宁可漏提醒不可造循环。
//
// 开关：Options.CollabReplyNudge 为 nil（默认）时本钩子逐字零行为 ——
// 不读邮箱、不发事件、不加消息、不多花模型轮次（铁律 2）。boot 只在
// 协作总开关与新 dial 同时为开时才传入非 nil（父开关优先，与 172 的
// FeedbackNudgeEnabled 同一 pre-AND 惯例）。

// CollabReplyNudgeMarker marks host-injected reply reminders so a reminder
// that comes back as input (replayed history, quoted text) is recognizable
// in transcripts, mirroring FeedbackNudgeMarker.
const CollabReplyNudgeMarker = "[跨会话回信提醒"

// collabReplyNudgeMaxPerTurn is the per-run hard cap (task 172 gate 1
// shape): one reminder round per turn, listing every currently owed mail.
const collabReplyNudgeMaxPerTurn = 1

// collabReplyNudgeKind is the MarkNotified key suffix for this reminder —
// the same "<msgID>:<kind>" shape notifySenderOnce uses.
const collabReplyNudgeKind = "reply_nudge"

// CollabReplyNudgeConfig carries what the turn-closure scan needs. Boot
// builds it only when the collaboration master switch AND the reply-nudge
// dial are both on; the session-path resolver is bound after the executor
// exists (the transcript path is bound by the control layer after boot,
// same as the collab toolset's ResolveSessionPath).
type CollabReplyNudgeConfig struct {
	// MailRoot overrides the shared collab mailbox root; empty resolves
	// config.SessionCollabMailDir() at call time (the live value).
	MailRoot string
	// ContactID is the boot-minted address of this session. Empty falls
	// back to the live session path lookup (and a first-use mint), exactly
	// like SessionCollabConfig.currentContactID.
	ContactID string
	// ResolveSessionPath answers the live transcript path (executor.SessionPath).
	// Nil keeps the boot snapshot semantics: an empty ContactID then means
	// the scan cannot resolve an inbox and does nothing.
	ResolveSessionPath func() string
	// StatusPath is the collab status stream the audit line rides (task 202);
	// empty skips the audit write.
	StatusPath string
}

// contactID resolves the scanning session's own address, preferring the boot
// snapshot over a mint and the live path over both.
func (c *CollabReplyNudgeConfig) contactID() string {
	if c == nil {
		return ""
	}
	if id := strings.TrimSpace(c.ContactID); id != "" {
		return id
	}
	var path string
	if c.ResolveSessionPath != nil {
		path = strings.TrimSpace(c.ResolveSessionPath())
	}
	if path == "" {
		return ""
	}
	if id := SessionContactID(path); id != "" {
		return id
	}
	if minted, err := EnsureContactID(path); err == nil {
		return minted
	}
	return ""
}

// mailRoot resolves the mailbox root at call time.
func (c *CollabReplyNudgeConfig) mailRoot() string {
	if c == nil {
		return ""
	}
	if root := strings.TrimSpace(c.MailRoot); root != "" {
		return root
	}
	return config.SessionCollabMailDir()
}

// owedRequireReplies scans the session's mailbox for require_reply mail that
// is owed a reply right now: consumed into a turn (settled), not yet answered
// on its thread, and replyable (sender and reply address known). Errors read
// as "nothing owed" — a reminder must never be built on a half-read mailbox.
func (c *CollabReplyNudgeConfig) owedRequireReplies() []sessioncollab.MailMessage {
	me := c.contactID()
	if me == "" {
		return nil
	}
	root := c.mailRoot()
	if root == "" {
		return nil
	}
	mail := sessioncollab.NewMailStore(root)
	inbox, err := mail.InboxMessages(me)
	if err != nil {
		return nil
	}
	answered := map[string]bool{}
	for _, s := range mail.ListSent(me, 0) {
		if id := strings.TrimSpace(s.ThreadID); id != "" {
			answered[id] = true
		}
	}
	var out []sessioncollab.MailMessage
	for _, m := range inbox {
		if !m.RequireReply {
			continue
		}
		if strings.TrimSpace(m.From) == "" || strings.TrimSpace(m.ReplyTo) == "" {
			continue // 无法回信的信，提醒毫无意义
		}
		if strings.TrimSpace(m.To) != me {
			continue // 防御：只认自己名下的收件
		}
		if !mail.Settled(me, m.ID) {
			continue // 尚未进入本轮上下文，模型还没见过它
		}
		if answered[m.ID] {
			continue // 同 thread 已回信（幂等条件①）
		}
		out = append(out, m)
	}
	return out
}

// collabReplyNudgeMessage renders the host reminder listing every owed mail.
func collabReplyNudgeMessage(owed []sessioncollab.MailMessage) string {
	var b strings.Builder
	b.WriteString(CollabReplyNudgeMarker + " task-530]\n")
	b.WriteString("本会话有 ")
	b.WriteString(strconv.Itoa(len(owed)))
	b.WriteString(" 封要求回信（require_reply）的跨会话消息尚未回复：\n")
	for i, m := range owed {
		excerpt := strings.TrimSpace(m.Body)
		if runes := []rune(excerpt); len(runes) > 80 {
			excerpt = string(runes[:80]) + "…"
		}
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". 来自 contact_id=")
		b.WriteString(m.From)
		if id := strings.TrimSpace(m.ID); id != "" {
			b.WriteString("，threadId=")
			b.WriteString(id)
		}
		b.WriteString("：「")
		b.WriteString(excerpt)
		b.WriteString("」\n")
	}
	b.WriteString("回信方式：调用 talk_to_session，to 传发件人 contact_id，thread_id 传对应 threadId，把答复写进 message；无法完成也要回信说明，不要只留在本会话里。")
	b.WriteString("本提醒对每封信只出现这一次，请在本轮处理。")
	return b.String()
}

// maybeNudgeCollabReply is the task-530 turn-closure hook. Called from
// handleFinalResponse right before the task-172 completion nudge: an owed
// cross-session reply outranks the feedback invitation for the extra round.
// It reports whether the loop should continue for the reminder round.
func (a *Agent) maybeNudgeCollabReply(state *turnRuntime) bool {
	cfg := a.svc.collabReplyNudge
	if cfg == nil {
		return false
	}
	if state.terminal.replyNudges >= collabReplyNudgeMaxPerTurn {
		return false
	}
	owed := cfg.owedRequireReplies()
	if len(owed) == 0 {
		return false
	}
	// Mark first, inject only what was marked: a lost mark race reads as
	// "already reminded" and stays silent — fail-closed against loops.
	root := cfg.mailRoot()
	mail := sessioncollab.NewMailStore(root)
	me := cfg.contactID()
	var due []sessioncollab.MailMessage
	for _, m := range owed {
		if mail.MarkNotified(me, m.ID+":"+collabReplyNudgeKind) {
			due = append(due, m)
		}
	}
	if len(due) == 0 {
		return false
	}
	state.terminal.replyNudges++
	threadIDs := make([]string, 0, len(due))
	for _, m := range due {
		threadIDs = append(threadIDs, m.ID)
	}
	// Audit (task 202 stream): the reminder is a needs-decision collab event,
	// the same class as a delivered require_reply awaiting an answer.
	AppendCollabStatusEvent(cfg.StatusPath, collabStatusSessionID(cfg.resolveSessionPath(), me), "",
		CollabStatusReplyNudge,
		fmt.Sprintf("turn 闭合回信提醒×%d（未回 require_reply：threadId=%s）", len(due), strings.Join(threadIDs, ",")),
		true)
	a.svc.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   event.NoticeCodeCollabReplyNudge,
		Text:   collabReplyNudgeNoticeText(len(due)),
		Detail: "threadIds=" + strings.Join(threadIDs, ","),
	})
	a.sess.conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(collabReplyNudgeMessage(due))))
	return true
}

// resolveSessionPath answers the live transcript path for the audit session id.
func (c *CollabReplyNudgeConfig) resolveSessionPath() string {
	if c.ResolveSessionPath == nil {
		return ""
	}
	return strings.TrimSpace(c.ResolveSessionPath())
}

func collabReplyNudgeNoticeText(n int) string {
	return fmt.Sprintf("有 %d 封要求回信的跨会话消息尚未回复，已在对话中注入回信提醒", n)
}
