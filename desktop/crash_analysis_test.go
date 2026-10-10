package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

func TestIsReasonixSourceDirAcceptsRealCheckoutShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "desktop"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module reasonix\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop", "crash_app.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isReasonixSourceDir(dir) {
		t.Fatal("a checkout with module reasonix + desktop source must be accepted")
	}
}

func TestIsReasonixSourceDirRejectsWrongModuleAndMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isReasonixSourceDir(dir) {
		t.Fatal("a different module must be rejected")
	}

	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module reasonix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isReasonixSourceDir(dir2) {
		t.Fatal("module reasonix without the desktop source must be rejected")
	}
}

func TestFilterEnvDropsOnlyNamedVars(t *testing.T) {
	environ := []string{"HOME=/home/x", "GITHUB_TOKEN=secret", "GH_TOKEN=also-secret", "PATH=/bin"}
	kept := filterEnv(environ, "GITHUB_TOKEN", "GH_TOKEN")
	joined := strings.Join(kept, "\n")
	if strings.Contains(joined, "GITHUB_TOKEN") || strings.Contains(joined, "GH_TOKEN") {
		t.Fatalf("token env vars survived filtering: %q", joined)
	}
	for _, want := range []string{"HOME=/home/x", "PATH=/bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("filter dropped unrelated var, missing %q in %q", want, joined)
		}
	}
}

func TestBuildCrashAnalysisInstructionCarriesPayloadAndContract(t *testing.T) {
	payload := `{
  "kind": "crash",
  "errorType": "panic",
  "topFrame": "desktop/crash_app.go:99"
}`
	instruction := buildCrashAnalysisInstruction(`C:\src\reasonix`, payload, false)
	for _, want := range []string{
		"gh-issue-submit",
		crashAnalysisRepo,
		`C:\src\reasonix`,
		"file:line",
		`"kind": "crash"`,
		"desktop/crash_app.go:99",
		"issue 链接",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q:\n%s", want, instruction)
		}
	}
	if !strings.HasPrefix(instruction, "请分析") {
		t.Fatalf("instruction should open with the analysis ask:\n%s", instruction)
	}
}

// ── task 643: gh discovery must survive a stale process PATH ────────────────

func stubGhFallbackDirs(t *testing.T, dirs ...string) {
	t.Helper()
	orig := ghFallbackLocationDirs
	ghFallbackLocationDirs = func() []string { return dirs }
	t.Cleanup(func() { ghFallbackLocationDirs = orig })
}

func clearPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
}

// Every production fallback location must be absolute: a half-set env var would
// otherwise yield a relative path that could stat against the process cwd.
func TestGhFallbackLocationsAreAllAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("ProgramFiles", "")
		t.Setenv("ProgramFiles(x86)", "")
		t.Setenv("LocalAppData", "")
		t.Setenv("USERPROFILE", "")
	}
	locations := ghFallbackLocations()
	if runtime.GOOS == "windows" {
		if len(locations) != 0 {
			t.Fatalf("all install env vars unset must yield no fallback locations, got %q", locations)
		}
		return
	}
	if len(locations) == 0 {
		t.Fatal("unix fallback must keep its fixed install locations")
	}
	for _, loc := range locations {
		if !filepath.IsAbs(loc) {
			t.Fatalf("fallback locations must be absolute, got %q", loc)
		}
	}
}

