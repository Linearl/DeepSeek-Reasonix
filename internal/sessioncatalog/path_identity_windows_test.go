//go:build windows

package sessioncatalog

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWindowsCatalogPathIdentityFoldsPerDirectory(t *testing.T) {
	caseSensitiveParent := filepath.Clean(`C:\Root`)
	identity := func(path string) string {
		return windowsCatalogPathIdentityBy(path, func(directory string) (bool, bool) {
			return !strings.EqualFold(filepath.Clean(directory), caseSensitiveParent), true
		})
	}

	upper := identity(`C:\ROOT\Foo\LEAF.jsonl`)
	lower := identity(`c:\root\foo\leaf.jsonl`)
	if upper == lower {
		t.Fatalf("case-sensitive child names collapsed to %q", upper)
	}
	if got, want := upper, filepath.Clean(`c:\root\Foo\leaf.jsonl`); got != want {
		t.Fatalf("segment-aware identity = %q, want %q", got, want)
	}
}

// TestWindowsDirectoryCaseInsensitiveCachedProbesOncePerTTL pins the tl1526
// memo: the per-component CreateFile case probe runs once per directory per
// TTL, not once per session record.
func TestWindowsDirectoryCaseInsensitiveCachedProbesOncePerTTL(t *testing.T) {
	probes := 0
	var probeMu sync.Mutex
	previousProbe := windowsCaseProbe
	windowsCaseProbe = func(directory string) (bool, bool) {
		probeMu.Lock()
		probes++
		probeMu.Unlock()
		return true, true
	}
	defer func() { windowsCaseProbe = previousProbe }()

	windowsCaseMu.Lock()
	windowsCaseCache = map[string]windowsCaseEntry{}
	windowsCaseMu.Unlock()

	dir := t.TempDir()
	for range 3 {
		insensitive, known := windowsDirectoryCaseInsensitiveCached(dir)
		if !known {
			t.Fatalf("cached probe must report the result as known")
		}
		if !insensitive {
			t.Fatalf("fixture directory reported case-sensitive")
		}
	}
	probeMu.Lock()
	defer probeMu.Unlock()
	if probes != 1 {
		t.Fatalf("case probe ran %d times for one directory within the TTL, want 1", probes)
	}
}

// TestWindowsDirectoryCaseInsensitiveCachedExpires pins the TTL: after expiry
// the directory is probed again, so a flipped case-sensitivity flag is picked up.
func TestWindowsDirectoryCaseInsensitiveCachedExpires(t *testing.T) {
	probes := 0
	var probeMu sync.Mutex
	previousProbe := windowsCaseProbe
	windowsCaseProbe = func(directory string) (bool, bool) {
		probeMu.Lock()
		probes++
		probeMu.Unlock()
		return false, true
	}
	defer func() { windowsCaseProbe = previousProbe }()

	windowsCaseMu.Lock()
	windowsCaseCache = map[string]windowsCaseEntry{
		t.TempDir(): {insensitive: true, known: true, expiresAt: time.Now().Add(-time.Second)},
	}
	windowsCaseMu.Unlock()

	dir := t.TempDir()
	if _, known := windowsDirectoryCaseInsensitiveCached(dir); !known {
		t.Fatalf("expired entry must be re-probed, not served as known")
	}
	probeMu.Lock()
	defer probeMu.Unlock()
	if probes != 1 {
		t.Fatalf("expired entry must trigger exactly one fresh probe, got %d", probes)
	}
}
