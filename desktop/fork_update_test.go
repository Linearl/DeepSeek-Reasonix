package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/forkupdate"
	"reasonix/internal/repair"
)

// forkUpdateTestEnv pins the tl1680 seams: the fork channel seam, the transport,
// the staging location, the platform gate, and the running build identity. Every
// test restores them, so the three-state contract below is hermetic — no real
// network, no real install, no dependence on this machine's config.
type forkUpdateTestEnv struct {
	savedVersion     string
	savedChannel     string
	savedCheckSeam   func(context.Context, *http.Client) (*forkupdate.Release, error)
	savedClient      func() (*http.Client, error)
	savedStaging     func() (string, error)
	savedGate        func() bool
	savedPublish     func(*App, string, string) (string, error)
	savedPending     func() bool
	savedReconcile   func(string) (repair.PendingUpdateReconcileResult, error)
	stagingDir       string
	publishCalls     []string // "sourceDir|version"
	publishAvailable bool
}

func newForkUpdateTestEnv(t *testing.T) *forkUpdateTestEnv {
	t.Helper()
	staging := t.TempDir()
	env := &forkUpdateTestEnv{
		savedVersion:   version,
		savedChannel:   channel,
		savedCheckSeam: forkCheckSeam,
		savedClient:    forkCheckClient,
		savedStaging:   forkStagingDir,
		savedGate:      forkApplyGate,
		savedPublish:   forkPublishSeam,
		savedPending:   pendingUpdateExistsForInstall,
		savedReconcile: reconcilePendingUpdateForInstall,
		stagingDir:     staging,
	}
	forkStagingDir = func() (string, error) { return staging, nil }
	forkCheckClient = func() (*http.Client, error) { return http.DefaultClient, nil }
	forkPublishSeam = func(_ *App, sourceDir, v string) (string, error) {
		env.publishCalls = append(env.publishCalls, sourceDir+"|"+v)
		return "", nil
	}
	pendingUpdateExistsForInstall = func() bool { return false }
	reconcilePendingUpdateForInstall = func(string) (repair.PendingUpdateReconcileResult, error) {
		return repair.PendingUpdateReconcileResult{}, nil
	}
	t.Cleanup(func() {
		version = env.savedVersion
		channel = env.savedChannel
		forkCheckSeam = env.savedCheckSeam
		forkCheckClient = env.savedClient
		forkStagingDir = env.savedStaging
		forkApplyGate = env.savedGate
		forkPublishSeam = env.savedPublish
		pendingUpdateExistsForInstall = env.savedPending
		reconcilePendingUpdateForInstall = env.savedReconcile
	})
	return env
}

