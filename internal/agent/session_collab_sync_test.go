package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

func writeEmpty(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A synchronous send must not lose the request when nobody answers in time:
// the call reports a timeout status and the message stays queued.
func TestTalkToSessionSyncTimesOutWithoutLosingRequest(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, err := EnsureContactID(to)
	if err != nil {
		t.Fatal(err)
	}
	fromID, err := EnsureContactID(from)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	start := time.Now()
	out, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"status?","timeout_ms":300}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout was not bounded: %s", elapsed)
	}
	if !strings.Contains(out, `"status":"timeout"`) {
		t.Fatalf("want timeout status, got %s", out)
	}
	var sent struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal([]byte(out), &sent); err != nil || sent.MessageID == "" {
		t.Fatalf("timeout must still report the delivered message: %s", out)
	}
	// The request is on disk, so a later pass still delivers it.
	queued, err := sessioncollab.NewMailStore(mailDir).Inbox(toID)
	if err != nil || len(queued) != 1 {
		t.Fatalf("request was lost: %v %v", queued, err)
	}
}

// A reply that carries the thread id resolves the synchronous call.
func TestTalkToSessionSyncReturnsReply(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	mail := sessioncollab.NewMailStore(mailDir)
	go func() {
		// Stand in for the answering session: wait for the request, then reply
		// on the same thread.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			box, _ := mail.Inbox(toID)
			if len(box) > 0 {
				_, _ = mail.Deliver(sessioncollab.MailMessage{
					From:     toID,
					To:       fromID,
					Body:     "done: build is green",
					ThreadID: box[0].ID,
				})
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	tool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	out, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"is it green?","timeout_ms":4000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"replied"`) || !strings.Contains(out, "build is green") {
		t.Fatalf("want the reply body, got %s", out)
	}
}

// The thread id must survive delivery, or a reply cannot be correlated.
func TestDeliveredMessageCarriesThreadID(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	tool := NewTalkToSessionTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	if _, err := tool.Execute(nil, []byte(`{"to":"`+toID+`","message":"question"}`)); err != nil {
		t.Fatal(err)
	}
	box, err := sessioncollab.NewMailStore(mailDir).Inbox(toID)
	if err != nil || len(box) != 1 {
		t.Fatalf("inbox: %v %v", box, err)
	}
	if box[0].ThreadID != box[0].ID {
		t.Fatalf("delivered message must carry its own id as the thread: %+v", box[0])
	}
}

// Task 158.A: the synchronous form must actually WAIT. The reported symptom was
// an immediate "queued" return, so the wait itself is the subject: the call must
// not come back before a reply that arrives late.
func TestTalkToSessionSyncWaitsForLateReply(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	mail := sessioncollab.NewMailStore(mailDir)
	const replyDelay = 700 * time.Millisecond
	go func() {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			box, _ := mail.Inbox(toID)
			if len(box) > 0 {
				// Answer LATE: an implementation that returns immediately
				// cannot produce the reply below.
				time.Sleep(replyDelay)
				_, _ = mail.Deliver(sessioncollab.MailMessage{
					From:     toID,
					To:       fromID,
					Body:     "late answer",
					ThreadID: box[0].ID,
				})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	syncTool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	start := time.Now()
	out, err := syncTool.Execute(context.Background(), []byte(`{"to":"`+toID+`","message":"is it green?","timeout_ms":8000}`))
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if !strings.Contains(out, `"status":"replied"`) || !strings.Contains(out, "late answer") {
		t.Fatalf("want the late reply, got %s", out)
	}
	if elapsed < replyDelay/2 {
		t.Fatalf("sync returned after %s — it did not wait for the reply: %s", elapsed, out)
	}
}

// Only the answer to THIS thread resolves the wait: an unrelated message in the
// same inbox must neither resolve it nor be consumed by it.
func TestTalkToSessionSyncIgnoresUnrelatedThread(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	mail := sessioncollab.NewMailStore(mailDir)
	unrelated, err := mail.Deliver(sessioncollab.MailMessage{
		From:     toID,
		To:       fromID,
		Body:     "unrelated chatter",
		ThreadID: "thread-from-somewhere-else",
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			box, _ := mail.Inbox(toID)
			if len(box) > 0 {
				_, _ = mail.Deliver(sessioncollab.MailMessage{
					From:     toID,
					To:       fromID,
					Body:     "the real answer",
					ThreadID: box[0].ID,
				})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	syncTool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	out, err := syncTool.Execute(context.Background(), []byte(`{"to":"`+toID+`","message":"is it green?","timeout_ms":8000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the real answer") || strings.Contains(out, "unrelated chatter") {
		t.Fatalf("the wait resolved on the wrong message: %s", out)
	}
	// Matching consumes exactly one message: the unrelated one stays pending
	// for the delivery pass, and the answer that resolved this wait does not.
	// (Inbox() lists the file, so pending-ness is asked of Claim, which is the
	// same query the delivery pump uses.)
	pending, _, err := mail.Claim(fromID)
	if err != nil {
		t.Fatal(err)
	}
	var pendingIDs []string
	for _, m := range pending {
		pendingIDs = append(pendingIDs, m.ID)
	}
	if len(pendingIDs) != 1 || pendingIDs[0] != unrelated.ID {
		t.Fatalf("want only the unrelated message left pending, got %v", pendingIDs)
	}
}

// A cancelled turn must not leave the tool asleep for the whole timeout: the
// wait ends with the caller, and the outcome is reported as "no reply yet"
// rather than as an error, because the request is already delivered.
func TestTalkToSessionSyncStopsWaitingWhenContextEnds(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(dir, "mail")
	from := filepath.Join(dir, "from.jsonl")
	to := filepath.Join(dir, "to.jsonl")
	writeEmpty(t, from)
	writeEmpty(t, to)
	toID, _ := EnsureContactID(to)
	fromID, _ := EnsureContactID(from)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	syncTool := NewTalkToSessionSyncTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		MailDir:            mailDir,
		CurrentSessionPath: from,
		CurrentContactID:   fromID,
	})
	start := time.Now()
	out, err := syncTool.Execute(ctx, []byte(`{"to":"`+toID+`","message":"anyone there?","timeout_ms":60000}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancelled wait ran for %s instead of ending with the context", elapsed)
	}
	if !strings.Contains(out, `"status":"timeout"`) || !strings.Contains(out, "wait ended early") {
		t.Fatalf("cancellation must report why the wait ended: %s", out)
	}
	// The request itself is settled: it is on disk and will still be answered.
	queued, err := sessioncollab.NewMailStore(mailDir).Inbox(toID)
	if err != nil || len(queued) != 1 {
		t.Fatalf("request was lost on cancel: %v %v", queued, err)
	}
}
