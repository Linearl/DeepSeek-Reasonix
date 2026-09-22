package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

func execEventWait(t *testing.T, cfg SessionCollabConfig, args string) map[string]any {
	t.Helper()
	out, err := NewEventWaitTool(cfg).Execute(context.Background(), []byte(args))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("event_wait must return JSON: %v\n%s", err, out)
	}
	return payload
}

// Task 228 core scenario: dispatch to peers, wake the moment every turn
// closes. The targets start running and flip to idle mid-wait; the call must
// return satisfied well before the timeout, with elapsed far below it.
func TestEventWaitAllIdleWakesOnTurnClose(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	statusFixture(t, dir, "b", "Beta", "sc_b", "top_b")

	var running atomic.Bool
	running.Store(true)
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) {
			return running.Load(), 1700000000000, 0, true
		},
	}

	// Flip to idle shortly after the wait starts: the poll loop must notice
	// on its 5s-clamped interval, not at the timeout.
	go func() {
		time.Sleep(200 * time.Millisecond)
		running.Store(false)
	}()

	started := time.Now()
	payload := execEventWait(t, cfg, `{"targets":["sc_a","sc_b"],"mode":"all_idle","interval_s":5,"timeout_s":60}`)
	elapsed := time.Since(started)
	if payload["satisfied"] != true {
		t.Fatalf("all_idle must satisfy once both peers go idle: %v", payload)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("the wake must come from the poll, not the timeout (elapsed %v)", elapsed)
	}
	fired, ok := payload["firedBy"].([]any)
	if !ok || len(fired) != 2 {
		t.Fatalf("firedBy must name every satisfied target: %v", payload["firedBy"])
	}
}

// The timeout path is the no-hang guarantee: targets that never reach the
// condition return satisfied:false plus the per-target snapshot.
func TestEventWaitTimeoutReturnsSnapshotNotHang(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) {
			return true, 1700000000000, 0, true // running forever
		},
	}
	started := time.Now()
	payload := execEventWait(t, cfg, `{"targets":["sc_a"],"mode":"all_idle","interval_s":5,"timeout_s":10}`)
	if time.Since(started) > 15*time.Second {
		t.Fatalf("timeout must cap the wait instead of hanging")
	}
	if payload["satisfied"] != false {
		t.Fatalf("a never-idle target must end unsatisfied: %v", payload)
	}
	sessions, _ := payload["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("the snapshot must carry every watched target: %v", payload["sessions"])
	}
	rec, _ := sessions[0].(map[string]any)
	if rec["state"] != "running" {
		t.Fatalf("snapshot state must match the live judgement: %v", rec)
	}
}

// Stop/interrupt lands as a cancelled context between polls: the tool settles
// with the last snapshot and no error, so the turn's recovery stays clean.
func TestEventWaitHonoursContextCancel(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) {
			return true, 1700000000000, 0, true
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	out, err := NewEventWaitTool(cfg).Execute(ctx, []byte(`{"targets":["sc_a"],"mode":"all_idle","interval_s":30,"timeout_s":600}`))
	if err != nil {
		t.Fatalf("cancel must settle the wait without an error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["satisfied"] != false || payload["interrupted"] != true {
		t.Fatalf("a cancelled wait reports interrupted+unsatisfied: %v", payload)
	}
}

// Task 228 acceptance: the wait and the one-shot sweep share one judgement —
// collabStatusRecords — so the same target cannot read idle to one tool and
// running to the other. Assert it over all four states.
func TestEventWaitAgreesWithGetSessionStatus(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	statusFixture(t, dir, "b", "Beta", "sc_b", "top_b")
	statusFixture(t, dir, "c", "Gamma", "sc_c", "top_c")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(contactID string) (bool, int64, int, bool) {
			switch contactID {
			case "sc_a":
				return true, 1700000000000, 0, true // running
			case "sc_b":
				return false, 0, 2, true // queued (degraded steers parked)
			case "sc_c":
				return false, 0, 0, true // idle
			}
			return false, 0, 0, false
		},
	}
	want := map[string]string{"sc_a": "running", "sc_b": "queued", "sc_c": "idle"}

	// any_idle wakes on sc_c even while sc_a/sc_b are busy...
	payload := execEventWait(t, cfg, `{"targets":["sc_a","sc_b","sc_c"],"mode":"any_idle","interval_s":60,"timeout_s":10}`)
	if payload["satisfied"] != true {
		t.Fatalf("any_idle must wake on the first idle target: %v", payload)
	}
	// ...and all_idle with a queued peer keeps waiting (timeout, not hang).
	payload = execEventWait(t, cfg, `{"targets":["sc_a","sc_b","sc_c"],"mode":"all_idle","interval_s":5,"timeout_s":10}`)
	if payload["satisfied"] != false {
		t.Fatalf("a queued peer is not idle: all_idle must keep waiting: %v", payload)
	}

	// The two tools answer the SAME records for the SAME targets.
	statusOut, err := NewGetSessionStatusTool(cfg).Execute(nil, []byte(`{"targets":["sc_a","sc_b","sc_c"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var sweep struct {
		Sessions []struct {
			ContactID string `json:"contactId"`
			State     string `json:"state"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(statusOut), &sweep); err != nil {
		t.Fatal(err)
	}
	if len(sweep.Sessions) != 3 {
		t.Fatalf("sweep must cover the three targets: %v", sweep.Sessions)
	}
	for _, rec := range sweep.Sessions {
		if rec.State != want[rec.ContactID] {
			t.Fatalf("%s reads %q, want %q", rec.ContactID, rec.State, want[rec.ContactID])
		}
	}
}

// any_message is the "they replied" trigger: unread mail (or a degraded steer
// parked in the session inbox) satisfies the wait.
func TestEventWaitAnyMessageWakesOnMail(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) {
			return true, 1700000000000, 0, true // still running: mail outranks nothing here
		},
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		mail := sessioncollab.NewMailStore(cfg.MailDir)
		if _, err := mail.Deliver(sessioncollab.MailMessage{From: "sc_peer", To: "sc_a", Body: "done"}); err != nil {
			t.Errorf("deliver: %v", err)
		}
	}()
	payload := execEventWait(t, cfg, `{"targets":["sc_a"],"mode":"any_message","interval_s":5,"timeout_s":30}`)
	if payload["satisfied"] != true {
		t.Fatalf("any_message must wake on unread mail: %v", payload)
	}
}

