package runjournal

// Projections: pure derivations over journal rows (task 419 design §5,
// borrowed from zcode's roster discipline). No I/O, no clock, no store —
// every input including `now` is an argument, so the rules (status-word
// precedence, absence readings, time clamping) are pinned by hand-built row
// tests instead of expensive database fixtures.

// progressEventKinds are the trail entries that count as "the run is still
// moving" (zcode's PROGRESS_EVENT_TYPES, cut to our five kinds). The two
// boundary kinds — run_created and run_ended — are phase transitions, not
// progress: counting run_created would make a freshly abandoned run look
// live forever, counting run_ended would make the last event's timestamp the
// end of time itself.
var progressEventKinds = map[string]bool{
	EventMemberDispatched: true,
	EventMemberSettled:    true,
	EventArtifactPublish:  true,
}

// terminalMemberStatuses are member states that can no longer change.
var terminalMemberStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"cancelled": true,
	"skipped":   true,
}

// RunStatusTerminal reports whether a run status is final.
func RunStatusTerminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// MemberStateView is one roster row for the UI.
type MemberStateView struct {
	Idx      int    `json:"idx"`
	MemberID string `json:"memberId"`
	Label    string `json:"label"`
	Profile  string `json:"profile"`
	Status   string `json:"status"`
	Ref      string `json:"ref"`
	Error    string `json:"error"`
	Preview  string `json:"preview"`
	// StartedAt/EndedAt are the member's dispatched/settled event times,
	// clamped to `now` (0 when the event never happened). Derived from the
	// trail — member rows carry no time columns by design.
	StartedAt int64 `json:"startedAt"`
	EndedAt   int64 `json:"endedAt"`
}

// RosterHealth is the run-level "is it still moving" summary.
type RosterHealth struct {
	Terminal bool `json:"terminal"`
	// LastProgressAt is the most recent progress-event time, clamped to now
	// (0 when the trail has no progress event yet).
	LastProgressAt int64 `json:"lastProgressAt"`
	// ConsecutiveFailures counts member_settled failures at the END of the
	// settlement order (a trailing streak predicts the next one; scattered
	// failures do not) — zcode's countTrailingFailures semantic.
	ConsecutiveFailures int `json:"consecutiveFailures"`
	// LeftoverRunning counts members still non-terminal AFTER the run went
	// terminal (a crashed writer's stale rows). Always 0 for a live run:
	// running members are the run doing its job, not a defect.
	LeftoverRunning int `json:"leftoverRunning"`
	// MembersTerminal / MembersTotal make the roster progress line.
	MembersTerminal int `json:"membersTerminal"`
	MembersTotal    int `json:"membersTotal"`
}

// RosterView is the derived situation slice: per-member state + health.
type RosterView struct {
	Members []MemberStateView `json:"members"`
	Health  RosterHealth      `json:"health"`
}

// BuildRoster derives the roster view from rows. `now` is the caller's read
// time and only ever clamps event times upward-bounded (an event stamped in
// the future by clock skew reads as now, never as a lie about the future).
func BuildRoster(run RunRow, members []MemberRow, events []EventRow, now int64) RosterView {
	terminal := RunStatusTerminal(run.Status)

	// Single sweep over the trail: first/last event time per member plus the
	// global progress tail. One pass — the read face gets more expensive as
	// runs grow, and paying it twice doubles the cost (zcode's index rule).
	type span struct{ start, end int64 }
	spans := make(map[int]*span, len(members))
	var lastProgress int64
	for _, e := range events {
		at := clampTime(e.At, now)
		if progressEventKinds[e.Kind] && at > lastProgress {
			lastProgress = at
		}
		if e.MemberIdx == 0 {
			continue
		}
		sp := spans[e.MemberIdx]
		if sp == nil {
			sp = &span{}
			spans[e.MemberIdx] = sp
		}
		switch e.Kind {
		case EventMemberDispatched:
			if sp.start == 0 {
				sp.start = at
			}
		case EventMemberSettled:
			sp.end = at
		}
	}

	views := make([]MemberStateView, 0, len(members))
	health := RosterHealth{
		Terminal:      terminal,
		LastProgressAt: lastProgress,
		MembersTotal:   len(members),
	}
	for _, m := range members {
		v := MemberStateView{
			Idx: m.Idx, MemberID: m.MemberID, Label: m.Label, Profile: m.Profile,
			Status: m.Status, Ref: m.Ref, Error: m.Error, Preview: m.Preview,
		}
		if sp := spans[m.Idx]; sp != nil {
			v.StartedAt = sp.start
			v.EndedAt = sp.end
		}
		if terminalMemberStatuses[m.Status] {
			health.MembersTerminal++
		}
		if terminal && !terminalMemberStatuses[m.Status] {
			health.LeftoverRunning++
		}
		views = append(views, v)
	}

	// Trailing failure streak: walk settlements in trail order, count the
	// contiguous failed tail (cancelled/skipped break the streak — they are
	// not the subagent going down).
	failed := map[int]bool{}
	settledIdx := []int{}
	for _, e := range events {
		if e.Kind != EventMemberSettled || e.MemberIdx == 0 {
			continue
		}
		if strings_HasPrefix(e.Detail, "failed") {
			failed[e.MemberIdx] = true
		}
		settledIdx = append(settledIdx, e.MemberIdx)
	}
	for i := len(settledIdx) - 1; i >= 0; i-- {
		if failed[settledIdx[i]] {
			health.ConsecutiveFailures++
			continue
		}
		break
	}
	return RosterView{Members: views, Health: health}
}

