package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/installlayout"
)

// fakeStagingDir drops a staged build into installRoot/staging. Binaries named
// in members are written; omitted members stay missing so health-bit paths can
// be exercised.
func fakeStagingDir(t *testing.T, root string, version string, complete bool) {
	t.Helper()
	dir := filepath.Join(root, "staging")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
	members := []string{installlayout.DesktopBinaryName()}
	if complete {
		members = append(members, installlayout.CLIBinaryName())
	}
	for _, name := range members {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// addVersionTreeWithCLI writes a version tree whose CLI binary presence is
// controlled, so versionTreeHealthy's both-members check can be exercised.
func addVersionTreeWithCLI(t *testing.T, root, version string, withCLI bool) {
	t.Helper()
	dir := filepath.Join(root, installlayout.VersionsDirName, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, installlayout.DesktopBinaryName()), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withCLI {
		if err := os.WriteFile(filepath.Join(dir, installlayout.CLIBinaryName()), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListVersionsReportsHealthAndStaging(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	// fakeInstallRoot writes desktop binaries only; the active tree gets its
	// CLI so the health check has a fully healthy row to compare against.
	addVersionTreeWithCLI(t, root, active, true)
	// An older tree missing the CLI: list_versions must brand it unhealthy so
	// the model never stages a rollback that bricks servepool (task 248).
	addVersionTreeWithCLI(t, root, "v1.38.3-20260922-0900", false)
	fakeStagingDir(t, root, "1.38.3-20260924-1200", true)

	app := &App{}
	controller := newAutonomousUpdateController(app)
	versions, active, staging, err := controller.ListVersions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if active != "v1.38.3-20260923-1010" {
		t.Fatalf("active = %q", active)
	}
	if staging != "v1.38.3-20260924-1200" {
		t.Fatalf("staging label = %q (version.txt must be normalized onto the v-prefixed install name)", staging)
	}
	byVersion := map[string]toolVersionHealth{}
	for _, v := range versions {
		byVersion[v.Version] = v
	}
	healthyTree, ok := byVersion["v1.38.3-20260923-1010"]
	if !ok || !healthyTree.Healthy || !healthyTree.Active {
		t.Fatalf("active tree missing or mislabeled: %+v", healthyTree)
	}
	unhealthyTree, ok := byVersion["v1.38.3-20260922-0900"]
	if !ok || unhealthyTree.Healthy {
		t.Fatalf("CLI-less tree must be unhealthy (task 248): %+v", unhealthyTree)
	}
	staged, ok := byVersion["v1.38.3-20260924-1200"]
	if !ok || !staged.Staging {
		t.Fatalf("staging row missing or mislabeled: %+v", staged)
	}
}

// toolVersionHealth mirrors tool.VersionHealth without importing the package
// twice under a different name in assertions.
type toolVersionHealth = struct {
	Version     string `json:"version"`
	Active      bool   `json:"active"`
	Healthy     bool   `json:"healthy"`
	Staging     bool   `json:"staging"`
	ModTimeUnix int64  `json:"modTimeUnix"`
}

func TestSetTargetStagesAndValidates(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	root := fakeInstallRoot(t, []string{active}, active)
	addVersionTreeWithCLI(t, root, "v1.38.3-20260922-0900", true)
	app := &App{}
	controller := newAutonomousUpdateController(app)

	// A bare version label is normalized onto the v-prefixed install name.
	description, err := controller.SetTarget(t.Context(), "1.38.3-20260922-0900")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(description, "v1.38.3-20260922-0900") {
		t.Fatalf("description should name the normalized version: %q", description)
	}
	app.autonomousMu.Lock()
	pending := app.autonomousPending
	app.autonomousMu.Unlock()
	if pending.kind != "installed" || pending.version != "v1.38.3-20260922-0900" {
		t.Fatalf("pending = %+v", pending)
	}

	// The active version is a no-op swap and must be refused.
	if _, err := controller.SetTarget(t.Context(), active); err == nil {
		t.Fatal("staging the active version must be refused")
	}
	// An unknown version is refused.
	if _, err := controller.SetTarget(t.Context(), "v9.9.9"); err == nil {
		t.Fatal("staging an unknown version must be refused")
	}

	// Staging without version.txt is a named-path refusal.
	broken := &App{}
	controller = newAutonomousUpdateController(broken)
	fakeStagingDir(t, root, "", false) // no version.txt
	if _, err := controller.SetTarget(t.Context(), "staging"); err == nil {
		t.Fatal("staging without version.txt must be refused")
	}
}

func TestExecuteTargetRequiresStagedTarget(t *testing.T) {
	isolateDesktopUserDirs(t)
	fakeInstallRoot(t, nil, "")
	app := &App{}
	controller := newAutonomousUpdateController(app)
	if _, err := controller.ExecuteTarget(t.Context(), ""); err == nil {
		t.Fatal("execute without set_target must be refused")
	}
}

func TestExecuteTargetRollbackSwitchesAndStagesResume(t *testing.T) {
	isolateDesktopUserDirs(t)
	active := "v1.38.3-20260923-1010"
	rollback := "v1.38.3-20260922-0900"
	root := fakeInstallRoot(t, []string{active, rollback}, active)
	addVersionTreeWithCLI(t, root, rollback, true)
	fakeVersionedConfig(t, true)

	callerSession := filepath.Join(t.TempDir(), "caller.session.jsonl")
	callerCtrl := &retargetRuntimeController{status: control.RuntimeStatus{Running: true}, path: callerSession}
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: callerSession, Ctrl: callerCtrl, autopilot: true, Ready: true},
	}}
	// The busy guard must exempt the calling session's own turn (task 254):
	// the caller is RUNNING, and the swap still has to go through.
	if busy := app.restartBusyReason(callerSession); busy != "" {
		t.Fatalf("caller session must be exempt: %s", busy)
	}
	if busy := app.restartBusyReason(""); busy == "" {
		t.Fatal("an empty caller session must stay busy-blocked")
	}

	// A second running tab blocks the swap even with the exemption.
	other := filepath.Join(t.TempDir(), "other.session.jsonl")
	app.tabs["t2"] = &WorkspaceTab{ID: "t2", SessionPath: other, Ctrl: &retargetRuntimeController{status: control.RuntimeStatus{Running: true}, path: other}, Ready: true}
	if busy := app.restartBusyReason(callerSession); busy == "" {
		t.Fatal("another tab's running turn must still refuse the swap")
	}
	delete(app.tabs, "t2")

	controller := newAutonomousUpdateController(app)
	versionSwitchStartLauncher = func(string, int) error { return nil }
	versionSwitchQuit = func(*App) {}

	if _, err := controller.SetTarget(t.Context(), rollback); err != nil {
		t.Fatal(err)
	}
	result, err := controller.ExecuteTarget(t.Context(), callerSession)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "do not retry") {
		t.Fatalf("result must warn against retrying: %q", result)
	}
	// The pointer moved: this is the visible effect of the rollback.
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != rollback {
		t.Fatalf("active version = %q, want %q", ptr.ActiveVersion, rollback)
	}
	// The pending target is consumed.
	app.autonomousMu.Lock()
	consumed := app.autonomousPending.kind == ""
	app.autonomousMu.Unlock()
	if !consumed {
		t.Fatal("execute must consume the staged target")
	}
	// The resume marker is staged for the autopilot caller.
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 1 || state.Sessions[0].Path != callerSession {
		t.Fatalf("resume marker = %+v, want exactly the caller session", state.Sessions)
	}
}

func TestStageAutonomousUpdateResumeSkipsNonAutopilot(t *testing.T) {
	isolateDesktopUserDirs(t)
	callerSession := filepath.Join(t.TempDir(), "attended.session.jsonl")
	app := &App{tabs: map[string]*WorkspaceTab{
		"t1": {ID: "t1", SessionPath: callerSession, Ctrl: &retargetRuntimeController{path: callerSession}, autopilot: false, Ready: true},
	}}
	app.stageAutonomousUpdateResume(callerSession)
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 0 {
		t.Fatalf("attended session must not be staged for resume: %+v", state.Sessions)
	}
}

// TestStageAutonomousUpdateResumeDial covers the user-ruled three scopes
// (task 254): off stages nothing, goal_autopilot stages only the autopilot
// caller, all additionally stages every other mid-turn session.
func TestStageAutonomousUpdateResumeDial(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		attended  bool
		wantCount int
	}{
		{mode: "off", attended: false, wantCount: 0},
		{mode: "off", attended: true, wantCount: 0},
		{mode: "goal_autopilot", attended: false, wantCount: 1},
		{mode: "goal_autopilot", attended: true, wantCount: 0},
		{mode: "all", attended: false, wantCount: 2},
		{mode: "all", attended: true, wantCount: 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			callerSession := filepath.Join(t.TempDir(), "caller.session.jsonl")
			otherSession := filepath.Join(t.TempDir(), "other-running.session.jsonl")
			app := &App{tabs: map[string]*WorkspaceTab{
				"t1": {ID: "t1", SessionPath: callerSession, Ctrl: &retargetRuntimeController{path: callerSession, status: control.RuntimeStatus{Running: true}}, autopilot: !tc.attended, Ready: true},
				"t2": {ID: "t2", SessionPath: otherSession, Ctrl: &retargetRuntimeController{path: otherSession, status: control.RuntimeStatus{Running: true}}, Ready: true},
			}}
			if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume(tc.mode) }); err != nil {
				t.Fatal(err)
			}
			app.stageAutonomousUpdateResume(callerSession)
			state := readAutonomousUpdateResumeFile()
			if len(state.Sessions) != tc.wantCount {
				t.Fatalf("mode %q: staged %d sessions, want %d: %+v", tc.mode, len(state.Sessions), tc.wantCount, state.Sessions)
			}
		})
	}
}

