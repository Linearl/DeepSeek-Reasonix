package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"reasonix/internal/forkupdate"
	"reasonix/internal/installlayout"
)

// fork_update.go is the fork's own update channel face (tl1680, issue #96).
//
// The legacy desktop updater (updater.go / updater_app.go) is the UPSTREAM
// signed-manifest chain: dl.reasonix.io, the crash.reasonix.io release gateway
// (shut down — NXDOMAIN — and the direct cause of the issue #96 noise), and
// the esengine GitHub fallback. None of those is a fork update target — the
// worst case is not noise but silently replacing a fork install with an
// upstream build and losing every fork feature. This file adds the fork
// channel instead of touching that chain:
//
//   - CheckUpdate (updater_app.go) calls checkForkRelease: the fork's GitHub
//     releases (Linearl/DeepSeek-Reasonix, tags desktop-v*), compared with the
//     running fork version by forkupdate's timestamp-aware comparator. A
//     channel failure (NXDOMAIN, 403 rate limit, EOF) degrades SILENTLY: the
//     check result carries checkFailed, never a UI error — a failed check is
//     not a failed update.
//   - ForkApplyUpdate is the one-action seamless upgrade: download the pinned
//     fork artifact, lay the release unit out in the staging directory (the
//     exact payload shape the restart-and-update path publishes), then hand
//     off to publishStagedVersion — the same ActivateVersion + launcher
//     relaunch sequence the task-81/210/tl1615 chain uses. The user clicks
//     once; the app relaunches onto the new version. Progress rides the
//     existing "updater:progress" events; the frontend shows phases only.
//
// Scope note (tl1680 v1): the staging publish path requires the complete
// release unit (desktop + CLI + update helper + launcher). Only the Windows
// portable zip carries all four, so the seamless apply is offered on Windows
// versioned installs; every other platform gets the check plus a manual
// download link, the same disposition as the official macOS manual path.

// forkCheckClient builds the transport for fork channel lookups. The updater
// client already carries the user's proxy settings and the redirect allowlist
// (github.com / *.githubusercontent.com) that release downloads need. A var so
// tests can point the whole face at a local mock transport.
var forkCheckClient = func() (*http.Client, error) {
	return httpClient()
}

// forkCheckSeam indirection so tests can pin the channel result without a
// network. Production wires the package's real fetch.
var forkCheckSeam = func(ctx context.Context, c *http.Client) (*forkupdate.Release, error) {
	return forkupdate.FetchLatestRelease(ctx, c)
}

// forkPublishSeam is the publish handoff, intercepted in tests the same way
// restartStartLauncher/versionSwitchQuit are.
var forkPublishSeam = func(a *App, sourceDir, version string) (string, error) {
	return a.publishStagedVersion(sourceDir, version, "")
}

// forkStagingDir is the staging layout seam (tests point it at a scratch dir
// instead of the real install root).
var forkStagingDir = stagingRoot

// forkApplyGate reports whether this platform/install can run the seamless
// publish (tl1680 v1: Windows versioned installs only — the only fork artifact
// that carries the complete release unit, on the only layout the publish path
// swaps). Intercepted in tests.
var forkApplyGate = func() bool {
	return runtime.GOOS == "windows" && installlayout.HasCurrent(currentInstallDir())
}

// checkForkRelease is the shared channel probe behind CheckUpdate and
// ForkApplyUpdate. Never returns a UI-facing error: a failed lookup is an
// expected environment condition (issue #96 taught this the hard way), and the
// callers degrade on it.
func checkForkRelease(ctx context.Context, c *http.Client) (*forkupdate.Release, error) {
	rel, err := forkCheckSeam(ctx, c)
	if err != nil {
		if forkupdate.RateLimited(err) {
			slog.Info("update: fork release channel rate limited; check skipped")
		} else {
			slog.Info("update: fork release channel unreachable; check skipped", "err", err)
		}
		return nil, err
	}
	return rel, nil
}

