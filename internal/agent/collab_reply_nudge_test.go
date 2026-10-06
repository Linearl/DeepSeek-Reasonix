package agent

// 任务 530 turn 闭合回信提醒钩子的用例。每个用例同时钉住默认关闭契约：
// 未配置 Options.CollabReplyNudge 的会话必须逐字零行为（无提醒消息、
// 无额外模型轮次、不碰邮箱）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// nudgeEventSink collects events for assertions.
type nudgeEventSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *nudgeEventSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *nudgeEventSink) collected() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.events...)
}

// countReplyNudgeMessages counts host-injected task-530 reminder messages.
// Only host-origin messages count: a user message merely quoting the marker
// must not read as a reminder.
func countReplyNudgeMessages(a *Agent) int {
	n := 0
	for _, m := range a.Session().Messages {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, CollabReplyNudgeMarker) {
			n++
		}
	}
	return n
}

// deliverRequireReply writes one require_reply mail into me's mailbox.
// settled=true acks it, i.e. the mail has entered a turn's context.
func deliverRequireReply(t *testing.T, root, from, me, body string, settled bool) sessioncollab.MailMessage {
	t.Helper()
	mail := sessioncollab.NewMailStore(root)
	msg, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From:         from,
		ReplyTo:      from,
		To:           me,
		Body:         body,
		RequireReply: true,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if settled {
		if err := mail.Ack(context.Background(), me, msg.ID); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	return msg
}

func replyNudgeConfig(root, statusPath string) *CollabReplyNudgeConfig {
	return &CollabReplyNudgeConfig{MailRoot: root, ContactID: "sc_me", StatusPath: statusPath}
}

// TestCollabReplyNudgeOffByDefaultKeepsTurnUntouched pins 铁律 2: with the
// config nil (the default) a completed turn costs exactly one provider round,
// leaves no reminder, and never touches the mailbox — a settled unanswered
// require_reply mail sits there with zero behavior change.
func TestCollabReplyNudgeOffByDefaultKeepsTurnUntouched(t *testing.T) {
	root := t.TempDir()
	deliverRequireReply(t, root, "sc_peer", "sc_me", "帮我确认构建结果", true)

	mp := testutil.NewMock("m", testutil.Turn{Text: "work done"})
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)

	if err := a.Run(context.Background(), "do the thing"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 with the nudge off", got)
	}
	if got := countReplyNudgeMessages(a); got != 0 {
		t.Fatalf("reminder messages = %d, want 0 with the nudge off", got)
	}
}