// The task 643 scenario: gh is installed at a known location but the process
// PATH predates it — resolution must still find the executable.
func TestResolveGhExecutableFallsBackWhenPathMisses(t *testing.T) {
	clearPath(t)
	dir := t.TempDir()
	candidate := filepath.Join(dir, ghExecutableName())
	if err := os.WriteFile(candidate, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubGhFallbackDirs(t, dir)

	gh, onPath, found := resolveGhExecutable()
	if !found {
		t.Fatalf("gh at a known install location must be found even when PATH misses (candidate %q)", candidate)
	}
	if onPath {
		t.Fatalf("a fallback hit must not be reported as a PATH hit: %q", gh)
	}
	if gh != candidate {
		t.Fatalf("resolved %q, want %q", gh, candidate)
	}
}

func TestResolveGhExecutableNotFoundWhenPathAndFallbackMiss(t *testing.T) {
	clearPath(t)
	stubGhFallbackDirs(t, t.TempDir()) // exists but holds no gh
	gh, onPath, found := resolveGhExecutable()
	if found || gh != "" || onPath {
		t.Fatalf("with no gh on PATH or in fallbacks, resolution must fail cleanly, got (%q, %v, %v)", gh, onPath, found)
	}
}

func TestGhAuthenticatedNotFoundDetailIsDistinct(t *testing.T) {
	clearPath(t)
	stubGhFallbackDirs(t, t.TempDir())
	ok, detail := ghAuthenticated()
	if ok {
		t.Fatal("ghAuthenticated must fail when gh is nowhere to be found")
	}
	// The task 643 false alarm: this state must be distinguishable from an auth
	// failure, not collapsed into the same generic message.
	if !strings.Contains(detail, "not found") {
		t.Fatalf("not-found detail must say so explicitly, got %q", detail)
	}
}

func TestGhSuccessDetailExplainsFallbackDiscovery(t *testing.T) {
	if detail := ghSuccessDetail(`C:\Program Files\GitHub CLI\gh.exe`, true); detail != "" {
		t.Fatalf("a PATH hit needs no success detail, got %q", detail)
	}
	detail := ghSuccessDetail(`C:\Program Files\GitHub CLI\gh.exe`, false)
	// 408 前置：origin 7d352b3af 的本文件在 for 循环中部被截断（缺三个闭括号），
	// 整个 desktop 测试包自此无法编译。按存活文本最小闭合，不新增断言。
	if detail == "" {
		t.Fatal("a fallback success needs an explanatory detail, got empty")
	}
	for _, want := range []string{"outside PATH", `C:\Program Files\GitHub CLI\gh.exe`, "auth OK"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("fallback success detail missing %q, got %q", want, detail)
		}
	}
}

// Task 642: a mock payload's analysis run must announce the simulation so the
// submitted issue is labeled mock/test instead of triaged as a real failure.
func TestBuildCrashAnalysisInstructionMarksMockPayload(t *testing.T) {
	instruction := buildCrashAnalysisInstruction(`C:\src\reasonix`, `{"kind":"crash"}`, true)
	if !strings.Contains(instruction, "测试/mock 报告") || !strings.Contains(instruction, "mock/test") {
		t.Fatalf("mock instruction missing the simulation notice:\n%s", instruction)
	}
	if !strings.HasPrefix(instruction, "请分析") {
		t.Fatalf("mock instruction should still open with the analysis ask:\n%s", instruction)
	}
	plain := buildCrashAnalysisInstruction(`C:\src\reasonix`, `{"kind":"crash"}`, false)
	if strings.Contains(plain, "测试/mock 报告") {
		t.Fatalf("non-mock instruction must not carry the simulation notice:\n%s", plain)
	}
}

// ── task 673: the popup's appended summary must not read as a garbled translation ──

