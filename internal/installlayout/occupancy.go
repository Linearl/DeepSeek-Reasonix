package installlayout

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Version-tree occupancy probing (task 1259).
//
// The 2026-10-29 incident: an elevated desktop instance kept running while
// versions pruning deleted its tree, leaving a ghost process (live PID, exe
// gone) that ordinary-privilege tools could not kill and whose session lease
// blocked batch deletion. The probing closes that blind spot, and it is
// deliberately enumeration-first: on the empirical Windows baseline
// (10.0.26200, see occupancy_windows.go) the file of a RUNNING image opens
// successfully even exclusively, so no file-handle probe can see a running
// instance — the image path is the only trustworthy occupancy signal:
//
//   - probeProcessAttribution enumerates the process table and attributes
//     processes to version trees by image path (two resolver tiers, the
//     second reading through integrity protection with no process handle).
//   - Processes that stay unattributed but match the desktop binary's name
//     become suspects that keep plausibly-hosting trees occupied.
//   - A failed tree deletion (race or opaque protection) counts as occupied
//     via markDeletionRefused, so the tree is skipped — not partially gutted
//     — on the next pass.
//
// Both faces are consumed by PruneVersionTrees (skip occupied trees instead
// of deleting them), by the desktop's version switch (warn about other
// instances before repointing current.json), and by DeleteInstalledVersion
// (refuse a tree a live process still executes from).

// OccupiedProcess is one live process executing a binary that lives inside a
// version tree under <installRoot>/versions/.
type OccupiedProcess struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	ExePath string `json:"exePath,omitempty"`
}

// OccupiedVersion is a version tree that deletion wanted to remove but a
// running process still occupies, so the tree was kept. Rounds counts
// consecutive prune passes that saw this tree occupied, including the
// current one; Escalated is Rounds >= OccupiedEscalateAfterRounds. Suspected
// marks the indirect verdicts: the pids could not be attributed to this tree
// by path (unreadable image path — the elevated-instance case) but match the
// desktop binary's name, or the OS refused the tree's deletion outright.
type OccupiedVersion struct {
	Version   string `json:"version"`
	PIDs      []int  `json:"pids,omitempty"`
	Suspected bool   `json:"suspected,omitempty"`
	Rounds    int    `json:"rounds"`
	Escalated bool   `json:"escalated"`
}

// Describe renders one line for prune output and log lines.
func (o OccupiedVersion) Describe() string {
	var b strings.Builder
	b.WriteString(o.Version)
	b.WriteString(" (")
	if len(o.PIDs) > 0 {
		b.WriteString("pid ")
		for i, pid := range o.PIDs {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(strconv.Itoa(pid))
		}
		if o.Suspected {
			b.WriteString("; ")
		}
	}
	if o.Suspected {
		b.WriteString("suspected: image path unreadable or deletion refused")
	}
	b.WriteString("; round ")
	b.WriteString(strconv.Itoa(o.Rounds))
	b.WriteString("/")
	b.WriteString(strconv.Itoa(OccupiedEscalateAfterRounds))
	b.WriteString(")")
	return b.String()
}

// OccupiedEscalateAfterRounds is how many consecutive prune passes may keep
// seeing a tree occupied before the surfaces (CLI output, retention log,
// picker annotation) escalate to "handle this yourself". Three rounds means
// three independent prune triggers (builds for the keep=5 CLI, launches for
// the desktop retention chain) all found the same live instance — a transient
// lock (antivirus scan, a dying process) clears within one round, so three
// is positive evidence of a persistent ghost-risk instance without letting a
// stale tree linger indefinitely (three also matches the retention rule's
// own history ceiling, keeping one number in play).
const OccupiedEscalateAfterRounds = 3

// OccupiedEscalateHint is appended to escalated surfaces as the user action.
const OccupiedEscalateHint = "close the running instance or delete the tree manually"

// ProbeVersionOccupancy returns the live processes executing binaries from
// <installRoot>/versions/<version>; an empty version matches every version
// tree. The calling process itself is never reported. Windows is the
// authoritative face (the ghost mechanism — deleting files under a running
// process — only bites there); other platforms return nothing and callers
// behave exactly as before task 1259.
func ProbeVersionOccupancy(installRoot, version string) []OccupiedProcess {
	root, err := cleanInstallRoot(installRoot)
	if err != nil {
		return nil
	}
	procs := probeVersionTreeProcesses(filepath.Join(root, VersionsDirName), version)
	if version == "" {
		return procs
	}
	out := make([]OccupiedProcess, 0, len(procs))
	for _, p := range procs {
		if strings.EqualFold(p.Version, version) {
			out = append(out, p)
		}
	}
	return out
}

// RunningVersionName reports the version tree this process executes from
// ("" on flat or non-versioned layouts). Prune hard-skips it the same way it
// skips the current.json target: past a pointer swap the running tree can
// legitimately differ from the active one (running != active is the design
// past a switch), and no prune may gut the tree under our own feet.
func RunningVersionName() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	if filepath.Base(filepath.Dir(dir)) != VersionsDirName {
		return ""
	}
	return filepath.Base(dir)
}

// ---- occupancy ledger: consecutive occupied rounds per version name ----

// occupancyLedgerName lives inside versions/ under a dot name, so every
// prune/cleanup pass ignores it by the same name rules that protect staging
// scratch and junk entries.
const occupancyLedgerName = ".occupied-ledger.json"

const occupancyLedgerSchema = 1

type occupancyLedgerFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	Rounds        map[string]int `json:"rounds"`
}