// TestCheckUpdateForkChannelThreeStates pins the issue #96 contract on the
// desktop check face: channel failure degrades silently (no Err, no available),
// an up-to-date build gets the normal "already current" result, and a newer
// fork release marks available. The legacy dead-gateway noise
// (`update: invalid stable version ”`) must be impossible from this face.
func TestCheckUpdateForkChannelThreeStates(t *testing.T) {
	newForkUpdateTestEnv(t)
	version = "v1.38.3-20261115-0841"
	channel = "stable"
	app := &App{}

	t.Run("channel failure degrades silently", func(t *testing.T) {
		forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return nil, errors.New(`Get "https://crash.reasonix.io/v1/cli/releases/stable/latest.json": EOF`)
		}
		info, err := app.CheckUpdate("stable")
		if err != nil {
			t.Fatalf("a failed check must not return an error: %v", err)
		}
		if !info.CheckFailed {
			t.Fatal("channel failure must surface as checkFailed")
		}
		if info.Err != "" {
			t.Fatalf("checkFailed must not carry a UI error string, got %q", info.Err)
		}
		if info.Available {
			t.Fatal("a failed check must not claim an update is available")
		}
		if info.Latest != "" {
			t.Fatalf("a failed check must not publish a latest version, got %q", info.Latest)
		}
	})

	t.Run("rate limited degrades silently too", func(t *testing.T) {
		forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return nil, forkupdate.NewRateLimitError("403 Forbidden")
		}
		info, err := app.CheckUpdate("stable")
		if err != nil || !info.CheckFailed || info.Err != "" {
			t.Fatalf("rate limit must degrade silently: info=%+v err=%v", info, err)
		}
	})

	t.Run("already current answers without availability", func(t *testing.T) {
		forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return &forkupdate.Release{Tag: "desktop-v1.38.3-20261115-0841", Version: "v1.38.3-20261115-0841"}, nil
		}
		info, err := app.CheckUpdate("stable")
		if err != nil {
			t.Fatalf("CheckUpdate: %v", err)
		}
		if info.CheckFailed {
			t.Fatal("a healthy channel must not mark checkFailed")
		}
		if info.Available {
			t.Fatal("the running build must not be flagged available")
		}
		if info.Latest != "v1.38.3-20261115-0841" {
			t.Fatalf("latest = %q, want the channel's current release", info.Latest)
		}
		if info.Current != version {
			t.Fatalf("current = %q, want the running version", info.Current)
		}
	})

	t.Run("newer fork release is available with an asset size", func(t *testing.T) {
		forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			return &forkupdate.Release{
				Tag:     "desktop-v1.38.3-20261117-1800",
				Version: "v1.38.3-20261117-1800",
				Notes:   "包99 notes",
				Assets: []forkupdate.Asset{{
					Name: "Reasonix-windows-amd64.zip",
					URL:  "https://github.com/Linearl/DeepSeek-Reasonix/releases/download/desktop-v1.38.3-20261117-1800/Reasonix-windows-amd64.zip",
					Size: 12345,
				}},
			}, nil
		}
		info, err := app.CheckUpdate("stable")
		if err != nil {
			t.Fatalf("CheckUpdate: %v", err)
		}
		if info.CheckFailed {
			t.Fatal("healthy channel must not mark checkFailed")
		}
		if !info.Available {
			t.Fatal("a newer timestamp build must be flagged available")
		}
		if info.Latest != "v1.38.3-20261117-1800" {
			t.Fatalf("latest = %q", info.Latest)
		}
		if info.AssetSize != 12345 {
			t.Fatalf("asset size = %d, want the platform artifact size", info.AssetSize)
		}
		if info.Notes != "包99 notes" {
			t.Fatalf("notes = %q, want the release body", info.Notes)
		}
	})

	t.Run("dev build stays quiet", func(t *testing.T) {
		version = "dev"
		defer func() { version = "v1.38.3-20261115-0841" }()
		forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
			t.Fatal("a dev build must not reach the channel")
			return nil, nil
		}
		info, err := app.CheckUpdate("stable")
		if err != nil || info.CheckFailed || info.Available || info.Err != "" {
			t.Fatalf("dev build must stay quiet: info=%+v err=%v", info, err)
		}
	})
}

// TestForkApplyUpdateStagesReleaseUnitAndPublishes drives the full apply face
// against a mock fork release serving a real Windows portable zip: download →
// stage the release unit (+version.txt) → hand off to the restart publish seam.
func TestForkApplyUpdateStagesReleaseUnitAndPublishes(t *testing.T) {
	env := newForkUpdateTestEnv(t)
	version = "v1.38.3-20261115-0841"
	channel = "stable"
	forkApplyGate = func() bool { return true }

	payload := buildForkReleaseZip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	release := &forkupdate.Release{
		Tag:     "desktop-v1.38.3-20261117-1800",
		Version: "v1.38.3-20261117-1800",
		Assets: []forkupdate.Asset{{
			Name: "Reasonix-windows-amd64.zip",
			URL:  srv.URL + "/Reasonix-windows-amd64.zip",
			Size: int64(len(payload)),
		}},
	}
	var channelCalls int
	forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
		channelCalls++
		return release, nil
	}

	app := &App{}
	if err := app.ForkApplyUpdate("v1.38.3-20261117-1800", "web-test-1"); err != nil {
		t.Fatalf("ForkApplyUpdate: %v", err)
	}

	if channelCalls != 1 {
		t.Fatalf("channel calls = %d, want 1 (check happens inside apply)", channelCalls)
	}
	if len(env.publishCalls) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(env.publishCalls))
	}
	gotSource, gotVersion, found := strings.Cut(env.publishCalls[0], "|")
	if !found || gotVersion != "v1.38.3-20261117-1800" {
		t.Fatalf("publish args = %q, want staging|v1.38.3-20261117-1800", env.publishCalls[0])
	}
	if gotSource != env.stagingDir {
		t.Fatalf("publish sourceDir = %q, want the staging seam dir %q", gotSource, env.stagingDir)
	}
	for _, name := range forkReleaseUnitMembers() {
		data, err := os.ReadFile(filepath.Join(env.stagingDir, name))
		if err != nil {
			t.Fatalf("staged %s missing: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("staged %s is empty", name)
		}
	}
	versionRaw, err := os.ReadFile(filepath.Join(env.stagingDir, "version.txt"))
	if err != nil {
		t.Fatalf("version.txt missing: %v", err)
	}
	if got := strings.TrimSpace(string(versionRaw)); got != "v1.38.3-20261117-1800" {
		t.Fatalf("version.txt = %q, want v1.38.3-20261117-1800", got)
	}
}