// The 2026-10-09 install feedback: the zh popup showed the localized "started"
// notice followed by this summary hardcoded in English, which read as a
// mistranslation and mangled the Windows path ("github-repo.reasonix"). The
// summary must speak Chinese like the rest of this surface's Go-side copy, and
// the source path must keep its real separators (filepath.Join output, never a
// re-concatenation that could eat a backslash).
func TestCrashAnalysisSummaryIsChineseWithPathSeparatorsIntact(t *testing.T) {
	dir := `C:\Users\_\AppData\Roaming\reasonix\global-workspace\github-repo\reasonix`
	summary := crashAnalysisSummary(dir)
	for _, want := range []string{
		"分析将对照本地源码",
		dir, // verbatim: separators intact, nothing re-joined or scrubbed
		"定位根因",
		crashAnalysisRepo,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	// The garbled-translation complaint: no English sentence may ride along —
	// the path, the repo constant and the fixed term "issue" are the only
	// Latin runs allowed.
	trimmed := strings.ReplaceAll(summary, crashAnalysisRepo, "")
	trimmed = strings.ReplaceAll(trimmed, dir, "")
	trimmed = strings.ReplaceAll(trimmed, "issue", "")
	for _, r := range trimmed {
		if r < 0x80 && r != ' ' {
			t.Fatalf("summary carries unexpected ASCII beyond the path and repo: %q", summary)
		}
	}
}

// ── 任务 672：一键分析会话归属 Global ──────────────────────────────────────

// crashAnalysisCtrlStub extends the heartbeat controller stub with SetMode,
// which the crash flow's YOLO switch applies (the heartbeat flow never calls
// it, so the heartbeat stub leaves the embedded nil interface to panic).
type crashAnalysisCtrlStub struct {
	heartbeatExecuteTaskCtrlStub
	modes []string
}

func (s *crashAnalysisCtrlStub) SetMode(plan, yolo bool) {
	s.modes = append(s.modes, fmt.Sprintf("plan=%v,yolo=%v", plan, yolo))
}

// TestStartCrashAnalysisHostsSessionInGlobalNotActiveProject pins task 672:
// triggering one-click analyze while a project tab is active must file the
// analysis conversation under the built-in Global scope — never under the open
// project (the 2026-10-09 run landed inside video_comprehension) — and must
// leave the user's project roster and open tab untouched.
func TestStartCrashAnalysisHostsSessionInGlobalNotActiveProject(t *testing.T) {
	isolateDesktopUserDirs(t)

	// The user is working inside a project when the crash prompt's analyze
	// button fires; that project must gain nothing from the analysis.
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "video_comprehension"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	app.mu.Lock()
	// The user tab already has a live runtime; without a controller here the
	// stub publisher below would grab this tab instead of the analysis tab.
	app.tabs["tab-user"] = &WorkspaceTab{ID: "tab-user", Scope: "project", WorkspaceRoot: projectRoot,
		TopicID: "topic-user", TopicTitle: "user work", Ctrl: &heartbeatExecuteTaskCtrlStub{}, Ready: true}
	app.tabOrder = []string{"tab-user"}
	app.activeTabID = "tab-user"
	app.mu.Unlock()

	// Hermetic gates and a recorded submission: the analysis proceeds without a
	// real fork checkout, gh probe, or model turn.
	sourceDir := t.TempDir()
	origSource, origGh, origSubmit := crashAnalysisSourceDir, crashAnalysisGhAuth, crashAnalysisSubmit
	crashAnalysisSourceDir = func() string { return sourceDir }
	crashAnalysisGhAuth = func() (bool, string) { return true, "" }
	submittedTab := make(chan string, 1)
	var submittedInstruction string
	crashAnalysisSubmit = func(a *App, tabID, instruction string) error {
		submittedInstruction = instruction
		submittedTab <- tabID
		return nil
	}
	t.Cleanup(func() {
		crashAnalysisSourceDir, crashAnalysisGhAuth, crashAnalysisSubmit = origSource, origGh, origSubmit
	})

	// The analysis tab's controller builds in a goroutine; publish a stub the
	// same way the heartbeat cold-boot tests fake a cold boot.
	ctrl := &crashAnalysisCtrlStub{}
	tabIDCh := publishControllerAfterDelay(t, app, ctrl, 30*time.Millisecond)
	origWait := crashAnalysisControllerWaitTimeout
	crashAnalysisControllerWaitTimeout = 10 * time.Second
	t.Cleanup(func() { crashAnalysisControllerWaitTimeout = origWait })

	summary, err := app.StartCrashAnalysis("crash", "panic: boom")
	if err != nil {
		t.Fatalf("StartCrashAnalysis: %v", err)
	}
	if !strings.Contains(summary, "Global") {
		t.Fatalf("summary = %q, want it to name the Global host", summary)
	}

	hostedID := <-tabIDCh
	if got := <-submittedTab; got != hostedID {
		t.Fatalf("instruction submitted to tab %q, want the analysis tab %q", got, hostedID)
	}
	if !strings.Contains(submittedInstruction, "gh-issue-submit") {
		t.Fatalf("instruction lost the analysis contract: %.200s", submittedInstruction)
	}

	app.mu.RLock()
	tab := app.tabs[hostedID]
	var scope, root, topicID, sessionPath, tabMode, approvalMode string
	if tab != nil {
		scope, root, topicID, sessionPath = tab.Scope, tab.WorkspaceRoot, tab.TopicID, tab.SessionPath
		tabMode, approvalMode = tab.mode, tab.toolApprovalMode
	}
	userTopic := ""
	if userTab := app.tabs["tab-user"]; userTab != nil {
		userTopic = userTab.TopicID
	}
	app.mu.RUnlock()
	if tab == nil {
		t.Fatal("the analysis tab vanished")
	}
	if scope != "global" {
		t.Fatalf("analysis tab scope = %q, want global (task 672)", scope)
	}
	// A global tab carries one of the host's own directories as its root (the
	// open chain resolves the global workspace root into WorkspaceRoot); the
	// sidebar strips builtin roots, so this is what keeps the run out of every
	// project list.
	if root == "" || !isBuiltinWorkspaceRoot(root) {
		t.Fatalf("analysis tab workspace root = %q, want a builtin (host-owned) root", root)
	}
	if sessionPath == "" {
		t.Fatal("analysis tab has no session path")
	}
	if wantDir := desktopSessionDir(globalWorkspaceRoot()); !sameDesktopPath(filepath.Dir(sessionPath), wantDir) {
		t.Fatalf("analysis session %q lives outside the global session dir %q", sessionPath, wantDir)
	}
	if tabMode != "yolo" || approvalMode != control.ToolApprovalYolo {
		t.Fatalf("analysis tab mode/approval = %q/%q, want yolo on both", tabMode, approvalMode)
	}
	if userTopic != "topic-user" {
		t.Fatalf("active project tab topic changed to %q — the flow must not touch the user's open tab", userTopic)
	}
	if topicID == "" || topicID == "topic-user" {
		t.Fatalf("analysis topic id = %q, want a fresh topic distinct from the user's", topicID)
	}

	// Sidebar index: the conversation is listed under Global, and the open
	// project's topic roster gained nothing.
	projects := loadProjectsFile()
	globalHit := false
	for _, id := range projects.GlobalTopics {
		if id == topicID {
			globalHit = true
		}
	}
	if !globalHit {
		t.Fatalf("analysis topic %q missing from GlobalTopics = %v", topicID, projects.GlobalTopics)
	}
	for _, project := range projects.Projects {
		if !sameProjectRoot(project.Root, projectRoot) {
			continue
		}
		for _, id := range project.Topics {
			if id == topicID {
				t.Fatalf("analysis topic %q leaked into project %q topics = %v", topicID, project.Root, project.Topics)
			}
		}
	}
}

// The instruction the analysis session receives quotes the same source dir;
// the visible template must render it with real separators too (the same
// complaint targeted this path as "github-repo.reasonix").
func TestBuildCrashAnalysisInstructionKeepsPathSeparators(t *testing.T) {
	dir := `C:\Users\_\AppData\Roaming\reasonix\global-workspace\github-repo\reasonix`
	instruction := buildCrashAnalysisInstruction(dir, `{"kind":"crash"}`, false)
	if !strings.Contains(instruction, dir) {
		t.Fatalf("instruction must carry the source path verbatim (separators intact):\n%s", instruction)
	}
	if strings.Contains(instruction, "github-repo.reasonix") {
		t.Fatalf("instruction must not mangle the path separators:\n%s", instruction)
	}
}

// TestCrashAnalysisSessionOpenTimesOutWithoutController pins the bound: a tab
// whose controller never arrives fails the analysis start with the explicit
// not-ready error (which points at the manual Copy route) instead of hanging
// the binding forever.
func TestCrashAnalysisSessionOpenTimesOutWithoutController(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}

	origWait := crashAnalysisControllerWaitTimeout
	crashAnalysisControllerWaitTimeout = 250 * time.Millisecond
	t.Cleanup(func() { crashAnalysisControllerWaitTimeout = origWait })

	// A controller that arrives only after an hour: the wait must give up on
	// its own bound instead of waiting for it.
	ctrl := &heartbeatExecuteTaskCtrlStub{}
	publishControllerAfterDelay(t, app, ctrl, time.Hour)

	start := time.Now()
	_, err := app.openCrashAnalysisSession("crash")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("openCrashAnalysisSession succeeded without a controller")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("error = %v, want the explicit not-ready message", err)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("wait took %s, want it bounded by %s", elapsed, crashAnalysisControllerWaitTimeout)
	}
}

