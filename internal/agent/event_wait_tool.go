package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/eventtrigger"
	"reasonix/internal/tool"
)

// Task 228: event_wait — the orchestration wait. The user's scenario: a
// session dispatches work to N peers and currently can only sleep a fixed,
// over-provisioned time (1200 s budgeted, the last reply landed at 500 s —
// 700 s wasted). This tool flips that around: poll the SAME state judgement
// get_session_status answers from (collabStatusRecords, so the two tools can
// never disagree), return as soon as the condition holds, and otherwise give
// the model a per-target snapshot at the timeout so it can decide what to do
// next instead of hanging.
//
// Tool-family relation (task 174 consolidation): get_session_status answers
// "who is busy right now?" (one shot); event_wait is a new verb — "wake me
// when that changes". It cannot fold into an existing tool's arguments
// because it occupies the turn while it waits; it stays read-only.
//
// Checker shape (task 230 groundwork): the v1 checkers are the built-in
// session predicates below (all_idle / any_idle / any_message). The polling
// loop + verdict + snapshot contract is the seam the task 230 trigger engine
// plugs general tool-call checkers into; nothing here needs to change then.
type eventWaitTool struct {
	cfg SessionCollabConfig
}

// NewEventWaitTool builds the orchestration wait tool (task 228). It shares
// SessionCollabConfig with the other collaboration tools: SessionStatus
// answers the in-process running truth (nil keeps every state unknown, which
// all_idle then never satisfies — honest, never a guessed wake), and the
// mailbox counters drive any_message.
func NewEventWaitTool(cfg SessionCollabConfig) tool.Tool {
	return eventWaitTool{cfg: cfg}
}

func (eventWaitTool) Name() string { return "event_wait" }

func (eventWaitTool) Description() string {
	return "Block until collaboration peers reach a condition, then return immediately with their status snapshot (task 228). Modes: all_idle (every target idle — the 'dispatch to N peers, wake me when all turns close' pattern; a target the directory cannot resolve keeps the wait unsatisfied, audit m2), any_idle (first target goes idle), any_message (any target has unread mail for you). Polls the same state judgement as get_session_status; each segment of the wait re-reads live state, so a target finishing early wakes the call well before the timeout. At timeout (default 1200s) returns satisfied:false plus the per-target snapshot instead of hanging — continue with other work or call again. Stop/interrupt is honored between polls and returns the snapshot collected so far. Read-only: it never sends anything. Experimental."
}

