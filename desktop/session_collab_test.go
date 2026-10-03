package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/runtimepolicy"
	"reasonix/internal/sessioncollab"
)

// appendRawLine writes a line straight into a mailbox file, standing in for a
// sender that wrote a record the validation path would have rejected.
func appendRawLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// newCollabTestMail builds an isolated mailbox and returns a delivery harness
// whose enqueue always succeeds unless failFor matches the message body.
func newCollabTestMail(t *testing.T) (*sessioncollab.MailStore, string) {
	t.Helper()
	dir := t.TempDir()
	return sessioncollab.NewMailStore(filepath.Join(dir, "mail")), "sc_target"
}

func collabTestDelivery(t *testing.T, mail *sessioncollab.MailStore, fail func(sessioncollab.MailMessage) error) (collabDelivery, *[]string, *[]string) {
	t.Helper()
	deliveredBodies := &[]string{}
	notices := &[]string{}
	d := collabDelivery{
		enqueue: func(msg sessioncollab.MailMessage, body string) (bool, error) {
			if fail != nil {
				if err := fail(msg); err != nil {
					return false, err
				}
			}
			*deliveredBodies = append(*deliveredBodies, body)
			return false, nil
		},
		notify: func(msg sessioncollab.MailMessage, kind, text string) {
			_ = kind
			// Route notices through the real store so the "sender is told" claim
			// is verified against the sender's actual inbox.
			if strings.TrimSpace(msg.From) != "" {
				_, _ = mail.Deliver(context.Background(), sessioncollab.MailMessage{
					To: msg.From, Body: text, Hop: msg.Hop, ThreadID: msg.ThreadID,
				})
			}
			*notices = append(*notices, text)
		},
		deriveHop: func(sessioncollab.MailMessage) (int, error) { return 0, nil },
		render:    func(msg sessioncollab.MailMessage, hop int) string { return msg.Body },
	}
	return d, deliveredBodies, notices
}

// The audit's F1: a claim that consumed the cursor before delivery lost every
// message that failed afterwards. A failed delivery must stay pending.
func TestDeliveryFailureDoesNotLoseMessage(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	d, _, notices := collabTestDelivery(t, mail, func(sessioncollab.MailMessage) error {
		return errors.New("workspace not ready")
	})
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if delivered != 0 || refused != 0 {
		t.Fatalf("nothing should have settled: %d/%d", delivered, refused)
	}
	if err == nil {
		t.Fatal("the failure must surface to the caller, not only to the log")
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must be told once, got %d notices", len(*notices))
	}
	pending, _, claimErr := mail.Claim(context.Background(), target)
	if claimErr != nil || len(pending) != 1 {
		t.Fatalf("the message must still be pending for retry, got %d (%v)", len(pending), claimErr)
	}
}

