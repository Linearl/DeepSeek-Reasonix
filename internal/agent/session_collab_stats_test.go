package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// statsFixture writes one session with a meta sidecar carrying a persisted turn
// count plus a primary event log of the given size — the same two sources
// heartbeat_session_rotate.py reconciles against (task 508's accounting rule).
func statsFixture(t *testing.T, dir, name string, turns int, eventLogBytes []byte) {
	t.Helper()
	path := filepath.Join(dir, name+".jsonl")
	writeEmpty(t, path)
	if err := UpdateBranchMeta(path, true, func(m *BranchMeta) error {
		m.CustomTitle = name
		m.Turns = turns
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if eventLogBytes != nil {
		if err := os.WriteFile(filepath.Join(dir, name+".events.jsonl"), eventLogBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Acceptance ② (task 508): without the new argument the answer is exactly the
// old shape — no stats keys anywhere in the payload, even though the fixture
// has both a turns counter and an event log to report.
func TestListAddressableSessionsStatsOffByDefault(t *testing.T) {
	dir := t.TempDir()
	statsFixture(t, dir, "sized", 42, make([]byte, 512))
	tool := NewListAddressableSessionsTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	})
	out, err := tool.Execute(nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"eventsBytes", `"turns"`, "lastActivityAt", `"stats"`} {
		if strings.Contains(out, key) {
			t.Fatalf("stats key %s must be absent without include_stats (zero-impact acceptance): %s", key, out)
		}
	}
}

// Acceptance ① + ③: one gated call carries the size and the turns, and both
// numbers reconcile with what the file system and the meta sidecar hold —
// the same sources heartbeat_session_rotate.py reads.
func TestListAddressableSessionsStatsOnDemand(t *testing.T) {
	dir := t.TempDir()
	statsFixture(t, dir, "sized", 42, make([]byte, 512))
	tool := NewListAddressableSessionsTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	})
	out, err := tool.Execute(nil, []byte(`{"include_stats":true,"query":"sized"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		StatsNote string `json:"stats"`
		Sessions  []struct {
			Title          string `json:"title"`
			EventsBytes    *int64 `json:"eventsBytes"`
			Turns          *int   `json:"turns"`
			LastActivityAt *int64 `json:"lastActivityAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload must decode: %v\n%s", err, out)
	}
	if payload.StatsNote == "" {
		t.Fatalf("stats-on answer must state the accounting: %s", out)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("query=sized must return exactly the fixture row: %s", out)
	}
	row := payload.Sessions[0]
	if row.EventsBytes == nil || *row.EventsBytes != 512 {
		t.Fatalf("eventsBytes must equal the primary event log size, got %v", row.EventsBytes)
	}
	if row.Turns == nil || *row.Turns != 42 {
		t.Fatalf("turns must come from the persisted meta counter, got %v", row.Turns)
	}
	info, err := os.Stat(filepath.Join(dir, "sized.events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if row.LastActivityAt == nil || *row.LastActivityAt != info.ModTime().UnixMilli() {
		t.Fatalf("lastActivityAt must equal the event log mtime, got %v want %d", row.LastActivityAt, info.ModTime().UnixMilli())
	}
}

// Opposite input: the meta exists but the event log does not (fresh or
// array-format session). The gated answer reports 0/unknown without failing —
// 0 means "no log", and the keys must still be present so a consumer can tell
// that from "stats not requested".
func TestListAddressableSessionsStatsMissingEventLog(t *testing.T) {
	dir := t.TempDir()
	statsFixture(t, dir, "bare", 7, nil)
	tool := NewListAddressableSessionsTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	})
	out, err := tool.Execute(nil, []byte(`{"include_stats":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Sessions []struct {
			Title          string `json:"title"`
			EventsBytes    *int64 `json:"eventsBytes"`
			Turns          *int   `json:"turns"`
			LastActivityAt *int64 `json:"lastActivityAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload must decode: %v\n%s", err, out)
	}
	for _, row := range payload.Sessions {
		if row.Title != "bare" {
			continue
		}
		if row.EventsBytes == nil || *row.EventsBytes != 0 {
			t.Fatalf("missing log must report eventsBytes 0, got %v", row.EventsBytes)
		}
		if row.Turns == nil || *row.Turns != 7 {
			t.Fatalf("turns must survive a missing event log, got %v", row.Turns)
		}
		if row.LastActivityAt == nil || *row.LastActivityAt != 0 {
			t.Fatalf("missing log must report lastActivityAt 0, got %v", row.LastActivityAt)
		}
		return
	}
	t.Fatalf("fixture row missing: %s", out)
}