func (eventWaitTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"targets":{"type":"array","items":{"type":"string"},"description":"Sessions to watch: contact_id, topic_id, or exact title, mixed freely. Required."},"mode":{"type":"string","enum":["all_idle","any_idle","any_message"],"description":"Wake condition. all_idle (default): every target reads idle. any_idle: the first target reads idle. any_message: any target has unread inbox mail."},"interval_s":{"type":"integer","description":"Seconds between polls (default 60, clamped 5..600)."},"timeout_s":{"type":"integer","description":"Give up and return the snapshot after this many seconds (default 1200, clamped 10..7200)."}},"required":["targets"]}`)
}

func (eventWaitTool) ReadOnly() bool { return true }

const (
	eventWaitDefaultInterval = 60 * time.Second
	eventWaitMinInterval     = 5 * time.Second
	eventWaitMaxInterval     = 600 * time.Second
	eventWaitDefaultTimeout  = 1200 * time.Second
	eventWaitMinTimeout      = 10 * time.Second
	eventWaitMaxTimeout      = 7200 * time.Second
)

func clampDuration(v, lo, hi, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// eventWaitVerdict is the wait condition evaluated over one poll of the
// status records. Satisfied states which predicate fired (mode + the contacts
// that satisfied it), so the model reads WHY it woke, not just that it did.
func eventWaitVerdict(mode string, records []map[string]any) (satisfied bool, firedBy []string) {
	idleFor := func(rec map[string]any) bool { return rec["state"] == "idle" }
	unreadFor := func(rec map[string]any) bool {
		// In-memory records carry int (collabStatusRecords); JSON-decoded
		// snapshots carry float64. Accept both so the verdict is type-agnostic.
		switch unread := rec["unreadInbox"].(type) {
		case int:
			return unread > 0
		case float64:
			return unread > 0
		}
		return false
	}
	contact := func(rec map[string]any) string {
		if id, _ := rec["contactId"].(string); id != "" {
			return id
		}
		title, _ := rec["title"].(string)
		return title
	}
	switch mode {
	case "any_idle":
		for _, rec := range records {
			if idleFor(rec) {
				return true, []string{contact(rec)}
			}
		}
	case "any_message":
		for _, rec := range records {
			if unreadFor(rec) {
				return true, []string{contact(rec)}
			}
		}
	default: // all_idle
		if len(records) == 0 {
			return false, nil
		}
		firedBy = make([]string, 0, len(records))
		for _, rec := range records {
			if !idleFor(rec) {
				return false, nil
			}
			firedBy = append(firedBy, contact(rec))
		}
		return true, firedBy
	}
	return false, nil
}

func (t eventWaitTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Targets   []string `json:"targets"`
		Mode      string   `json:"mode"`
		IntervalS int      `json:"interval_s"`
		TimeoutS  int      `json:"timeout_s"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if len(p.Targets) == 0 {
		return "", fmt.Errorf("targets is required: name the sessions to watch (get_session_status with no arguments is the one-shot sweep)")
	}
	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	if mode == "" {
		mode = "all_idle"
	}
	switch mode {
	case "all_idle", "any_idle", "any_message":
	default:
		return "", fmt.Errorf("unknown mode %q: use all_idle, any_idle or any_message", p.Mode)
	}
	interval := clampDuration(time.Duration(p.IntervalS)*time.Second, eventWaitMinInterval, eventWaitMaxInterval, eventWaitDefaultInterval)
	timeout := clampDuration(time.Duration(p.TimeoutS)*time.Second, eventWaitMinTimeout, eventWaitMaxTimeout, eventWaitDefaultTimeout)

	// Task 230: the verdict and the loop now come from the eventtrigger
	// engine — event_wait is the first consumer of the generic trigger
	// mechanism (228/230 boundary: event_wait WAITS by occupying this turn;
	// the engine TRIGGERS independently). The builtin below routes the very
	// same eventWaitVerdict + unmatched rule through engine.Evaluate, so the
	// two call sites cannot disagree (acceptance: one judgement, two
	// conclusions).
	records, unmatched := []map[string]any{}, []string{}
	firedBy := []string{}
	var modeJSON json.RawMessage = json.RawMessage(strconv.Quote(mode))
	eng := eventtrigger.NewEngine(nil) // tools unused: this wait has no tool checker
	if err := eng.RegisterBuiltin("session_status", func(_ context.Context, args json.RawMessage) (string, error) {
		records, unmatched, _ = collabStatusRecords(t.cfg, p.Targets)
		sat, fb := eventWaitVerdict(mode, records)
		// Task 228 audit m2: under all_idle an unmatched target was never
		// observed, so "every target is idle" cannot honestly hold while one
		// is missing from the records (the any_* modes are different).
		if mode == "all_idle" && len(unmatched) > 0 {
			sat = false
		}
		firedBy = fb
		out, _ := json.Marshal(map[string]any{"satisfied": sat})
		return string(out), nil
	}); err != nil {
		return "", err
	}
	trigger := eventtrigger.Trigger{
		ID:        "event_wait",
		IntervalS: int(interval / time.Second),
		TimeoutS:  int(timeout / time.Second),
		Checker:   eventtrigger.Checker{Kind: eventtrigger.CheckerBuiltin, Name: "session_status", Args: modeJSON},
		Match:     eventtrigger.Match{Kind: eventtrigger.MatchJSONPath, Expr: "satisfied==true"},
	}
	if err := eng.Validate(trigger); err != nil {
		return "", err
	}
	poll := func() (satisfied bool, fb []string, recs []map[string]any, um []string) {
		hit, _, err := eng.Evaluate(context.Background(), trigger)
		if err != nil {
			return false, firedBy, records, unmatched
		}
		return hit, firedBy, records, unmatched
	}
	snapshot := func(satisfied bool, firedBy []string, records []map[string]any, unmatched []string, elapsed time.Duration, interrupted bool) string {
		out, _ := json.Marshal(map[string]any{
			"satisfied":   satisfied,
			"mode":        mode,
			"targets":     p.Targets,
			"unmatched":   unmatched,
			"firedBy":     firedBy,
			"elapsedS":    int(elapsed.Seconds()),
			"timeoutS":    int(timeout.Seconds()),
			"intervalS":   int(interval.Seconds()),
			"interrupted": interrupted,
			// Task 228: the timeout/interrupt path hands back the same records
			// get_session_status would answer with, so "keep waiting, re-check
			// or give up" is decidable without another round trip.
			"sessions": records,
		})
		return string(out)
	}

	// First tick + schedule + cancellation come from the shared loop
	// (eventtrigger.PollLoop) — the same semantics the trigger engine polls
	// with. poll data (records/unmatched/firedBy) is refreshed by the builtin
	// closure on every tick.
	outcome := eventtrigger.PollLoop(ctx, eventtrigger.PollSpec{Interval: interval, Timeout: timeout}, func() bool {
		ok, _, _, _ := poll()
		return ok
	})
	switch {
	case outcome.Satisfied:
		return snapshot(true, firedBy, records, unmatched, outcome.Elapsed, false), nil
	case outcome.Interrupted:
		return snapshot(false, nil, records, unmatched, outcome.Elapsed, true), nil
	default:
		return snapshot(false, nil, records, unmatched, outcome.Elapsed, false), nil
	}
}
