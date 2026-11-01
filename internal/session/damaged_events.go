package session

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// tl1317: stores written before the write-side payload gate (or through
// migration paths) can hold events whose payload is not valid JSON — the
// double-enveloped {{"inner":1}} shape among them. Serializing such an event
// fails wholesale, which used to wedge the session autosave and the open.
// During replay those events are skipped and isolated here so the data is not
// lost: the original log is never rewritten, the sidecar records every
// excluded event byte for later inspection.
const damagedEventsSidecarName = ".damaged-events.jsonl"

type damagedEventRecord struct {
	Kind          string    `json:"kind"`
	Sequence      uint64    `json:"seq"`
	EventID       string    `json:"eventId,omitempty"`
	Reason        string    `json:"reason"`
	PayloadBase64 string    `json:"payloadBase64,omitempty"`
	QuarantinedAt time.Time `json:"quarantinedAt"`
}

// quarantineDamagedEvent reports whether the event must be excluded from
// replay, isolating it to the session-dir sidecar first. Empty payloads are
// legal (the projection tolerates kinds without one today), and events still
// held as external references are judged after their body is resolved.
func quarantineDamagedEvent(dir string, event Event) bool {
	if event.PayloadRef != nil || len(event.Payload) == 0 || json.Valid(event.Payload) {
		return false
	}
	isolateDamagedEvent(dir, event)
	return true
}

// quarantineCommitPayloads drops events whose inline payload is not valid
// JSON from the replay set. The commit's own identity — ids, sequences,
// operation hash — stays intact: only the events slice is narrowed, so
// cursors and sequence bookkeeping keep advancing exactly as before.
func quarantineCommitPayloads(dir string, commit Commit) Commit {
	if len(commit.Events) == 0 {
		return commit
	}
	kept := make([]Event, 0, len(commit.Events))
	for _, event := range commit.Events {
		if quarantineDamagedEvent(dir, event) {
			continue
		}
		kept = append(kept, event)
	}
	if len(kept) == len(commit.Events) {
		return commit
	}
	commit.Events = kept
	return commit
}

// isolateDamagedEvent appends the event to the sidecar. Best effort: a failed
// sidecar write never fails the open — the source bytes stay in the events
// log either way, the sidecar only adds visibility.
func isolateDamagedEvent(dir string, event Event) {
	payload := ""
	if len(event.Payload) > 0 {
		payload = base64.StdEncoding.EncodeToString(event.Payload)
	}
	record := damagedEventRecord{
		Kind:          event.Kind,
		Sequence:      event.Sequence,
		EventID:       event.ID,
		Reason:        "payload is not valid JSON",
		PayloadBase64: payload,
		QuarantinedAt: time.Now().UTC(),
	}
	line, err := json.Marshal(record)
	if err != nil {
		slog.Warn("session: damaged event record marshal failed", "kind", event.Kind, "seq", event.Sequence, "err", err)
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, damagedEventsSidecarName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("session: damaged events sidecar unavailable", "dir", dir, "err", err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		slog.Warn("session: damaged event sidecar write failed", "dir", dir, "err", err)
		return
	}
	slog.Warn("session: quarantined event with invalid JSON payload", "kind", event.Kind, "seq", event.Sequence, "sidecar", damagedEventsSidecarName)
}
