package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/doctor"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/store"
	"reasonix/internal/turnevent"
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
			// 任务 1028: the gate reads ledger activity too. The minimal shape
			// these legacy cases exercise is "no write since the turn began"
			// (LastEventAt == StartedAt), so age and silence rise together.
			LastEventAt: started.UTC().Format(time.RFC3339),
		}
	}
	return report
}

// hangReportFirstResponseWait builds a first_response_wait report with
// INDEPENDENT age and ledger-silence clocks — the two dimensions 任务 1028
// splits apart (issue #58: age counted compaction prep, silence was 83s).
func hangReportFirstResponseWait(now, started, lastEvent time.Time) doctor.ResponsivenessReport {
	return doctor.ResponsivenessReport{
		Verdict: "first_response_wait",
		Detail:  "detail for first_response_wait",
		Active: &doctor.ResponsivenessTurn{
			TurnID:      "turn-1",
			Open:        true,
			StartedAt:   started.UTC().Format(time.RFC3339),
			LastEventAt: lastEvent.UTC().Format(time.RFC3339),
		},
	}
}

// The hang gate is the task-663 trigger definition: silent (ledger silent past
// the stream-watchdog window) is hung; first_response_wait counts only once
// BOTH its age and its ledger silence outlive the same threshold (任务 1028);
// everything alive or waiting on the user must not trigger.
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
		{"long-blocking tool silence (task 879) is not hung", hangReportForVerdict("waiting_tool", 0), false},
		{"no ledger is not hung", hangReportForVerdict("no_ledger", 0), false},
	}
	for _, tc := range cases {
		if got := responsivenessVerdictIsHang(tc.report, now); got != tc.want {
			t.Errorf("%s: hung = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 任务 1028 (fork issue #58) 验收①②: the first_response_wait gate is the
// AND of two independent clocks — turn age ≥ ResponsivenessSilenceAfter AND
// ledger silence ≥ the same threshold. Age alone let a long compaction prep
// (两段 compaction 期间 ledger 持续落账) through the gate while the runtime
// was demonstrably alive.
//
// 边界定义（验收③ 要求按实现写明）：两个条件都用闭区间 `>=` —— 静默恰好
// 6m 且年龄恰好 6m 判 hang，与 silent 分支 `lastAge >= ResponsivenessSilenceAfter`
// 同口径；任一条件差 1 秒不判。LastEventAt 缺失/不可解析时无法证明静默，
// 按不报 hang 处理（本门的失效方向是误报）。
func TestResponsivenessVerdictIsHangNeedsLedgerSilenceToo(t *testing.T) {
	now := time.Now()
	age := 7*time.Minute + 13*time.Second // issue #58 实测 turn 年龄 7m13s
	cases := []struct {
		name       string
		startedAgo time.Duration
		silenceAgo time.Duration // 最近 ledger 写入距今
		want       bool
	}{
		{"长 compaction 准备期（年龄 7m13s、最后写 83s 前）不报", age, 83 * time.Second, false},
		{"真死（年龄 7m13s、最后写 6m30s 前）报 hang", age, 6*time.Minute + 30*time.Second, true},
		{"边界：年龄恰 6m 且静默恰 6m（闭区间）报 hang", doctor.ResponsivenessSilenceAfter, doctor.ResponsivenessSilenceAfter, true},
		{"边界内侧：年龄 6m 但静默 5m59s 不报", doctor.ResponsivenessSilenceAfter, doctor.ResponsivenessSilenceAfter - time.Second, false},
		{"边界内侧：静默 6m 但年龄 5m59s 不报", doctor.ResponsivenessSilenceAfter - time.Second, doctor.ResponsivenessSilenceAfter, false},
		{"年龄远超阈值但 ledger 秒级活跃（看门狗恢复循环期）不报", 10 * time.Minute, 5 * time.Second, false},
	}
	for _, tc := range cases {
		started := now.Add(-tc.startedAgo)
		lastEvent := now.Add(-tc.silenceAgo)
		report := hangReportFirstResponseWait(now, started, lastEvent)
		if got := responsivenessVerdictIsHang(report, now); got != tc.want {
			t.Errorf("%s: hung = %v, want %v (detail: %s)", tc.name, got, tc.want, report.Detail)
		}
	}

	// LastEventAt 缺失 / 不可解析：无法证明静默 ⇒ 不报 hang（fail-safe 方向是
	// 少报，因为本门缺陷史全部是误报方向）。
	noLastEvent := doctor.ResponsivenessReport{
		Verdict: "first_response_wait",
		Active:  &doctor.ResponsivenessTurn{TurnID: "t", Open: true, StartedAt: now.Add(-age).UTC().Format(time.RFC3339)},
	}
	if responsivenessVerdictIsHang(noLastEvent, now) {
		t.Error("first_response_wait without a parseable LastEventAt must not gate as hang — silence is unprovable")
	}
	noLastEvent.Active.LastEventAt = "not-a-timestamp"
	if responsivenessVerdictIsHang(noLastEvent, now) {
		t.Error("first_response_wait with an unparseable LastEventAt must not gate as hang")
	}
}

// 任务 1028 验收③: replay of the issue #58 measured timeline (UTC) through a
// REAL ledger — turn started 01:33:10, two compactions with the ledger
// writing throughout, stream_attempt 01:39:00, last retained record 01:39:11,
// doctor collected at 01:40:24 (age 7m13s, silence 73s). The verdict must be
// first_response_wait AND the gate must refuse: runtime alive, not hung.
func TestIssue58TimelineReplayDoesNotGateAsHang(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "s.jsonl")
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	if err := os.WriteFile(sessionPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	day := func(h, m, s, ms int) time.Time {
		return time.Date(2026, 10, 16, h, m, s, ms*int(time.Millisecond), time.UTC)
	}
	inProgress := event.TurnInProgress
	writeHangLedger(t, ledgerPath,
		hangLedgerEnv("turn_58", 1, "turn_started", inProgress, day(1, 33, 10, 583), nil),
		hangLedgerEnv("turn_58", 2, "turn_phase", inProgress, day(1, 33, 14, 548), nil),
		hangLedgerEnv("turn_58", 3, "compaction_progress", inProgress, day(1, 35, 39, 436), nil),
		hangLedgerEnv("turn_58", 4, "usage", inProgress, day(1, 35, 39, 436), nil),
		hangLedgerEnv("turn_58", 5, "compaction_done", inProgress, day(1, 37, 7, 521), nil),
		hangLedgerEnv("turn_58", 6, "compaction_started", inProgress, day(1, 37, 8, 370), nil),
		hangLedgerEnv("turn_58", 7, "compaction_progress", inProgress, day(1, 38, 59, 188), nil),
		hangLedgerEnv("turn_58", 8, "compaction_done", inProgress, day(1, 38, 59, 955), nil),
		hangLedgerEnv("turn_58", 9, "context_maintenance", inProgress, day(1, 38, 59, 955), nil),
		hangLedgerEnv("turn_58", 10, "stream_attempt", inProgress, day(1, 39, 0, 446), nil),
		hangLedgerEnv("turn_58", 11, "stream_attempt", inProgress, day(1, 39, 11, 0), nil),
	)
	collectedAt := day(1, 40, 24, 0) // 采集点：age 7m13s ≥ 6m，silence 73s < 6m
	report, err := doctor.CollectResponsiveness(sessionPath, collectedAt)
	if err != nil {
		t.Fatalf("CollectResponsiveness: %v", err)
	}
	if report.Verdict != "first_response_wait" {
		t.Fatalf("verdict = %q, want first_response_wait (detail: %s)", report.Verdict, report.Detail)
	}
	if responsivenessVerdictIsHang(report, collectedAt) {
		t.Fatalf("issue #58 timeline gated as hang — the runtime was alive the whole time (detail: %s)", report.Detail)
	}
	if err := hangGateError(report, collectedAt); err == nil {
		t.Fatal("the hang gate must refuse the issue #58 compaction-prep session")
	}
}

// The refusal must name the verdict so the user can tell "not stuck" refusals
// from a dead button (task 663 ②: failures are explicit).
func TestHangGateErrorRefusesHealthySessionExplicitly(t *testing.T) {
	if err := hangGateError(hangReportForVerdict("silent", 0), time.Now()); err != nil {
		t.Fatalf("a silent session must pass the hang gate, got %v", err)
	}
	// 任务 879: waiting_tool refuses by name too — the analyze button must not
	// start an analysis for a session legitimately waiting on its background
	// job, and the message must say which verdict refused.
	for _, verdict := range []string{"working", "waiting_tool"} {
		err := hangGateError(hangReportForVerdict(verdict, 0), time.Now())
		if err == nil {
			t.Fatalf("a %s session must be refused", verdict)
		}
		for _, want := range []string{"not hung", "verdict: " + verdict, "analysis not started"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal for %s missing %q: %v", verdict, want, err)
			}
		}
	}
}

// hangLedgerRecord mirrors the schema-2 on-disk turn-ledger line (recordType +
// inline envelope) for the 任务 879 end-to-end fixture.
type hangLedgerRecord struct {
	RecordType string `json:"recordType"`
	turnevent.Envelope
}

func writeHangLedger(t *testing.T, path string, recs ...turnevent.Envelope) {
	t.Helper()
	var b strings.Builder
	for _, rec := range recs {
		line, err := json.Marshal(hangLedgerRecord{RecordType: "event", Envelope: rec})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hangLedgerEnv(turn string, seq uint64, kind string, status event.TurnStatus, at time.Time, tool *eventwire.Tool) turnevent.Envelope {
	rec := turnevent.Envelope{
		SchemaVersion: 2,
		SessionID:     "sess-879",
		TurnID:        turn,
		Sequence:      seq,
		Kind:          kind,
		Status:        status,
		CreatedAt:     at.UnixMilli(),
		Event:         eventwire.Event{Kind: kind},
	}
	if tool != nil {
		rec.Event.Tool = tool
	}
	return rec
}

// 任务 879 验收①: end-to-end over a REAL turn ledger — an open turn that
// dispatched the wait tool 10 minutes ago is silent past
// ResponsivenessSilenceAfter, yet must collect as waiting_tool and must not
// pass the hang gate: 会话疑似卡死 surfaces from exactly this gate
// (shouldSurfaceHangPrompt → report.hung), so a legitimate out-of-stream wait
// can never reach the prompt. The negative control is the same ledger with the
// wait removed: no blocking tool + silence must still collect as silent and
// gate as hung (验收② 真死场景回归零变化).
func TestWaitSilenceLedgerDoesNotGateAsHang(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "s.jsonl")
	ledgerPath := store.SessionTurnEventLog(sessionPath)
	// ResolveSessionRef requires the session file itself to exist; the ledger
	// beside it carries the turn records under test.
	if err := os.WriteFile(sessionPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	start := now.Add(-30 * time.Minute)
	dispatch := now.Add(-10 * time.Minute) // silence 10m — past the 6m window

	writeHangLedger(t, ledgerPath,
		hangLedgerEnv("turn_e2e", 1, "turn_started", event.TurnInProgress, start, nil),
		hangLedgerEnv("turn_e2e", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), nil),
		hangLedgerEnv("turn_e2e", 3, "tool_dispatch", event.TurnInProgress, dispatch,
			&eventwire.Tool{ID: "call-1", Name: "wait", Args: `{"job_ids":["bash-1"]}`}),
	)
	report, err := doctor.CollectResponsiveness(sessionPath, now)
	if err != nil {
		t.Fatalf("CollectResponsiveness: %v", err)
	}
	if report.Verdict != "waiting_tool" {
		t.Fatalf("verdict = %q, want waiting_tool (detail: %s)", report.Verdict, report.Detail)
	}
	if responsivenessVerdictIsHang(report, now) {
		t.Fatalf("hung = true for a wait past %s — the 会话疑似卡死 prompt would fire again (detail: %s)",
			doctor.ResponsivenessSilenceAfter, report.Detail)
	}
	if gateErr := hangGateError(report, now); gateErr == nil {
		t.Fatal("the hang gate must refuse a session waiting on its background job")
	}

	// Negative control: drop the wait dispatch — the same silence is the
	// dead/stalled runtime shape and must gate as hung, exactly as before 879.
	writeHangLedger(t, ledgerPath,
		hangLedgerEnv("turn_e2e", 1, "turn_started", event.TurnInProgress, start, nil),
		hangLedgerEnv("turn_e2e", 2, "reasoning", event.TurnInProgress, start.Add(4*time.Second), nil),
	)
	report, err = doctor.CollectResponsiveness(sessionPath, now)
	if err != nil {
		t.Fatalf("CollectResponsiveness (control): %v", err)
	}
	if report.Verdict != "silent" {
		t.Fatalf("control verdict = %q, want silent (detail: %s)", report.Verdict, report.Detail)
	}
	if !responsivenessVerdictIsHang(report, now) {
		t.Fatal("silent with no blocking tool must still gate as hung (验收② 回归零变化)")
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

// stubCrashAnalysisSourceDir hides any real fork checkout from every analysis
// entry: crash availability, the task-674 auto-clone fallback, and the hang
// entry all read the task-672 seam var (768b ② retired the hang entry's raw
// detect read, so stubbing the var alone is the whole seam again — a future
// direct detect read will fail these tests loudly instead of silently
// escaping the stub).
func stubCrashAnalysisSourceDir(t *testing.T, dir string) {
	t.Helper()
	origVar := crashAnalysisSourceDir
	crashAnalysisSourceDir = func() string { return dir }
	t.Cleanup(func() { crashAnalysisSourceDir = origVar })
}

// stubGhUnavailable makes the gh resolution fail closed the same way a machine
// without any gh install does, so the task-674 clone fallback refuses
// hermetically instead of reaching for the network.
func stubGhUnavailable(t *testing.T) {
	t.Helper()
	origFallbacks := ghFallbackLocationDirs
	ghFallbackLocationDirs = func() []string { return nil }
	t.Cleanup(func() { ghFallbackLocationDirs = origFallbacks })
	t.Setenv("PATH", "")
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
	// 任务 674: a missing fork checkout no longer refuses outright — the entry
	// falls back to an automatic gh clone. With the source hidden AND gh made
	// unavailable, that fallback must still refuse explicitly (no silent death,
	// no network reach) and keep pointing at the manual Copy path.
	stubCrashAnalysisSourceDir(t, "")
	stubGhUnavailable(t)
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	app := NewApp()
	_, err := app.StartCrashAnalysis("crash", `{"kind":"crash","message":"boom"}`)
	if err == nil {
		t.Fatal("missing source with no gh must refuse the analysis")
	}
	for _, want := range []string{"cannot clone", "gh CLI not found", "Copy button"} {
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

// ── task 687: the hang entry lands Global, same as the crash entry ──────────

// hangProbeCtrlStub gives the active-tab session a path so the doctor
// responsiveness probe (which the hang gate inherently requires — something
// must be hung to analyze) can run against a stubbed verdict.
type hangProbeCtrlStub struct {
	heartbeatExecuteTaskCtrlStub
	sessionPath string
}

func (s *hangProbeCtrlStub) SessionPath() string { return s.sessionPath }

// TestStartHangAnalysisHostsSessionInGlobalNotActiveProject pins task 687: the
// hang entry of the one-click analysis family must host its conversation under
// the built-in Global scope (task 672 parity with the crash entry) — never
// rotate the hung session's tab in place, and never file the run under the open
// project. It also pins the tab:backend-activated activation event (task 688):
// the backend opened the tab outside any frontend navigation, so without the
// event the webview would keep rendering the previous conversation.
func TestStartHangAnalysisHostsSessionInGlobalNotActiveProject(t *testing.T) {
	isolateDesktopUserDirs(t)

	// The user is working inside a project when the hang prompt's analyze
	// button fires; that project must gain nothing from the analysis, and the
	// hung session's tab must survive untouched.
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "fork_dev"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	hungSession := filepath.Join("C:", "hung", "session.jsonl")
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	var activatedEvent *BackendTabActivatedEvent
	app.runtimeEvents.emit = func(_ context.Context, name string, args ...any) {
		if name != tabBackendActivatedEvent || len(args) == 0 {
			return
		}
		if event, ok := args[0].(BackendTabActivatedEvent); ok {
			activatedEvent = &event
		}
	}
	app.mu.Lock()
	app.tabs["tab-user"] = &WorkspaceTab{ID: "tab-user", Scope: "project", WorkspaceRoot: projectRoot,
		TopicID: "topic-user", TopicTitle: "user work", Ctrl: &hangProbeCtrlStub{sessionPath: hungSession}, Ready: true}
	app.tabOrder = []string{"tab-user"}
	app.activeTabID = "tab-user"
	app.mu.Unlock()

	// Hermetic gates: source + gh identity + a doctor verdict the hang gate
	// accepts, and a recorded submission instead of a real model turn.
	stubCrashAnalysisSourceDir(t, fixtureReasonixSourceDir(t))
	stubGhProbe(t, true, "")
	origCollect, origNow := hangResponsivenessCollect, hangNow
	hangResponsivenessCollect = func(string, time.Time) (doctor.ResponsivenessReport, error) {
		return hangReportForVerdict("silent", 0), nil
	}
	hangNow = func() time.Time { return time.Now() }
	t.Cleanup(func() { hangResponsivenessCollect, hangNow = origCollect, origNow })

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
	// same way the 672 crash-entry test fakes a cold boot.
	ctrl := &crashAnalysisCtrlStub{}
	tabIDCh := publishControllerAfterDelay(t, app, ctrl, 30*time.Millisecond)
	origWait := crashAnalysisControllerWaitTimeout
	crashAnalysisControllerWaitTimeout = 10 * time.Second
	t.Cleanup(func() { crashAnalysisControllerWaitTimeout = origWait })

	summary, err := app.StartHangAnalysis()
	if err != nil {
		t.Fatalf("StartHangAnalysis: %v", err)
	}
	if !strings.Contains(summary, "Global") {
		t.Fatalf("summary = %q, want it to name the Global host", summary)
	}

	hostedID := <-tabIDCh
	if got := <-submittedTab; got != hostedID {
		t.Fatalf("instruction submitted to tab %q, want the analysis tab %q", got, hostedID)
	}
	if !strings.Contains(submittedInstruction, "卡顿") {
		t.Fatalf("hang instruction lost the hang framing: %.200s", submittedInstruction)
	}
	// Task 688: the backend-side activation must reach the frontend.
	if activatedEvent == nil {
		t.Fatalf("%s event not emitted — the webview would never follow the analysis tab", tabBackendActivatedEvent)
	}
	if activatedEvent.TabID != hostedID || activatedEvent.Reason != "analysis" {
		t.Fatalf("activation event = %+v, want tab %q reason %q", activatedEvent, hostedID, "analysis")
	}

	app.mu.RLock()
	tab := app.tabs[hostedID]
	var scope, root, topicID, sessionPath string
	if tab != nil {
		scope, root, topicID, sessionPath = tab.Scope, tab.WorkspaceRoot, tab.TopicID, tab.SessionPath
	}
	userTopic := ""
	if userTab := app.tabs["tab-user"]; userTab != nil {
		userTopic = userTab.TopicID
		if userTab.Ctrl != nil && userTab.Ctrl.SessionPath() != hungSession {
			t.Errorf("the hung session's tab was rotated in place to %q — the hang analysis must not touch the user's tab", userTab.Ctrl.SessionPath())
		}
	}
	app.mu.RUnlock()
	if tab == nil {
		t.Fatal("the analysis tab vanished")
	}
	if scope != "global" {
		t.Fatalf("analysis tab scope = %q, want global (task 687)", scope)
	}
	if root == "" || !isBuiltinWorkspaceRoot(root) {
		t.Fatalf("analysis tab workspace root = %q, want a builtin (host-owned) root", root)
	}
	if sessionPath == "" {
		t.Fatal("analysis tab has no session path")
	}
	if wantDir := desktopSessionDir(globalWorkspaceRoot()); !sameDesktopPath(filepath.Dir(sessionPath), wantDir) {
		t.Fatalf("analysis session %q lives outside the global session dir %q", sessionPath, wantDir)
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
