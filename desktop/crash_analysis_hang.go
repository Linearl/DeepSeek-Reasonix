package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/doctor"
)

// crash_analysis_hang.go is task 663 gap ① (refined by the ⑥ scope addendum):
// the hang scenario is a first-class citizen of the one-click analysis family.
// "没崩但卡死" — a session whose open turn stopped making progress — never
// produces a crash report, so the crash overlay never fires for it. The trigger
// is the doctor responsiveness verdict (task 370, a54890acd): verdict
// "silent" (open turn, ledger silent past the stream-watchdog window with no
// long-blocking tool in flight — task 879 split the legitimate wait out), or
// "first_response_wait" that has itself outlived ResponsivenessSilenceAfter.
// Verdicts that mean alive or waiting (working / idle / waiting_user /
// waiting_tool) refuse with an explicit error instead of analyzing a healthy
// session.
//
// Upstream comparison (调研对照, 2026-10-09): the stream idle watchdog
// (provider defaultStreamIdleTimeout) force-recovers a silent started stream
// at 5m, so a *streaming* stall self-heals. What this entry covers is the
// residue the watchdog cannot classify: a stalled tool outside the stream or a
// dead runtime. Task 879: the first of those has two faces — a long-blocking
// tool (wait/event_wait) still executing is legitimate silence (doctor verdict
// waiting_tool, never a hang), while silence with no such tool open is the
// dead/stalled residue this entry analyzes (doctor verdict silent).
//
// The pipeline is the crash one byte for byte (fresh Global-scope named session
// → YOLO → instruction with a sanitized payload → gh-issue-submit, task 687),
// only the framing notice and the payload source differ, so there is one
// analysis family, not two.

// Test seams: the doctor collector reads real files; tests point it at
// fixtures and pin the clock.
var (
	hangResponsivenessCollect = doctor.CollectResponsiveness
	hangNow                   = time.Now
)

// HangAnalysisAvailabilityReport is the hang-side read model for the frontend
// watch: verdict plus the explicit hung gate, so the UI never has to
// re-implement the state classification.
type HangAnalysisAvailabilityReport struct {
	SessionPath string `json:"sessionPath,omitempty"` // user paths scrubbed
	Verdict     string `json:"verdict"`
	Detail      string `json:"detail,omitempty"`
	Hung        bool   `json:"hung"`
	Ready       bool   `json:"ready"`
}

// responsivenessVerdictIsHang is the task-663 hang gate over the doctor
// report. "silent" is the hang verdict. "first_response_wait" counts only once
// the wait itself outlives ResponsivenessSilenceAfter — below that it is the
// legitimate prefill/thinking phase the doctor explicitly refuses to call
// stuck. "waiting_tool" (task 879) is the doctor's split of a silent ledger
// whose long-blocking tool call (wait/event_wait) is still executing: the
// session waits for its background job, it is not hung, so a wait past six
// minutes never surfaces the 会话疑似卡死 prompt again.
func responsivenessVerdictIsHang(report doctor.ResponsivenessReport, now time.Time) bool {
	switch report.Verdict {
	case "silent":
		return true
	case "waiting_tool":
		return false
	case "first_response_wait":
		if report.Active == nil {
			return false
		}
		started, err := time.Parse(time.RFC3339, report.Active.StartedAt)
		return err == nil && now.Sub(started) >= doctor.ResponsivenessSilenceAfter
	default:
		return false
	}
}

// collectActiveSessionResponsiveness probes the active tab's session with the
// doctor responsiveness reader. The session path travels back because the
// payload and the availability report both name what was measured.
func (a *App) collectActiveSessionResponsiveness() (doctor.ResponsivenessReport, string, error) {
	a.mu.RLock()
	tab := a.activeTabLocked()
	live := tab != nil && !a.tabIsReadOnly(tab) && tab.Ctrl != nil
	var path string
	if live {
		path = strings.TrimSpace(tab.Ctrl.SessionPath())
	}
	a.mu.RUnlock()
	if !live || path == "" {
		return doctor.ResponsivenessReport{}, "", fmt.Errorf("no live session on the active tab to probe")
	}
	report, err := hangResponsivenessCollect(path, hangNow())
	if err != nil {
		return doctor.ResponsivenessReport{}, scrubUserPaths(path), err
	}
	return report, path, nil
}

// hangGateError is the explicit refusal for a session the doctor does not call
// hung: analyzing a working session would spend quota on a non-failure (same
// discipline as the crash gates). Pure, so the gate is unit-testable without a
// live tab.
func hangGateError(report doctor.ResponsivenessReport, now time.Time) error {
	if responsivenessVerdictIsHang(report, now) {
		return nil
	}
	return fmt.Errorf("the active session is not hung (verdict: %s) — %s; analysis not started", report.Verdict, report.Detail)
}

// HangAnalysisAvailability probes the active session's responsiveness verdict
// without starting anything.
func (a *App) HangAnalysisAvailability() HangAnalysisAvailabilityReport {
	report := HangAnalysisAvailabilityReport{Verdict: "unknown", Ready: false}
	collected, path, err := a.collectActiveSessionResponsiveness()
	if err != nil {
		report.Detail = err.Error()
		return report
	}
	report.SessionPath = scrubUserPaths(path)
	report.Verdict = collected.Verdict
	report.Detail = collected.Detail
	report.Hung = responsivenessVerdictIsHang(collected, hangNow())
	report.Ready = true
	return report
}

