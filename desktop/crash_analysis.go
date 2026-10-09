package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/proc"	"reasonix/internal/control")

// crash_analysis.go is task 617 route B: "one-click analyze" from the crash /
// performance prompt. It reuses the existing session surface — open a fresh
// Global-scope conversation (task 672: never under the active tab's project),
// switch it to YOLO, and submit an initial instruction that carries the
// sanitized diagnostic payload and asks the agent to run the gh-issue-submit
// skill and report the issue link back into the session. No new network
// endpoint is introduced; the only side effects are a local gh auth probe and
// the user-approved session itself.
//
// Route A (copy a paste-ready issue skeleton) stays the zero-dependency
// fallback whenever any prerequisite here fails.

const crashAnalysisRepo = "Linearl/DeepSeek-Reasonix"

// ghAuthenticatedProbe is the test seam over ghAuthenticated: the analysis
// entry points must be provable without a real `gh auth status` (task 663).
var ghAuthenticatedProbe = ghAuthenticated

type CrashAnalysisAvailabilityReport struct {
	// SourceReady: a local reasonix fork checkout was detected. Root-cause
	// analysis needs the source; diagnostics alone only carry symptoms.
	SourceReady bool   `json:"sourceReady"`
	SourceDir   string `json:"sourceDir,omitempty"`
	// GhAuthenticated: `gh auth status` (with token env vars stripped, matching
	// the libs-side keyring convention) reports a usable GitHub identity.
	GhAuthenticated bool   `json:"ghAuthenticated"`
	GhCheckDetail   string `json:"ghCheckDetail,omitempty"`
	// WorkspaceReady: the app has a live writable tab. The analysis itself is
	// hosted in a fresh Global tab (task 672), so this stays a readiness proxy
	// for the session surface, not the analysis host.
	WorkspaceReady bool `json:"workspaceReady"`
	Ready          bool `json:"ready"`
}

// Task 672 seams: the gates and the final submission are indirected so tests
// can drive StartCrashAnalysis end to end without a real fork checkout, gh
// identity, or model runtime (same pattern as ghFallbackLocationDirs below).
var (
	crashAnalysisSourceDir = detectCrashAnalysisSourceDir
	crashAnalysisGhAuth    = ghAuthenticated
	crashAnalysisSubmit    = func(a *App, tabID, instruction string) error {
		return a.SubmitToTab(tabID, instruction)
	}
)

// crashAnalysisControllerWaitTimeout bounds the wait for the freshly opened
// analysis tab's controller (the build runs in a goroutine, so the tab exists
// before its runtime does — heartbeatControllerWaitTimeout is the same idea
// for heartbeat runs). A var so tests can shorten it.
var crashAnalysisControllerWaitTimeout = 60 * time.Second

const crashAnalysisControllerPollInterval = 50 * time.Millisecond

// CrashAnalysisAvailability runs the three route-B prerequisite checks without
// starting anything. The frontend gates the one-click flow on this report and
// shows one distinct notice per failed check (missing source / spend warning /
// gh auth).
func (a *App) CrashAnalysisAvailability() CrashAnalysisAvailabilityReport {
<<<<<<< HEAD
	dir := crashAnalysisSourceDir()
	ghOK, ghDetail := crashAnalysisGhAuth()
=======
	dir := detectCrashAnalysisSourceDir()
	ghOK, ghDetail := ghAuthenticatedProbe()
>>>>>>> wt-663
	report := CrashAnalysisAvailabilityReport{
		SourceReady:     dir != "",
		SourceDir:       dir,
		GhAuthenticated: ghOK,
		GhCheckDetail:   ghDetail,
	}
	a.mu.RLock()
	tab := a.activeTabLocked()
	live := tab != nil && !a.tabIsReadOnly(tab) && tab.Ctrl != nil
	a.mu.RUnlock()
	report.WorkspaceReady = live
	report.Ready = report.SourceReady && report.GhAuthenticated && report.WorkspaceReady
	return report
}

