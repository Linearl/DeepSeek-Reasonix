package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/store"
)

// TestTabSessionEventsSizeStatsOwnEventLog pins task 997's current-conversation
// data source: one stat of THIS tab session's own events log, in bytes.
//   - tab with a session whose events log grew on disk -> exact byte count;
//   - session without an events log yet (fresh conversation) -> 0 (frontend
//     renders a dash, never a fake "0.0 MB");
//   - unknown tab / booting controller (Ctrl == nil) -> 0, no panic.
func TestTabSessionEventsSizeStatsOwnEventLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	a, _ := appWithTab(t, path)

	if got := a.TabSessionEventsSize("test_tab"); got != 0 {
		t.Fatalf("fresh session events size = %d, want 0 (log not created yet)", got)
	}

	eventsPath := store.SessionEventLog(path)
	payload := make([]byte, 4096)
	if err := os.WriteFile(eventsPath, payload, 0o600); err != nil {
		t.Fatalf("write events log: %v", err)
	}
	if got := a.TabSessionEventsSize("test_tab"); got != int64(len(payload)) {
		t.Fatalf("events size = %d, want %d (exact stat of the tab's own log)", got, len(payload))
	}

	if got := a.TabSessionEventsSize("no_such_tab"); got != 0 {
		t.Fatalf("unknown tab events size = %d, want 0", got)
	}

	a.tabs["booting"] = &WorkspaceTab{ID: "booting", Scope: "global"}
	if got := a.TabSessionEventsSize("booting"); got != 0 {
		t.Fatalf("booting tab (nil Ctrl) events size = %d, want 0, no panic", got)
	}
}
