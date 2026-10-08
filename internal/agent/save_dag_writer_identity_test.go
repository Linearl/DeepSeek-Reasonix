package agent

import (
	"os"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// buildLogWithoutWriterIdentity writes a schema-2 log whose message entries
// carry this process's writer id (encodeSessionDAGEntries stamps the default)
// but no writer identity entry, so the replayed registry record has pid 0 —
// the state a log written by a previous app incarnation, or by a build
// predating the identity stamps, leaves behind (task 646).
func buildLogWithoutWriterIdentity(t *testing.T) (string, []provider.Message) {
	t.Helper()
	path := dagTestSession(t)
	now := time.Now().UTC()
	msgs := []provider.Message{
		dagMsg(provider.RoleUser, "q1", "id-q1"),
		dagMsg(provider.RoleAssistant, "a1", "id-a1"),
	}
	entries := []sessionDAGEntry{
		{Type: sessionDAGTypeLog, At: now, Generation: 1, UpgradedFrom: sessionEventSchemaVersion},
	}
	parent, digest := "", ""
	for _, m := range msgs {
		raw, err := encodeSessionDAGMessage(m)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		d, err := sessionDAGChainDigest(digest, m)
		if err != nil {
			t.Fatalf("digest: %v", err)
		}
		entries = append(entries, sessionDAGEntry{Type: sessionDAGTypeMessage, ID: m.ID, Head: SessionMainHead, Parent: parent, Digest: d, Msgs: raw, At: now})
		parent, digest = m.ID, d
	}
	dagAppend(t, path, entries...)
	return path, msgs
}

// TestDAGSaveSelfRegistersWriterIdentity pins task 646's root-cause fix: the
// first save on a log whose writer registry lacks this process's pid appends
// a writer identity entry (and updates the live registry), so in-process
// forks stop classifying unknown and alarming the user about another window.
func TestDAGSaveSelfRegistersWriterIdentity(t *testing.T) {
	path, _ := buildLogWithoutWriterIdentity(t)
	before := dagReplay(t, path)
	w := before.writers[SessionWriterID()]
	if w == nil || w.pid != 0 {
		t.Fatalf("precondition: writer record = %+v, want pid 0", w)
	}
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q2"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, typ := range dagEntryTypes(t, path) {
		if typ == sessionDAGTypeWriter {
			found = true
		}
	}
	if !found {
		t.Fatal("save did not append a writer identity entry")
	}
	st := dagReplay(t, path)
	w = st.writers[SessionWriterID()]
	if w == nil || w.pid != os.Getpid() || w.hostname == "" {
		t.Fatalf("writer record after save = %+v, want this pid and a hostname", w)
	}
	// The registry entry must survive a rotation re-emitting the writer table.
	host, _ := os.Hostname()
	if w.hostname != host {
		t.Fatalf("hostname = %q, want %q", w.hostname, host)
	}
}

// TestDAGSaveInProcessForkAttributesLocalAfterSelfRegistration pins the field
// misattribution (2026-10-08): two Session objects on one log whose registry
// lacked pids — the single-instance concurrent-writer warning — must classify
// local now that the first save registers the writer identity.
func TestDAGSaveInProcessForkAttributesLocalAfterSelfRegistration(t *testing.T) {
	path, _ := buildLogWithoutWriterIdentity(t)
	b, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Add(provider.Message{Role: provider.RoleUser, Content: "q2-from-a"})
	if err := a.Save(path); err != nil {
		t.Fatal(err)
	}
	b.Add(provider.Message{Role: provider.RoleUser, Content: "q2-from-b"})
	if err := b.Save(path); err != nil {
		t.Fatalf("second writer must not conflict: %v", err)
	}
	events := b.DrainHeadEvents()
	if len(events) != 1 || events[0].Kind != HeadEventForkedConcurrent {
		t.Fatalf("events = %+v", events)
	}
	ev := events[0]
	if ev.Class != HeadDivergenceLocal {
		t.Fatalf("fork class = %q (reason %q), want local after self-registration", ev.Class, ev.UnknownReason)
	}
	if ev.OtherWriter != SessionWriterID() || ev.OtherPID != os.Getpid() {
		t.Fatalf("identity = writer %q pid %d, want %q pid %d", ev.OtherWriter, ev.OtherPID, SessionWriterID(), os.Getpid())
	}
	if ev.UnknownReason != "" {
		t.Fatalf("unknown_reason = %q, want empty for a classified fork", ev.UnknownReason)
	}
}
