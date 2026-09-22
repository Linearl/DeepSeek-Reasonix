package claimcheck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"dev.workflow.yaml",
		"routes/dev/README.md",
		"routes/dev/dev.workflow.yaml",
		"node_modules/pkg/dev.workflow.yaml", // black hole: must NOT count
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestMissingClaimRefutedByAnyHit(t *testing.T) {
	root := writeTree(t)
	res, err := Check("missing", "", "dev.workflow.yaml", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Refuted || res.TotalHits != 2 {
		t.Fatalf("verdict=%v hits=%d (want REFUTED/2: node_modules excluded)", res.Verdict, res.TotalHits)
	}
	if res.Uncertain {
		t.Fatal("clean walk must not be uncertain")
	}
}

func TestMissingClaimConfirmedWhenNothingMatches(t *testing.T) {
	root := writeTree(t)
	res, err := Check("missing", "", "no-such-file.xyz", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Confirmed || res.TotalHits != 0 {
		t.Fatalf("verdict=%v hits=%d", res.Verdict, res.TotalHits)
	}
}

func TestExistsClaimMirrorsMissing(t *testing.T) {
	root := writeTree(t)
	res, err := Check("exists", filepath.Join(root, "dev.workflow.yaml"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Confirmed || res.TotalHits != 1 {
		t.Fatalf("verdict=%v hits=%d", res.Verdict, res.TotalHits)
	}
	missing, err := Check("exists", filepath.Join(root, "ghost.txt"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Verdict != Refuted {
		t.Fatalf("exists on absent path = %v", missing.Verdict)
	}
}

func TestCaseSensitivePatternOnEveryPlatform(t *testing.T) {
	root := writeTree(t)
	res, err := Check("missing", "", "DEV.WORKFLOW.YAML", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Confirmed || res.TotalHits != 0 {
		t.Fatalf("case-insensitive match leaked: verdict=%v hits=%d", res.Verdict, res.TotalHits)
	}
}

func TestUnreadableEntryForcesUncertain(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "target.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot lock directory on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	// Windows ACLs may ignore the POSIX mode bits for the owner; only run the
	// assertion when the lock actually took (POSIX semantics). The WalkDir
	// error-counting branch itself is platform-independent code.
	if entries, readErr := os.ReadDir(locked); readErr == nil {
		_ = entries
		t.Skip("platform ignores mode 0o000 for the owner (Windows ACL); unreadable-dir branch needs POSIX")
	}
	res, err := Check("missing", "", "target.yaml", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Uncertain || res.Skipped == 0 {
		t.Fatalf("skipped=%d uncertain=%v; an unreadable dir must refuse a clean verdict", res.Skipped, res.Uncertain)
	}
}

func TestUnusableBaseIsReportedNotFatal(t *testing.T) {
	root := writeTree(t)
	res, err := Check("missing", "", "dev.workflow.yaml", []string{filepath.Join(root, "ghost-dir"), root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Refuted {
		t.Fatalf("usable base still counts: %v", res.Verdict)
	}
	var unusable *TriedEntry
	for i := range res.Tried {
		if res.Tried[i].Kind == "base" && !res.Tried[i].Usable {
			unusable = &res.Tried[i]
		}
	}
	if unusable == nil {
		t.Fatal("unusable base missing from tried list")
	}
}

func TestUsageErrors(t *testing.T) {
	if _, err := Check("missing", "", "", nil); !errors.Is(err, ErrUsage) {
		t.Fatalf("empty invocation = %v", err)
	}
	if _, err := Check("missing", "a.txt", "p.yaml", []string{"."}); !errors.Is(err, ErrUsage) {
		t.Fatalf("path+pattern = %v", err)
	}
	if _, err := Check("wrong", "a.txt", "", nil); !errors.Is(err, ErrUsage) {
		t.Fatalf("bad claim = %v", err)
	}
}

func TestResultJSONCarriesScopeNote(t *testing.T) {
	root := writeTree(t)
	res, err := Check("missing", "", "dev.workflow.yaml", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(blob), "existence-only") {
		t.Fatal("scope note missing from serialized verdict")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
