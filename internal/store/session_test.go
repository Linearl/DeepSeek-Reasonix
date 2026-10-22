package store

import "testing"

func TestSessionSidecarLayout(t *testing.T) {
	const p = "/home/u/.reasonix/sessions/abc.jsonl"
	cases := []struct {
		name string
		got  string
		want string
	}{
		// .meta appends to the full path (historical layout); the rest replace .jsonl.
		{"meta", SessionMeta(p), p + ".meta"},
		{"goal-state", SessionGoalState(p), "/home/u/.reasonix/sessions/abc.goal-state.json"},
		{"event-log", SessionEventLog(p), "/home/u/.reasonix/sessions/abc.events.jsonl"},
		{"event-log-damaged", SessionEventLogDamaged(p), "/home/u/.reasonix/sessions/abc.events.jsonl.damaged"},
		{"turn-event-log", SessionTurnEventLog(p), "/home/u/.reasonix/sessions/abc.turns.jsonl"},
		{"turn-event-log-damaged", SessionTurnEventLogDamaged(p), "/home/u/.reasonix/sessions/abc.turns.jsonl.damaged"},
		{"event-index", SessionEventIndex(p), "/home/u/.reasonix/sessions/abc.event-index.json"},
		{"display-index", SessionDisplayIndex(p), "/home/u/.reasonix/sessions/abc.display-index.json"},
		{"conflict-log", SessionConflictLog(p), "/home/u/.reasonix/sessions/abc.conflicts.jsonl"},
		{"lock", SessionLockFile(p), p + ".lock"},
		{"lease-lock", SessionLeaseLock(p), p + ".lease.lock"},
		{"lease-info", SessionLeaseInfo(p), p + ".lease.json"},
		{"checkpoint", SessionCheckpointDir(p), "/home/u/.reasonix/sessions/abc.ckpt"},
		{"jobs", SessionJobsDir(p), "/home/u/.reasonix/sessions/abc.jobs"},
		{"inbox", SessionInboxDir(p), "/home/u/.reasonix/sessions/abc.inbox"},
		{"cleanup-pending", SessionCleanupPending(p), "/home/u/.reasonix/sessions/abc.cleanup-pending.json"},
		{"context", SessionContext(p), "/home/u/.reasonix/sessions/abc.context.json"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestSessionSidecarEmptyPath(t *testing.T) {
	for _, fn := range []struct {
		name string
		f    func(string) string
	}{
		{"meta", SessionMeta},
		{"goal-state", SessionGoalState},
		{"event-log", SessionEventLog},
		{"event-log-damaged", SessionEventLogDamaged},
		{"turn-event-log", SessionTurnEventLog},
		{"turn-event-log-damaged", SessionTurnEventLogDamaged},
		{"event-index", SessionEventIndex},
		{"display-index", SessionDisplayIndex},
		{"conflict-log", SessionConflictLog},
		{"lock", SessionLockFile},
		{"lease-lock", SessionLeaseLock},
		{"lease-info", SessionLeaseInfo},
		{"checkpoint", SessionCheckpointDir},
		{"jobs", SessionJobsDir},
		{"inbox", SessionInboxDir},
		{"cleanup-pending", SessionCleanupPending},
		{"context", SessionContext},
	} {
		if got := fn.f(""); got != "" {
			t.Errorf("%s(\"\") = %q, want empty", fn.name, got)
		}
	}
}

func TestIsSessionTranscriptName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"session.jsonl", true},
		{"session.events.jsonl", false},
		{"session.turns.jsonl", false},
		{"session.conflicts.jsonl", false},
		{"session.repairlog.jsonl", false},
		{"session.guardian.jsonl", false},
		{"session.guardian.events.jsonl", false},
		{"session.events.jsonl.damaged", false},
		{"session.jsonl.meta", false},
		{"notes.txt", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsSessionTranscriptName(c.name); got != c.want {
			t.Errorf("IsSessionTranscriptName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestIsSessionTranscriptNameRepairLogNoSidecar (task 1071) pins the
// classification that caused the original bug: the self-heal repair log
// (<stem>.repairlog.jsonl) must never classify as a transcript, so no
// downstream scan (desktop health, catalog reconcile, GC) ever derives
// display-index/meta/lock sidecars beside it, or rewrites it through the
// transcript save path.
func TestIsSessionTranscriptNameRepairLogNoSidecar(t *testing.T) {
	// A real repair-log name must not classify as a transcript.
	for _, name := range []string{
		"abc.repairlog.jsonl",
		"20260831-035608.215282300-deepseek-deepseek-v4-flash-vision-exp.repairlog.jsonl",
	} {
		if IsSessionTranscriptName(name) {
			t.Errorf("IsSessionTranscriptName(%q) = true, must be false — a repair log is a sidecar, not a transcript", name)
		}
	}

	// SessionSidecarFiles must list the repair log as a sidecar (so deleting
	// the session removes it) and must never derive a nested "transcript" name
	// from the repair log itself — nothing downstream can mistake the repair
	// log's own name or its derived sidecars for a session.
	const transcript = "/home/u/.reasonix/sessions/abc.jsonl"
	sawRepairLog := false
	for _, sidecar := range SessionSidecarFiles(transcript) {
		if sidecar == SessionRepairLog(transcript) {
			sawRepairLog = true
		}
		if IsSessionTranscriptName(sidecar) {
			t.Errorf("SessionSidecarFiles derived %q, which still classifies as a transcript — a sidecar must never re-enter transcript classification", sidecar)
		}
	}
	if !sawRepairLog {
		t.Error("SessionSidecarFiles does not list the repair log — deleting a session would leak its repair history")
	}
}

func TestSessionSidecarFiles(t *testing.T) {
	const p = "/home/u/.reasonix/sessions/abc.jsonl"
	got := SessionSidecarFiles(p)
	want := []string{
		p + ".meta",
		"/home/u/.reasonix/sessions/abc.goal-state.json",
		"/home/u/.reasonix/sessions/abc.events.jsonl",
		"/home/u/.reasonix/sessions/abc.events.jsonl.damaged",
		"/home/u/.reasonix/sessions/abc.events.jsonl.rotating",
		"/home/u/.reasonix/sessions/abc.turns.jsonl",
		"/home/u/.reasonix/sessions/abc.turns.jsonl.damaged",
		"/home/u/.reasonix/sessions/abc.event-index.json",
		"/home/u/.reasonix/sessions/abc.display-index.json",
		"/home/u/.reasonix/sessions/abc.conflicts.jsonl",
		"/home/u/.reasonix/sessions/abc.repairlog.jsonl",
		"/home/u/.reasonix/sessions/abc.recovery.json",
		"/home/u/.reasonix/sessions/abc.context.json",
		"/home/u/.reasonix/sessions/abc.pinned-context.json",
	}
	if len(got) != len(want) {
		t.Fatalf("SessionSidecarFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SessionSidecarFiles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if SessionSidecarFiles("") != nil {
		t.Error("SessionSidecarFiles(\"\") should be nil")
	}
}
