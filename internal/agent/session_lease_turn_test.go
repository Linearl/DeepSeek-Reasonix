package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Task 290 S1: the optional Turn registration on SessionLeaseInfo.
// The hand-written MarshalJSON wire copy is the whole point of these tests —
// a field that exists on the struct but not in `wire` is silently dropped
// from every persisted sidecar (the 81/123 render-table lesson).

func TestSessionLeaseTurnRoundtripThroughWire(t *testing.T) {
	started := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	in := SessionLeaseInfo{
		SessionPath: "x", WriterID: "writer", PID: 1,
		AcquiredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Turn: &SessionLeaseTurn{
			TurnID: "t-123", Status: "in_progress", StartedAt: started,
			Control: &SessionLeaseTurnControl{Kind: "tcp", Addr: "127.0.0.1:9", Token: "tok"},
		},
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	// Wire sync: the hand-written wire copy must actually emit the field.
	if !strings.Contains(string(encoded), `"turn"`) {
		t.Fatalf("Turn field missing from marshaled sidecar (wire copy out of sync): %s", encoded)
	}
	out, err := decodeSessionLeaseInfo(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if out.Turn == nil {
		t.Fatalf("Turn lost in roundtrip: %s", encoded)
	}
	if out.Turn.TurnID != "t-123" || out.Turn.Status != "in_progress" || !out.Turn.StartedAt.Equal(started) {
		t.Fatalf("Turn content mismatch: %+v", out.Turn)
	}
	if out.Turn.Control == nil || out.Turn.Control.Kind != "tcp" ||
		out.Turn.Control.Addr != "127.0.0.1:9" || out.Turn.Control.Token != "tok" {
		t.Fatalf("Control content mismatch: %+v", out.Turn.Control)
	}
}

func TestSessionLeaseTurnAbsentKeepsLegacyShape(t *testing.T) {
	encoded, err := json.Marshal(SessionLeaseInfo{
		SessionPath: "x", WriterID: "writer", PID: 1,
		AcquiredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"turn"`) {
		t.Fatalf("nil Turn must not be serialized (byte-compat with pre-290 sidecars): %s", encoded)
	}
	info, err := decodeSessionLeaseInfo(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if info.Turn != nil {
		t.Fatalf("nil Turn did not survive roundtrip: %+v", info.Turn)
	}
}

func TestSessionLeaseTurnLegacyJSONReadsNil(t *testing.T) {
	// A sidecar written by a pre-290 build: no turn field at all.
	info, err := decodeSessionLeaseInfo([]byte(
		`{"session_path":"x","writer_id":"legacy","pid":1,"acquired_at":"2026-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Turn != nil {
		t.Fatalf("legacy sidecar decoded with a turn registration: %+v", info.Turn)
	}
}

func TestSessionLeaseTurnIgnoredByLegacyReader(t *testing.T) {
	// Simulate a pre-290 reader: a struct without the turn field parsed by
	// standard encoding/json (the compatibility strategy the handoff fields
	// already rely on). The unknown field must be ignored, not fatal, and the
	// known fields must still land.
	legacyWire := struct {
		SessionPath string    `json:"session_path"`
		WriterID    string    `json:"writer_id"`
		PID         int       `json:"pid"`
		AcquiredAt  time.Time `json:"acquired_at"`
	}{}
	current := SessionLeaseInfo{
		SessionPath: "x", WriterID: "writer", PID: 1,
		AcquiredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Turn:       &SessionLeaseTurn{TurnID: "t-1", StartedAt: time.Now().UTC()},
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &legacyWire); err != nil {
		t.Fatalf("legacy reader failed on a turn-bearing sidecar: %v", err)
	}
	if legacyWire.WriterID != "writer" || legacyWire.PID != 1 {
		t.Fatalf("legacy reader lost known fields: %+v", legacyWire)
	}
}

func TestSessionLeaseTurnSurvivesSaveLoad(t *testing.T) {
	userPath, key := leaseTestPath(t)
	t.Cleanup(func() { _ = os.Remove(sessionLeaseInfoPath(key)) })
	started := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	if err := SaveSessionLeaseInfo(key, SessionLeaseInfo{
		SessionPath: key,
		WriterID:    "writer",
		PID:         1,
		AcquiredAt:  time.Now().UTC(),
		Turn: &SessionLeaseTurn{
			TurnID: "t-save", Status: "waiting_user", StartedAt: started,
			Control: &SessionLeaseTurnControl{Kind: "tcp", Addr: "127.0.0.1:7", Token: "s"},
		},
	}); err != nil {
		t.Fatalf("SaveSessionLeaseInfo: %v", err)
	}
	info, err := LoadSessionLeaseInfo(userPath)
	if err != nil {
		t.Fatalf("LoadSessionLeaseInfo: %v", err)
	}
	if info.Turn == nil || info.Turn.TurnID != "t-save" || info.Turn.Status != "waiting_user" ||
		!info.Turn.StartedAt.Equal(started) {
		t.Fatalf("turn registration lost across the real save/load path: %+v", info.Turn)
	}
	if info.Turn.Control == nil || info.Turn.Control.Addr != "127.0.0.1:7" {
		t.Fatalf("control endpoint lost across save/load: %+v", info.Turn.Control)
	}
}
