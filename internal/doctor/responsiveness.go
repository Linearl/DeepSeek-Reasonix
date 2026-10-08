// Package doctor: session responsiveness diagnostics (task 370). Answers
// "stuck or slow?" for an unresponsive session from durable files only —
// read-only, never repairs, never appends.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/store"
	"reasonix/internal/turnevent"
)

// Responsiveness thresholds. They are presentation judgment, not protocol:
// the stream idle watchdog (provider defaultStreamIdleTimeout) force-recovers
// a silent started stream at 5m, so a live runtime keeps writing at least
// every ~5m while a turn runs. Silence past 6m therefore means something the
// ledger alone cannot classify: a stalled wait outside the stream, or a dead
// runtime.
const (
	// ResponsivenessSilenceAfter: last ledger record older than this on an
	// open turn reads as "silent", not "working".
	ResponsivenessSilenceAfter = 6 * time.Minute
	// ResponsivenessFirstResponseSoft: first response slower than this on a
	// filling window is slow-but-alive; the report says so instead of
	// implying failure.
	ResponsivenessFirstResponseSoft = 2 * time.Minute
)

// responsivenessActivityKinds are the ledger record kinds that prove model- or
// host-driven forward progress. turn_started/status-only records do not count:
// a queued turn that never gets its first token produces only those.
var responsivenessActivityKinds = map[string]bool{
	"reasoning": true, "text": true, "tool_dispatch": true, "tool_started": true,
	"tool_progress": true, "tool_result": true, "turn_phase": true,
	"compaction_started": true, "compaction_progress": true, "compaction_done": true,
	"usage": true,
}

// responsivenessResponseKinds are the record kinds that count as the turn's
// first response to the user (what the 13-minute incident measured).
var responsivenessResponseKinds = map[string]bool{
	"reasoning": true, "text": true, "tool_dispatch": true, "tool_started": true,
}

// ResponsivenessTurn summarizes one turn found in the retained ledger records.
type ResponsivenessTurn struct {
	TurnID          string `json:"turnId"`
	Status          string `json:"status"`
	Open            bool   `json:"open"`
	StartedAt       string `json:"startedAt,omitempty"`
	LastEventAt     string `json:"lastEventAt,omitempty"`
	FinishedAt      string `json:"finishedAt,omitempty"`
	Records         int    `json:"records"`
	FirstResponseMs int64  `json:"firstResponseMs,omitempty"`
	FirstActivityMs int64  `json:"firstActivityMs,omitempty"`
	LatestPrompt    int    `json:"latestPromptTokens,omitempty"`
	DurationMs      int64  `json:"durationMs,omitempty"` // summary sidecar only
	Outcome         string `json:"outcome,omitempty"`    // summary sidecar only
	firstResponseAt time.Time
	firstActivityAt time.Time
	startedAt       time.Time
	lastEventAt     time.Time
	finishedAt      time.Time
}

// ResponsivenessReport is the content-free answer to "stuck or slow?".
type ResponsivenessReport struct {
	SessionPath    string               `json:"sessionPath"` // redacted for display
	LedgerPath     string               `json:"ledgerPath"`  // redacted for display
	LedgerExists   bool                 `json:"ledgerExists"`
	LedgerSize     int64                `json:"ledgerSizeBytes"`
	LedgerModAt    string               `json:"ledgerModifiedAt,omitempty"`
	LedgerModAgeMs int64                `json:"ledgerModifiedAgeMs,omitempty"`
	SessionModAt   string               `json:"sessionModifiedAt,omitempty"`
	SessionModAge  int64                `json:"sessionModifiedAgeMs,omitempty"`
	EventsModAt    string               `json:"eventsLogModifiedAt,omitempty"`
	EventsModAge   int64                `json:"eventsLogModifiedAgeMs,omitempty"`
	TornTail       bool                 `json:"tornTail"`
	LatestSequence uint64               `json:"latestSequence"`
	Active         *ResponsivenessTurn  `json:"activeTurn,omitempty"`
	Recent         []ResponsivenessTurn `json:"recentTurns,omitempty"`
	LatestPrompt   int                  `json:"latestPromptTokens,omitempty"`
	Verdict        string               `json:"verdict"`
	Detail         string               `json:"detail"`
	CollectedAt    string               `json:"collectedAt"`
}

