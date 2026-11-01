package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// tl1317 P0: hashOperation marshals the whole batch, so one event whose
// payload is not valid JSON (the double-enveloped {{"inner":1}} shape) used to
// kill the batch with "json: error calling MarshalJSON for type
// json.RawMessage" and re-kill every later batch that re-hashed it, wedging
// the session autosave permanently. The entrance must reject the batch
// instead, naming the offending index and kind.
func TestPrepareBatchRejectsInvalidJSONPayload(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session")
	s, err := OpenWithOptions(dir, "s", OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	batch := Batch{Events: []Event{
		{Kind: "turn/start", Payload: []byte(`{"turnId":"t1"}`)},
		{Kind: "diagnostic", Payload: []byte(`{{"inner":1}}`)},
	}}
	if _, err := s.PrepareBatchContext(t.Context(), "bad-payload", batch); err == nil {
		t.Fatal("double-enveloped payload was accepted")
	} else {
		if !errors.Is(err, ErrDamagedStore) {
			t.Fatalf("err = %v, want ErrDamagedStore", err)
		}
		if !strings.Contains(err.Error(), "events[1]") || !strings.Contains(err.Error(), `"diagnostic"`) {
			t.Fatalf("err = %v, want events[1] index and kind", err)
		}
	}
	if s.EventSequence() != 0 {
		t.Fatalf("rejected batch consumed sequence %d", s.EventSequence())
	}

	// The rejected batch must not poison later ones: a clean batch still
	// prepares and hashes normally afterwards.
	clean, err := s.PrepareBatchContext(t.Context(), "clean", Batch{Events: []Event{
		{Kind: "turn/start", Payload: []byte(`{"turnId":"t1"}`)},
	}})
	if err != nil {
		t.Fatalf("clean batch rejected after damaged batch: %v", err)
	}
	if len(clean.events) != 1 {
		t.Fatalf("clean batch = %+v", clean)
	}
}

// tl1317 P0: the defect reproduced at the exact crash point — hashing a batch
// holding a double-enveloped payload fails wholesale. Pinned so a future
// hashOperation change cannot silently re-widen the blast radius without the
// entrance gate noticing.
func TestHashOperationFailsOnInvalidJSONPayload(t *testing.T) {
	_, err := hashOperation("s", "t", []Event{{Kind: "diagnostic", Payload: []byte(`{{"inner":1}}`)}})
	if err == nil {
		t.Fatal("hashOperation accepted an invalid JSON payload")
	}
	if !strings.Contains(err.Error(), "json.RawMessage") {
		t.Fatalf("err = %v, want the RawMessage marshal failure", err)
	}
}
