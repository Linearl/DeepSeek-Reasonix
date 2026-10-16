package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Task 919: the parse cache must never trade freshness for speed. These tests
// pin the four freshness edges — external edit, store mutation, sidecar edit,
// and shared-cache isolation — plus the warning-replay behavior that keeps
// repeated List/Read calls identical to repeated full parses.

func newCacheStore(t *testing.T, home string, stderr *bytes.Buffer, shared *ParseCache) *Store {
	t.Helper()
	return New(Options{
		HomeDir:         home,
		DisableBuiltins: true,
		Stderr:          stderr,
		ParseCache:      shared,
	})
}

func TestParseCacheSeesExternalEdit(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".reasonix/skills/edit.md", "---\ndescription: v1\n---\nbody v1")
	var stderr bytes.Buffer
	st := newCacheStore(t, home, &stderr, nil)

	sk, ok := st.Read("edit")
	if !ok || sk.Description != "v1" {
		t.Fatalf("first read = %+v, %v; want v1", sk, ok)
	}
	writeSkill(t, home, ".reasonix/skills/edit.md", "---\ndescription: v2\n---\nbody v2")
	sk, ok = st.Read("edit")
	if !ok {
		t.Fatal("edited skill must stay discoverable")
	}
	if sk.Description != "v2" || sk.Body != "body v2" {
		t.Fatalf("stale cache served edit: desc=%q body=%q", sk.Description, sk.Body)
	}
}

func TestParseCacheSeesStoreMutation(t *testing.T) {
	home := t.TempDir()
	var stderr bytes.Buffer
	st := newCacheStore(t, home, &stderr, nil)
	if _, ok := st.Read("fresh"); ok {
		t.Fatal("skill must not exist before CreateWithContent")
	}
	if _, err := st.CreateWithContent("fresh", ScopeGlobal, "---\ndescription: created\n---\nnew body"); err != nil {
		t.Fatalf("CreateWithContent: %v", err)
	}
	sk, ok := st.Read("fresh")
	if !ok || sk.Description != "created" || sk.Body != "new body" {
		t.Fatalf("created skill invisible to cached Read: %+v, %v", sk, ok)
	}
	if err := st.Delete("fresh", ScopeGlobal); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := st.Read("fresh"); ok {
		t.Fatal("deleted skill must disappear from cached Read")
	}
}

func TestParseCacheSeesReferenceEdit(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".reasonix", "skills", "withrefs")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, home, ".reasonix/skills/withrefs/SKILL.md", "---\ndescription: d\n---\nmain")
	refPath := filepath.Join(dir, "references", "notes.md")
	if err := os.WriteFile(refPath, []byte("reference v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	st := newCacheStore(t, home, &stderr, nil)
	sk, ok := st.Read("withrefs")
	if !ok || !strings.Contains(sk.Body, "reference v1") {
		t.Fatalf("first read body = %q, %v", sk.Body, ok)
	}
	// A references-only rewrite never touches SKILL.md — the sidecar signature
	// (not the SKILL.md stat) is what must catch it.
	if err := os.WriteFile(refPath, []byte("reference v2 longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, _ = st.Read("withrefs")
	if !strings.Contains(sk.Body, "reference v2 longer") {
		t.Fatalf("reference edit not picked up, body = %q", sk.Body)
	}
}

func TestParseCacheReplaysMissingDescriptionWarning(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".reasonix/skills/nodesc.md", "---\nname: nodesc\n---\nbody")
	var stderr bytes.Buffer
	st := newCacheStore(t, home, &stderr, nil)

	st.List()
	if !strings.Contains(stderr.String(), "has no description") {
		t.Fatalf("first List should warn, got %q", stderr.String())
	}
	stderr.Reset()
	st.Read("nodesc")
	if !strings.Contains(stderr.String(), "has no description") {
		t.Fatalf("cached Read must replay the warning, got %q", stderr.String())
	}
}

func TestParseCacheSharedAcrossStoresButIsolatedByPath(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	writeSkill(t, homeA, ".reasonix/skills/shared.md", "---\ndescription: A\n---\nbody A")
	writeSkill(t, homeB, ".reasonix/skills/shared.md", "---\ndescription: B\n---\nbody B")

	shared := NewParseCache()
	var stderrA, stderrB bytes.Buffer
	stA := newCacheStore(t, homeA, &stderrA, shared)
	stB := newCacheStore(t, homeB, &stderrB, shared)

	skA, _ := stA.Read("shared")
	skB, _ := stB.Read("shared")
	if skA.Description != "A" || skB.Description != "B" {
		t.Fatalf("shared cache leaked across paths: A=%q B=%q", skA.Description, skB.Description)
	}
}

func TestParseCacheConcurrentReads(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".reasonix/skills/par.md", "---\ndescription: par\n---\nbody")
	var stderr bytes.Buffer
	st := newCacheStore(t, home, &stderr, nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				if sk, ok := st.Read("par"); !ok || sk.Description != "par" {
					t.Errorf("concurrent Read = %+v, %v", sk, ok)
					return
				}
				st.List()
			}
		}()
	}
	wg.Wait()
}

func TestParseCacheNotStoresTransientReadErrors(t *testing.T) {
	// A missing file must not poison the cache: the entry is keyed off a stat
	// that fails, so nothing is written and the next scan retries.
	home := t.TempDir()
	writeSkill(t, home, ".reasonix/skills/gone/SKILL.md", "---\ndescription: gone\n---\nbody")
	var stderr bytes.Buffer
	st := newCacheStore(t, home, &stderr, nil)
	if _, ok := st.Read("gone"); !ok {
		t.Fatal("first read must succeed")
	}
	path := filepath.Join(home, ".reasonix", "skills", "gone", SkillFile)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := st.Read("gone"); ok {
		t.Fatal("removed skill must disappear (delete removes the dir, not just the file)")
	}
	// Re-create with different content: must be served fresh, not from any
	// negative entry.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ndescription: back\n---\nback body"), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, ok := st.Read("gone")
	if !ok || sk.Description != "back" {
		t.Fatalf("recreated skill = %+v, %v; want description=back", sk, ok)
	}
}
