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

// ── 任务 879: wait 等长阻塞工具的静默不是 hang ───────────────────────────────

// toolCallRecord builds a tool_dispatch/tool_result record carrying a
// named tool call — the exact durable shape the runtime writes around every
// tool execution (turn_events persistAndPublish treats both kinds as barriers).
func toolCallRecord(turn string, seq uint64, kind string, status event.TurnStatus, at time.Time, id, name string) turnevent.Envelope {
	return env(turn, seq, kind, status, at, func(r *turnevent.Envelope) {
		r.Event.Tool = &eventwire.Tool{ID: id, Name: name}
	})
}

// Task 879 (验收①): a wait blocked past ResponsivenessSilenceAfter writes
// nothing to the ledger by design — the wait tool blocks until its background
// jobs finish (tool/builtin/bgjobs.go), so a >6min silence with the wait call
// still open must classify as waiting_tool, not the "silent" verdict the
// desktop hang gate maps to the 会话疑似卡死 prompt. The controls pin the
// boundary: once the wait's tool_result landed, the same silence is silent
// again (验收② — the dead-runtime shape is unchanged), and a non-blocking tool
// in flight never downgrades.
func TestResponsivenessWaitToolSilenceIsWaitingToolNotSilent(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	start := now.Add(-30 * time.Minute)
	dispatch := now.Add(-10 * time.Minute) // ledger silent 10m — past the 6m window
	openWait := []turnevent.Envelope{
		env("turn_b", 1, "turn_started", event.TurnInProgress, start, nil),
		env("turn_b", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		toolCallRecord("turn_b", 3, "tool_dispatch", event.TurnInProgress, dispatch, "call-1", "wait"),
	}

	writeLedger(t, ledgerPath, openWait, "")
	report := readResponsiveness(sessionPath, ledgerPath, now)
	if report.Verdict != "waiting_tool" {
		t.Fatalf("verdict = %q, want waiting_tool for the open wait (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active == nil || report.Active.BlockingTool != "wait" {
		t.Fatalf("active = %+v, want blockingTool \"wait\"", report.Active)
	}
	if !strings.Contains(report.Detail, "wait") {
		t.Errorf("detail must name the blocking tool: %s", report.Detail)
	}

	// The wait returned 9 minutes ago and the ledger went quiet again: no
	// blocking tool in flight → silent, byte-for-byte the pre-task-879 verdict
	// for the dead-runtime shape (验收② 回归零变化).
	closedWait := append(append([]turnevent.Envelope{}, openWait...),
		env("turn_b", 4, "tool_result", event.TurnInProgress, now.Add(-9*time.Minute), func(r *turnevent.Envelope) {
			r.Event.Tool = &eventwire.Tool{ID: "call-1", Name: "wait", Output: "[bash-1] done"}
		}))
	writeLedger(t, ledgerPath, closedWait, "")
	report = readResponsiveness(sessionPath, ledgerPath, now)
	if report.Verdict != "silent" {
		t.Fatalf("verdict = %q, want silent once the wait returned (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active == nil || report.Active.BlockingTool != "" {
		t.Fatalf("active = %+v, want no blocking tool after the result", report.Active)
	}

	// A non-blocking tool (edit) in flight past the window stays silent: only
	// wait-family silence is designed behavior.
	writeLedger(t, ledgerPath, []turnevent.Envelope{
		env("turn_b", 1, "turn_started", event.TurnInProgress, start, nil),
		env("turn_b", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		toolCallRecord("turn_b", 3, "tool_dispatch", event.TurnInProgress, dispatch, "call-2", "edit"),
	}, "")
	report = readResponsiveness(sessionPath, ledgerPath, now)
	if report.Verdict != "silent" {
		t.Fatalf("verdict = %q, want silent for a non-blocking tool in flight (detail: %s)", report.Verdict, report.Detail)
	}
	if report.Active == nil || report.Active.BlockingTool != "" {
		t.Fatalf("active = %+v, want no blocking tool for edit", report.Active)
	}
}

// Task 879: the blocking set covers every wait-family tool whose contract is
// an unbounded/long block — event_wait (default 1200s) and a capability proxy
// fronting wait (display name in Name, real target in ResolvedName) both
// downgrade; wait's non-blocking siblings never do.
func TestResponsivenessBlockingToolSetBoundary(t *testing.T) {
	cases := []struct {
		name         string
		toolName     string
		resolvedName string
		wantVerdict  string
		wantBlocking string
	}{
		{"event_wait blocks up to 20min by default", "event_wait", "", "waiting_tool", "event_wait"},
		{"proxy fronting wait matches on ResolvedName", "cap_proxy", "wait", "waiting_tool", "wait"},
		{"bash_output never blocks", "bash_output", "", "silent", ""},
		{"kill_shell never blocks", "kill_shell", "", "silent", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionPath, now := responsivenessFixture(t)
			ledgerPath := store.SessionTurnEventLog(sessionPath)
			start := now.Add(-30 * time.Minute)
			dispatch := now.Add(-10 * time.Minute)
			rec := toolCallRecord("turn_x", 3, "tool_dispatch", event.TurnInProgress, dispatch, "call-1", tc.toolName)
			if tc.resolvedName != "" {
				rec.Event.Tool.ResolvedName = tc.resolvedName
			}
			writeLedger(t, ledgerPath, []turnevent.Envelope{
				env("turn_x", 1, "turn_started", event.TurnInProgress, start, nil),
				env("turn_x", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
				rec,
			}, "")
			report := readResponsiveness(sessionPath, ledgerPath, now)
			if report.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q (detail: %s)", report.Verdict, tc.wantVerdict, report.Detail)
			}
			got := ""
			if report.Active != nil {
				got = report.Active.BlockingTool
			}
			if got != tc.wantBlocking {
				t.Fatalf("blockingTool = %q, want %q", got, tc.wantBlocking)
			}
		})
	}
}

// Task 879 (验收③): the three liveness criteria are orthogonal — running
// (streaming inside the window), pendingPrompt (waiting_user), and a
// long-blocking tool in flight each hold their own verdict, none reads as the
// hang, and only "no criterion present + silence past the window" remains the
// hang shape. Precedence between them is pinned too: a user prompt outranks a
// blocking tool, and the silence window still outranks nothing.
func TestResponsivenessLivenessCriteriaAreOrthogonal(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	start := now.Add(-30 * time.Minute)
	early := func(r *turnevent.Envelope) { r.Event.Text = "…" }
	writeBase := func(recs ...turnevent.Envelope) {
		t.Helper()
		base := []turnevent.Envelope{
			env("turn_o", 1, "turn_started", event.TurnInProgress, start, nil),
			env("turn_o", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), early),
		}
		writeLedger(t, ledgerPath, append(base, recs...), "")
	}
	waitDispatch := func(at time.Time) turnevent.Envelope {
		return toolCallRecord("turn_o", 3, "tool_dispatch", event.TurnInProgress, at, "call-1", "wait")
	}

	t.Run("running: last write inside the window is working", func(t *testing.T) {
		writeBase(env("turn_o", 3, "text", event.TurnInProgress, now.Add(-time.Minute), early))
		if report := readResponsiveness(sessionPath, ledgerPath, now); report.Verdict != "working" {
			t.Fatalf("verdict = %q, want working (detail: %s)", report.Verdict, report.Detail)
		}
	})

	t.Run("pendingPrompt: waiting_user wins even past the silence window", func(t *testing.T) {
		writeBase(env("turn_o", 3, "ask_request", event.TurnWaitingUser, now.Add(-10*time.Minute), nil))
		if report := readResponsiveness(sessionPath, ledgerPath, now); report.Verdict != "waiting_user" {
			t.Fatalf("verdict = %q, want waiting_user (detail: %s)", report.Verdict, report.Detail)
		}
	})

	t.Run("blocking tool: silence with wait open is waiting_tool", func(t *testing.T) {
		writeBase(waitDispatch(now.Add(-10 * time.Minute)))
		if report := readResponsiveness(sessionPath, ledgerPath, now); report.Verdict != "waiting_tool" {
			t.Fatalf("verdict = %q, want waiting_tool (detail: %s)", report.Verdict, report.Detail)
		}
	})

	t.Run("prompt outranks blocking tool", func(t *testing.T) {
		writeBase(
			waitDispatch(now.Add(-20*time.Minute)),
			env("turn_o", 4, "turn_status", event.TurnWaitingUser, now.Add(-10*time.Minute), nil),
		)
		if report := readResponsiveness(sessionPath, ledgerPath, now); report.Verdict != "waiting_user" {
			t.Fatalf("verdict = %q, want waiting_user (detail: %s)", report.Verdict, report.Detail)
		}
	})

	t.Run("blocking tool inside the window is ordinary working", func(t *testing.T) {
		writeBase(waitDispatch(now.Add(-time.Minute)))
		if report := readResponsiveness(sessionPath, ledgerPath, now); report.Verdict != "working" {
			t.Fatalf("verdict = %q, want working (detail: %s)", report.Verdict, report.Detail)
		}
	})

	t.Run("no criterion + silence past the window is silent", func(t *testing.T) {
		writeBase(env("turn_o", 3, "text", event.TurnInProgress, now.Add(-10*time.Minute), early))
		if report := readResponsiveness(sessionPath, ledgerPath, now); report.Verdict != "silent" {
			t.Fatalf("verdict = %q, want silent (detail: %s)", report.Verdict, report.Detail)
		}
	})
}

// Task 879: the human report exposes the blocking tool the verdict rests on
// and keeps the --watch next step — a waiting_tool session reads as "waiting,
// here is how to confirm" instead of an unexplained silence.
func TestRenderResponsivenessShowsBlockingToolAndWaitHint(t *testing.T) {
	sessionPath, now := responsivenessFixture(t)
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	start := now.Add(-30 * time.Minute)
	writeLedger(t, ledgerPath, []turnevent.Envelope{
		env("turn_r", 1, "turn_started", event.TurnInProgress, start, nil),
		env("turn_r", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), func(r *turnevent.Envelope) { r.Event.Text = "…" }),
		toolCallRecord("turn_r", 3, "tool_dispatch", event.TurnInProgress, now.Add(-10*time.Minute), "call-1", "wait"),
	}, "")
	report := readResponsiveness(sessionPath, ledgerPath, now)
	if report.Verdict != "waiting_tool" {
		t.Fatalf("verdict = %q, want waiting_tool (detail: %s)", report.Verdict, report.Detail)
	}
	out := RenderResponsivenessText(report, now)
	for _, want := range []string{
		"verdict: waiting_tool",
		"blocking tool in flight: wait",
		"--watch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}
