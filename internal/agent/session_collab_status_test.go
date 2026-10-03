package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// statusFixture writes one addressable session with a controlled identity.
func statusFixture(t *testing.T, dir, stem, title, contact, topic string) string {
	t.Helper()
	p := filepath.Join(dir, stem+".jsonl")
	writeEmpty(t, p)
	if err := UpdateBranchMeta(p, true, func(m *BranchMeta) error {
		m.CustomTitle = title
		m.ContactID = contact
		m.TopicID = topic
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

type statusRecord struct {
	ContactID  string `json:"contactId"`
	Title      string `json:"title"`
	State      string `json:"state"`
	Unread     int    `json:"unreadInbox"`
	LastActity int64  `json:"lastActivity"`
	Hint       string `json:"hint,omitempty"`
}

type statusPayload struct {
	Total     int            `json:"total"`
	Unmatched []string       `json:"unmatched"`
	Sessions  []statusRecord `json:"sessions"`
}

func execStatus(t *testing.T, cfg SessionCollabConfig, args string) statusPayload {
	t.Helper()
	out, err := NewGetSessionStatusTool(cfg).Execute(nil, []byte(args))
	if err != nil {
		t.Fatal(err)
	}
	var payload statusPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("status must return JSON: %v\n%s", err, out)
	}
	return payload
}

// Task 218: one tool answers "who is busy?" — no arguments sweeps the whole
// directory, explicit targets resolve by contact_id / topic_id / title mixed
// freely, and an unresolvable target is reported rather than dropped.
func TestGetSessionStatusFullSweepAndBatchTargets(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	statusFixture(t, dir, "b", "Beta", "sc_b", "top_b")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(contactID string) (bool, int64, int, bool) {
			if contactID == "sc_a" {
				return true, 1700000000000, 0, true
			}
			return false, 0, 0, true
		},
	}

	full := execStatus(t, cfg, `{}`)
	// The sweep covers the whole addressable directory (the scan also sees the
	// real global workspace), so assert on our fixtures being present with the
	// right states rather than on an exact total.
	if full.Total < 2 || len(full.Sessions) < 2 {
		t.Fatalf("no-argument call must sweep the directory: %d/%d", full.Total, len(full.Sessions))
	}
	byContact := map[string]statusRecord{}
	for _, rec := range full.Sessions {
		byContact[rec.ContactID] = rec
	}
	if byContact["sc_a"].State != "running" || byContact["sc_a"].LastActity != 1700000000000 {
		t.Fatalf("the active turn must read running with the probe's timestamp: %+v", byContact["sc_a"])
	}
	if byContact["sc_b"].State != "idle" {
		t.Fatalf("a quiet, drained peer reads idle: %+v", byContact["sc_b"])
	}

	batch := execStatus(t, cfg, `{"targets":["sc_a","top_b","Beta","sc_nobody"]}`)
	if len(batch.Sessions) != 3 {
		t.Fatalf("batch must answer every resolvable target: %+v", batch.Sessions)
	}
	if len(batch.Unmatched) != 1 || batch.Unmatched[0] != "sc_nobody" {
		t.Fatalf("an unresolvable target must be reported explicitly: %v", batch.Unmatched)
	}

	// Pending mail outranks idle: a peer with unconsumed inbox reads queued.
	mail := sessioncollab.NewMailStore(cfg.MailDir)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_other", To: "sc_b", Body: "work item"}); err != nil {
		t.Fatal(err)
	}
	queued := execStatus(t, cfg, `{"targets":["sc_b"]}`)
	if queued.Sessions[0].State != "queued" || queued.Sessions[0].Unread != 1 {
		t.Fatalf("unconsumed mail must read queued: %+v", queued.Sessions[0])
	}
}

// Task 218: a runtime the probe cannot see is unknown — never a guessed idle.
// An idle guess double-dispatches work onto a peer that may be mid-turn in
// another process, which is exactly the failure this tool exists to prevent.
func TestGetSessionStatusInvisibleProcessIsUnknown(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "ghost", "Ghost", "sc_ghost", "")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) { return false, 0, 0, false },
	}
	payload := execStatus(t, cfg, `{"targets":["sc_ghost"]}`)
	if payload.Sessions[0].State != "unknown" {
		t.Fatalf("an invisible process must read unknown: %+v", payload.Sessions[0])
	}
}