// TestForkApplyUpdateRefusesWhenPointerMoves pins the expectedVersion contract:
// a channel pointer change between check and apply must refuse, never swap a
// different payload.
func TestForkApplyUpdateRefusesWhenPointerMoves(t *testing.T) {
	newForkUpdateTestEnv(t)
	version = "v1.38.3-20261115-0841"
	channel = "stable"
	forkApplyGate = func() bool { return true }
	forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
		return &forkupdate.Release{Tag: "desktop-v1.38.3-20261118-2100", Version: "v1.38.3-20261118-2100"}, nil
	}
	app := &App{}
	err := app.ForkApplyUpdate("v1.38.3-20261117-1800", "web-test-2")
	if err == nil || !strings.Contains(err.Error(), "pointer changed") {
		t.Fatalf("pointer change must refuse with the check-again error, got %v", err)
	}
}

// TestForkApplyUpdateManualOutsideVersionedWindows pins the tl1680 v1 platform
// scope: everything outside a Windows versioned install gets the manual
// download fallback (a refusal with a working alternative), never a partial
// staging attempt.
func TestForkApplyUpdateManualOutsideVersionedWindows(t *testing.T) {
	env := newForkUpdateTestEnv(t)
	version = "v1.38.3-20261115-0841"
	channel = "stable"
	forkApplyGate = func() bool { return false }
	forkCheckSeam = func(context.Context, *http.Client) (*forkupdate.Release, error) {
		t.Fatal("a gated-out platform must not reach the channel")
		return nil, nil
	}
	app := &App{}
	err := app.ForkApplyUpdate("v1.38.3-20261117-1800", "web-test-3")
	if err == nil || !errors.Is(err, errUpdateManualRequired) {
		t.Fatalf("gated-out platform must refuse as manual update required, got %v", err)
	}
	if len(env.publishCalls) != 0 {
		t.Fatal("no publish may happen for a gated-out platform")
	}
}

// TestForkApplyUpdateRejectsBadArguments pins the entry guards: malformed
// request ids and non-fork version labels refuse before any side effect.
func TestForkApplyUpdateRejectsBadArguments(t *testing.T) {
	env := newForkUpdateTestEnv(t)
	version = "v1.38.3-20261115-0841"
	channel = "stable"
	app := &App{}
	if err := app.ForkApplyUpdate("v1.38.3-20261117-1800", "bad request id"); err == nil {
		t.Fatal("an unsafe request id must be refused")
	}
	if err := app.ForkApplyUpdate("v1.38.3-preview.1", "web-test-4"); err == nil {
		t.Fatal("an upstream-style prerelease label must be refused")
	}
	if err := app.ForkApplyUpdate("", "web-test-4"); err == nil {
		t.Fatal("an empty version must be refused")
	}
	if len(env.publishCalls) != 0 {
		t.Fatal("bad arguments must not reach the publish seam")
	}
}

// TestStageForkReleaseUnitRefusesIncompleteArtifact pins the integrity gate:
// an artifact missing any release-unit member is refused before the staging
// directory is touched.
func TestStageForkReleaseUnitRefusesIncompleteArtifact(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"reasonix-desktop.exe", "reasonix-cli.exe"} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("partial")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	err := stageForkReleaseUnit(buf.Bytes(), staging, "v1.38.3-20261117-1800")
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("incomplete artifact must refuse with a missing-member error, got %v", err)
	}
	entries, _ := os.ReadDir(staging)
	if len(entries) != 0 {
		t.Fatalf("a refused artifact must leave the staging dir untouched, found %d entries", len(entries))
	}
}

