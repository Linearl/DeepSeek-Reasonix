package servepool

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOrphanDecisionPinsTheKillRule pins task 272 L3's reap signature:
// owner desktop dead + serve alive = kill; everything else is left alone.
func TestOrphanDecisionPinsTheKillRule(t *testing.T) {
	self := 100
	alive := map[int]bool{101: true, 200: true, 300: false}
	probe := func(pid int) bool { return alive[pid] }

	cases := []struct {
		name               string
		servePID, ownerPID int
		want               bool
	}{
		{name: "orphan: owner dead, serve alive", servePID: 200, ownerPID: 300, want: true},
		{name: "live owner untouched", servePID: 200, ownerPID: 101, want: false},
		{name: "already dead serve skipped", servePID: 300, ownerPID: 301, want: false},
		{name: "self pid never killed", servePID: 100, ownerPID: 300, want: false},
		{name: "self as owner means a peer desktop, not an orphan", servePID: 200, ownerPID: 100, want: false},
		{name: "garbage pids rejected", servePID: 0, ownerPID: -1, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := orphanDecision(tc.servePID, tc.ownerPID, self, probe); got != tc.want {
				t.Fatalf("orphanDecision(%d, %d, %d) = %v, want %v", tc.servePID, tc.ownerPID, self, got, tc.want)
			}
		})
	}
	// owner 101 not in the alive map => not alive; serve 200 alive — wait,
	// that combination IS the orphan rule; pin it explicitly for an owner the
	// probe has never heard of (conservative platforms may not resolve pids).
	if !orphanDecision(200, 999, self, probe) {
		t.Fatal("unknown owner must be treated as dead when the serve is alive")
	}
}

// TestSpawnFileRoundTrip pins the on-disk contract between spawn and reap:
// "<servePid>\n<desktopPid>\n", tolerant of CRLF, rejecting malformed input.
func TestSpawnFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, spawnFileRel)
	if err := os.WriteFile(path, []byte("4321\n8765\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	servePID, ownerPID, ok := readSpawnFile(path)
	if !ok || servePID != 4321 || ownerPID != 8765 {
		t.Fatalf("round-trip = (%d, %d, %v), want (4321, 8765, true)", servePID, ownerPID, ok)
	}
	// Missing file and malformed content are both "no record", never a kill.
	if _, _, ok := readSpawnFile(filepath.Join(dir, "absent")); ok {
		t.Fatal("missing file must not read as a record")
	}
	for _, junk := range []string{"", "12", "abc\ndef\n", "12\nabc\n"} {
		if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, ok := readSpawnFile(path); ok {
			t.Fatalf("malformed record %q must be rejected", junk)
		}
	}
}