// The retry must actually deliver, and only then settle the message.
func TestDeliveryRetrySucceedsAfterTransientFailure(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: "first"}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	d, bodies, _ := collabTestDelivery(t, mail, func(sessioncollab.MailMessage) error {
		attempts++
		if attempts == 1 {
			return errors.New("workspace not ready")
		}
		return nil
	})
	if _, _, err := runCollabDelivery(mail, target, d); err == nil {
		t.Fatal("first pass must report the failure")
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil || delivered != 1 || refused != 0 {
		t.Fatalf("retry must deliver: %d/%d (%v)", delivered, refused, err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("exactly one delivery expected, got %d", len(*bodies))
	}
	after, _, _ := mail.Claim(context.Background(), target)
	if len(after) != 0 {
		t.Fatalf("settled message must not be re-offered, got %d", len(after))
	}
}

// A batch is per-message: one bad message must not block or drop its siblings.
func TestOneFailedMessageDoesNotBlockTheRest(t *testing.T) {
	mail, target := newCollabTestMail(t)
	for _, body := range []string{"ok-1", "bad", "ok-2"} {
		if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	d, bodies, _ := collabTestDelivery(t, mail, func(msg sessioncollab.MailMessage) error {
		if msg.Body == "bad" {
			return errors.New("workspace not ready")
		}
		return nil
	})
	delivered, _, err := runCollabDelivery(mail, target, d)
	if err == nil {
		t.Fatal("the failing message must surface")
	}
	if delivered != 2 || len(*bodies) != 2 {
		t.Fatalf("the two good messages must land: delivered=%d bodies=%v", delivered, *bodies)
	}
	pending, _, _ := mail.Claim(context.Background(), target)
	if len(pending) != 1 || pending[0].Body != "bad" {
		t.Fatalf("only the failed message should remain pending, got %+v", pending)
	}
}

// A hop-exhausted message is reported to its sender and settled, so it cannot
// be re-reported forever.
func TestHopExhaustedIsReportedAndSettled(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: "ok"}); err != nil {
		t.Fatal(err)
	}
	d, bodies, notices := collabTestDelivery(t, mail, nil)
	// Force the over-limit record the way a lying relay would.
	raw := mail.InboxPath(target)
	if err := appendRawLine(raw, `{"id":"msg_deep","fromContactId":"sc_from","toContactId":"`+target+
		`","body":"too-deep","hop":6}`); err != nil {
		t.Fatal(err)
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 1 || refused != 1 {
		t.Fatalf("want 1 delivered + 1 refused, got %d/%d", delivered, refused)
	}
	if len(*bodies) != 1 || (*bodies)[0] != "ok" {
		t.Fatalf("only the in-limit message may reach the target: %v", *bodies)
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must be told, got %d notices", len(*notices))
	}
	// Both are settled, so a second pass is quiet.
	again, _, _ := mail.Claim(context.Background(), target)
	if len(again) != 0 {
		t.Fatalf("settled messages must not reappear: %+v", again)
	}
}

// A steer that cannot inject must be reported as degraded, and the message is
// still delivered as a queued follow-up.
func TestDegradedSteerIsReported(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_from", To: target, Body: "urgent", Delivery: string(sessioncollab.DeliverySteer),
	}); err != nil {
		t.Fatal(err)
	}
	d, bodies, notices := collabTestDelivery(t, mail, nil)
	delivered, _, err := runCollabDelivery(mail, target, d)
	if err != nil || delivered != 1 {
		t.Fatalf("degraded steer is still a delivery: %d (%v)", delivered, err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("the body must still reach the target: %v", *bodies)
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must hear about the degradation, got %d", len(*notices))
	}
}

// Provenance failure is a refusal, not a delivery: a reply that cannot name a
// parent is settled and reported rather than handed over.
func TestUnverifiableProvenanceIsRefusedAndReported(t *testing.T) {
	mail, target := newCollabTestMail(t)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
		From: "sc_from", To: target, Body: "relay", ThreadID: "msg_ghost",
	}); err != nil {
		t.Fatal(err)
	}
	d, bodies, notices := collabTestDelivery(t, mail, nil)
	d.deriveHop = func(sessioncollab.MailMessage) (int, error) {
		return 0, errors.New("thread not in sender's mailbox")
	}
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 0 || refused != 1 {
		t.Fatalf("want 0 delivered + 1 refused, got %d/%d", delivered, refused)
	}
	if len(*bodies) != 0 {
		t.Fatal("an unverifiable message must not reach the target")
	}
	if len(*notices) != 1 {
		t.Fatalf("the sender must be told, got %d", len(*notices))
	}
}

// The delivery text must carry both sides of the conversation so the recipient
// can verify it landed on the right session by ID, not by a title that may have
// been auto-renamed since (user feedback 2026-09-17 #3).
func TestSessionCollabDeliveryTextCarriesBothIDs(t *testing.T) {
	msg := sessioncollab.MailMessage{
		ID:   "msg_1",
		From: "sc_alice",
		To:   "sc_bob",
		Body: "please review the PR",
	}
	text := sessionCollabDeliveryText(msg, 0)
	if !strings.Contains(text, "contact_id=sc_alice") {
		t.Fatalf("delivery text must carry the sender id: %s", text)
	}
	if !strings.Contains(text, "contact_id=sc_bob") {
		t.Fatalf("delivery text must carry the recipient id: %s", text)
	}
	if !strings.Contains(text, "please review the PR") {
		t.Fatalf("delivery text must carry the body: %s", text)
	}
	if strings.Contains(text, "hop=") {
		t.Fatalf("hop 0 should not print a hop token: %s", text)
	}
}