// StartCrashAnalysis performs route B end to end and returns a short summary of
// what was started. Every prerequisite is re-checked here so the binding stays
// safe even if the frontend's earlier availability probe went stale.
//
// Task 663: every milestone is logged (the 2026-10-09 click left no trace in
// desktop.log and the user could not tell started from silently dead), the new
// session is named 崩溃分析-<时间> so it is identifiable in the tab/topic list,
// and the run is registered for CrashAnalysisProgress polling.
func (a *App) StartCrashAnalysis(kind, detail string) (string, error) {
	dir := crashAnalysisSourceDir()
	if dir == "" {
		return "", fmt.Errorf("no local reasonix source detected — root-cause analysis needs the fork checkout; use the Copy button to report manually")
	}
<<<<<<< HEAD
	if ok, ghDetail := crashAnalysisGhAuth(); !ok {
=======
	if ok, ghDetail := ghAuthenticatedProbe(); !ok {
>>>>>>> wt-663
		hint := strings.TrimSpace(ghDetail)
		if hint == "" {
			hint = "gh auth status failed"
		}
		return "", fmt.Errorf("GitHub CLI check failed (%s) — install the GitHub CLI or run gh auth login first, or use the Copy button to report manually", hint)
	}

	r, err := crashReportFromDetail(kind, detail)
	if err != nil {
		return "", err
	}
	if err := ensureCrashIdentity(&r); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}

	// Task 672: the analysis conversation is hosted under the built-in Global
	// scope, never under the active tab's project. The route used to rotate the
	// active tab (NewSessionForTab("")), which filed the fresh topic under
	// whichever project the user had open — a 2026-10-09 run landed inside
	// video_comprehension. The analysis is a system-level diagnosis and belongs
	// to no user project, so it takes the same path heartbeat runs do:
	// CreateTopic(global) → open a global tab → wait for its controller. The
	// user's open project gains nothing; the sidebar lists the run under Global.
	tabID, err := a.openCrashAnalysisSession(r.Kind)
	if err != nil {
		return "", err
	}
<<<<<<< HEAD
	// Force YOLO so the analysis run is not interrupted by approval prompts,
	// then submit the instruction.
	a.SetModeForTab(tabID, "yolo")
=======
	sessionPath, sessionDir := a.activeSessionLocation()
	title := crashAnalysisSessionTitle("崩溃分析", r.TestMock)
	if sessionPath != "" {
		// Task 663 ⑤: name the session so the analysis is identifiable in the
		// tab list ("新的会话" is how the 2026-10-09 run got lost). Best-effort:
		// a rename failure must not kill the analysis itself.
		if renameErr := agent.RenameSession(sessionPath, title); renameErr != nil {
			slog.Warn("crash-analysis: session rename failed (analysis continues)", "err", renameErr)
		} else if titleErr := a.onSessionTitleChanged(sessionDir, sessionPath, title); titleErr != nil {
			slog.Warn("crash-analysis: session title projection failed (analysis continues)", "err", titleErr)
		}
	}
	a.SetModeForTab("", "yolo")
>>>>>>> wt-663
	instruction := buildCrashAnalysisInstruction(dir, string(payload), r.TestMock)
	if err := crashAnalysisSubmit(a, tabID, instruction); err != nil {
		return "", fmt.Errorf("could not submit the analysis instruction (%v) — use the Copy button to report manually", err)
	}
<<<<<<< HEAD
	return crashAnalysisSummary(dir), nil
}

// crashAnalysisSummary is the detail line the frontend appends under the
// localized "analysis started" notice (task 673). It was hardcoded English,
// so the zh popup read as a garbled half-translated blob and the raw Windows
// path transcribed as "github-repo.reasonix" (separators visually lost).
// Chinese matches every other Go-side string of this surface (the instruction
// template), and the phrasing complements the notice instead of repeating it:
// the notice already says the session started and where the issue link lands.
func crashAnalysisSummary(dir string) string {
	return fmt.Sprintf("分析将对照本地源码 %s 定位根因，并提交 issue 到 %s。", dir, crashAnalysisRepo)

// openCrashAnalysisSession creates the Global-scope conversation hosting a
// one-click analysis run and waits for its tab controller. It returns the ID
// of the tab the analysis instruction must be submitted to.
func (a *App) openCrashAnalysisSession(kind string) (string, error) {
	topic, err := a.CreateTopic("global", "", crashAnalysisTopicTitle(kind))
	if err != nil {
		return "", fmt.Errorf("could not create the analysis conversation under Global (%v) — use the Copy button to report manually", err)
	}
	tabMeta, err := a.openGlobalTab(topic.ID)
	if err != nil {
		return "", fmt.Errorf("could not open the analysis tab (%v) — use the Copy button to report manually", err)
	}
	if a.awaitAnalysisController(tabMeta.ID) == nil {
		return "", fmt.Errorf("the analysis workspace did not become ready in time — use the Copy button to report manually")
	}
	return tabMeta.ID, nil
}

// crashAnalysisTopicTitle names the sidebar row the analysis files under, so
// the run is recognizable in the Global folder at a glance.
func crashAnalysisTopicTitle(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "一键分析"
	}
	return "一键分析：" + kind
}

