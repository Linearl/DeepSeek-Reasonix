package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// breakIndexRead replaces MEMORY.md with a directory so every read fails with a
// non-NotExist error on every platform. Upstream uses chmod(0) for the same
// purpose, but Windows only maps chmod to the read-only bit — reads keep
// succeeding, which would turn these assertions into a skip. A directory
// carrying the index name fails reads unconditionally (EISDIR / access denied).
func breakIndexRead(t *testing.T, dir string) {
	t.Helper()
	indexPath := filepath.Join(dir, indexFile)
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(indexPath) })
}

func TestStoreSaveReportsUnreadableIndex(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{Name: "alpha", Description: "keep", Type: TypeProject, Body: "body"}); err != nil {
		t.Fatal(err)
	}
	breakIndexRead(t, s.Dir)
	if _, err := s.Save(Memory{Name: "beta", Description: "new", Type: TypeProject, Body: "body"}); err == nil {
		t.Fatal("Save should report the index read error")
	}
	indexPath := filepath.Join(s.Dir, indexFile)
	if info, err := os.Stat(indexPath); err != nil || !info.IsDir() {
		t.Fatalf("Save overwrote an unreadable index: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha.md")); err != nil {
		t.Fatalf("Save disturbed the existing fact: %v", err)
	}
}

func TestStoreArchivePreservesUnreadableIndex(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := s.Save(Memory{Name: name, Description: "keep", Type: TypeProject, Body: "body"}); err != nil {
			t.Fatal(err)
		}
	}
	breakIndexRead(t, s.Dir)
	if _, err := s.Archive("alpha"); err == nil {
		t.Fatal("Archive should report the index read error")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha.md")); err != nil {
		t.Fatalf("Archive moved the fact before checking the index: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, indexFile)); err != nil {
		t.Fatalf("Archive overwrote an unreadable index: %v", err)
	}
}

func TestFlushIndexPreservesUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, indexFile), []byte("# Memory\n\nHand-written note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	breakIndexRead(t, dir)
	if err := flushIndexIn(dir, map[string]string{"beta": "- [beta](beta.md) — new"}); err == nil {
		t.Fatal("flushIndexIn should report the index read error")
	}
}