// A hop of 0 must not force the recipient into a reply loop.
func TestSessionCollabDeliveryTextWithoutReplyAddress(t *testing.T) {
	msg := sessioncollab.MailMessage{ID: "msg_1", From: "sc_alice", To: "sc_bob", ReplyTo: "sc_alice", Body: "n"}
	text := sessionCollabDeliveryText(msg, 0)
	if strings.Contains(text, "无需回复") {
		t.Fatalf("a message with a ReplyTo must not be labelled one-way: %s", text)
	}
}

// Task 213: a deriveHop failure is classified by cause, so the sender hears
// the real reason. An exhausted chain must never wear the provenance text —
// the old catch-all sent senders hunting for a threadId bug that did not
// exist — and a cross-wired thread is its own kind, distinct from an unknown
// one.
func TestDeriveHopRefusalsAreClassifiedByCause(t *testing.T) {
	mail, target := newCollabTestMail(t)
	cases := []struct {
		name     string
		cause    error
		wantKind string
		wantIn   string
		notIn    string
	}{
		{
			name:     "hop exhausted names the limit, not provenance",
			cause:    errSessionCollabHopExhausted,
			wantKind: "refused_hop",
			wantIn:   "已达 hop 上限",
			notIn:    "无法核实",
		},
		{
			name:     "the store's hop ceiling is a hop refusal too",
			cause:    fmt.Errorf("%w (max 5): hop=6", sessioncollab.ErrHopLimit),
			wantKind: "refused_hop",
			wantIn:   "已达 hop 上限",
			notIn:    "无法核实",
		},
		{
			name:     "cross-wired thread is its own kind",
			cause:    fmt.Errorf("%w: thread msg_p was opened by sc_other, not sc_from", sessioncollab.ErrReplyThreadCrossWired),
			wantKind: "refused_cross_wire",
			wantIn:   "串线",
			notIn:    "无法核实",
		},
		{
			name:     "unknown thread stays a provenance refusal",
			cause:    fmt.Errorf("%w: msg_ghost is not in sc_from's mailbox", sessioncollab.ErrReplyThreadUnknown),
			wantKind: "refused_provenance",
			wantIn:   "无法核实",
			notIn:    "已达 hop 上限",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{
				From: "sc_from", To: target, Body: fmt.Sprintf("reply-%d", i), ThreadID: "msg_parent",
			}); err != nil {
				t.Fatal(err)
			}
			var kind, text string
			d := collabDelivery{
				enqueue: func(sessioncollab.MailMessage, string) (bool, error) {
					t.Fatal("a refused message must not reach the target")
					return false, nil
				},
				notify:    func(msg sessioncollab.MailMessage, k, note string) { kind, text = k, note },
				deriveHop: func(sessioncollab.MailMessage) (int, error) { return 0, tc.cause },
				render:    func(msg sessioncollab.MailMessage, hop int) string { return msg.Body },
			}
			delivered, refused, err := runCollabDelivery(mail, target, d)
			if err != nil {
				t.Fatal(err)
			}
			if delivered != 0 || refused != 1 {
				t.Fatalf("want 0 delivered + 1 refused, got %d/%d", delivered, refused)
			}
			if kind != tc.wantKind {
				t.Fatalf("notice kind must be %s, got %s (%s)", tc.wantKind, kind, text)
			}
			if !strings.Contains(text, tc.wantIn) {
				t.Fatalf("notice must say %q: %s", tc.wantIn, text)
			}
			if strings.Contains(text, tc.notIn) {
				t.Fatalf("notice must not carry the wrong cause %q: %s", tc.notIn, text)
			}
			if !strings.Contains(text, "messageId=") {
				t.Fatalf("notice must keep the message id: %s", text)
			}
		})
	}
}

