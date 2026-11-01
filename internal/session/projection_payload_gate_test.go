package session

import (
	"errors"
	"strings"
	"testing"
)

// tl1317 P2: the projection entrance must reject an event whose payload is
// not valid JSON for every kind — not only the kinds whose handler happens to
// decode strictly. A handler added later that forgets its own validation
// cannot turn a malformed payload into a silently skipped event again.
func TestProjectionEntranceRejectsInvalidJSONPayload(t *testing.T) {
	for _, kind := range []string{
		"session/title", "turn/start", "step/start", "tool/call", "todo/write",
		"interaction/created", "runtime/recovery", "compaction", "assistant/attempt",
	} {
		_, err := Project([]Commit{{Events: []Event{
			{Sequence: 7, Kind: kind, Payload: []byte(`{{"inner":1}}`)},
		}}})
		if err == nil {
			t.Fatalf("%s: invalid JSON payload projected without error", kind)
		}
		if !errors.Is(err, ErrDamagedStore) {
			t.Fatalf("%s: err = %v, want ErrDamagedStore", kind, err)
		}
		if !strings.Contains(err.Error(), kind) || !strings.Contains(err.Error(), "7") {
			t.Fatalf("%s: err = %v, want kind and sequence", kind, err)
		}
	}
}

// Scalar and empty payloads keep their historical treatment: scalars are
// valid JSON (diagnostic allows them), empty payloads are legal for the
// kinds that carry none.
func TestProjectionEntranceAcceptsScalarAndEmptyPayloads(t *testing.T) {
	projection, err := Project([]Commit{{Events: []Event{
		{Sequence: 1, Kind: "diagnostic", Payload: []byte(`"note"`)},
		{Sequence: 2, Kind: "diagnostic"},
	}}})
	if err != nil {
		t.Fatalf("scalar/empty payloads rejected: %v", err)
	}
	if projection.CommittedSequence != 2 {
		t.Fatalf("committed sequence = %d, want 2", projection.CommittedSequence)
	}
}