// CollectResponsiveness resolves the session reference and reads its turn
// ledger read-only. It never writes: a diagnostic must not repair the state
// it is measuring (the runtime repairs on next open). A .turns.jsonl path is
// accepted directly — the stuck-session workflow usually starts from
// whichever file the user found first, and an orphaned ledger (primary
// transcript already gone) is itself a diagnostic subject.
func CollectResponsiveness(sessionRef string, now time.Time) (ResponsivenessReport, error) {
	path, err := ResolveSessionRef(sessionRef)
	if err != nil {
		return ResponsivenessReport{}, err
	}
	if trimmed := strings.TrimSuffix(path, ".turns.jsonl"); trimmed != path {
		return readResponsiveness(trimmed+".jsonl", path, now), nil
	}
	return readResponsiveness(path, store.SessionTurnEventLog(path), now), nil
}

func readResponsiveness(sessionPath, ledgerPath string, now time.Time) ResponsivenessReport {
	report := ResponsivenessReport{
		SessionPath: redactSessionBundlePath(sessionPath),
		LedgerPath:  redactSessionBundlePath(ledgerPath),
		CollectedAt: now.UTC().Format(time.RFC3339),
		Verdict:     "unknown",
	}
	stamp := func(path string) (modAt string, ageMs int64, size int64) {
		info, err := os.Stat(path)
		if err != nil {
			return "", 0, 0
		}
		return info.ModTime().UTC().Format(time.RFC3339), now.Sub(info.ModTime()).Milliseconds(), info.Size()
	}
	report.LedgerModAt, report.LedgerModAgeMs, report.LedgerSize = stamp(ledgerPath)
	report.LedgerExists = report.LedgerModAt != ""
	report.SessionModAt, report.SessionModAge, _ = stamp(sessionPath)
	report.EventsModAt, report.EventsModAge, _ = stamp(store.SessionEventLog(sessionPath))
	if !report.LedgerExists {
		report.Verdict = "no_ledger"
		if report.SessionModAt == "" {
			report.Detail = "neither the session file nor a turn ledger exists at the resolved path — check the reference"
		} else {
			report.Detail = "turn ledger not found; this runtime never recorded a turn here (legacy session or ledger removed) — compare the session/events log ages above"
		}
		return report
	}
	records, torn := readTurnLedger(ledgerPath)
	report.TornTail = torn
	turns := summarizeTurns(records, &report)
	if len(turns) == 0 {
		report.Verdict = "no_turns"
		report.Detail = "ledger exists but holds no turn records yet"
		return report
	}
	assignVerdict(&report, turns, now)
	return report
}

// readTurnLedger parses the retained records of a turn ledger without
// repairing it. It tolerates both schemas and a torn tail (stops at the first
// bad line and reports it); the runtime performs the actual repair on open.
func readTurnLedger(path string) (records []turnevent.Envelope, torn bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var header struct {
			SchemaVersion int    `json:"schemaVersion"`
			RecordType    string `json:"recordType"`
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			return records, true
		}
		switch {
		case header.SchemaVersion == 1:
			var rec turnevent.Envelope
			if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.TurnID == "" {
				return records, true
			}
			records = append(records, rec)
		case header.RecordType == "event":
			var rec struct {
				turnevent.Envelope
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.TurnID == "" {
				return records, true
			}
			records = append(records, rec.Envelope)
		case header.RecordType == "checkpoint" || header.RecordType == "projection_ack":
			// Retention bookkeeping: carries no per-event timeline. Older
			// events were compacted away; summaries carry their durations.
			continue
		default:
			return records, true
		}
	}
	return records, false
}