// Task 375: unknown must be self-explanatory — the record carries an
// actionable hint (process invisible; check lastActivity and the inbox tail
// before concluding idle), and running/idle/queued rows carry NO hint so the
// field never dilutes a definite answer.
func TestGetSessionStatusUnknownCarriesHint(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "ghost", "Ghost", "sc_ghost375", "")
	statusFixture(t, dir, "live", "Live", "sc_live375", "")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(id string) (bool, int64, int, bool) {
			if id == "sc_live375" {
				return true, 0, 0, true // running, known
			}
			return false, 0, 0, false // invisible -> unknown
		},
	}
	out, err := NewGetSessionStatusTool(cfg).Execute(nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unknown = this process cannot see the session's runtime") {
		t.Fatalf("the unknown hint must be present and actionable, got: %s", out)
	}
	if !strings.Contains(out, "inbox.jsonl tail") {
		t.Fatalf("the hint must name the authoritative check (inbox tail), got: %s", out)
	}
	// Per-record check: the ghost row carries the hint; the known-running row
	// must NOT — the field never dilutes a definite answer.
	full := execStatus(t, cfg, `{}`)
	ghostHint, liveHint := "", ""
	for _, rec := range full.Sessions {
		if rec.ContactID == "sc_ghost375" {
			ghostHint = rec.Hint
		}
		if rec.ContactID == "sc_live375" {
			liveHint = rec.Hint
		}
	}
	if ghostHint == "" || !strings.Contains(ghostHint, "runtime") {
		t.Fatalf("the unknown record must carry the actionable hint, got %q", ghostHint)
	}
	if liveHint != "" {
		t.Fatalf("a known-running record must not carry the unknown hint, got %q", liveHint)
	}
}

// Task 218: a status probe is strictly read-only — the mailbox file and the
// consumption cursor must survive a sweep byte-identical.
func TestGetSessionStatusDoesNotTouchTheMailbox(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "quiet", "Quiet", "sc_quiet", "")
	mailDir := filepath.Join(t.TempDir(), "mail")
	mail := sessioncollab.NewMailStore(mailDir)
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_other", To: "sc_quiet", Body: "pending"}); err != nil {
		t.Fatal(err)
	}
	inboxPath := filepath.Join(mailDir, "sc_quiet.inbox.jsonl")
	before, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := SessionCollabConfig{
		Enabled: true, SessionDir: dir, WorkspaceRoot: dir, MailDir: mailDir,
	}
	payload := execStatus(t, cfg, `{"targets":["sc_quiet"]}`)
	if payload.Sessions[0].Unread != 1 {
		t.Fatalf("the probe must see the pending mail: %+v", payload.Sessions[0])
	}
	after, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a status probe must not write the inbox file")
	}
	if _, err := os.Stat(filepath.Join(mailDir, "sc_quiet.inbox.seen.json")); !os.IsNotExist(err) {
		t.Fatal("a status probe must not advance the consumption cursor")
	}
	if strings.Contains(payload.Sessions[0].Title, "pending") {
		t.Fatal("status must carry metadata, not transcript bytes")
	}
}

// Task 218 (dispatch-round feedback): a steer degraded to followup is queued
// INSIDE the target's session inbox, past the collab mailbox cursor. The
// probe's pending must join the counter — an idle peer with a degraded steer
// parked in its inbox must read queued, never idle.
func TestDegradedSteerBacklogCountsAsQueued(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "d", "Degraded", "sc_d", "")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) { return false, 0, 2, true },
	}
	payload := execStatus(t, cfg, `{"targets":["sc_d"]}`)
	if payload.Sessions[0].State != "queued" || payload.Sessions[0].Unread != 2 {
		t.Fatalf("a degraded steer parked in the session inbox must read queued with the probe's count: %+v", payload.Sessions[0])
	}
}