// The cross-wired text and the provenance text must stay distinguishable: the
// sender resolved a thread but aimed it at the wrong peer, which is a
// different mistake with a different fix.
func TestCrossWiredTextIsDistinctFromProvenanceText(t *testing.T) {
	msg := sessioncollab.MailMessage{ID: "msg_1", From: "sc_from", To: "sc_target", ThreadID: "msg_p"}
	cross := sessionCollabCrossWiredText(msg, fmt.Errorf("%w: thread msg_p was opened by sc_other", sessioncollab.ErrReplyThreadCrossWired))
	prov := sessionCollabBadProvenanceText(msg, fmt.Errorf("%w: msg_ghost", sessioncollab.ErrReplyThreadUnknown))
	if cross == prov {
		t.Fatal("cross-wired and provenance refusals must not share one text")
	}
	if !strings.Contains(cross, "另一条链") || strings.Contains(cross, "无法核实它的链路来源") {
		t.Fatalf("cross-wired text must name the cross-wiring: %s", cross)
	}
}

// Task 173: a require_reply message must state the demand as a requirement in
// the delivery text; an ordinary message keeps the 156.D guidance alone.
func TestDeliveryTextMarksARequiredReply(t *testing.T) {
	plain := sessionCollabDeliveryText(sessioncollab.MailMessage{
		ID: "msg_1", From: "sc_from", To: "sc_target", ReplyTo: "sc_from", Body: "work",
	}, 0)
	if strings.Contains(plain, "要求回信") {
		t.Fatalf("an ordinary message must not demand a reply: %s", plain)
	}
	demanded := sessionCollabDeliveryText(sessioncollab.MailMessage{
		ID: "msg_2", From: "sc_from", To: "sc_target", ReplyTo: "sc_from", Body: "work", RequireReply: true,
	}, 0)
	if !strings.Contains(demanded, "要求回信") || !strings.Contains(demanded, "msg_2") {
		t.Fatalf("a demanded reply must be stated and keep the thread id: %s", demanded)
	}
}

// Task 220 M1: the constraint stripper must handle the renderer's REAL
// output — body after a blank line, closed by the --- separator and the
// trailer. This test feeds sessionCollabDeliveryText's actual bytes through
// runtimepolicy.StripQuotedConstraints, so the two sides can never drift
// apart again: a renderer change that the stripper misses fails here first.
func TestConstraintStrippingHandlesRealDeliveryText(t *testing.T) {
	delivered := sessionCollabDeliveryText(sessioncollab.MailMessage{
		ID:       "msg_x",
		From:     "sc_auditor",
		To:       "sc_main",
		Body:     "审计报告：只读审计，未重跑测试。\n\n不要修改任何文件。禁止 push。",
		ReplyTo:  "sc_auditor",
		ThreadID: "msg_x",
	}, 1)

	stripped := runtimepolicy.StripQuotedConstraints("帮忙看看这条：\n\n" + delivered)
	if got := runtimepolicy.ParseConstraints(stripped); got.ForbidMutation {
		t.Fatalf("the peer's audit body must not freeze the turn: %+v\nstripped=%q", got, stripped)
	}
	for _, leaked := range []string{"跨会话消息", "不要修改任何文件", "禁止 push", "回复方式", "threadId=msg_x"} {
		if strings.Contains(stripped, leaked) {
			t.Fatalf("real delivery text must be stripped whole, leaked %q: %q", leaked, stripped)
		}
	}
	if !strings.Contains(stripped, "帮忙看看这条") {
		t.Fatalf("text outside the envelope must survive: %q", stripped)
	}

	// The caller's own imperative next to the envelope still binds.
	bound := runtimepolicy.ParseConstraints(runtimepolicy.StripQuotedConstraints("只读分析这个 diff\n\n" + delivered))
	if !bound.ForbidMutation {
		t.Fatalf("the caller's own imperative must still bind: %+v", bound)
	}
}