// TestCrashAnalysisTopicTitleCarriesKind keeps the Global sidebar row
// recognizable at a glance.
func TestCrashAnalysisTopicTitleCarriesKind(t *testing.T) {
	if got := crashAnalysisTopicTitle("performance"); got != "一键分析：performance" {
		t.Fatalf("crashAnalysisTopicTitle = %q", got)
	}
	if got := crashAnalysisTopicTitle("  "); got != "一键分析" {
		t.Fatalf("blank kind title = %q", got)
	}
}

// TestCrashAnalysisAvailabilityNoLongerGatesOnWorkspace pins task 687: with
// nothing open (no project expanded, no live tab), route B still reads ready —
// the analysis self-hosts in a fresh Global tab (task 672), so a live workspace
// is not its prerequisite. WorkspaceReady keeps reporting the honest live-tab
// state as informational only; the 2026-10-09 17:55 report was exactly this
// gate refusing the click until the user expanded a project.
func TestCrashAnalysisAvailabilityNoLongerGatesOnWorkspace(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	origSource, origGh := crashAnalysisSourceDir, crashAnalysisGhAuth
	crashAnalysisSourceDir = func() string { return `C:\src\reasonix` }
	crashAnalysisGhAuth = func() (bool, string) { return true, "" }
	t.Cleanup(func() { crashAnalysisSourceDir, crashAnalysisGhAuth = origSource, origGh })

	report := app.CrashAnalysisAvailability()
	if !report.Ready {
		t.Fatalf("route B must be ready with no live tab (the analysis self-hosts): %+v", report)
	}
	if report.WorkspaceReady {
		t.Fatalf("WorkspaceReady must stay honest about the no-tab state: %+v", report)
	}

	// The gh gate still works: without an identity route B is not ready.
	crashAnalysisGhAuth = func() (bool, string) { return false, "gh auth status failed" }
	if report := app.CrashAnalysisAvailability(); report.Ready {
		t.Fatalf("missing gh identity must keep route B not ready: %+v", report)
	}
}

