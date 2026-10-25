package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TestingM is the subset of testing.M needed by RunWithIsolatedUserState.
type TestingM interface {
	Run() int
}

// StaleIsolatedHomeMaxAge is the age after which a leftover isolated test
// home directory is considered stale. Residue is only expected when a test
// run is interrupted before its cleanup runs (killed process, panic, or a
// Windows file lock that makes os.RemoveAll fail silently), so anything this
// old cannot belong to a live run and is reclaimed by the startup sweep.
const StaleIsolatedHomeMaxAge = 24 * time.Hour

// isolatedHomePrefixes are the os.MkdirTemp name prefixes IsolateUserState
// can produce: the dot-prefixed pattern is created under the caller's home
// directory, the bare pattern is the fallback created under the system
// temporary directory.
var isolatedHomePrefixes = []string{
	".reasonix-test-home-",
	"reasonix-test-home-",
}

func matchesIsolatedHomePattern(name string) bool {
	for _, prefix := range isolatedHomePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// SweepStaleIsolatedHomes removes leftover isolated test home directories
// under root whose modification time is older than maxAge. Only directories
// whose names match the IsolateUserState temp patterns are considered;
// files, symlinks, unrelated entries and anything not provably stale are
// left untouched. It returns the number of directories removed; failures on
// individual entries are reported to stderr and do not abort the sweep.
func SweepStaleIsolatedHomes(root string, maxAge time.Duration) int {
	if root == "" || maxAge <= 0 {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !matchesIsolatedHomePattern(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		target := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(os.Stderr, "testenv: remove stale isolated test home %s: %v\n", target, err)
			continue
		}
		removed++
	}
	return removed
}

// sweepStaleIsolatedHomes reclaims residue from previous runs in the two
// locations IsolateUserState may have created it: the user's home directory
// and the system temporary directory. It runs at the start of every test
// binary that installs the isolation guard (test-side only, never a product
// startup path), so residue from an interrupted run is picked up by the next
// run instead of accumulating forever.
func sweepStaleIsolatedHomes() {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		SweepStaleIsolatedHomes(home, StaleIsolatedHomeMaxAge)
	}
	SweepStaleIsolatedHomes(os.TempDir(), StaleIsolatedHomeMaxAge)
}

type savedEnvironment struct {
	value string
	set   bool
}

// IsolateUserState redirects default user-scoped Reasonix paths to a disposable
// home and clears inherited explicit path overrides. Tests may still override
// any of these variables for a focused scenario after this process-level guard
// is installed.
func IsolateUserState() (func(), error) {
	sweepStaleIsolatedHomes()
	originalHome, _ := os.UserHomeDir()
	home, err := os.MkdirTemp(originalHome, ".reasonix-test-home-*")
	if err != nil && originalHome != "" {
		home, err = os.MkdirTemp("", "reasonix-test-home-*")
	}
	if err != nil {
		return nil, fmt.Errorf("create isolated test home: %w", err)
	}

	set := map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"AppData":         filepath.Join(home, "AppData", "Roaming"),
		"LocalAppData":    filepath.Join(home, "AppData", "Local"),
	}
	unset := []string{
		"REASONIX_HOME",
		"REASONIX_STATE_HOME",
		"REASONIX_CACHE_HOME",
	}

	saved := make(map[string]savedEnvironment, len(set)+len(unset))
	remember := func(key string) {
		if _, ok := saved[key]; ok {
			return
		}
		value, ok := os.LookupEnv(key)
		saved[key] = savedEnvironment{value: value, set: ok}
	}
	restore := func() {
		for key, previous := range saved {
			if previous.set {
				_ = os.Setenv(key, previous.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
	fail := func(cause error) (func(), error) {
		restore()
		_ = os.RemoveAll(home)
		return nil, cause
	}

	for key, value := range set {
		remember(key)
		if err := os.Setenv(key, value); err != nil {
			return fail(fmt.Errorf("set %s for isolated test home: %w", key, err))
		}
	}
	for _, key := range unset {
		remember(key)
		if err := os.Unsetenv(key); err != nil {
			return fail(fmt.Errorf("unset %s for isolated test home: %w", key, err))
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			restore()
			_ = os.RemoveAll(home)
		})
	}, nil
}

// RunWithIsolatedUserState runs a package test binary behind the user-state
// isolation guard and exits with the test result.
func RunWithIsolatedUserState(m TestingM) {
	cleanup, err := IsolateUserState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// cleanup is once-guarded, so the deferred call only covers the panic
	// path (a panic out of m.Run would otherwise skip the explicit cleanup
	// below and leak the isolated home); the normal path removes the home
	// before os.Exit, and os.Exit itself never unwinds defers.
	defer cleanup()
	exitCode := m.Run()
	cleanup()
	os.Exit(exitCode)
}