// TimelineEntry is one line of the time-ordered view (design §7: the UI
// renders this directly).
type TimelineEntry struct {
	Seq         int64  `json:"seq"`
	At          int64  `json:"at"`
	Kind        string `json:"kind"`
	MemberIdx   int    `json:"memberIdx"`
	MemberLabel string `json:"memberLabel"`
	Status      string `json:"status"` // settled member status ("" for other kinds)
	Detail      string `json:"detail"`
}

// BuildTimeline derives the time-ordered entries. Events are already seq-
// ordered from the query; this enriches member labels from the roster and
// extracts the settled status from the detail head so the UI never parses
// detail strings itself.
func BuildTimeline(members []MemberRow, events []EventRow) []TimelineEntry {
	labels := make(map[int]string, len(members))
	for _, m := range members {
		labels[m.Idx] = m.Label
	}
	out := make([]TimelineEntry, 0, len(events))
	for _, e := range events {
		entry := TimelineEntry{
			Seq: e.Seq, At: e.At, Kind: e.Kind,
			MemberIdx: e.MemberIdx, Detail: e.Detail,
		}
		if e.MemberIdx != 0 {
			entry.MemberLabel = labels[e.MemberIdx]
		}
		if e.Kind == EventMemberSettled {
			entry.Status, _ = splitHead(e.Detail)
		}
		out = append(out, entry)
	}
	return out
}

// ArtifactView is the merged, UI-ready artifact (borrow 4: latest version's
// fields lifted to the top, primary rows first, completed only).
type ArtifactView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Ref         string `json:"ref"`
	MemberIdx   int    `json:"memberIdx"`
	Version     int    `json:"version"`
	Primary     bool   `json:"primary"`
	PublishedAt int64  `json:"publishedAt"`
}

// ArtifactsOf merges artifact rows by id: version ascending, the highest
// version's title/ref lifted to the view, primary sticky across versions
// (any version carrying the flag makes the id primary — old writers may omit
// it). Input order is preserved for non-primary rows (first-publish order);
// primaries are stably hoisted to the front — one sort, done here, so no
// caller re-sorts differently.
func ArtifactsOf(rows []ArtifactRow) []ArtifactView {
	type bucket struct {
		kind     string
		member   int
		versions []ArtifactRow
	}
	byID := map[string]*bucket{}
	order := []string{}
	for _, r := range rows {
		if r.Status != "completed" {
			continue // defensive: writes already filter, reads must too
		}
		b := byID[r.ID]
		if b == nil {
			b = &bucket{kind: r.Kind, member: r.MemberIdx}
			byID[r.ID] = b
			order = append(order, r.ID)
		}
		b.versions = append(b.versions, r)
	}
	views := make([]ArtifactView, 0, len(order))
	primaries := []ArtifactView{}
	for _, id := range order {
		b := byID[id]
		// versions arrive ascending from the query; keep a defensive sort so
		// hand-built test rows behave the same as stored ones.
		latest := b.versions[0]
		primary := false
		for _, v := range b.versions {
			if v.Version > latest.Version {
				latest = v
			}
			if v.Primary {
				primary = true
			}
		}
		view := ArtifactView{
			ID: id, Kind: b.kind, Title: latest.Title, Ref: latest.Ref,
			MemberIdx: b.member, Version: latest.Version, Primary: primary,
			PublishedAt: latest.PublishedAt,
		}
		if primary {
			primaries = append(primaries, view)
		} else {
			views = append(views, view)
		}
	}
	return append(primaries, views...)
}

// clampTime bounds a trail timestamp to the read time (clock skew reads as
// "now", never as a future event).
func clampTime(at, now int64) int64 {
	if at <= 0 {
		return 0
	}
	if now > 0 && at > now {
		return now
	}
	return at
}

// strings_HasPrefix avoids an import just for one call in a hot loop file;
// detail heads are always lowercase status words written by Settled.
func strings_HasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// splitHead returns the first space-separated token and the remainder.
func splitHead(s string) (head, rest string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
