package testenv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepStaleIsolatedHomes(t *testing.T) {
	root := t.TempDir()
	staleAge := StaleIsolatedHomeMaxAge + time.Hour

	staleDotted := filepath.Join(root, ".reasonix-test-home-stale")
	staleBare := filepath.Join(root, "reasonix-test-home-stale-fallback")
	freshDotted := filepath.Join(root, ".reasonix-test-home-fresh")
	unrelatedStale := filepath.Join(root, "unrelated-stale-dir")
	staleFile := filepath.Join(root, ".reasonix-test-home-looks-like-dir")

	for _, dir := range []string{staleDotted, staleBare, freshDotted, unrelatedStale} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(staleFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-staleAge)
	for _, path := range []string{staleDotted, staleBare, unrelatedStale, staleFile} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	removed := SweepStaleIsolatedHomes(root, StaleIsolatedHomeMaxAge)
	if removed != 2 {
		t.Fatalf("SweepStaleIsolatedHomes removed %d entries, want 2 (the stale dotted and bare homes)", removed)
	}
	if _, err := os.Stat(staleDotted); !os.IsNotExist(err) {
		t.Fatalf("stale dotted home still present after sweep: %v", err)
	}
	if _, err := os.Stat(staleBare); !os.IsNotExist(err) {
		t.Fatalf("stale bare fallback home still present after sweep: %v", err)
	}
	// 新残留不误删：运行中的隔离 home 是新建的，mtime 远新于阈值。
	if _, err := os.Stat(freshDotted); err != nil {
		t.Fatalf("fresh (live) isolated home must not be swept: %v", err)
	}
	// 非匹配目录不误删。
	if _, err := os.Stat(unrelatedStale); err != nil {
		t.Fatalf("unrelated stale directory must not be swept: %v", err)
	}
	// 同名文件不误删。
	if _, err := os.Stat(staleFile); err != nil {
		t.Fatalf("file with matching name must not be swept: %v", err)
	}
}

func TestSweepStaleIsolatedHomesGuards(t *testing.T) {
	root := t.TempDir()
	matching := filepath.Join(root, ".reasonix-test-home-stale")
	if err := os.Mkdir(matching, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-StaleIsolatedHomeMaxAge - time.Hour)
	if err := os.Chtimes(matching, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	if removed := SweepStaleIsolatedHomes("", StaleIsolatedHomeMaxAge); removed != 0 {
		t.Fatalf("empty root must be a no-op, removed %d", removed)
	}
	if removed := SweepStaleIsolatedHomes(root, 0); removed != 0 {
		t.Fatalf("non-positive maxAge must be a no-op, removed %d", removed)
	}
	if removed := SweepStaleIsolatedHomes(filepath.Join(root, "does-not-exist"), StaleIsolatedHomeMaxAge); removed != 0 {
		t.Fatalf("missing root must be a no-op, removed %d", removed)
	}
	if _, err := os.Stat(matching); err != nil {
		t.Fatalf("guard cases must not remove the stale home: %v", err)
	}
}