// awaitAnalysisController waits for the analysis tab's controller, mirroring
// the heartbeat engine's wait: the controller may still be building when the
// tab-open returns, so the wait follows the build's completion signal instead
// of a fixed sleep, and gives up on the configured bound.
func (a *App) awaitAnalysisController(tabID string) control.SessionAPI {
	deadline := time.NewTimer(crashAnalysisControllerWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(crashAnalysisControllerPollInterval)
	defer ticker.Stop()
	for {
		if ctrl := a.ctrlByTabID(tabID); ctrl != nil {
			return ctrl
		}
		buildDone := a.tabBuildDone(tabID)
		if buildDone == nil {
			// No build in flight: either the controller is already published
			// (checked above) or the build failed. Keep polling so a build that
			// starts a moment later is not missed, but never past the deadline.
			select {
			case <-deadline.C:
				return nil
			case <-ticker.C:
			}
			continue
		}
		select {
		case <-deadline.C:
			return nil
		case <-buildDone:
			// Re-check on the next round: the build may have failed, leaving
			// the controller nil.
		case <-ticker.C:
		}
	}

=======
	a.beginCrashAnalysisRun(sessionPath)
	slog.Info("crash-analysis: started", "kind", r.Kind, "testMock", r.TestMock,
		"session", scrubUserPaths(sessionPath), "title", title, "sourceDir", scrubUserPaths(dir))
	return fmt.Sprintf("YOLO 分析会话已启动并命名为「%s」（%s）；分析将对照 %s 定位根因，并提交 issue 到 %s。结论（issue 链接或根因分析）会发送到该会话。",
		title, scrubUserPaths(sessionPath), dir, crashAnalysisRepo), nil
}

// activeSessionLocation returns the active tab's session path and dir, "" when
// the workspace has no live session. Read-only helper for the analysis entries.
func (a *App) activeSessionLocation() (string, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.activeTabLocked()
	if tab == nil || a.tabIsReadOnly(tab) || tab.Ctrl == nil {
		return "", ""
	}
	return strings.TrimSpace(tab.Ctrl.SessionPath()), strings.TrimSpace(tab.Ctrl.SessionDir())
}

// crashAnalysisSessionTitle builds the task-663 session name: <base>-<时间>,
// with an explicit -测试 marker for lab mock drills so a drill is never
// readable as a real failure in the tab list either.
func crashAnalysisSessionTitle(base string, testMock bool) string {
	name := base
	if testMock {
		name += "-测试"
	}
	return name + "-" + time.Now().Format("20060102-150405")
>>>>>>> wt-663
}

func buildCrashAnalysisInstruction(sourceDir, payload string, testMock bool) string {
	notice := ""
	if testMock {
		// Task 642: the lab mock entry reuses this chain, so the analysis run
		// must know the diagnostic is simulated — it is a pipeline drill, not a
		// real failure, and the submitted issue has to say so.
		notice = "注意：这是一条测试/mock 报告（实验室「模拟崩溃测试」入口生成），用于验证上报链路，不是真实故障。issue 请在标题与正文明确标注 mock/test，不要当成真实故障归类。"
	}
	return buildAnalysisInstruction(sourceDir, payload, notice)
}

// buildAnalysisInstruction is the shared contract of the one-click analysis
// family (task 663): notice carries the per-entry framing (mock drill, hang
// triage) ahead of the shared requirements.
func buildAnalysisInstruction(sourceDir, payload, notice string) string {
	var b strings.Builder
	b.WriteString("请分析以下 Reasonix 桌面端诊断报告，定位根因并提交 issue。\n\n")
	if notice != "" {
		b.WriteString(notice)
		b.WriteString("\n\n")
	}
	b.WriteString("要求：\n")
	b.WriteString(fmt.Sprintf("1. 调用 gh-issue-submit 技能，分析该诊断的根因，提交 issue 到 %s，附复现线索与 file:line。\n", crashAnalysisRepo))
	b.WriteString(fmt.Sprintf("2. 本地 fork 源码位于 %s ，优先在其中定位相关代码路径，给出具体文件与行号。\n", sourceDir))
	b.WriteString("3. issue 提交成功后，把 issue 链接发送到本会话；若提交失败，把根因分析与建议的 issue 标题/正文发送到本会话。\n\n")
	b.WriteString("诊断 payload（已脱敏）：\n```json\n")
	b.WriteString(payload)
	b.WriteString("\n```\n")
	return b.String()
}

// detectCrashAnalysisSourceDir probes the known fork checkout locations and
// returns the first one that actually looks like the reasonix source tree.
// Detection failure is the normal "source not downloaded" case: route B stops
// and the UI points at route A. Var so tests can point the analysis entries at
// a fixture checkout (task 663).
var detectCrashAnalysisSourceDir = func() string {
	candidates := []string{
		// The workspace layout on this machine: the global workspace (sibling of
		// the app's own user dir) hosts local repo checkouts under github-repo/.
		filepath.Join(config.MemoryUserDir(), "global-workspace", "github-repo", "reasonix"),
	}
	for _, dir := range candidates {
		if isReasonixSourceDir(dir) {
			return dir
		}
	}
	return ""
}

func isReasonixSourceDir(dir string) bool {
	goModPath := filepath.Join(dir, "go.mod")
	body, err := os.ReadFile(goModPath)
	if err != nil || !bytes.Contains(body, []byte("module reasonix")) {
		return false
	}
	// The diagnostics under analysis live in the desktop surface; require its
	// source so "some other repo named reasonix" does not pass.
	_, err = os.Stat(filepath.Join(dir, "desktop", "crash_app.go"))
	return err == nil
}

// ghFallbackLocationDirs yields the candidate install directories for gh that a
// launcher-started GUI process can miss: desktop.exe inherits the PATH snapshot
// captured at launch, so a gh installed (or PATH-extended) afterwards stays
// invisible to exec.LookPath even though shells see it fine (task 643).
// Tests stub this var; production resolves from the platform env per call.
var ghFallbackLocationDirs = func() []string {
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(os.Getenv("ProgramFiles"), "GitHub CLI"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "GitHub CLI"),
			filepath.Join(os.Getenv("LocalAppData"), "Programs", "GitHub CLI"),
			filepath.Join(os.Getenv("LocalAppData"), "Microsoft", "WinGet", "Links"),
			filepath.Join(os.Getenv("USERPROFILE"), "scoop", "shims"),
		}
	}
	return []string{"/usr/local/bin", "/opt/homebrew/bin"}
}