// loadOccupancyLedger reads the ledger; a missing, corrupt, or foreign-schema
// file is a fresh ledger (worst case the escalation counter restarts at one).
func loadOccupancyLedger(versionsDir string) *occupancyLedgerFile {
	ledger := &occupancyLedgerFile{SchemaVersion: occupancyLedgerSchema, Rounds: map[string]int{}}
	data, err := os.ReadFile(filepath.Join(versionsDir, occupancyLedgerName))
	if err != nil {
		return ledger
	}
	var parsed occupancyLedgerFile
	if json.Unmarshal(data, &parsed) != nil || parsed.SchemaVersion != occupancyLedgerSchema || parsed.Rounds == nil {
		return ledger
	}
	ledger.Rounds = parsed.Rounds
	return ledger
}

// saveOccupancyLedger persists best-effort (atomic replace): a ledger write
// failure never fails the prune, it only costs escalation continuity.
func saveOccupancyLedger(versionsDir string, ledger *occupancyLedgerFile) {
	body, err := json.Marshal(ledger)
	if err != nil {
		return
	}
	path := filepath.Join(versionsDir, occupancyLedgerName)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, body, 0o644) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// removeOccupancyLedger drops the file once no tree is occupied, so a healthy
// install carries no ledger residue.
func removeOccupancyLedger(versionsDir string) {
	_ = os.Remove(filepath.Join(versionsDir, occupancyLedgerName))
}

// occupiedVersionFrom merges one pass's probe result with the ledger's
// history and records the new round count.
func occupiedVersionFrom(name string, pids []int, suspected bool, ledger *occupancyLedgerFile) OccupiedVersion {
	rounds := ledger.Rounds[name] + 1
	ledger.Rounds[name] = rounds
	occ := OccupiedVersion{Version: name, Rounds: rounds, Suspected: suspected}
	if len(pids) > 0 {
		occ.PIDs = append([]int(nil), pids...)
	}
	occ.Escalated = rounds >= OccupiedEscalateAfterRounds
	return occ
}

// markDeletionRefused is the backstop for a tree that passed the probes but
// whose RemoveAll failed anyway (a process raced into the tree between probe
// and delete, or an opaque protection refused the delete): the tree counts as
// occupied from this pass on, so the next pass skips it instead of forcing a
// partially gutted tree on a live process.
func markDeletionRefused(name, versionsDir string) OccupiedVersion {
	ledger := loadOccupancyLedger(versionsDir)
	occ := occupiedVersionFrom(name, nil, true, ledger)
	if len(ledger.Rounds) == 0 {
		removeOccupancyLedger(versionsDir)
	} else {
		saveOccupancyLedger(versionsDir, ledger)
	}
	return occ
}

// treeHasDesktopBinary reports whether the tree carries a desktop executable,
// which is what an unattributable same-named suspect could be running from.
func treeHasDesktopBinary(treeDir string) bool {
	info, err := os.Stat(filepath.Join(treeDir, DesktopBinaryName()))
	return err == nil && info.Mode().IsRegular()
}

// pruneOccupiedNames walks the prunable trees of one prune pass and returns
// the occupied ones, merging the ledger history in and dropping entries for
// trees that no longer exist on disk. Enumeration happens once for the whole
// pass; trees the enumeration could not attribute by path stay candidates
// unless an unattributable same-named desktop-binary process exists (the
// elevated-process fallback). allTrees is every valid version tree present
// this pass (including keep-protected ones): a tree that slid back under
// `keep` keeps its round history so a later re-exposure continues the ladder
// instead of restarting it.
func pruneOccupiedNames(installRoot, versionsDir string, candidates, allTrees []string) []OccupiedVersion {
	ledger := loadOccupancyLedger(versionsDir)

	// Ledger entries for trees that vanished (manually deleted or gone since
	// the last pass) must not survive: the escalation state is per tree.
	existing := make(map[string]struct{}, len(allTrees))
	for _, name := range allTrees {
		existing[name] = struct{}{}
	}
	for name := range ledger.Rounds {
		if _, ok := existing[name]; !ok {
			delete(ledger.Rounds, name)
		}
	}

	attributed, suspectPids := probeProcessAttribution(versionsDir)
	byVersion := make(map[string][]int, len(attributed))
	for _, p := range attributed {
		byVersion[p.Version] = append(byVersion[p.Version], p.PID)
	}

	var occupied []OccupiedVersion
	for _, name := range candidates {
		pids := byVersion[name]
		suspected := false
		if len(pids) == 0 && len(suspectPids) > 0 && treeHasDesktopBinary(filepath.Join(versionsDir, name)) {
			// No path attribution, but a desktop-binary process with an
			// unreadable image path is live somewhere: this tree plausibly
			// hosts it, so it is kept (conservative; the worst case is a tree
			// kept with a suspect note until the user resolves it).
			pids = suspectPids
			suspected = true
		}
		if len(pids) == 0 && !suspected {
			delete(ledger.Rounds, name) // free again: history resets
			continue
		}
		occ := occupiedVersionFrom(name, pids, suspected, ledger)
		if occ.Escalated {
			slog.Warn("installlayout: version tree kept, still occupied by a running process",
				"version", occ.Version, "pids", occ.PIDs, "rounds", occ.Rounds, "hint", OccupiedEscalateHint)
		} else {
			slog.Info("installlayout: version tree kept, occupied", "version", occ.Version,
				"pids", occ.PIDs, "suspected", occ.Suspected, "rounds", occ.Rounds)
		}
		occupied = append(occupied, occ)
	}

	if len(ledger.Rounds) == 0 {
		removeOccupancyLedger(versionsDir)
	} else {
		saveOccupancyLedger(versionsDir, ledger)
	}
	sort.Slice(occupied, func(i, j int) bool { return occupied[i].Version > occupied[j].Version })
	return occupied
}