// Unmatched targets are reported in every snapshot, never dropped.
func TestEventWaitReportsUnmatchedTargets(t *testing.T) {
	dir := t.TempDir()
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
	}
	payload := execEventWait(t, cfg, `{"targets":["sc_ghost"],"mode":"all_idle","interval_s":5,"timeout_s":10}`)
	unmatched, _ := payload["unmatched"].([]any)
	if len(unmatched) != 1 || unmatched[0] != "sc_ghost" {
		t.Fatalf("an unresolvable target must be reported: %v", payload["unmatched"])
	}
	if payload["satisfied"] != false {
		t.Fatalf("nothing to watch means nothing to satisfy: %v", payload)
	}
}

// Argument validation: no targets is a hard error pointing at the one-shot
// sweep; an unknown mode names the legal set.
func TestEventWaitArgValidation(t *testing.T) {
	dir := t.TempDir()
	cfg := SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}
	_, err := NewEventWaitTool(cfg).Execute(nil, []byte(`{"mode":"all_idle"}`))
	if err == nil || !strings.Contains(err.Error(), "targets is required") {
		t.Fatalf("missing targets must error with the sweep pointer: %v", err)
	}
	_, err = NewEventWaitTool(cfg).Execute(nil, []byte(`{"targets":["a"],"mode":"eventually"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("unknown mode must error naming the legal set: %v", err)
	}
	if (NewEventWaitTool(cfg)).ReadOnly() != true {
		t.Fatal("event_wait must be read-only: it polls status and sends nothing")
	}
}

// Task 228 audit m2: under all_idle a target the directory cannot resolve was
// never observed, so "every target idle" must not satisfy while one is
// missing — even when every RESOLVED target reads idle. any_idle keeps its
// "one observed hit is enough" semantics.
func TestEventWaitAllIdleStaysUnsatisfiedWithUnmatchedTarget(t *testing.T) {
	dir := t.TempDir()
	statusFixture(t, dir, "a", "Alpha", "sc_a", "top_a")
	cfg := SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
		MailDir:       filepath.Join(t.TempDir(), "mail"),
		SessionStatus: func(string) (bool, int64, int, bool) {
			return false, 0, 0, true // every resolvable target is idle
		},
	}
	payload := execEventWait(t, cfg, `{"targets":["sc_a","sc_typo"],"mode":"all_idle","interval_s":5,"timeout_s":10}`)
	if payload["satisfied"] != false {
		t.Fatalf("all_idle must stay unsatisfied while a target is unmatched: %v", payload)
	}
	unmatched, _ := payload["unmatched"].([]any)
	if len(unmatched) != 1 || unmatched[0] != "sc_typo" {
		t.Fatalf("the unmatched target must stay visible in the snapshot: %v", payload["unmatched"])
	}
	// any_idle over the same mixed set is honest the other way: the observed
	// idle target satisfies "any".
	payload = execEventWait(t, cfg, `{"targets":["sc_a","sc_typo"],"mode":"any_idle","interval_s":5,"timeout_s":10}`)
	if payload["satisfied"] != true {
		t.Fatalf("any_idle is satisfied by the one observed idle target: %v", payload)
	}
}