// buildForkReleaseZip produces the Windows portable payload the fork workflow
// ships: the four release-unit members plus extra members the publish
// whitelist ignores (guard, uninstaller).
func buildForkReleaseZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	members := append(forkReleaseUnitMembers(), "reasonix-uninstall.exe")
	for _, name := range members {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("binary:" + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// forkupdate.RateLimitProbeError exists so the desktop test can pin the
// rate-limited degradation without reaching into the package's unexported
// error type. It wraps forkupdate.RateLimited's classification contract.
var _ = fmt.Sprintf

// TestForkListReleasesSilentDegrade pins the tl1693 version-page face: a
// successful lookup returns the release list (newest first, current-version
// marker, artifact links, RFC3339 timestamps) and any channel failure comes
// back as {failed: true} with no error — the UI hides the list, never errors.
func TestForkListReleasesSilentDegrade(t *testing.T) {
	env := newForkUpdateTestEnv(t)
	version = "v1.38.3-20261115-0841"
	app := &App{}

	t.Run("success lists releases with current marker", func(t *testing.T) {
		forkListSeam = func(context.Context, *http.Client) ([]forkupdate.Release, error) {
			return []forkupdate.Release{
				{Tag: "desktop-v1.38.3-20261117-1800", Version: "v1.38.3-20261117-1800", Notes: "newest notes", Published: time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC),
					Assets: []forkupdate.Asset{{Name: "Reasonix-windows-amd64.zip", URL: "https://example.com/win.zip", Size: 1}}},
				{Tag: "desktop-v1.38.3-20261115-0841", Version: "v1.38.3-20261115-0841", Notes: "running notes"},
			}, nil
		}
		got := app.ForkListReleases()
		if got.Failed {
			t.Fatal("a successful lookup must not report failed")
		}
		if len(got.Releases) != 2 {
			t.Fatalf("rows = %d, want 2", len(got.Releases))
		}
		if got.Releases[0].Current {
			t.Fatal("the newest release is not the running one; current must be false")
		}
		if !got.Releases[1].Current {
			t.Fatalf("running version %q must be marked current", version)
		}
		if got.Releases[1].Assets == nil {
			t.Fatal("assets must be a non-nil slice for JSON stability")
		}
		if got.Releases[0].Published != "2026-11-17T18:00:00Z" {
			t.Fatalf("published = %q, want RFC3339 UTC", got.Releases[0].Published)
		}
		if got.Releases[0].Notes != "newest notes" {
			t.Fatalf("notes = %q", got.Releases[0].Notes)
		}
		if len(got.Releases[0].Assets) != 1 || got.Releases[0].Assets[0].URL != "https://example.com/win.zip" {
			t.Fatalf("assets = %+v", got.Releases[0].Assets)
		}
	})

	t.Run("channel failure degrades silently", func(t *testing.T) {
		forkListSeam = func(context.Context, *http.Client) ([]forkupdate.Release, error) {
			return nil, forkupdate.NewRateLimitError("403 Too Many Requests")
		}
		got := app.ForkListReleases()
		if !got.Failed {
			t.Fatal("a failed lookup must report failed for the UI to hide the list")
		}
		if len(got.Releases) != 0 {
			t.Fatalf("failed lookup must carry no rows; got %d", len(got.Releases))
		}
	})

	t.Run("transport client unavailable degrades silently", func(t *testing.T) {
		forkCheckClient = func() (*http.Client, error) { return nil, errors.New("no transport") }
		forkListSeam = func(context.Context, *http.Client) ([]forkupdate.Release, error) {
			t.Fatal("list seam must not run when the client is unavailable")
			return nil, nil
		}
		got := app.ForkListReleases()
		if !got.Failed || len(got.Releases) != 0 {
			t.Fatalf("client failure must degrade to {failed: true, rows: 0}; got %+v", got)
		}
	})

	_ = env
}
