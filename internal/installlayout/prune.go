package installlayout

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Retention pruning for versions/ (task 411).
//
// Local installer builds publish a new version tree on every run and nothing
// deleted them, so versions/ regrew to 40 trees / 6.2GB between manual
// cleanups. This file holds the single tested implementation of the retention
// rule — "keep the newest N trees plus whatever current.json points at" — and
// both consumers go through it: the UI's DeleteInstalledVersion handles
// one-off deletions, and tools/prune-versions (called from
// scripts/build-local-installer.sh after every build) applies the rule
// automatically. The bash side deliberately contains no retention logic; it
// only forwards -root/-keep, so the unit tests in prune_test.go are the
// authority for the behavior.
//
// Task 1259 added the third hard-skip next to `keep` and the pointer target:
// a tree a live process still executes from is kept (and reported in the
// retained return) instead of deleted — the 2026-10-29 incident had pruning
// gut a running elevated instance's tree, leaving a ghost process (live PID,
// exe gone) that normal-privilege tools could not kill. Occupancy state
// across passes lives in the versions/.occupied-ledger.json ledger, and a
// tree that stays occupied for OccupiedEscalateAfterRounds consecutive passes
// surfaces "handle this yourself" on every consumer.

// PruneVersionTrees deletes version trees under <installRoot>/versions so that
// at most the newest `keep` trees (name order, newest first — the same order
// the version picker shows) survive, plus the tree current.json points at,
// which is hard-skipped even when it is among the oldest. Staging directories
// and entries that are not valid version names are never touched. keep < 0 is
// an error; keep == 0 prunes everything except the active tree.
//
// Trees a live process still executes from (process enumeration with
// image-path attribution, plus a same-named-unattributable fallback for
// unreadable image paths) are never deleted: PruneVersionTrees returns them
// in retained with per-tree consecutive-round counts. The tree this process
// itself runs from is hard-skipped the same way — past a pointer swap the
// running tree may differ from the active one.
// Returns the names that were deleted, in deletion order, followed by the
// occupied trees that were kept. A missing versions/ directory is not an
// error — there is simply nothing to prune.
func PruneVersionTrees(installRoot string, keep int) ([]string, []OccupiedVersion, error) {
	if keep < 0 {
		return nil, nil, fmt.Errorf("installlayout: prune keep must be >= 0, got %d", keep)
	}
	versionsDir := filepath.Join(installRoot, VersionsDirName)
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("installlayout: read versions directory: %w", err)
	}

	// The active version is untouchable: the launcher boots whatever
	// current.json names, so pruning it would strand the next start. A missing
	// or unreadable pointer means there is nothing to hard-skip; that is the
	// portable/flat-layout case where pruning valid trees is still safe.
	active := ""
	if ptr, ptrErr := ReadCurrent(installRoot); ptrErr == nil {
		active = ptr.ActiveVersion
	}
	// The tree under our own feet gets the same protection: the desktop's
	// retention chain can run while running != active (past a switch).
	running := RunningVersionName()

	var trees []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := ValidateVersionName(entry.Name()); err != nil {
			continue // staging dirs (.staging-*) and junk stay untouched
		}
		trees = append(trees, entry.Name())
	}
	// Newest-name first; fork names carry -YYYYMMDD-HHMM so name order is
	// chronological (same sort the picker relies on).
	sort.Slice(trees, func(i, j int) bool { return trees[i] > trees[j] })

	// Occupancy is decided for every tree beyond the keep rule up front so one
	// enumeration serves the whole pass; locked-but-unattributed trees fall
	// back to the exclusive-open probe inside pruneOccupiedNames.
	var candidates []string
	for i, name := range trees {
		if i < keep || name == active || (running != "" && name == running) {
			continue
		}
		candidates = append(candidates, name)
	}
	occupied := pruneOccupiedNames(installRoot, versionsDir, candidates, trees)
	occupiedByName := make(map[string]OccupiedVersion, len(occupied))
	for _, occ := range occupied {
		occupiedByName[occ.Version] = occ
	}

	var pruned []string
	var retained []OccupiedVersion
	var removeErr error
	for _, name := range candidates {
		if occ, isOccupied := occupiedByName[name]; isOccupied {
			retained = append(retained, occ)
			continue
		}
		if err := os.RemoveAll(filepath.Join(versionsDir, name)); err != nil {
			// Backstop (task 1259): the OS refused a deletion the probes did
			// not foresee (a process raced into the tree, or an opaque
			// protection). Keep the tree — counting it occupied so the next
			// pass skips it — and keep pruning the remaining candidates, so
			// one locked tree no longer stalls the whole rule.
			retained = append(retained, markDeletionRefused(name, versionsDir))
			if removeErr == nil {
				removeErr = fmt.Errorf("installlayout: prune version %s: %w", name, err)
			}
			continue
		}
		pruned = append(pruned, name)
	}
	return pruned, retained, removeErr
}