func ghExecutableName() string {
	if runtime.GOOS == "windows" {
		return "gh.exe"
	}
	return "gh"
}

// ghFallbackLocations turns the candidate directories into absolute executable
// paths. An unset env var would yield a relative path (e.g. "GitHub CLI") that
// could accidentally resolve against the process cwd — those are dropped.
func ghFallbackLocations() []string {
	exe := ghExecutableName()
	locations := make([]string, 0)
	for _, dir := range ghFallbackLocationDirs() {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		locations = append(locations, filepath.Join(dir, exe))
	}
	return locations
}

// resolveGhExecutable finds gh: PATH first, then the known install locations.
// The returned path is exec-ready (an absolute path with spaces is fine — no
// shell is involved). onPath records which source matched so the report can
// explain a stale process PATH instead of silently absorbing it.
func resolveGhExecutable() (ghPath string, onPath bool, found bool) {
	if gh, err := exec.LookPath("gh"); err == nil {
		return gh, true, true
	}
	for _, candidate := range ghFallbackLocations() {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, false, true
		}
	}
	return "", false, false
}

// ghAuthenticated probes the GitHub CLI the same way the analysis run will use
// it: token env vars stripped so gh resolves the keyring-stored identity (the
// libs-side `env -u GITHUB_TOKEN gh ...` convention) rather than an exported
// variable the session would not see. The detail string distinguishes the three
// outcomes the UI needs to tell apart (task 643): not found anywhere, found
// outside PATH (a stale process PATH — the historical false alarm), and found
// but `gh auth status` failed (its output is passed through).
func ghAuthenticated() (bool, string) {
	gh, onPath, found := resolveGhExecutable()
	if !found {
		return false, "gh CLI not found on PATH or in known install locations"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// proc.CommandContext hides the console window on Windows: this gh pre-flight
	// runs as a background check inside the desktop app (no console expected).
	cmd := proc.CommandContext(ctx, gh, "auth", "status")
	cmd.Env = filterEnv(os.Environ(), "GITHUB_TOKEN", "GH_TOKEN")
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if ctx.Err() != nil {
			detail = "gh auth status timed out"
		}
		return false, detail
	}
	return true, ghSuccessDetail(gh, onPath)
}

// ghSuccessDetail keeps a fallback discovery visible even when auth passes: the
// analysis session itself runs gh from the user's shell (whose PATH is fine),
// so route B can proceed, but the report should still say why the app process
// could not see gh directly.
func ghSuccessDetail(ghPath string, onPath bool) string {
	if onPath {
		return ""
	}
	return fmt.Sprintf("gh found outside PATH at %s (this process inherited an outdated PATH); auth OK", ghPath)
}

