package desktoplauncher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Task 876: the launcher must vouch for a planned update-restart handoff
// (fresh marker from the same PID) and keep reporting genuine deaths. The
// marker shape mirrors the desktop writer (desktop/update_restart_marker.go).
// Isolation: REASONIX_HOME overrides MemoryUserDir (internal/config/paths.go).
func TestSuperviseMarkerVouches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)

	plant := func(t *testing.T, pid int, age time.Duration) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"pid": pid, "at": time.Now().Add(-age).Unix(), "reason": "publish"})
		if err := os.WriteFile(filepath.Join(home, "update-restart-marker.json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Fresh marker from the dead PID: vouch (planned handoff).
	plant(t, 4242, time.Minute)
	if !superviseMarkerVouches(4242) {
		t.Fatal("fresh same-PID marker must vouch for a planned handoff")
	}
	// Another PID's marker: never vouch.
	if superviseMarkerVouches(9999) {
		t.Fatal("a different PID must never vouch")
	}
	// Stale marker (beyond the 10-minute fresh window): no vouch.
	plant(t, 4242, 15*time.Minute)
	if superviseMarkerVouches(4242) {
		t.Fatal("a stale marker must not vouch")
	}
	// Missing marker (plain crash): no vouch.
	if err := os.Remove(filepath.Join(home, "update-restart-marker.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if superviseMarkerVouches(4242) {
		t.Fatal("without a marker nothing may vouch")
	}
	// Degenerate PID: can never vouch by construction.
	plant(t, 4242, time.Minute)
	if superviseMarkerVouches(0) {
		t.Fatal("a non-positive PID must never vouch")
	}
}
