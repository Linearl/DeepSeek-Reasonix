package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"reasonix/internal/forkupdate"
)

// TestForkUpgradeCommandThreeStates pins the CLI fork face's contract
// (tl1680, issue #96): the dead upstream gateway era's behavior — a scary
// "release gateway … EOF" error line and exit 1 — is replaced by three states:
// silent degradation on channel failure (exit 0), the positive
// "already latest" answer on a current build, and the newer-release pointer.
func TestForkUpgradeCommandThreeStates(t *testing.T) {
	restore := forkFetchLatest
	t.Cleanup(func() { forkFetchLatest = restore })

	newer := &forkupdate.Release{Tag: "desktop-v1.38.3-20261117-1800", Version: "v1.38.3-20261117-1800"}
	same := &forkupdate.Release{Tag: "desktop-v1.38.3-20261115-0841", Version: "v1.38.3-20261115-0841"}

	t.Run("channel failure degrades silently with exit 0", func(t *testing.T) {
		forkFetchLatest = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return nil, errors.New(`Get "https://crash.reasonix.io/v1/cli/releases/stable/latest.json": EOF`)
		}
		var out, errOut string
		var rc int
		out = captureStdout(t, func() {
			errOut = captureStderr(t, func() {
				rc = forkUpgradeCommand(cliUpgradeSyntax{}, "v1.38.3-20261115-0841", http.DefaultClient)
			})
		})
		if rc != 0 {
			t.Fatalf("a failed check must exit 0, got %d", rc)
		}
		if strings.Contains(out, "release gateway") || strings.Contains(errOut, "release gateway") {
			t.Fatalf("the dead-gateway noise must be gone: out=%q err=%q", out, errOut)
		}
		if !strings.Contains(errOut, "skipped") && !strings.Contains(errOut, "跳过") && !strings.Contains(errOut, "跳過") {
			t.Fatalf("the neutral skip line must explain the check was skipped: %q", errOut)
		}
	})

	t.Run("rate limit degrades with the same neutral line", func(t *testing.T) {
		forkFetchLatest = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return nil, forkupdate.NewRateLimitError("403 Forbidden")
		}
		var rc int
		captureStdout(t, func() {
			errOut := captureStderr(t, func() {
				rc = forkUpgradeCommand(cliUpgradeSyntax{checkOnly: true}, "v1.38.3-20261115-0841", http.DefaultClient)
			})
			if strings.Contains(errOut, "release gateway") {
				t.Fatalf("rate limit must not resurface gateway noise: %q", errOut)
			}
		})
		if rc != 0 {
			t.Fatalf("rate-limited check must exit 0, got %d", rc)
		}
	})

	t.Run("current build answers already latest", func(t *testing.T) {
		forkFetchLatest = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return same, nil
		}
		var out string
		var rc int
		out = captureStdout(t, func() {
			rc = forkUpgradeCommand(cliUpgradeSyntax{}, "v1.38.3-20261115-0841", http.DefaultClient)
		})
		if rc != 0 {
			t.Fatalf("already-latest must exit 0, got %d", rc)
		}
		if !strings.Contains(out, "latest version") && !strings.Contains(out, "已是最新") {
			t.Fatalf("the up-to-date answer is missing: %q", out)
		}
	})

	t.Run("newer release prints version and channel page", func(t *testing.T) {
		forkFetchLatest = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return newer, nil
		}
		var out string
		var rc int
		out = captureStdout(t, func() {
			rc = forkUpgradeCommand(cliUpgradeSyntax{checkOnly: true}, "v1.38.3-20261115-0841", http.DefaultClient)
		})
		if rc != 0 {
			t.Fatalf("newer-release check must exit 0, got %d", rc)
		}
		if !strings.Contains(out, "v1.38.3-20261117-1800") {
			t.Fatalf("the newer version must be named: %q", out)
		}
		if !strings.Contains(out, forkupdate.ReleasesPage) {
			t.Fatalf("the fork releases page must be printed: %q", out)
		}
	})

	t.Run("newer release face is informational for --check and full upgrade alike", func(t *testing.T) {
		forkFetchLatest = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return newer, nil
		}
		var rc int
		out := captureStdout(t, func() {
			rc = forkUpgradeCommand(cliUpgradeSyntax{}, "v1.38.3-20261115-0841", http.DefaultClient)
		})
		if rc != 0 {
			t.Fatalf("full upgrade without a CLI asset must still exit 0, got %d", rc)
		}
		if !strings.Contains(out, "v1.38.3-20261117-1800") {
			t.Fatalf("the full upgrade face still names the newer version: %q", out)
		}
	})
}

// TestUpgradeCommandRoutesForkVersionsToTheForkFace pins the dispatch: a fork
// version label routes to the fork face; an official strict version does not.
func TestUpgradeCommandRoutesForkVersionsToTheForkFace(t *testing.T) {
	if _, ok := forkupdate.ParseForkVersion("v1.38.3-20261117-1800"); !ok {
		t.Fatal("the fork version shape must parse")
	}
	if _, ok := forkupdate.ParseForkVersion("v1.38.3-20261117-1800-rc"); ok {
		t.Fatal("upstream prerelease text must not parse as a fork version")
	}
}
