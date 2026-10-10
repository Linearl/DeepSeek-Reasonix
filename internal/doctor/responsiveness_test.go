package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/store"
	"reasonix/internal/turnevent"
)

// ledgerRecord mirrors the schema-2 on-disk shape (recordType + inline
// envelope) exactly as the turn ledger writes it.
type ledgerRecord struct {
	RecordType string `json:"recordType"`
	turnevent.Envelope
}

func env(turn string, seq uint64, kind string, status event.TurnStatus, at time.Time, mutate func(*turnevent.Envelope)) turnevent.Envelope {
	rec := turnevent.Envelope{
		SchemaVersion: 2,
		SessionID:     "sess",
		TurnID:        turn,
		Sequence:      seq,
		Kind:          kind,
		Status:        status,
		CreatedAt:     at.UnixMilli(),
		Event:         eventwire.Event{Kind: kind},
	}
	if mutate != nil {
		mutate(&rec)
	}
	return rec
}

func writeLedger(t *testing.T, path string, recs []turnevent.Envelope, tornTail string) {
	t.Helper()
	var b strings.Builder
	for _, rec := range recs {
		line, err := json.Marshal(ledgerRecord{RecordType: "event", Envelope: rec})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if tornTail != "" {
		b.WriteString(tornTail)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSchema1Line(t *testing.T, path string, rec turnevent.Envelope) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(append(line, '\n'))
}

func responsivenessFixture(t *testing.T) (sessionPath string, now time.Time) {
	t.Helper()
	return filepath.Join(t.TempDir(), "session.jsonl"), time.Now()
}

func TestResponsivenessFirstResponseWait(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledger := store.SessionTurnEventLog(sessionPath)
	// Turn 1 completed an hour ago: first response after 5s, usage reported a
	// 800k prompt (the long-context shape from the task-370 incident).
	// Turn 2 open 10 minutes ago, nothing but the turn_started record.
	t1 := now.Add(-time.Hour)
	t2 := now.Add(-10 * time.Minute)
	recs := []turnevent.Envelope{
		env("turn_1", 1, "turn_started", event.TurnInProgress, t1, nil),
		env("turn_1", 2, "reasoning", event.TurnInProgress, t1.Add(5*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		env("turn_1", 3, "text", event.TurnInProgress, t1.Add(9*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "answer" }),
		env("turn_1", 4, "usage", event.TurnCompleted, t1.Add(30*time.Second), func(r *turnevent.Envelope) {
			r.Event.Usage = &eventwire.Usage{PromptTokens: 1000, ContextPromptTokens: 800000}
		}),
		env("turn_2", 5, "turn_started", event.TurnInProgress, t2, nil),
	}
	writeLedger(t, ledger, recs, "")

	report := readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "first_response_wait" {
		t.Fatalf("verdict = %q, want first_response_wait (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active == nil || report.Active.TurnID != "turn_2" {
		t.Fatalf("active turn = %+v, want turn_2", report.Active)
	}
	if report.LatestPrompt != 800000 {
		t.Fatalf("latest prompt tokens = %d, want 800000 (from turn_1 usage)", report.LatestPrompt)
	}
	if len(report.Recent) != 1 || report.Recent[0].TurnID != "turn_1" || report.Recent[0].FirstResponseMs != 5000 {
		t.Fatalf("recent = %+v, want turn_1 with first response 5000ms", report.Recent)
	}
}

func TestResponsivenessWorkingAndSilent(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledger := store.SessionTurnEventLog(sessionPath)

	// Working: open turn, first response landed, last write 1m ago.
	workingStart := now.Add(-10 * time.Minute)
	writeLedger(t, ledger, []turnevent.Envelope{
		env("turn_w", 1, "turn_started", event.TurnInProgress, workingStart, nil),
		env("turn_w", 2, "reasoning", event.TurnInProgress, workingStart.Add(8*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		env("turn_w", 3, "tool_dispatch", event.TurnInProgress, now.Add(-time.Minute), nil),
	}, "")
	report := readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "working" {
		t.Fatalf("verdict = %q, want working (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active == nil || report.Active.FirstResponseMs != 8000 {
		t.Fatalf("active = %+v, want first response 8000ms", report.Active)
	}

	// Silent: same shape, but the last write is 10 minutes old — past the
	// stream watchdog window.
	silentStart := now.Add(-30 * time.Minute)
	writeLedger(t, ledger, []turnevent.Envelope{
		env("turn_s", 1, "turn_started", event.TurnInProgress, silentStart, nil),
		env("turn_s", 2, "reasoning", event.TurnInProgress, silentStart.Add(4*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		env("turn_s", 3, "tool_dispatch", event.TurnInProgress, now.Add(-10*time.Minute), nil),
	}, "")
	report = readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "silent" {
		t.Fatalf("verdict = %q, want silent (detail: %s)", report.Verdict, report.Detail)
	}
}

func TestResponsivenessWaitingUserAndIdle(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledger := store.SessionTurnEventLog(sessionPath)

	// waiting_user: the turn is open but blocked on the user, not stuck.
	writeLedger(t, ledger, []turnevent.Envelope{
		env("turn_q", 1, "turn_started", event.TurnInProgress, now.Add(-5*time.Minute), nil),
		env("turn_q", 2, "ask_request", event.TurnWaitingUser, now.Add(-4*time.Minute), nil),
	}, "")
	report := readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "waiting_user" {
		t.Fatalf("verdict = %q, want waiting_user (detail: %s)", report.Verdict, report.Detail)
	}

	// idle: the last turn reached a terminal status.
	writeLedger(t, ledger, []turnevent.Envelope{
		env("turn_d", 1, "turn_started", event.TurnInProgress, now.Add(-time.Hour), nil),
		env("turn_d", 2, "text", event.TurnInProgress, now.Add(-59*time.Minute), func(r *turnevent.Envelope) { r.Event.Text = "done" }),
		env("turn_d", 3, "turn_done", event.TurnCompleted, now.Add(-50*time.Minute), nil),
	}, "")
	report = readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "idle" {
		t.Fatalf("verdict = %q, want idle (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active != nil {
		t.Fatalf("active = %+v, want none", report.Active)
	}
}

func TestResponsivenessNoLedgerAndTornTail(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)

	// No ledger at all.
	report := readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "no_ledger" {
		t.Fatalf("verdict = %q, want no_ledger", report.Verdict)
	}

	// Torn tail: a crashed write leaves a partial line; the valid prefix must
	// still classify and the tear must be reported, without repairing.
	ledger := store.SessionTurnEventLog(sessionPath)
	start := now.Add(-2 * time.Minute)
	writeLedger(t, ledger, []turnevent.Envelope{
		env("turn_t", 1, "turn_started", event.TurnInProgress, start, nil),
	}, `{"schemaVersion":2,"recordType":"event","tur`)
	report = readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if !report.TornTail {
		t.Fatalf("tornTail = false, want true")
	}
	if report.Verdict != "first_response_wait" {
		t.Fatalf("verdict = %q, want first_response_wait from the valid prefix (detail: %s)", report.Verdict, report.Detail)
	}
}

func TestResponsivenessReadsSchema1AndNeverWrites(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledger := store.SessionTurnEventLog(sessionPath)
	// Schema-1 session: the legacy bootstrap line is a bare envelope with
	// schemaVersion 1 and no recordType wrapper.
	legacy := env("turn_l", 1, "turn_status", event.TurnCompleted, now.Add(-time.Hour), nil)
	legacy.SchemaVersion = 1
	writeSchema1Line(t, ledger, legacy)

	before, err := os.Stat(ledger)
	if err != nil {
		t.Fatal(err)
	}
	beforeSession, err := os.Stat(sessionPath)
	if err == nil {
		// A zero-byte session file may or may not exist; both are fine.
		_ = beforeSession
	}

	report := readResponsiveness(sessionPath, store.SessionTurnEventLog(sessionPath), now)
	if report.Verdict != "idle" {
		t.Fatalf("verdict = %q, want idle for a legacy terminal bootstrap (detail: %s)", report.Verdict, report.Detail)
	}
	// Read-only guarantee: byte size and content unchanged (mtime can only be
	// asserted on size here — reading never rewrites, unlike turnevent.Open).
	after, err := os.Stat(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("ledger size changed %d -> %d: diagnostic must not repair", before.Size(), after.Size())
	}
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), `{"schemaVersion":1`) {
		t.Fatalf("schema-1 record was rewritten: %s", data)
	}
	if _, err := os.Stat(ledger + ".damaged"); !os.IsNotExist(err) {
		t.Fatalf("diagnostic created a damaged sidecar: %v", err)
	}
}

func TestResponsivenessAcceptsLedgerPathDirectly(t *testing.T) {
	// The stuck-session workflow starts from whichever file the user found
	// first; a .turns.jsonl path must diagnose as-is (orphaned ledgers are
	// themselves diagnostic subjects).
	sessionPath, now := responsivenessFixture(t)
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	writeLedger(t, ledgerPath, []turnevent.Envelope{
		env("turn_o", 1, "turn_started", event.TurnInProgress, now.Add(-3*time.Minute), nil),
	}, "")
	report, err := CollectResponsiveness(ledgerPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "first_response_wait" {
		t.Fatalf("verdict = %q, want first_response_wait (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active == nil || report.Active.TurnID != "turn_o" {
		t.Fatalf("active = %+v, want turn_o", report.Active)
	}
}

// Task 732 (fork issue #40): an ask forwarded to the task source (the task-225
// cascade) blocks the child's open turn for up to DefaultAutopilotAskWait
// without an ask_request of its own. The fixed controller stamps the wait
// window as turn_status(waiting_user) plus a naming notice; the doctor must
// classify that shape as waiting_user — never as the silent hang the unmarked
// shape used to produce.
func TestResponsivenessCascadeWaitIsNotAHang(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	start := now.Add(-30 * time.Minute)
	waitBegin := now.Add(-9 * time.Minute)
	base := []turnevent.Envelope{
		env("turn_c", 1, "turn_started", event.TurnInProgress, start, nil),
		env("turn_c", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		env("turn_c", 3, "tool_dispatch", event.TurnInProgress, waitBegin, nil),
	}

	// The ask-forward wait as the fixed controller records it: ledger silent
	// since the wait began, but the turn is stamped waiting_user.
	marked := append(append([]turnevent.Envelope{}, base...),
		env("turn_c", 4, "turn_status", event.TurnWaitingUser, waitBegin.Add(time.Second), nil),
		env("turn_c", 5, "notice", event.TurnWaitingUser, waitBegin.Add(time.Second), func(r *turnevent.Envelope) {
			r.Event.Text = "cascade · 1 prompt(s) forwarded to task source src-contact — the turn waits for the source's decision (bounded); not stuck"
		}))
	writeLedger(t, ledgerPath, marked, "")
	report := readResponsiveness(sessionPath, ledgerPath, now)
	if report.Verdict != "waiting_user" {
		t.Fatalf("verdict = %q, want waiting_user for the marked cascade wait (detail: %s)", report.Verdict, report.Detail)
	}

	// The same session without the wait stamp is the issue-#40 misreport:
	// silence past the stream-watchdog window read as a hang.
	writeLedger(t, ledgerPath, base, "")
	report = readResponsiveness(sessionPath, ledgerPath, now)
	if report.Verdict != "silent" {
		t.Fatalf("verdict = %q, want silent for the unmarked shape (the negative control)", report.Verdict)
	}
}