// StartHangAnalysis runs the shared analysis pipeline for a hang. Every
// prerequisite (source / gh / live workspace) and the verdict gate are
// re-checked here so a stale frontend probe cannot analyze a healthy session.
func (a *App) StartHangAnalysis() (string, error) {
	// Read the source location through the task-672 seam var, not detect
	// directly: by default the var wraps detect (runtime behavior unchanged),
	// and tests stub the var alone — a raw detect read here was the leftover
	// seam bypass from the task-674 auto-clone cleanup (768b ②), forcing the
	// test helper to stub two resolution faces to keep the hang entries
	// hermetic.
	dir := crashAnalysisSourceDir()
	if dir == "" {
		return "", fmt.Errorf("no local reasonix source detected — root-cause analysis needs the fork checkout; use the Copy button to report manually")
	}
	if ok, ghDetail := ghAuthenticatedProbe(); !ok {
		hint := strings.TrimSpace(ghDetail)
		if hint == "" {
			hint = "gh auth status failed"
		}
		return "", fmt.Errorf("GitHub CLI check failed (%s) — install the GitHub CLI or run gh auth login first, or use the Copy button to report manually", hint)
	}
	collected, path, err := a.collectActiveSessionResponsiveness()
	if err != nil {
		return "", fmt.Errorf("could not read the active session's responsiveness (%v)", err)
	}
	if gateErr := hangGateError(collected, hangNow()); gateErr != nil {
		return "", gateErr
	}
	payload, err := hangAnalysisPayload(collected, hangNow())
	if err != nil {
		return "", err
	}

	// Task 687: the analysis conversation is hosted under the built-in Global
	// scope, same as the crash entry (task 672). The route used to rotate the
	// active tab in place (NewSessionForTab("")), which both filed the fresh
	// topic under the hung session's project and required a live tab before the
	// flow could even start. The shared openCrashAnalysisSession path removes
	// both couplings: the hang analysis lands under Global, and the flow only
	// needs the (inherently live) hung session for the verdict probe above.
	tabID, topicID, err := a.openCrashAnalysisSession("卡顿")
	if err != nil {
		return "", err
	}
	// 任务780: same pin as the crash entry — name the tab this flow opened,
	// never whichever tab happens to be active when the controller wait settles.
	// 任务858: same second gate too — if the tab's controller no longer points
	// at this flow's own global session, the rename (and the run tracking)
	// stands down instead of stamping 卡顿分析-<时间> onto a user conversation.
	sessionPath, sessionDir := a.sessionLocationForTab(tabID)
	if sessionPath != "" && !analysisRenameAllowed(sessionPath, topicID) {
		slog.Warn("hang-analysis: rename skipped — target is not the analysis session this flow opened (task 858)",
			"session", scrubUserPaths(sessionPath), "wantTopic", topicID)
		sessionPath, sessionDir = "", ""
	}
	title := crashAnalysisSessionTitle("卡顿分析", false)
	if sessionPath != "" {
		if renameErr := agent.RenameSession(sessionPath, title); renameErr != nil {
			slog.Warn("hang-analysis: session rename failed (analysis continues)", "err", renameErr)
		} else if titleErr := a.onSessionTitleChanged(sessionDir, sessionPath, title); titleErr != nil {
			slog.Warn("hang-analysis: session title projection failed (analysis continues)", "err", titleErr)
		}
	}
	a.SetModeForTab(tabID, "yolo")
	instruction := buildHangAnalysisInstruction(dir, payload)
	if err := crashAnalysisSubmit(a, tabID, instruction); err != nil {
		return "", fmt.Errorf("could not submit the analysis instruction (%v) — use the Copy button to report manually", err)
	}
	a.beginCrashAnalysisRun(sessionPath)
	slog.Info("hang-analysis: started", "verdict", collected.Verdict,
		"session", scrubUserPaths(sessionPath), "title", title, "analyzed", scrubUserPaths(path))
	return fmt.Sprintf("YOLO 卡顿分析会话已在 Global 下启动并命名为「%s」（%s）；分析对象是会话 %s（doctor 判定：%s）。结论会发送到该分析会话。",
		title, scrubUserPaths(sessionPath), scrubUserPaths(path), collected.Verdict), nil
}

// hangAnalysisPayload wraps the doctor report into the schema-2 frontend
// payload shape, so it travels the same sanitizer (crashReportFromDetail) and
// the same instruction builder as a crash report. Kind "performance" — a hang
// is a responsiveness failure, not a crash.
func hangAnalysisPayload(report doctor.ResponsivenessReport, now time.Time) (string, error) {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serialize responsiveness report: %w", err)
	}
	payload := frontendCrashPayload{
		SchemaVersion: 2,
		Kind:          "performance",
		Source:        "hang",
		Label:         "session.hang",
		Message:       string(body),
		ErrorType:     "SessionUnresponsive",
		ErrorMessage: fmt.Sprintf("Session turn is unresponsive (doctor verdict: %s). %s",
			report.Verdict, strings.TrimSpace(report.Detail)),
		TopFrame:    "doctor.responsiveness:" + report.Verdict,
		BuildCommit: version,
		OccurredAt:  now.UTC().Format(time.RFC3339),
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("serialize hang payload: %w", err)
	}
	return string(out), nil
}

// buildHangAnalysisInstruction frames the analysis as hang triage: the issue
// must not be triaged as a crash, and the first suspects are the states the
// stream watchdog cannot self-heal (a wait outside the stream, a dead runtime).
func buildHangAnalysisInstruction(sourceDir, payload string) string {
	notice := "注意：这是一次会话卡顿（hang）诊断——会话没有崩溃，但当前回合长时间无进展（doctor responsiveness 判定）。" +
		"上游流空闲看门狗（defaultStreamIdleTimeout）只能自愈流内停顿；请优先排查看门狗覆盖不到的路径（流外等待、死锁、runtime 中途死亡），" +
		"issue 归类为 hang/performance，不要归类为崩溃。"
	return buildAnalysisInstruction(sourceDir, payload, notice)
}
