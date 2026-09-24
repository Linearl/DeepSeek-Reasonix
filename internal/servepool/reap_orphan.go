package servepool

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Task 272 L3: startup reaping is the belt to the Job's suspenders. The Job
// (StartTrackedRequired) reaps serves spawned BY THIS desktop on any death;
// but a serve spawned by an ALREADY-DEAD desktop (a pre-Job build, or a crash
// that beat the job handle) sits outside every live job — that is exactly the
// orphan that held session leases until the user rebooted the machine
// (incidents ②③④). Each spawn now writes serve.spawn
// "<servePid>\n<ownerDesktopPid>\n<serveImagePath>\n"; the new desktop reads
// it before starting the pool: owner pid dead + serve pid alive + image path
// still matching the recorded serve binary = orphan => kill. The image-path
// check (audit M2) refuses to kill a recycled pid that now belongs to some
// unrelated process; a record without the third line (pre-fix spawns) is left
// alone — missing evidence must fail toward NOT killing.

// spawnFileRel is the per-project ownership record name, next to serve.port.
const spawnFileRel = "serve.spawn"

// orphanDecision is the pure part of the reap check: does this recorded spawn
// qualify as an orphan worth killing? processAlive is injected so the test
// never touches a real pid.
func orphanDecision(servePID, ownerPID, selfPID int, processAlive func(int) bool) bool {
	if servePID <= 0 || ownerPID <= 0 {
		return false
	}
	if servePID == selfPID || ownerPID == selfPID {
		return false
	}
	return !processAlive(ownerPID) && processAlive(servePID)
}

// imagePathMatches is the audit-M2 pid-reuse guard: only kill when the live
// process at that pid still maps to the recorded serve image. Comparison is
// case-insensitive (Windows paths) and tolerant of an unreadable actual path
// (no permission => unknown => do not kill).
func imagePathMatches(recorded, actual string) bool {
	recorded = strings.TrimSpace(recorded)
	if recorded == "" {
		return false
	}
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(recorded), filepath.Clean(actual))
}

// ReapOrphanSpawns scans every project's spawn record and kills serves whose
// owning desktop is gone. Safe to call before the pool loop starts; every
// failure is a WARN, never a startup failure.
func (m *Manager) ReapOrphanSpawns() {
	alive := processAlive
	m.mu.Lock()
	roots := make([]string, 0, len(m.projects))
	for _, p := range m.projects {
		roots = append(roots, p.root)
	}
	m.mu.Unlock()
	self := os.Getpid()
	for _, root := range roots {
		spawnFile := filepath.Join(m.portFileDir(root), spawnFileRel)
		servePID, ownerPID, recordImage, ok := readSpawnFile(spawnFile)
		if !ok {
			continue
		}
		if !orphanDecision(servePID, ownerPID, self, alive) {
			continue
		}
		// Audit M2: pids are recycled — before killing, prove the live process
		// at servePID still IS the recorded serve image. A record from before
		// the image path was written (empty) or an unreadable live path both
		// fail toward skipping: a missed orphan is recoverable (next scan, or
		// the lease error names the pid), a killed innocent is not.
		actualImage := processImagePath(servePID)
		if !imagePathMatches(recordImage, actualImage) {
			log.Printf("[servepool] skip reap: pid %d no longer matches the recorded serve image (recorded=%q actual=%q); leaving the record for the next scan",
				servePID, recordImage, actualImage)
			continue
		}
		proc, err := os.FindProcess(servePID)
		if err != nil || proc == nil {
			continue
		}
		if killErr := proc.Kill(); killErr != nil {
			log.Printf("[servepool] reap orphan serve failed project=%q pid=%d owner=%d: %v", root, servePID, ownerPID, killErr)
			continue
		}
		log.Printf("[servepool] reaped orphan serve project=%q pid=%d (owner desktop pid %d is dead)", root, servePID, ownerPID)
		_ = os.Remove(spawnFile)
	}
}

// writeSpawnRecord writes the ownership record next to serve.port.
// Task 272 note②: a failed write previously vanished into `_ =` — the reap
// then silently skipped this project forever, so a leak was undiagnosable.
// Missing record only means "cannot auto-reap" (the lease error still names
// the pid), but it must be observable.
func writeSpawnRecord(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		log.Printf("[servepool] write spawn record failed (auto-reap will skip this project): path=%q err=%v", path, err)
	}
}

// readSpawnFile parses the spawn record. Two-line records (pre-M2) return an
// empty image path, which the reap treats as "cannot prove identity => skip".
func readSpawnFile(path string) (servePID, ownerPID int, image string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return 0, 0, "", false
	}
	servePID, err1 := strconv.Atoi(strings.TrimSpace(lines[0]))
	ownerPID, err2 := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, "", false
	}
	if len(lines) >= 3 {
		image = strings.TrimSpace(lines[2])
	}
	return servePID, ownerPID, image, true
}

// processAlive is factored for test injection.
var processAlive = defaultProcessAlive

func defaultProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows FindProcess is OpenProcess: reaching here means the handle
		// opened, i.e. the process exists; a dead pid errored out above.
		return true
	}
	// Unix FindProcess always succeeds; probe with a zero signal.
	return zeroSignalAlive(proc.Signal(syscall.Signal(0)))
}

// zeroSignalAlive interprets a Signal(0) probe result (audit M1): no error or
// EPERM both mean the process EXISTS — EPERM is "owned by someone we cannot
// signal", not death. Treating it as dead would misclassify a live peer
// desktop's serve as an orphan and kill it.
func zeroSignalAlive(err error) bool {
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