// ── 任务 734：一键分析首条 user 输入走可见流（issue #42）───────────────────

// TestCrashAnalysisSubmitAnnouncesUserInputOnVisibleStream pins task 734: the
// backend-driven analysis submission must announce its user row on the tab's
// visible event stream (task 580's UserInput channel). The frontend hydrates
// the fresh session strictly before the backend write lands (tab:backend-
// activated → enqueueTabSwitch → hydrate, then awaitAnalysisController →
// submit), and before this fix only the durable-inbox claim path emitted
// UserInput — so the first user input stayed invisible until the next history
// reload (fork issue #42, 2026-10-10).
func TestCrashAnalysisSubmitAnnouncesUserInputOnVisibleStream(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := agent.NewSessionPath(dir, "test")
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &appendingDesktopRunner{session: sess, started: make(chan string, 1)}
	ctrl := control.New(control.Options{
		Runner: runner, Executor: exec, Sink: event.Discard, SessionDir: dir,
		SessionPath: path, Label: "test",
	})
	defer ctrl.Close()

	app := NewApp()
	app.setTestCtrl(ctrl, "deepseek/test")
	// setTestCtrl leaves the production sink unbuilt; install one so the
	// announcement has the same per-tab visible stream it rides in prod.
	app.tabs["test"].sink = &tabEventSink{tabID: "test", app: app}

	const instruction = "请对照诊断载荷定位根因，并提交 issue 后把链接发回本会话"
	if err := crashAnalysisSubmit(app, "test", instruction); err != nil {
		t.Fatalf("crashAnalysisSubmit: %v", err)
	}
	select {
	case got := <-runner.started:
		// The controller composes the raw input (reasoning-language preamble
		// etc.), so pin the instruction as the submitted tail, not equality.
		if !strings.HasSuffix(got, instruction) {
			t.Fatalf("admitted turn input %q does not carry the analysis instruction", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the analysis turn was never admitted")
	}

	// The tab sink has no wails ctx in tests, so the wire envelope lands in
	// the #9601 pending buffer — the same bytes setContext would flush.
	tab := app.tabs["test"]
	if tab == nil || tab.sink == nil {
		t.Fatal("test tab and sink must exist")
	}
	tab.sink.mu.RLock()
	buffered := append([]runtimeEventEnvelope(nil), tab.sink.pendingRuntimeEvents...)
	tab.sink.mu.RUnlock()

	var announced bool
	for _, env := range buffered {
		if env.name != eventChannel || len(env.payload) == 0 {
			continue
		}
		wire, err := json.Marshal(env.payload[0])
		if err != nil {
			continue
		}
		var decoded struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		if json.Unmarshal(wire, &decoded) != nil {
			continue
		}
		if decoded.Kind == "user_input" && decoded.Text == instruction {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("no user_input wire event carrying the instruction on %s (buffered=%d)", eventChannel, len(buffered))
	}

	// Best-effort contract: a missing tab or a blank display must never fail.
	app.emitBackendSubmittedUserInput("tab-missing", instruction)
	app.emitBackendSubmittedUserInput("test", "   ")
}