// TestCollabReplyNudgeUnansweredRemindsOnce pins acceptance ①③: a turn that
// closes with a settled unanswered require_reply mail gets exactly one visible
// reminder round (work round + reminder round), and repeated closures never
// repeat it — the second turn stays at one reminder total.
func TestCollabReplyNudgeUnansweredRemindsOnce(t *testing.T) {
	root := t.TempDir()
	statusPath := filepath.Join(t.TempDir(), "collab-status.jsonl")
	mail := deliverRequireReply(t, root, "sc_peer", "sc_me", "帮我确认构建结果", true)

	mp := testutil.NewMock("m",
		testutil.Turn{Text: "work done"},
		testutil.Turn{Text: "我会回信说明"},
		testutil.Turn{Text: "second turn work"},
	)
	sink := &nudgeEventSink{}
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{CollabReplyNudge: replyNudgeConfig(root, statusPath)}, sink)

	if err := a.Run(context.Background(), "turn 1"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls after turn 1 = %d, want work round + one reminder round", got)
	}
	if got := countReplyNudgeMessages(a); got != 1 {
		t.Fatalf("reminder messages after turn 1 = %d, want exactly 1", got)
	}
	for _, m := range a.Session().Messages {
		if strings.Contains(m.Content, CollabReplyNudgeMarker) && m.Origin != provider.MessageOriginHost {
			t.Fatal("the reminder must be a host-generated message, never user intent")
		}
	}
	// 提醒必须点名 threadId，模型才知道回哪条 thread。
	reminded := false
	for _, m := range a.Session().Messages {
		if strings.Contains(m.Content, CollabReplyNudgeMarker) {
			reminded = true
			if !strings.Contains(m.Content, "threadId="+mail.ID) || !strings.Contains(m.Content, "sc_peer") {
				t.Fatalf("reminder must name threadId=%s and the sender, got: %s", mail.ID, m.Content)
			}
		}
	}
	if !reminded {
		t.Fatal("reminder message missing")
	}

	// 重复闭合：第二个 turn 即使仍未回信，也不再提醒（同一封信最多一次）。
	if err := a.Run(context.Background(), "turn 2"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if got := mp.CallCount(); got != 3 {
		t.Fatalf("provider calls after turn 2 = %d, want no extra reminder round", got)
	}
	if got := countReplyNudgeMessages(a); got != 1 {
		t.Fatalf("reminder messages after turn 2 = %d, want still 1 (once per mail)", got)
	}

	// Notice 事件恰好一条。
	notices := 0
	for _, e := range sink.collected() {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeCollabReplyNudge {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("collab_reply_nudge notices = %d, want exactly 1", notices)
	}
}

// TestCollabReplyNudgeAnsweredThreadStaysSilent pins acceptance ②: a thread
// whose mail was answered (my sent log names it as thread parent) never gets
// a reminder — the turn costs exactly one provider round.
func TestCollabReplyNudgeAnsweredThreadStaysSilent(t *testing.T) {
	root := t.TempDir()
	mail := deliverRequireReply(t, root, "sc_peer", "sc_me", "帮我确认构建结果", true)
	stored := sessioncollab.NewMailStore(root)
	stored.RecordSent(context.Background(), sessioncollab.MailMessage{
		ID: "msg_reply1", From: "sc_me", To: "sc_peer", ThreadID: mail.ID, Body: "结果如下",
	}, "peer")

	mp := testutil.NewMock("m", testutil.Turn{Text: "work done"})
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{CollabReplyNudge: replyNudgeConfig(root, "")}, event.Discard)

	if err := a.Run(context.Background(), "turn 1"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 when the thread is answered", got)
	}
	if got := countReplyNudgeMessages(a); got != 0 {
		t.Fatalf("reminder messages = %d, want 0 for an answered thread", got)
	}
}

// TestCollabReplyNudgeUnsettledMailNotReminded pins the settled gate: a mail
// that never entered a turn's context (not acked) is not the model's debt yet
// — reminding would reference a message the model cannot see.
func TestCollabReplyNudgeUnsettledMailNotReminded(t *testing.T) {
	root := t.TempDir()
	deliverRequireReply(t, root, "sc_peer", "sc_me", "还没投递的信", false)

	mp := testutil.NewMock("m", testutil.Turn{Text: "work done"})
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{CollabReplyNudge: replyNudgeConfig(root, "")}, event.Discard)

	if err := a.Run(context.Background(), "turn 1"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 for an unsettled mail", got)
	}
	if got := countReplyNudgeMessages(a); got != 0 {
		t.Fatalf("reminder messages = %d, want 0 for an unsettled mail", got)
	}
}

