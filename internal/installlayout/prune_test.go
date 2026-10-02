package installlayout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writePruneFixture builds a versions/ tree with the given version names plus
// noise that must never be pruned (a staging dir, a junk file), and points
// current.json at active ("" writes no pointer at all).
func writePruneFixture(t *testing.T, versions []string, active string) string {
	t.Helper()
	root := t.TempDir()
	versionsDir := filepath.Join(root, VersionsDirName)
	if err := os.MkdirAll(filepath.Join(versionsDir, StagingDirName("v1.0.0", "nonce")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionsDir, "not-a-version.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		dir := filepath.Join(versionsDir, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, DesktopBinaryName()), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if active != "" {
		body, err := json.Marshal(CurrentPointer{SchemaVersion: CurrentSchemaVersion, ActiveVersion: active, ActiveDir: VersionDirRelative(active)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, CurrentFileName), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func remainingVersions(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, VersionsDirName))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if e.IsDir() && ValidateVersionName(e.Name()) == nil {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)
	return got
}

// Task 411 acceptance: newest N survive plus the current.json target, even
// when that target is the oldest tree on disk.
func TestPruneVersionTreesKeepsNewestNPlusCurrent(t *testing.T) {
	versions := []string{"v1.38.0", "v1.38.1", "v1.38.2", "v1.38.3", "v1.38.3-20260928-1000", "v1.38.3-20260929-1100", "v1.38.3-20260930-1200"}
	root := writePruneFixture(t, versions, "v1.38.0") // current points at the OLDEST

	pruned, err := PruneVersionTrees(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	// Newest-name first: the two trees beyond keep that are NOT the pointer
	// target go, in name-descending order; the oldest (v1.38.0) survives
	// because current.json points at it.
	wantPruned := []string{"v1.38.1"}
	if strings.Join(pruned, ",") != strings.Join(wantPruned, ",") {
		t.Fatalf("pruned=%v want %v (beyond keep, minus current, deletion order)", pruned, wantPruned)
	}
	got := remainingVersions(t, root)
	wantLeft := []string{"v1.38.0", "v1.38.2", "v1.38.3", "v1.38.3-20260928-1000", "v1.38.3-20260929-1100", "v1.38.3-20260930-1200"}
	if strings.Join(got, ",") != strings.Join(wantLeft, ",") {
		t.Fatalf("remaining=%v want %v (newest 5 + current)", got, wantLeft)
	}
	// Noise is never pruned.
	if _, err := os.Stat(filepath.Join(root, VersionsDirName, ".staging-v1.0.0-nonce")); err != nil {
		t.Fatalf("staging dir must survive pruning: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, VersionsDirName, "not-a-version.txt")); err != nil {
		t.Fatalf("junk file must survive pruning: %v", err)
	}
}

// When current.json already names one of the newest N, the guarantee is "at
// most keep trees": no extra slot is reserved for the active version.
func TestPruneVersionTreesCurrentAmongKept(t *testing.T) {
	versions := []string{"v1.37.0", "v1.38.0", "v1.38.1", "v1.38.2", "v1.38.3", "v1.38.3-20261001-0900"}
	root := writePruneFixture(t, versions, "v1.38.3-20261001-0900")

	pruned, err := PruneVersionTrees(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != "v1.37.0" {
		t.Fatalf("pruned=%v want exactly the single oldest tree", pruned)
	}
	got := remainingVersions(t, root)
	if len(got) != 5 {
		t.Fatalf("remaining=%v want exactly 5 trees", got)
	}
}

// No current.json (flat/portable checkout): the rule degenerates to "keep the
// newest N", which is still safe because the launcher cannot boot any tree
// from such a layout anyway.
func TestPruneVersionTreesWithoutPointer(t *testing.T) {
	versions := []string{"v1.38.0", "v1.38.1", "v1.38.2"}
	root := writePruneFixture(t, versions, "")

	pruned, err := PruneVersionTrees(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pruned, ",") != "v1.38.1,v1.38.0" {
		t.Fatalf("pruned=%v want the two oldest (deletion order)", pruned)
	}
	if got := remainingVersions(t, root); strings.Join(got, ",") != "v1.38.2" {
		t.Fatalf("remaining=%v want only the newest", got)
	}
}

// keep == 0 prunes everything except the pointer target; negative keep is a
// programming error and must refuse loudly instead of nuking versions/.
func TestPruneVersionTreesKeepEdges(t *testing.T) {
	root := writePruneFixture(t, []string{"v1.38.0", "v1.38.1"}, "v1.38.0")
	if pruned, err := PruneVersionTrees(root, 0); err != nil || len(pruned) != 1 {
		t.Fatalf("keep=0 must prune all but current, got pruned=%v err=%v", pruned, err)
	}
	if got := remainingVersions(t, root); strings.Join(got, ",") != "v1.38.0" {
		t.Fatalf("remaining=%v want only the current tree", got)
	}
	if _, err := PruneVersionTrees(root, -1); err == nil || !strings.Contains(err.Error(), "keep must be >= 0") {
		t.Fatalf("negative keep must error, got %v", err)
	}
}

// A missing versions/ directory is the nothing-to-do case, not a failure —
// the post-build prune must never turn a flat checkout into a failed build.
func TestPruneVersionTreesMissingVersionsDir(t *testing.T) {
	root := t.TempDir()
	pruned, err := PruneVersionTrees(root, 5)
	if err != nil || pruned != nil {
		t.Fatalf("missing versions/ must be a no-op, got pruned=%v err=%v", pruned, err)
	}
}
