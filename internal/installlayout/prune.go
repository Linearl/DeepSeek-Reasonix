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

// PruneVersionTrees deletes version trees under <installRoot>/versions so that
// at most the newest `keep` trees (name order, newest first — the same order
// the version picker shows) survive, plus the tree current.json points at,
// which is hard-skipped even when it is among the oldest. Staging directories
// and entries that are not valid version names are never touched. keep < 0 is
// an error; keep == 0 prunes everything except the active tree. Returns the
// names that were deleted, in deletion order. A missing versions/ directory is
// not an error — there is simply nothing to prune.
func PruneVersionTrees(installRoot string, keep int) ([]string, error) {
	if keep < 0 {
		return nil, fmt.Errorf("installlayout: prune keep must be >= 0, got %d", keep)
	}
	versionsDir := filepath.Join(installRoot, VersionsDirName)
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("installlayout: read versions directory: %w", err)
	}

	// The active version is untouchable: the launcher boots whatever
	// current.json names, so pruning it would strand the next start. A missing
	// or unreadable pointer means there is nothing to hard-skip; that is the
	// portable/flat-layout case where pruning valid trees is still safe.
	active := ""
	if ptr, ptrErr := ReadCurrent(installRoot); ptrErr == nil {
		active = ptr.ActiveVersion
	}

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

	var pruned []string
	for i, name := range trees {
		if i < keep || name == active {
			continue
		}
		if err := os.RemoveAll(filepath.Join(versionsDir, name)); err != nil {
			return pruned, fmt.Errorf("installlayout: prune version %s: %w", name, err)
		}
		pruned = append(pruned, name)
	}
	return pruned, nil
}