// summarizeTurns folds records into per-turn summaries in first-appearance
// order and fills the report's latest-prompt token gauge.
func summarizeTurns(records []turnevent.Envelope, report *ResponsivenessReport) []ResponsivenessTurn {
	var order []string
	byTurn := map[string]*ResponsivenessTurn{}
	for _, rec := range records {
		if rec.TurnID == "" {
			continue
		}
		if rec.Sequence > report.LatestSequence {
			report.LatestSequence = rec.Sequence
		}
		t := byTurn[rec.TurnID]
		if t == nil {
			t = &ResponsivenessTurn{TurnID: rec.TurnID, startedAt: time.UnixMilli(rec.CreatedAt)}
			byTurn[rec.TurnID] = t
			order = append(order, rec.TurnID)
		}
		t.Records++
		t.Status = string(rec.Status)
		t.lastEventAt = time.UnixMilli(rec.CreatedAt)
		// The envelope's Kind is the stable wire name written at append time
		// (eventwire.KindName), so no reverse mapping is needed here.
		kind := rec.Kind
		at := time.UnixMilli(rec.CreatedAt)
		if responsivenessActivityKinds[kind] {
			if t.firstActivityAt.IsZero() {
				t.firstActivityAt = at
			}
		}
		if responsivenessResponseKinds[kind] && t.firstResponseAt.IsZero() {
			t.firstResponseAt = at
		}
		if rec.Event.Usage != nil {
			prompt := rec.Event.Usage.ContextPromptTokens
			if prompt <= 0 {
				prompt = rec.Event.Usage.PromptTokens
			}
			if prompt > 0 {
				t.LatestPrompt = prompt
				report.LatestPrompt = prompt
			}
		}
		if rec.Status.Terminal() {
			t.Open = false
			t.finishedAt = at
		} else if !t.finishedAt.IsZero() {
			// Records after a terminal event mean the turn resumed (schema-1
			// legacy reopen); trust the newest non-terminal status.
			t.Open = true
			t.finishedAt = time.Time{}
		} else {
			t.Open = true
		}
	}
	out := make([]ResponsivenessTurn, 0, len(order))
	for _, id := range order {
		t := byTurn[id]
		t.fillDerived()
		out = append(out, *t)
	}
	return out
}

func (t *ResponsivenessTurn) fillDerived() {
	t.StartedAt = t.startedAt.UTC().Format(time.RFC3339)
	t.LastEventAt = t.lastEventAt.UTC().Format(time.RFC3339)
	if !t.finishedAt.IsZero() {
		t.FinishedAt = t.finishedAt.UTC().Format(time.RFC3339)
		t.DurationMs = t.finishedAt.Sub(t.startedAt).Milliseconds()
	}
	if !t.firstResponseAt.IsZero() {
		t.FirstResponseMs = t.firstResponseAt.Sub(t.startedAt).Milliseconds()
	}
	if !t.firstActivityAt.IsZero() {
		t.FirstActivityMs = t.firstActivityAt.Sub(t.startedAt).Milliseconds()
	}
}

// assignVerdict classifies the session state. The verdict names the state,
// the detail names the next action — in the incident this automates, the
// three manual criteria (last write / open turn / recovery after silence)
// become one read.
func assignVerdict(report *ResponsivenessReport, turns []ResponsivenessTurn, now time.Time) {
	// Active turn = the newest still-open turn (records are per actor lane, so
	// at most one is truly in flight; older open turns are crash remnants).
	var active *ResponsivenessTurn
	for i := range turns {
		if turns[i].Open {
			active = &turns[i]
		}
	}
	if active != nil {
		report.Active = active
	}
	// Recent completed turns keep the first-response history the incident
	// had to reconstruct by hand; cap the tail to keep the report short.
	const recentCap = 5
	for i := len(turns) - 1; i >= 0 && len(report.Recent) < recentCap; i-- {
		if !turns[i].Open {
			report.Recent = append(report.Recent, turns[i])
		}
	}
	if active == nil {
		last := turns[len(turns)-1]
		report.Verdict = "idle"
		report.Detail = fmt.Sprintf("no open turn; last turn %s, last ledger activity %s ago — the session is not stuck",
			shortStatus(last.Status), relativeAge(now.Sub(last.lastEventAt)))
		return
	}
	if event.TurnStatus(active.Status) == event.TurnWaitingUser {
		report.Verdict = "waiting_user"
		report.Detail = fmt.Sprintf("turn is open and waiting for you (approval/ask pending since %s) — answer the prompt; not stuck",
			relativeAge(now.Sub(active.lastEventAt)))
		return
	}
	if active.FirstResponseMs == 0 {
		age := now.Sub(active.startedAt)
		report.Verdict = "first_response_wait"
		report.Detail = fmt.Sprintf("turn open %s, no first response yet — this is the prefill/thinking phase; a filling window can legitimately spend minutes here before the first token. Not stuck by itself: re-run with --watch (or re-run this command) and check whether the sequence advances",
			relativeAge(age))
		if report.LatestPrompt > 0 {
			report.Detail += fmt.Sprintf("; latest prompt size %dk tokens", report.LatestPrompt/1000)
		}
		if age >= ResponsivenessFirstResponseSoft {
			report.Detail += "; past the soft expectation — still consistent with a long-context first turn"
		}
		return
	}
	lastAge := now.Sub(active.lastEventAt)
	if lastAge >= ResponsivenessSilenceAfter {
		report.Verdict = "silent"
		report.Detail = fmt.Sprintf("turn open, but the ledger has been silent for %s (first response landed after %s) — past the stream watchdog window: either a wait outside the stream, or the runtime died mid-turn. Re-run with --watch: advancing sequence means alive; no progress plus a dead reasonix process means crash remnant (recovered on next open)",
			relativeAge(lastAge), formatMs(active.FirstResponseMs))
		return
	}
	report.Verdict = "working"
	report.Detail = fmt.Sprintf("turn open and streaming — last ledger write %s ago (first response took %s). Alive, not stuck",
		relativeAge(lastAge), formatMs(active.FirstResponseMs))
}