// ForkApplyUpdate is the fork's one-action seamless update (tl1680). expectedVersion
// pins the checked release so a channel pointer change cannot silently swap the
// payload; requestID streams the "updater:progress" events.
func (a *App) ForkApplyUpdate(expectedVersion, requestID string) error {
	requestID = strings.TrimSpace(requestID)
	if !updaterRequestIDRE.MatchString(requestID) {
		return fmt.Errorf("update: invalid request id")
	}
	expectedVersion = strings.TrimSpace(expectedVersion)
	target, ok := forkupdate.ParseForkVersion(expectedVersion)
	if !ok {
		return fmt.Errorf("update: invalid fork version %q", expectedVersion)
	}

	finish, err := a.beginUpdaterOperation(requestID)
	if err != nil {
		return err
	}
	// One owner covers download -> stage -> publish -> relaunch, mirroring the
	// official path's single-owner guarantee.
	defer finish()

	channel := runningUpdateChannel()
	// The same pending-transaction healing pass the official path runs before
	// paying download costs: a stuck previous update must not strand the user
	// mid-download.
	if err := a.reconcilePendingUpdateForRequest(requestID, &cachedUpdate{
		Channel: channel,
		Version: target.String(),
	}); err != nil {
		return err
	}

	// Platform scope (tl1680 v1): the publish path needs the complete release
	// unit in a versioned layout. Outside that, route to the manual download
	// page — an explicit refusal with a working alternative, never noise.
	if !forkApplyGate() {
		profile := installProfile{
			Mode:          installModeManual,
			CanSelfUpdate: false,
			ManualReason: fmt.Sprintf(
				"one-click fork updates currently support Windows versioned installs; download from %s",
				forkupdate.ReleasesPage,
			),
		}
		return a.requireManualUpdate(requestID, channel, target.String(), profile)
	}

	c, err := forkCheckClient()
	if err != nil {
		return a.failUpdate(requestID, channel, target.String(), err)
	}
	rel, err := checkForkRelease(a.reqCtx(), c)
	if err != nil {
		return a.failUpdate(requestID, channel, target.String(), fmt.Errorf(
			"fork release channel unreachable; the pinned version %s cannot be downloaded right now", target.String(),
		))
	}
	if err := ensureExpectedUpdateVersion(channel, target.String(), rel.Version); err != nil {
		return a.failUpdate(requestID, channel, target.String(), err)
	}
	asset, ok := rel.SelectPlatformAsset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return a.failUpdate(requestID, channel, target.String(), fmt.Errorf(
			"fork release %s has no artifact for %s/%s", rel.Version, runtime.GOOS, runtime.GOARCH,
		))
	}

	v4, _ := httpClientIPv4() // best-effort IPv4 fallback; nil just means retries reuse c
	data, err := downloadForChannel(a.reqCtx(), c, v4, channel, asset.URL, asset.Size, func(rcv, total int64) {
		a.emitProgress(requestID, channel, target.String(), "downloading", rcv, total, "")
	})
	if err != nil {
		return a.failUpdate(requestID, channel, target.String(), err)
	}

	a.emitProgress(requestID, channel, target.String(), "verifying", asset.Size, asset.Size, "")
	stagingDir, err := forkStagingDir()
	if err != nil {
		return a.failUpdate(requestID, channel, target.String(), err)
	}
	if err := stageForkReleaseUnit(data, stagingDir, target.String()); err != nil {
		return a.failUpdate(requestID, channel, target.String(), err)
	}

	a.emitProgress(requestID, channel, target.String(), "installing", asset.Size, asset.Size, "")
	if _, err := forkPublishSeam(a, stagingDir, target.String()); err != nil {
		return a.failUpdate(requestID, channel, target.String(), err)
	}
	// publishStagedVersion has committed the swap and scheduled the relaunch;
	// surface the final phase for the frontend the same way the official path
	// does before its own exit.
	a.emitProgress(requestID, channel, target.String(), "relaunching", 0, 0, "")
	return nil
}

// forkReleaseUnitMembers lists the members a fork release artifact must carry
// for the staging publish path: the version-dir whitelist (desktop, CLI,
// update helper) plus the root launcher.
func forkReleaseUnitMembers() []string {
	return []string{
		installlayout.DesktopBinaryName(),
		installlayout.CLIBinaryName(),
		installlayout.UpdateHelperBinaryName(),
		installlayout.LauncherBinaryName(),
	}
}

// stageForkReleaseUnit extracts the release unit from the downloaded fork
// artifact (Windows portable zip: flat payload of executables) into the
// staging directory and writes version.txt — the exact payload shape
// publishStagedVersion validates and publishes. Extra members (guard,
// uninstaller, payload manifests) are ignored: the publish whitelist decides
// what enters a version tree.
//
// The fork channel publishes no signature manifest, so artifact integrity on
// this path is: HTTPS transport + GitHub release immutability + the publish
// path's own member checks. (Follow-up candidate: publish SHA256SUMS assets in
// the fork workflow and verify here.)
func stageForkReleaseUnit(data []byte, stagingDir, version string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("update: fork release artifact is not a readable zip: %w", err)
	}
	members := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		members[filepath.Base(f.Name)] = f.Name
	}
	for _, name := range forkReleaseUnitMembers() {
		if _, ok := members[name]; !ok {
			return fmt.Errorf("update: fork release artifact is missing %s; refusing to stage an incomplete release unit", name)
		}
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("update: create staging directory: %w", err)
	}
	for _, name := range forkReleaseUnitMembers() {
		src, err := zr.Open(members[name])
		if err != nil {
			return fmt.Errorf("update: open staged %s: %w", name, err)
		}
		info, err := src.Stat()
		if err != nil || !info.Mode().IsRegular() {
			src.Close()
			return fmt.Errorf("update: staged %s is not a regular file", name)
		}
		dst, err := os.OpenFile(filepath.Join(stagingDir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			src.Close()
			return fmt.Errorf("update: write staged %s: %w", name, err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return fmt.Errorf("update: copy staged %s: %w", name, err)
		}
		if err := src.Close(); err != nil {
			dst.Close()
			return fmt.Errorf("update: close staged %s: %w", name, err)
		}
		if err := dst.Close(); err != nil {
			return fmt.Errorf("update: close staged %s: %w", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "version.txt"), []byte(version), 0o644); err != nil {
		return fmt.Errorf("update: write staging version: %w", err)
	}
	slog.Info("update: fork release unit staged", "version", version, "stagingDir", stagingDir)
	return nil
}