// TestCollabReplyNudgeNewMailRemindsAgain pins the 再收信再提醒 branch: the
// once-per-mail key never blocks a NEW require_reply mail from getting its
// own single reminder.
func TestCollabReplyNudgeNewMailRemindsAgain(t *testing.T) {
	root := t.TempDir()
	first := deliverRequireReply(t, root, "sc_peer", "sc_me", "第一封待回信", true)

	mp := testutil.NewMock("m",
		testutil.Turn{Text: "work done"},
		testutil.Turn{Text: "我会回信"},
		testutil.Turn{Text: "more work"},
		testutil.Turn{Text: "我会回第二封"},
	)
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{CollabReplyNudge: replyNudgeConfig(root, "")}, event.Discard)

	if err := a.Run(context.Background(), "turn 1"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if got := countReplyNudgeMessages(a); got != 1 {
		t.Fatalf("reminder messages after turn 1 = %d, want 1", got)
	}
	// 第二封 require_reply 信落箱并进入上下文，随后闭合触发它自己的提醒。
	second := deliverRequireReply(t, root, "sc_other", "sc_me", "第二封待回信", true)
	if err := a.Run(context.Background(), "turn 2"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if got := mp.CallCount(); got != 4 {
		t.Fatalf("provider calls after turn 2 = %d, want turn-2 work + its reminder round", got)
	}
	if got := countReplyNudgeMessages(a); got != 2 {
		t.Fatalf("reminder messages after turn 2 = %d, want 2 (one per mail)", got)
	}
	reminders := 0
	for _, m := range a.Session().Messages {
		if !strings.Contains(m.Content, CollabReplyNudgeMarker) {
			continue
		}
		reminders++
		if reminders == 1 && !strings.Contains(m.Content, "threadId="+first.ID) {
			t.Fatalf("first reminder should name threadId=%s, got: %s", first.ID, m.Content)
		}
		if reminders == 2 {
			if !strings.Contains(m.Content, "threadId="+second.ID) {
				t.Fatalf("second reminder should name the NEW threadId=%s, got: %s", second.ID, m.Content)
			}
			if strings.Contains(m.Content, "threadId="+first.ID) {
				t.Fatal("the already-reminded mail must not be re-listed")
			}
		}
	}
	if reminders != 2 {
		t.Fatalf("reminder count = %d, want 2", reminders)
	}
}

// TestCollabReplyNudgeWritesStatusStreamAudit pins the audit leg: the reminder
// rides the task-202 collab status stream as a needs_decision reply_nudge line.
func TestCollabReplyNudgeWritesStatusStreamAudit(t *testing.T) {
	root := t.TempDir()
	statusPath := filepath.Join(t.TempDir(), "collab-status.jsonl")
	deliverRequireReply(t, root, "sc_peer", "sc_me", "帮我确认构建结果", true)

	mp := testutil.NewMock("m",
		testutil.Turn{Text: "work done"},
		testutil.Turn{Text: "我会回信"},
	)
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{CollabReplyNudge: replyNudgeConfig(root, statusPath)}, event.Discard)

	if err := a.Run(context.Background(), "turn 1"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("status stream not written: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, CollabStatusReplyNudge) {
			continue
		}
		var ev struct {
			Event         string `json:"event"`
			NeedsDecision bool   `json:"needs_decision"`
			Summary       string `json:"summary"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("status line parse: %v (%s)", err, line)
		}
		if ev.Event != CollabStatusReplyNudge || !ev.NeedsDecision {
			t.Fatalf("reply_nudge line malformed: %s", line)
		}
		if !strings.Contains(ev.Summary, "回信提醒") {
			t.Fatalf("reply_nudge summary should name the reminder, got: %s", ev.Summary)
		}
		found = true
	}
	if !found {
		t.Fatalf("no %s line in status stream: %s", CollabStatusReplyNudge, string(b))
	}
}

// TestInboxMessagesReturnsSettledMail pins the MailStore reader the scan
// relies on: settled mail stays readable (Peek would hide it).
func TestInboxMessagesReturnsSettledMail(t *testing.T) {
	root := t.TempDir()
	mail := deliverRequireReply(t, root, "sc_peer", "sc_me", "已读未回的信", true)
	stored := sessioncollab.NewMailStore(root)
	inbox, err := stored.InboxMessages("sc_me")
	if err != nil {
		t.Fatalf("InboxMessages: %v", err)
	}
	var found bool
	for _, m := range inbox {
		if m.ID == mail.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("settled mail %s must stay visible in InboxMessages", mail.ID)
	}
	if peek, err := stored.Peek("sc_me"); err == nil && len(peek) != 0 {
		t.Fatalf("Peek should be empty after ack, got %d", len(peek))
	}
}