// Opposite input, large side: a sparse multi-megabyte event log must answer
// with the LOGICAL size — the tool stats the file and never opens the body, so
// a rotation audit over grown sessions costs one stat per row, not the bytes.
func TestListAddressableSessionsStatsSparseLargeLog(t *testing.T) {
	dir := t.TempDir()
	const hole = int64(32 * 1024 * 1024) // 32 MiB logical, ~no real allocation
	statsFixture(t, dir, "grown", 3, nil)
	f, err := os.OpenFile(filepath.Join(dir, "grown.events.jsonl"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(hole, 0); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("tail")); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	tool := NewListAddressableSessionsTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	})
	out, err := tool.Execute(nil, []byte(`{"include_stats":true,"query":"grown"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Sessions []struct {
			EventsBytes *int64 `json:"eventsBytes"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload must decode: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].EventsBytes == nil ||
		*payload.Sessions[0].EventsBytes != hole+4 {
		t.Fatalf("sparse log must report logical size %d, got %s", hole+4, out)
	}
}

// Pagination is untouched by the enrichment: with stats on, the page still
// returns limit rows and the unchanged total, and only emitted rows carry
// stats (a paged-out row costs no stat).
func TestListAddressableSessionsStatsStillPaged(t *testing.T) {
	dir := t.TempDir()
	statsFixture(t, dir, "a", 1, make([]byte, 16))
	statsFixture(t, dir, "b", 2, make([]byte, 16))
	statsFixture(t, dir, "c", 3, make([]byte, 16))
	tool := NewListAddressableSessionsTool(SessionCollabConfig{
		Enabled:       true,
		SessionDir:    dir,
		WorkspaceRoot: dir,
	})
	out, err := tool.Execute(nil, []byte(`{"include_stats":true,"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Returned int `json:"returned"`
		Total    int `json:"total"`
		Sessions []struct {
			Turns *int `json:"turns"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload must decode: %v\n%s", err, out)
	}
	// total is machine-dependent (the scan also sees the global archive), so
	// pin the fixture's minimum — the same rule the metadata-only test uses.
	if payload.Returned != 2 || payload.Total < 3 || len(payload.Sessions) != 2 {
		t.Fatalf("stats must not change paging: returned=%d total=%d rows=%d", payload.Returned, payload.Total, len(payload.Sessions))
	}
	for _, row := range payload.Sessions {
		if row.Turns == nil {
			t.Fatalf("every emitted row must carry turns when stats are on: %s", out)
		}
	}
}

// The turns value rides scanAddressable (which loads the full meta), while the
// desktop roster's four-field ScanDir loader leaves it zero — the desktop wire
// gains no key even though the field exists on the shared struct.
func TestTurnsRidesScanAddressableOnly(t *testing.T) {
	dir := t.TempDir()
	statsFixture(t, dir, "counter", 99, nil)
	fixture := filepath.Join(dir, "counter.jsonl")
	// The scan also sees this machine's real sessions/archive, so locate the
	// fixture row instead of asserting on the whole slice.
	ids := scanAddressable(dir, dir)
	for _, id := range ids {
		if id.SessionPath == fixture {
			if id.Turns != 99 {
				t.Fatalf("scanAddressable must carry the persisted turns, got %d", id.Turns)
			}
			for _, viaScanDir := range sessioncollab.ScanDir(dir, dir, func(p string) (contactID, purpose, topicID, title string, ok bool) {
				// Mirrors desktop/session_collab.go's collabRoster loader: the
				// four fields only — the closure never populates Turns.
				m, found, err := LoadBranchMeta(p)
				if err != nil {
					return "", "", "", "", false
				}
				if found {
					return m.ContactID, m.Purpose, m.TopicID, SessionDirectoryTitle(p), true
				}
				return "", "", "", SessionDirectoryTitle(p), true
			}) {
				if viaScanDir.SessionPath == fixture && viaScanDir.Turns != 0 {
					t.Fatalf("ScanDir (desktop loader) must leave Turns zero, got %d", viaScanDir.Turns)
				}
			}
			return
		}
	}
	t.Fatalf("fixture row missing from scan: %s", fixture)
}