func shortStatus(s string) string {
	if s == "" {
		return "unknown status"
	}
	return s
}

func relativeAge(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func formatMs(ms int64) string {
	if ms <= 0 {
		return "n/a"
	}
	return relativeAge(time.Duration(ms) * time.Millisecond)
}

// RenderResponsivenessText renders the human report. English, matching the
// other doctor subcommands.
func RenderResponsivenessText(r ResponsivenessReport, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reasonix session responsiveness\n")
	fmt.Fprintf(&b, "  session: %s\n", r.SessionPath)
	fmt.Fprintf(&b, "  verdict: %s\n", r.Verdict)
	fmt.Fprintf(&b, "  %s\n", r.Detail)
	fmt.Fprintf(&b, "  criterion 1 — last write:\n")
	fmt.Fprintf(&b, "    turns ledger: %s\n", describeStamp(r.LedgerExists, r.LedgerModAt, r.LedgerModAgeMs, r.LedgerSize))
	fmt.Fprintf(&b, "    session file: %s\n", describeStamp(r.SessionModAt != "", r.SessionModAt, r.SessionModAge, 0))
	fmt.Fprintf(&b, "    events log:   %s\n", describeStamp(r.EventsModAt != "", r.EventsModAt, r.EventsModAge, 0))
	fmt.Fprintf(&b, "  criterion 2 — turn state:\n")
	switch {
	case r.Active != nil:
		a := r.Active
		fmt.Fprintf(&b, "    open turn %s status=%s started %s ago, %d records, first response %s\n",
			short(a.TurnID), a.Status, relativeAge(now.Sub(a.startedAt)), a.Records, formatMs(a.FirstResponseMs))
	case r.Verdict == "idle":
		fmt.Fprintf(&b, "    no open turn (last turn closed normally)\n")
	default:
		fmt.Fprintf(&b, "    no open turn information available\n")
	}
	fmt.Fprintf(&b, "  criterion 3 — first-response history (retained records):\n")
	if len(r.Recent) == 0 {
		fmt.Fprintf(&b, "    no completed turns retained (compacted or none yet)\n")
	}
	for _, t := range r.Recent {
		fmt.Fprintf(&b, "    %s first response %s, duration %s, ended %s ago\n",
			short(t.TurnID), formatMs(t.FirstResponseMs), formatMs(t.DurationMs), relativeAge(now.Sub(t.lastEventAt)))
	}
	if r.LatestPrompt > 0 {
		fmt.Fprintf(&b, "  latest prompt size: %dk tokens\n", r.LatestPrompt/1000)
	}
	if r.TornTail {
		fmt.Fprintf(&b, "  note: ledger tail is torn; the runtime repairs it on next open (diagnostic read only)\n")
	}
	if r.Verdict == "first_response_wait" || r.Verdict == "silent" {
		fmt.Fprintf(&b, "  next: re-run with --watch <duration> to see whether the ledger advances\n")
	}
	return b.String()
}

func describeStamp(exists bool, modAt string, ageMs, size int64) string {
	if !exists {
		return "missing"
	}
	out := fmt.Sprintf("%s (%s ago", modAt, relativeAge(time.Duration(ageMs)*time.Millisecond))
	if size > 0 {
		out += fmt.Sprintf(", %dkB", size/1024)
	}
	return out + ")"
}

func short(turnID string) string {
	if len(turnID) > 14 {
		return turnID[:14] + "…"
	}
	return turnID
}
