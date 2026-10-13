package installlayout

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 任务835: the updater-side retention rule. The build side already prunes
// (tools/prune-versions keep=5 after every build), but the desktop update
// chain never did — local rebuilds publish one tree per run, so versions/
// regrew without bound between manual cleanups (实测 2026-10-12: 7 trees,
// 1.3GB + 91MB of .replaced- shells + 164MB of quarantine nodes).
//
// The rule (user ruling 2026-10-12): keep the current running version plus at
// most three historical trees — four version directories in total — plus the
// staging payload. Everything older, and every *.replaced-* shell, is removed
// once the update is confirmed. "Confirmed" is enforced by the CALLER, not
// here: the desktop entry runs only after the new version's own startup has
// clearly succeeded (the process is alive and standing up its services), so a
// failed update never loses its rollback source. A wrong drop is also bounded
// by construction: the newest three trees always survive, which spans the
// one-version rollback window several times over.
//
// current.json and the staging payload itself are never touched: the prune
// keeps whatever the pointer names, and the staging cleanup only removes
// .staging-* scratch directories older than the retention window (a fresh
// staged build waiting to be published survives).

// retentionStaleAge is the grace window for update debris (.replaced- shells,
// .staging- scratch dirs, quarantined nodes). These are crash leftovers by
// construction — a completed activate removes its own backup — so anything
// older than a day has no rollback value left.
const retentionStaleAge = 24 * time.Hour

// RetentionKeepHistory is how many historical (non-active) version trees the
// rule keeps. Three plus the active tree gives the four-directory ceiling the
// ruling fixed.
const RetentionKeepHistory = 3

// RetentionReport names what one retention pass removed, for the log line and
// the tests.
type RetentionReport struct {
	PrunedTrees        []string // version directories removed (over the keep rule)
	RemovedReplaced    []string // *.replaced-<nonce> backup shells removed
	RemovedStaging     []string // .staging-* scratch directories removed
	RemovedQuarantined []string // quarantined update-backup nodes removed
}

// Empty reports whether the pass removed nothing.
func (r RetentionReport) Empty() bool {
	return len(r.PrunedTrees) == 0 && len(r.RemovedReplaced) == 0 &&
		len(r.RemovedStaging) == 0 && len(r.RemovedQuarantined) == 0
}

// EnforceVersionRetention applies the 任务835 rule to one install root:
// at most RetentionKeepHistory historical trees + the active tree survive,
// and update debris older than a day (.replaced- shells, .staging- scratch
// directories, quarantined update backups) is removed. Staging payloads,
// current.json, and the active tree are never touched. A missing versions/
// directory is not an error. Best-effort by design: every sub-pass logs its
// own failure and the pass reports what it removed so far.
func EnforceVersionRetention(installRoot string) RetentionReport {
	report := RetentionReport{}
	if pruned, err := PruneVersionTrees(installRoot, RetentionKeepHistory); err != nil {
		slog.Warn("installlayout: retention prune", "err", err)
	} else {
		report.PrunedTrees = pruned
	}
	replacedBefore := listReplacedShellNames(installRoot)
	if err := CleanupStaleReplacedVersions(installRoot, retentionStaleAge); err != nil {
		slog.Warn("installlayout: retention replaced cleanup", "err", err)
	}
	report.RemovedReplaced = diffNames(replacedBefore, listReplacedShellNames(installRoot))
	stagingBefore := listStagingScratchNames(installRoot)
	if err := CleanupStaleStaging(installRoot, retentionStaleAge); err != nil {
		slog.Warn("installlayout: retention staging cleanup", "err", err)
	}
	report.RemovedStaging = diffNames(stagingBefore, listStagingScratchNames(installRoot))
	report.RemovedQuarantined = CleanupStaleQuarantinedNodes(installRoot, retentionStaleAge)
	if !report.Empty() {
		slog.Info("installlayout: versions retention applied",
			"prunedTrees", report.PrunedTrees,
			"replacedShells", report.RemovedReplaced,
			"stagingScratch", report.RemovedStaging,
			"quarantinedNodes", report.RemovedQuarantined)
	}
	return report
}

// CleanupStaleQuarantinedNodes removes quarantined update-backup nodes under
// the install root (including versions/) older than maxAge. Two name shapes
// exist: the mac handoff backup quarantine (<path>.reasonix-orphaned-<ts>) and
// the repair config quarantine (<path>.reasonix-quarantine-<ts>). Both are
// siblings of the node they preserved, so a shallow walk of the install root
// and versions/ covers them; %APPDATA% config quarantines belong to the repair
// mechanism's own recovery semantics and are deliberately out of scope here.
// Symlinks are never followed or removed. The returned names are the nodes
// this call removed, in walk order.
func CleanupStaleQuarantinedNodes(installRoot string, maxAge time.Duration) []string {
	installRoot, err := cleanInstallRoot(installRoot)
	if err != nil {
		return nil
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	cutoff := time.Now().Add(-maxAge)
	var removed []string
	roots := []string{installRoot, filepath.Join(installRoot, VersionsDirName)}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.Contains(name, ".reasonix-orphaned-") && !strings.Contains(name, ".reasonix-quarantine-") {
				continue
			}
			path := filepath.Join(root, name)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || info.ModTime().After(cutoff) {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				slog.Debug("installlayout: quarantine node removal failed", "path", path, "err", err)
				continue
			}
			removed = append(removed, name)
		}
	}
	return removed
}

func listReplacedShellNames(installRoot string) []string {
	return listVersionsNamesMatching(installRoot, func(name string) bool {
		return !strings.HasPrefix(name, ".") && strings.Contains(name, ".replaced-")
	})
}

func listStagingScratchNames(installRoot string) []string {
	return listVersionsNamesMatching(installRoot, func(name string) bool {
		return strings.HasPrefix(name, ".staging-")
	})
}

func listVersionsNamesMatching(installRoot string, match func(string) bool) []string {
	root, err := cleanInstallRoot(installRoot)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, VersionsDirName))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if match(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// diffNames returns the names present in before but absent from after, in
// before order — the removal face of one cleanup sub-pass.
func diffNames(before, after []string) []string {
	if len(before) == 0 {
		return nil
	}
	kept := make(map[string]struct{}, len(after))
	for _, name := range after {
		kept[name] = struct{}{}
	}
	out := []string{}
	for _, name := range before {
		if _, ok := kept[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}
