package agent

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1430: head_switch is an appended marker, never a rewrite. Replay must
// skip it without touching the transcript or the revision baseline, a damaged
// marker (missing from/to) must degrade the log instead of corrupting replay,
// and appends after the marker must keep working on the same log.
func TestHeadSwitchEventReplayKeepsTranscriptAndBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	session := NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	logPath := store.SessionEventLog(path)
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	firstReplay, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstReplay.msgs) != 3 || firstReplay.damaged {
		t.Fatalf("baseline replay = %d messages damaged=%v, want 3 healthy", len(firstReplay.msgs), firstReplay.damaged)
	}

	if err := AppendSessionHeadSwitchEvent(path, filepath.Join(dir, "old.jsonl"), path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) <= len(before) || string(after[:len(before)]) != string(before) {
		t.Fatal("the switch marker rewrote or reordered prior event bytes")
	}

	markerReplay, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if markerReplay.damaged {
		t.Fatal("replay reported the log damaged after a well-formed head_switch marker")
	}
	if len(markerReplay.msgs) != len(firstReplay.msgs) {
		t.Fatalf("marker changed the transcript: %d -> %d messages", len(firstReplay.msgs), len(markerReplay.msgs))
	}
	for i := range markerReplay.msgs {
		if markerReplay.msgs[i].Content != firstReplay.msgs[i].Content {
			t.Fatalf("marker changed message %d: %q -> %q", i, firstReplay.msgs[i].Content, markerReplay.msgs[i].Content)
		}
	}
	if markerReplay.lastRevision != firstReplay.lastRevision {
		t.Fatalf("marker bumped the revision baseline: %d -> %d", firstReplay.lastRevision, markerReplay.lastRevision)
	}
	if markerReplay.records != firstReplay.records+1 {
		t.Fatalf("marker record count = %d, want baseline %d + 1", markerReplay.records, firstReplay.records)
	}

	// The projection on disk (the transcript file) is a replay of the log;
	// appending must keep working after the marker with the same baseline.
	appended := append(append([]provider.Message{}, markerReplay.msgs...), provider.Message{Role: provider.RoleUser, Content: "q2"})
	digest, err := digestSessionMessages(appended)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionAppendEvent(path, len(markerReplay.msgs), []provider.Message{{Role: provider.RoleUser, Content: "q2"}}, digest, markerReplay.lastRevision); err != nil {
		t.Fatal(err)
	}
	finalReplay, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if finalReplay.damaged {
		t.Fatal("append after the marker left the log damaged")
	}
	if len(finalReplay.msgs) != len(firstReplay.msgs)+1 || finalReplay.msgs[len(finalReplay.msgs)-1].Content != "q2" {
		t.Fatalf("post-marker append replay = %d messages, want baseline + q2", len(finalReplay.msgs))
	}
}

// A marker without both sides is a damaged record: replay degrades (stops,
// keeps prior messages) instead of treating it as a healthy marker.
func TestHeadSwitchEventReplayRejectsIncompleteMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	session := NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	logPath := store.SessionEventLog(path)
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionEvent(path, sessionEventRecord{Type: sessionEventTypeHeadSwitch, HeadFrom: "only-from"}, true); err != nil {
		t.Fatal(err)
	}
	replay, err := replaySessionEventLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.damaged {
		t.Fatal("an incomplete head_switch marker replayed as healthy")
	}
	if len(replay.msgs) != 2 {
		t.Fatalf("damaged marker replay kept %d messages, want the pre-marker transcript", len(replay.msgs))
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after[:len(before)]) != string(before) {
		t.Fatal("replay of a damaged marker rewrote prior event bytes")
	}
}