func filterEnv(environ []string, names ...string) []string {
	drop := make(map[string]bool, len(names))
	for _, name := range names {
		drop[name] = true
	}
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if key, _, found := strings.Cut(entry, "="); found && drop[key] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// ── task 663 ②: analysis run tracking ───────────────────────────────────────
// The 2026-10-09 incident: the user clicked one-click analyze, could not tell
// whether anything started (no visible progress, no completion), and killed it
// early. beginCrashAnalysisRun registers the hosting session after a successful
// start; CrashAnalysisProgress is the read model the frontend polls. The state
// machine itself (observeCrashAnalysisRun) is pure and unit-tested; the App
// method only feeds it the tab's live RuntimeStatus.

type crashAnalysisRun struct {
	sessionPath string
	startedAt   time.Time
	seenRunning bool
	done        bool
	doneAt      time.Time
}

// crashAnalysisTurnObs is one observation of the hosting tab's turn state.
type crashAnalysisTurnObs struct {
	found    bool // a live tab still hosts the session
	running  bool // its runtime reports a running turn
	terminal bool // the current turn settled in a terminal state
}

// observeCrashAnalysisRun folds an observation into the run state. Done fires
// when the turn settled (terminal status, or running→not-running after the run
// was seen streaming), or when the hosting tab is gone — an analysis whose tab
// died cannot progress anymore.
func observeCrashAnalysisRun(run *crashAnalysisRun, obs crashAnalysisTurnObs, now time.Time) {
	if run.done {
		return
	}
	if !obs.found {
		run.done, run.doneAt = true, now
		return
	}
	if obs.running {
		run.seenRunning = true
		return
	}
	if obs.terminal || run.seenRunning {
		run.done, run.doneAt = true, now
	}
}

// beginCrashAnalysisRun registers the session hosting a fresh analysis run.
func (a *App) beginCrashAnalysisRun(sessionPath string) {
	if sessionPath == "" {
		return
	}
	a.crashAnalysisMu.Lock()
	a.crashAnalysisRun = &crashAnalysisRun{sessionPath: sessionPath, startedAt: time.Now()}
	a.crashAnalysisMu.Unlock()
}

// CrashAnalysisProgressReport is the read model for the analysis progress
// poller. Active=false means nothing is being tracked (no run started in this
// process yet); Done means the analysis turn settled (or its tab disappeared).
type CrashAnalysisProgressReport struct {
	Active      bool   `json:"active"`
	SessionPath string `json:"sessionPath,omitempty"` // user paths scrubbed
	StartedAt   string `json:"startedAt,omitempty"`
	Running     bool   `json:"running"`
	Done        bool   `json:"done"`
	DoneAt      string `json:"doneAt,omitempty"`
}

// CrashAnalysisProgress reports the tracked analysis run's live state.
func (a *App) CrashAnalysisProgress() CrashAnalysisProgressReport {
	a.crashAnalysisMu.Lock()
	run := a.crashAnalysisRun
	a.crashAnalysisMu.Unlock()
	if run == nil {
		return CrashAnalysisProgressReport{Active: false}
	}
	now := time.Now()
	obs := a.observeAnalysisSession(run.sessionPath)
	a.crashAnalysisMu.Lock()
	observeCrashAnalysisRun(run, obs, now)
	report := CrashAnalysisProgressReport{
		Active:      true,
		SessionPath: scrubUserPaths(run.sessionPath),
		StartedAt:   run.startedAt.UTC().Format(time.RFC3339),
		Running:     obs.running,
		Done:        run.done,
	}
	if !run.doneAt.IsZero() {
		report.DoneAt = run.doneAt.UTC().Format(time.RFC3339)
	}
	a.crashAnalysisMu.Unlock()
	return report
}

// observeAnalysisSession finds the tab hosting sessionPath and reads its turn
// state. Read-only; a missing tab reads as found=false.
func (a *App) observeAnalysisSession(sessionPath string) crashAnalysisTurnObs {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		if strings.TrimSpace(tab.Ctrl.SessionPath()) != sessionPath {
			continue
		}
		status := tab.Ctrl.RuntimeStatus()
		return crashAnalysisTurnObs{
			found:    true,
			running:  status.Running,
			terminal: status.Status != "" && status.Status.Terminal(),
		}
	}
	return crashAnalysisTurnObs{found: false}
}
