package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/doctor"
)

// ── task 663 gap ①/⑥: hang analysis ─────────────────────────────────────────

func hangReportForVerdict(verdict string, startedAgo time.Duration) doctor.ResponsivenessReport {
	report := doctor.ResponsivenessReport{Verdict: verdict, Detail: "detail for " + verdict}
	if startedAgo > 0 {
		started := time.Now().Add(-startedAgo)
		report.Active = &doctor.ResponsivenessTurn{
			TurnID:    "turn-1",
			Open:      true,
			StartedAt: started.UTC().Format(time.RFC3339),
		}
	}
	return report
}

// The hang gate is the task-663 trigger definition: silent (ledger silent past
// the stream-watchdog window) is hung; first_response_wait counts only once
// the wait itself outlives the same threshold; everything alive or waiting on
// the user must not trigger.
func TestResponsivenessVerdictIsHang(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		report doctor.ResponsivenessReport
		want   bool
	}{
		{"silent is hung", hangReportForVerdict("silent", 0), true},
		{"first response wait past the silence threshold is hung", hangReportForVerdict("first_response_wait", doctor.ResponsivenessSilenceAfter), true},
		{"first response wait under the threshold is not hung", hangReportForVerdict("first_response_wait", doctor.ResponsivenessSilenceAfter-time.Minute), false},
		{"first response wait without an active turn is not hung", hangReportForVerdict("first_response_wait", 0), false},
		{"working is not hung", hangReportForVerdict("working", 0), false},
		{"idle is not hung", hangReportForVerdict("idle", 0), false},
		{"waiting user is not hung", hangReportForVerdict("waiting_user", 0), false},
		{"no ledger is not hung", hangReportForVerdict("no_ledger", 0), false},
	}
	for _, tc := range cases {
		if got := responsivenessVerdictIsHang(tc.report, now); got != tc.want {
			t.Errorf("%s: hung = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The refusal must name the verdict so the user can tell "not stuck" refusals
// from a dead button (task 663 ②: failures are explicit).
func TestHangGateErrorRefusesHealthySessionExplicitly(t *testing.T) {
	if err := hangGateError(hangReportForVerdict("silent", 0), time.Now()); err != nil {
		t.Fatalf("a silent session must pass the hang gate, got %v", err)
	}
	err := hangGateError(hangReportForVerdict("working", 0), time.Now())
	if err == nil {
		t.Fatal("a working session must be refused")
	}
	for _, want := range []string{"not hung", "verdict: working", "analysis not started"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

// The hang instruction reuses the crash contract but frames the triage as a
// hang (never a crash) and names the watchdog-blind paths upstream self-heal
// cannot cover.
func TestBuildHangAnalysisInstructionFramesHangTriage(t *testing.T) {
	instruction := buildHangAnalysisInstruction(`C:\src\reasonix`, `{"kind":"performance"}`)
	for _, want := range []string{
		"请分析",
		"卡顿",
		"hang",
		"不要归类为崩溃",
		"defaultStreamIdleTimeout",
		"gh-issue-submit",
		crashAnalysisRepo,
		`C:\src\reasonix`,
		`{"kind":"performance"}`,
		"issue 链接",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("hang instruction missing %q:\n%s", want, instruction)
		}
	}
	if strings.Contains(instruction, "测试/mock 报告") {
		t.Fatalf("hang instruction must not carry the mock notice:\n%s", instruction)
	}
}

// The hang payload must survive the same sanitizer the crash entries use, so
// the pipeline is literally one family (task 663 ⑥: same pipeline).
func TestHangAnalysisPayloadRoundTripsThroughCrashReportFromDetail(t *testing.T) {
	report := hangReportForVerdict("silent", 0)
	report.SessionPath = `C:\Users\me\...\session.jsonl`
	payload, err := hangAnalysisPayload(report, time.Now())
	if err != nil {
		t.Fatalf("hangAnalysisPayload: %v", err)
	}
	r, err := crashReportFromDetail("performance", payload)
	if err != nil {
		t.Fatalf("crashReportFromDetail rejected the hang payload: %v", err)
	}
	if r.Kind != "performance" {
		t.Errorf("kind = %q, want performance", r.Kind)
	}
	if r.Source != "hang" {
		t.Errorf("source = %q, want hang", r.Source)
	}
	if r.Label != "session.hang" {
		t.Errorf("label = %q, want session.hang", r.Label)
	}
	if r.ErrorType != "SessionUnresponsive" {
		t.Errorf("errorType = %q, want SessionUnresponsive", r.ErrorType)
	}
	if !strings.Contains(r.Message, "silent") {
		t.Errorf("message must carry the doctor verdict, got: %s", r.Message)
	}
	if !strings.Contains(r.TopFrame, "doctor.responsiveness:silent") {
		t.Errorf("topFrame must name the verdict source, got %q", r.TopFrame)
	}
}

// ── task 663 ⑤: analysis session naming ─────────────────────────────────────

func TestCrashAnalysisSessionTitleNamesAndMarksMock(t *testing.T) {
	title := crashAnalysisSessionTitle("崩溃分析", false)
	if !strings.HasPrefix(title, "崩溃分析-") {
		t.Errorf("title %q must start with the base name", title)
	}
	if strings.Contains(title, "测试") {
		t.Errorf("a real analysis must not carry the mock marker: %q", title)
	}
	mock := crashAnalysisSessionTitle("崩溃分析", true)
	if !strings.HasPrefix(mock, "崩溃分析-测试-") {
		t.Errorf("mock title %q must carry the explicit 测试 marker", mock)
	}
	hang := crashAnalysisSessionTitle("卡顿分析", false)
	if !strings.HasPrefix(hang, "卡顿分析-") {
		t.Errorf("hang title %q must start with its own base name", hang)
	}
	// The timestamp suffix must sort: same-second titles are identical, and the
	// shape is YYYYMMDD-HHMMSS.
	suffix := strings.TrimPrefix(title, "崩溃分析-")
	if len(suffix) != len("20060102-150405") || suffix[8] != '-' {
		t.Errorf("timestamp suffix shape wrong: %q", suffix)
	}
}

// ── task 663 ②: analysis run tracking state machine ─────────────────────────

func TestObserveCrashAnalysisRunStateMachine(t *testing.T) {
	now := time.Now()
	newRun := func() *crashAnalysisRun {
		return &crashAnalysisRun{sessionPath: `C:\x\s.jsonl`, startedAt: now}
	}

	// Streaming keeps the run open and records that progress was seen.
	run := newRun()
	observeCrashAnalysisRun(run, crashAnalysisTurnObs{found: true, running: true}, now)
	if run.done || !run.seenRunning {
		t.Fatalf("a running observation must keep the run open, got done=%v seenRunning=%v", run.done, run.seenRunning)
	}
	// running → not running: the turn settled.
	observeCrashAnalysisRun(run, crashAnalysisTurnObs{found: true, running: false}, now.Add(time.Minute))
	if !run.done || run.doneAt.IsZero() {
		t.Fatalf("running→idle must finish the run, got done=%v doneAt=%v", run.done, run.doneAt)
	}
	// Done is sticky.
	observeCrashAnalysisRun(run, crashAnalysisTurnObs{found: true, running: true}, now.Add(2*time.Minute))
	if !run.done {
		t.Fatal("a done run must stay done")
	}

	// A terminal status finishes the run even if the poller never caught the
	// running window (fast failure between polls).
	run = newRun()
	observeCrashAnalysisRun(run, crashAnalysisTurnObs{found: true, running: false, terminal: true}, now)
	if !run.done {
		t.Fatal("a terminal turn observation must finish the run")
	}

	// The hosting tab disappearing finishes the run: an analysis whose tab is
	// gone cannot progress.
	run = newRun()
	observeCrashAnalysisRun(run, crashAnalysisTurnObs{found: false}, now)
	if !run.done {
		t.Fatal("a vanished hosting tab must finish the run")
	}

	// Never-streaming and never-terminal stays open (submission admission lag).
	run = newRun()
	observeCrashAnalysisRun(run, crashAnalysisTurnObs{found: true, running: false}, now)
	if run.done {
		t.Fatal("not-yet-admitted turn must keep the run open")
	}
}

// ── task 663 ②: explicit failures at the entry gates ─────────────────────────

func stubCrashAnalysisSourceDir(t *testing.T, dir string) {
	t.Helper()
	orig := detectCrashAnalysisSourceDir
	detectCrashAnalysisSourceDir = func() string { return dir }
	t.Cleanup(func() { detectCrashAnalysisSourceDir = orig })
}

func stubGhProbe(t *testing.T, ok bool, detail string) {
	t.Helper()
	orig := ghAuthenticatedProbe
	ghAuthenticatedProbe = func() (bool, string) { return ok, detail }
	t.Cleanup(func() { ghAuthenticatedProbe = orig })
}

func fixtureReasonixSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "desktop"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module reasonix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desktop", "crash_app.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The 2026-10-09 incident face: a click that starts nothing must say so in the
// returned error instead of vanishing (the frontend paints it verbatim).
func TestStartCrashAnalysisRefusesWithoutSourceExplicitly(t *testing.T) {
	stubCrashAnalysisSourceDir(t, "")
	app := NewApp()
	_, err := app.StartCrashAnalysis("crash", `{"kind":"crash","message":"boom"}`)
	if err == nil {
		t.Fatal("missing source must refuse the analysis")
	}
	for _, want := range []string{"no local reasonix source detected", "Copy button"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestStartHangAnalysisRefusesWithoutSourceExplicitly(t *testing.T) {
	stubCrashAnalysisSourceDir(t, "")
	app := NewApp()
	_, err := app.StartHangAnalysis()
	if err == nil {
		t.Fatal("missing source must refuse the hang analysis")
	}
	if !strings.Contains(err.Error(), "no local reasonix source detected") {
		t.Errorf("error must name the missing source: %v", err)
	}
}

// Without a live active-tab session the hang probe cannot even measure — that
// must be an explicit error too, not a silent no-op.
func TestStartHangAnalysisRefusesWithoutLiveSession(t *testing.T) {
	stubCrashAnalysisSourceDir(t, fixtureReasonixSourceDir(t))
	stubGhProbe(t, true, "")
	app := NewApp()
	_, err := app.StartHangAnalysis()
	if err == nil {
		t.Fatal("no live session must refuse the hang analysis")
	}
	if !strings.Contains(err.Error(), "could not read the active session's responsiveness") {
		t.Errorf("error must name the probe failure: %v", err)
	}
	avail := app.HangAnalysisAvailability()
	if avail.Ready || avail.Hung {
		t.Errorf("availability with no live session must be not-ready/not-hung, got %+v", avail)
	}
	if avail.Verdict != "unknown" {
		t.Errorf("availability verdict with no live session must be unknown, got %q", avail.Verdict)
	}
}

// The progress read model without any run reports inactive, and the begin call
// flips it active with a scrubbed session path.
func TestCrashAnalysisProgressTracksBegunRun(t *testing.T) {
	app := NewApp()
	if report := app.CrashAnalysisProgress(); report.Active {
		t.Fatalf("no run begun yet, progress must be inactive: %+v", report)
	}
	app.beginCrashAnalysisRun("") // empty path must be a no-op, not a panic
	if report := app.CrashAnalysisProgress(); report.Active {
		t.Fatalf("empty session path must not begin a run: %+v", report)
	}
	app.beginCrashAnalysisRun(`C:\Users\me\sessions\s1.jsonl`)
	report := app.CrashAnalysisProgress()
	// NewApp() has no tabs, so nothing hosts the session: the run must already
	// read as finished (the vanished-host rule), not stuck open forever.
	if !report.Active || !report.Done {
		t.Fatalf("a begun run with no hosting tab must be active+done, got: %+v", report)
	}
	if strings.Contains(report.SessionPath, "me") {
		t.Errorf("progress must scrub user paths, got %q", report.SessionPath)
	}
	if report.StartedAt == "" || report.DoneAt == "" {
		t.Error("progress must carry the start and done times")
	}
}
