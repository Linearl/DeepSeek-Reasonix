package servepool

import (
	"os"
	"path/filepath"
	"syscall"
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
	// owner unknown to the probe reads as dead: conservative platforms may not
	// resolve every pid, and the orphan rule still requires a live serve.
	if !orphanDecision(200, 999, self, probe) {
		t.Fatal("unknown owner must be treated as dead when the serve is alive")
	}
}

// TestSpawnFileRoundTrip pins the on-disk contract between spawn and reap:
// "<servePid>\n<ownerDesktopPid>\n<serveImagePath>\n", tolerant of CRLF. A
// legacy two-line record parses with an empty image path — which the reap
// refuses to kill against (audit M2: no identity proof, no kill).
func TestSpawnFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, spawnFileRel)

	// Current three-line format.
	if err := os.WriteFile(path, []byte("4321\n8765\nC:\\bin\\reasonix-cli.exe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	servePID, ownerPID, image, ok := readSpawnFile(path)
	if !ok || servePID != 4321 || ownerPID != 8765 || image != `C:\bin\reasonix-cli.exe` {
		t.Fatalf("round-trip = (%d, %d, %q, %v)", servePID, ownerPID, image, ok)
	}

	// Legacy two-line record: parses, but image is empty (no kill evidence).
	if err := os.WriteFile(path, []byte("4321\r\n8765\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, image, ok = readSpawnFile(path)
	if !ok || image != "" {
		t.Fatalf("legacy record must parse with empty image, got image=%q ok=%v", image, ok)
	}

	// Missing file and malformed content are both "no record", never a kill.
	if _, _, _, ok := readSpawnFile(filepath.Join(dir, "absent")); ok {
		t.Fatal("missing file must not read as a record")
	}
	for _, junk := range []string{"", "12", "abc\ndef\n", "12\nabc\n"} {
		if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, ok := readSpawnFile(path); ok {
			t.Fatalf("malformed record %q must be rejected", junk)
		}
	}
}

// TestImagePathMatchesPidReuseGuard pins audit M2's core rule: a recycled pid
// whose live image no longer matches the record must never be killed, and an
// unreadable live path (or empty record) fails toward NOT killing.
func TestImagePathMatchesPidReuseGuard(t *testing.T) {
	cases := []struct {
		name, recorded, actual string
		want                   bool
	}{
		{name: "same path kills", recorded: `C:\apps\reasonix\reasonix-cli.exe`, actual: `c:\apps\reasonix\REASONIX-CLI.EXE`, want: true},
		{name: "unix exact match kills", recorded: "/opt/reasonix/reasonix-cli", actual: "/opt/reasonix/reasonix-cli", want: true},
		{name: "recycled pid: unrelated image never killed", recorded: `C:\apps\reasonix\reasonix-cli.exe`, actual: `C:\Windows\System32\notepad.exe`, want: false},
		{name: "empty record (legacy) never killed", recorded: "", actual: `C:\apps\reasonix\reasonix-cli.exe`, want: false},
		{name: "unreadable live path never killed", recorded: `C:\apps\reasonix\reasonix-cli.exe`, actual: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imagePathMatches(tc.recorded, tc.actual); got != tc.want {
				t.Fatalf("imagePathMatches(%q, %q) = %v, want %v", tc.recorded, tc.actual, got, tc.want)
			}
		})
	}
}

// TestZeroSignalAliveTreatsEPERMAsAlive pins audit M1: EPERM from a Signal(0)
// probe means "exists but not signalable by us", not death — misreading it
// would let a live peer desktop's serve be classified as an orphan.
func TestZeroSignalAliveTreatsEPERMAsAlive(t *testing.T) {
	if !zeroSignalAlive(nil) {
		t.Fatal("nil error must read as alive")
	}
	if !zeroSignalAlive(syscall.EPERM) {
		t.Fatal("EPERM must read as alive: the process exists, we just cannot signal it")
	}
	if zeroSignalAlive(syscall.ESRCH) {
		t.Fatal("ESRCH (no such process) must read as dead")
	}
}