func TestMaybeResumeAutonomousUpdateTabConsumesMarkerOnce(t *testing.T) {
	isolateDesktopUserDirs(t)
	sessionPath := filepath.Join(t.TempDir(), "resumed.session.jsonl")
	app := &App{}
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: sessionPath, StagedAt: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	// Ready tab whose session matches the marker. SubmitToTab may refuse (no
	// wails runtime here) — the marker must be consumed regardless, so a
	// failed resume can never resurrect on a later ordinary restart.
	ctrl := &retargetRuntimeController{path: sessionPath}
	tab := &WorkspaceTab{ID: "t1", SessionPath: sessionPath, Ready: true, Ctrl: ctrl}
	app.maybeResumeAutonomousUpdateTab(tab)
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 0 {
		t.Fatalf("marker must be consumed on first sight: %+v", state.Sessions)
	}
	// Task 263 fix 2 + 300: the auto-resume owns the "continue" decision, so it
	// clears the recovery pause (the banner's precondition) in the same hand-off —
	// one channel: no "review before resuming" notice while the resume is
	// already on its way. Task 300 pins the PASSIVE form: unpause without a
	// dispatch, so leftover pending guidance never replays into the conversation.
	if len(ctrl.inboxPausedCalls) != 1 || ctrl.inboxPausedCalls[0] {
		t.Fatalf("auto-resume must clear the recovery pause exactly once, passively (unpause): %+v", ctrl.inboxPausedCalls)
	}

	// An unrelated session's restore must neither touch nor consume the entry.
	keep := filepath.Join(t.TempDir(), "other.session.jsonl")
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: keep, StagedAt: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	app.maybeResumeAutonomousUpdateTab(&WorkspaceTab{ID: "t2", SessionPath: sessionPath, Ready: true})
	state = readAutonomousUpdateResumeFile()
	if len(state.Sessions) != 1 || state.Sessions[0].Path != keep {
		t.Fatalf("unrelated restore must leave the marker alone: %+v", state.Sessions)
	}
}

// TestResumeMarkerRoundTripsJSON guards the file format: the resume path reads
// it back with plain json.Unmarshal, so the shape is part of the contract.
func TestResumeMarkerRoundTripsJSON(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{
		{Path: "a.jsonl", StagedAt: 7},
	}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(autonomousUpdateResumePath())
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["sessions"]; !ok {
		t.Fatalf("marker file must carry a sessions array: %s", raw)
	}
}
