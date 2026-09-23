package servepool

import (
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
// (incidents ②③④). Each spawn now writes serve.spawn "<servePid>\n<desktopPid>\n";
// the new desktop reads it before starting the pool: owner pid dead + serve
// pid alive = orphan => kill. Owner alive means a peer desktop owns it
// (single-instance makes this rare) => leave alone.

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
		servePID, ownerPID, ok := readSpawnFile(spawnFile)
		if !ok {
			continue
		}
		if !orphanDecision(servePID, ownerPID, self, alive) {
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

// readSpawnFile parses the two-line spawn record.
func readSpawnFile(path string) (servePID, ownerPID int, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return 0, 0, false
	}
	servePID, err1 := strconv.Atoi(strings.TrimSpace(lines[0]))
	ownerPID, err2 := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return servePID, ownerPID, true
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
	return proc.Signal(syscall.Signal(0)) == nil
}
