package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// tl1317 P1: a store that already holds an event whose payload is not valid
// JSON (written before the write-side gate) used to fail the whole session
// open — the stuck-session wedge. Replay must skip such events, isolate them
// into the session-dir sidecar, and keep sequences and every healthy event
// intact.
func TestOpenSkipsDamagedPayloadEvent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	s, err := OpenWithOptions(dir, "s", OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	generation := s.Handle().(*Store).Manifest().WriterGeneration
	if _, err := s.Append(t.Context(), Batch{OperationID: "good", Events: []Event{
		{Kind: "turn/start", Payload: []byte(`{"turnId":"t1"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Craft the legacy damage directly in the log bytes, bypassing the
	// write-side gate, exactly like a store written before the gate existed.
	damaged := Commit{
		SchemaVersion: SchemaVersion, Codec: Codec, RecordType: "commit", ID: "damaged-commit",
		OperationID: "damaged-operation", OperationHash: "damaged-hash", FirstSequence: 2,
		EventCount: 2, WriterGeneration: generation, CreatedAt: time.Now().UTC(),
		Events: []Event{
			{ID: "damaged-plan", Sequence: 2, Kind: "plan/state", Payload: []byte(`{{"plan":1}}`)},
			{ID: "damaged-message", Sequence: 3, Kind: "message/complete", Payload: []byte(`{{"message":1}}`)},
		},
	}
	path := filepath.Join(dir, currentLogName)
	logBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if _, err := encodeV4Commits(t.Context(), &encoded, contentStoreForSessionDir(dir), []Commit{damaged}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte{}, logBefore...), []byte(encoded.String())...), 0600); err != nil {
		t.Fatal(err)
	}

	// The open must succeed and quarantine, not fail with ErrDamagedStore.
	reopened, err := Open(dir, "s")
	if err != nil {
		t.Fatalf("open with damaged payload event: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })

	snapshot := reopened.Snapshot()
	if snapshot.DurableSequence != 3 {
		t.Fatalf("durable sequence = %d, want 3 (quarantine must not rewind sequences)", snapshot.DurableSequence)
	}
	if snapshot.Projection.PlanState != nil {
		t.Fatalf("damaged plan/state leaked into the projection: %s", snapshot.Projection.PlanState)
	}
	if len(snapshot.Projection.Messages) != 0 {
		t.Fatalf("damaged message leaked into the projection: %+v", snapshot.Projection.Messages)
	}

	// The sidecar records both quarantined events with their exact bytes.
	sidecar, err := os.ReadFile(filepath.Join(dir, ".damaged-events.jsonl"))
	if err != nil {
		t.Fatalf("damaged events sidecar missing: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(string(sidecar)), "\n"); len(lines) != 2 {
		t.Fatalf("sidecar lines = %d, want 2: %s", len(lines), sidecar)
	}
	for _, want := range []string{
		`"kind":"plan/state"`, `"seq":2`,
		`"kind":"message/complete"`, `"seq":3`,
		`"payloadBase64":"` + base64.StdEncoding.EncodeToString([]byte(`{{"plan":1}}`)) + `"`,
	} {
		if !strings.Contains(string(sidecar), want) {
			t.Fatalf("sidecar missing %s: %s", want, sidecar)
		}
	}

	// The source log is never rewritten: the quarantined bytes stay in place.
	logAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(logAfter) != string(append(append([]byte{}, logBefore...), []byte(encoded.String())...)) {
		t.Fatal("replay rewrote the source log")
	}

	// Cold page reads skip the damaged events but keep commit metadata.
	page, err := readCommitPage(t.Context(), dir, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Commits) != 2 {
		t.Fatalf("cold page commits = %d, want 2", len(page.Commits))
	}
	if len(page.Commits[0].Events) != 1 {
		t.Fatalf("healthy commit events = %d, want 1", len(page.Commits[0].Events))
	}
	if len(page.Commits[1].Events) != 0 {
		t.Fatalf("damaged events were not filtered from the cold page: %+v", page.Commits[1].Events)
	}
	if page.Commits[1].LastSequence() != 3 {
		t.Fatalf("cold page commit metadata changed: %+v", page.Commits[1])
	}
}

// tl1317 P1: the bounded (external-history) open path must quarantine through
// the business, recent and model passes as well.
func TestOpenBoundedPathSkipsDamagedPayloadEvent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	s, err := OpenWithOptions(dir, "s", OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	generation := s.Handle().(*Store).Manifest().WriterGeneration
	if _, err := s.Append(t.Context(), Batch{OperationID: "good", Events: []Event{
		{Kind: "turn/start", Payload: []byte(`{"turnId":"t1"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	damaged := Commit{
		SchemaVersion: SchemaVersion, Codec: Codec, RecordType: "commit", ID: "damaged-commit",
		OperationID: "damaged-operation", OperationHash: "damaged-hash", FirstSequence: 2,
		EventCount: 2, WriterGeneration: generation, CreatedAt: time.Now().UTC(),
		Events: []Event{
			{ID: "damaged-plan", Sequence: 2, Kind: "plan/state", Payload: []byte(`{{"plan":1}}`)},
			{ID: "damaged-message", Sequence: 3, Kind: "message/complete", Payload: []byte(`{{"message":1}}`)},
		},
	}
	path := filepath.Join(dir, currentLogName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if _, err := encodeV4Commits(t.Context(), &encoded, contentStoreForSessionDir(dir), []Commit{damaged}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(encoded.String())...), 0600); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithOptions(dir, "s", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatalf("bounded open with damaged payload event: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })

	snapshot := reopened.Snapshot()
	if snapshot.DurableSequence != 3 {
		t.Fatalf("bounded durable sequence = %d, want 3", snapshot.DurableSequence)
	}
	if snapshot.Projection.PlanState != nil {
		t.Fatal("bounded business pass leaked the damaged plan/state")
	}
	if len(snapshot.Projection.Messages) != 0 {
		t.Fatalf("bounded recent pass leaked the damaged message: %+v", snapshot.Projection.Messages)
	}
	sidecar, err := os.ReadFile(filepath.Join(dir, ".damaged-events.jsonl"))
	if err != nil {
		t.Fatalf("sidecar missing after bounded open: %v", err)
	}
	if !strings.Contains(string(sidecar), `"kind":"plan/state"`) || !strings.Contains(string(sidecar), `"kind":"message/complete"`) {
		t.Fatalf("sidecar incomplete after bounded open: %s", sidecar)
	}
}

// Normal path zero change: a healthy session never grows a sidecar and its
// projection is untouched by the quarantine pass.
func TestHealthySessionHasNoSidecar(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	s, err := OpenWithOptions(dir, "s", OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: "m1", Role: provider.RoleUser}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "good", Events: []Event{
		{Kind: "turn/start", Payload: []byte(`{"turnId":"t1"}`)},
		{Kind: "plan/state", Payload: []byte(`{"steps":["a"]}`)},
		{Kind: "message/complete", Payload: payload},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })

	if _, err := os.Stat(filepath.Join(dir, ".damaged-events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("healthy session grew a sidecar: %v", err)
	}
	snapshot := reopened.Snapshot()
	if snapshot.Projection.PlanState == nil {
		t.Fatal("healthy plan/state vanished")
	}
	if len(snapshot.Projection.Messages) != 1 {
		t.Fatalf("healthy messages = %d, want 1", len(snapshot.Projection.Messages))
	}
}
