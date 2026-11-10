package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// ── tl1530 (issue #85): WebView2 受控重启双 crash 误报回归钉 ─────────────────
// 装机实锤：一次 WebView2 browser 崩溃出两条报表——第 1 条 fatal_app_exit
// （web.runtime.native，真实）+ 第 2 条 abnormal_exit（native.lifecycle，误报）。
// 误报根因：requestRestart 是计划内退出（markAsync(webview2_restarting) +
// journal auto_restart 同调用内先后落盘），但启动期 disposition switch 只豁免
// update marker 与 shutting_down/healthy，webview2_restarting 落 default 被
// 写成 abnormal_exit。
//
// 修法（A 档）：disposition 新增 webview2_restart 豁免——判据仿
// updateRestartMarkerFreshForLifecycle（PID 自证 + 新鲜窗口，journal 的
// auto_restart 行为载体，tl1530 起行内带作者 PID）。替代 sink 恒存在：触发
// 恢复的 browser crash 报表在 requestRestart 上游必然先落盘
// （webview2_diagnostics_windows.go 的 process 同步先写报表再做重启决策），
// 满足 task1018-fix2「删保护前必须有替代」；wedged/unknown 相位不豁免。
//
// C 档顺手：kind=0 实际执行 restart，报表 recovery 恒报 not_applicable 属
// 语义误导，改为 restart。

// plantWebView2RestartJournal writes the journal's auto_restart line with an
// explicit author PID and timestamp so tests can plant foreign/stale lines;
// pid 0 models a pre-tl1530 record (the omitempty field was absent).
func plantWebView2RestartJournal(t *testing.T, pid int, at time.Time) {
	t.Helper()
	journal := newWebView2RecoveryJournal()
	record := webView2RecoveryJournalRecord{
		SchemaVersion: webView2RecoveryJournalSchema,
		At:            at.UTC().Format(time.RFC3339Nano),
		Stage:         "auto_restart",
		Reason:        "browser_process_exited.crashed",
		PID:           pid,
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journal.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal.path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 验收①主钉：计划内 webview2 重启的残留不再产生 abnormal_exit 误报。
func TestWebView2PlannedRestartResidueNotReported(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	plantWebView2RestartJournal(t, 4242, time.Now().Add(-time.Minute))

	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: webview2RestartingPhase, PID: 4242, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("planned webview2 restart residue wrote %d reports, want 0 (tl1530: a planned exit must not double-report one fault)", got)
	}
}

// 验收①fail-open 反向钉：phase 在场但 journal 证据缺失（journal 追加失败 /
// pre-tl1530 旧构建写的行没有 PID）时，webview2_restarting 残留照写报告——
// 豁免只在证据齐时生效，修复不得吞掉证据不足的真崩溃。
func TestWebView2RestartingResidueWithoutJournalStillReported(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: webview2RestartingPhase, PID: 4242, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("journal-less webview2_restarting residue wrote %d reports, want 1 (fail-closed: no vouch evidence keeps the report)", got)
	}
}

// 验收①端到端（issue #85 原始形态）：触发恢复的 fatal browser 报表已在队列，
// 计划内残留不再加报第二条 → 队列里恰好一份、且是那份真实报表。这也钉住
// 「替代 sink 恒存在」——豁免生效时上游报表必然已落盘。
func TestWebView2PlannedRestartSingleReportEndToEnd(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	plantWebView2RestartJournal(t, 4242, time.Now().Add(-time.Minute))

	// The issue-#85 first report: the real fatal browser crash, written by the
	// dying process BEFORE requestRestart marked the phase and quit.
	fatal, outcome := webView2NativeFailureReport(webView2NativeEvent{
		Kind: 0, Reason: 3, ReasonAvailable: true, ExitCode: -1073741819, ExitCodeAvailable: true,
	}, "155.0.4283.62", false)
	if outcome != "fatal_app_exit" {
		t.Fatalf("fixture outcome = %q, want fatal_app_exit", outcome)
	}
	body, err := json.Marshal(fatal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pendingCrashDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	// 队列文件名是 `<unixnano>-<seq>.json`；固定名会排在水位之下被当成积压，
	// 必须用真实时间戳命名才能模拟「本启动新写入的崩溃报告」。
	freshName := strconv.FormatInt(time.Now().UTC().UnixNano(), 10) + "-85001-1.json"
	if err := os.WriteFile(filepath.Join(pendingCrashDir(), freshName), body, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: webview2RestartingPhase, PID: 4242, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	app.snapshotPendingCrashForAnalysis()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("queue holds %d reports, want exactly 1 (the real fatal browser report — no second abnormal_exit for the same fault)", got)
	}
	snap := app.PendingCrashSnapshot()
	if snap.Count != 1 {
		t.Fatalf("surfaced %d reports, want 1", snap.Count)
	}
	var parsed frontendCrashPayload
	if err := json.Unmarshal([]byte(snap.Reports[0]), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Label != "windows.webview2.process_failed" {
		t.Fatalf("surfaced label = %q, want the real fatal browser report (not desktop.abnormal_exit.v2)", parsed.Label)
	}
}

// 验收②豁免判据钉：PID 自证 / 新鲜窗口 / 无 journal / 前格式无 PID 全部拒绝。
func TestWebView2RestartJournalVouchCriteria(t *testing.T) {
	isolateDesktopUserDirs(t)

	// Fresh line from the residue's own author vouches.
	plantWebView2RestartJournal(t, 4242, time.Now().Add(-time.Minute))
	if !webView2RestartJournalFreshForLifecycle(4242) {
		t.Fatal("a fresh auto_restart line from the same PID must vouch")
	}
	// A line from another process is someone else's restart — never vouches.
	if webView2RestartJournalFreshForLifecycle(9999) {
		t.Fatal("a PID mismatch must never vouch")
	}
	// Beyond the fresh window the line cannot excuse a later launch's evidence.
	plantWebView2RestartJournal(t, 4242, time.Now().Add(-webView2RestartVouchWindow-time.Minute))
	if webView2RestartJournalFreshForLifecycle(4242) {
		t.Fatal("a stale auto_restart line must never vouch")
	}
	// No journal at all (plain launch / first run) — the residue keeps reporting.
	if err := os.Remove(newWebView2RecoveryJournal().path); err != nil {
		t.Fatal(err)
	}
	if webView2RestartJournalFreshForLifecycle(4242) {
		t.Fatal("without a journal nothing may vouch")
	}
	// A degenerate observation PID can never match, by construction.
	plantWebView2RestartJournal(t, 4242, time.Now())
	if webView2RestartJournalFreshForLifecycle(0) {
		t.Fatal("a non-positive PID must never vouch")
	}
	// Pre-tl1530 records carry no PID (pid=0): fail-closed, keep reporting.
	plantWebView2RestartJournal(t, 0, time.Now())
	if webView2RestartJournalFreshForLifecycle(4242) {
		t.Fatal("a PID-less pre-tl1530 line must never vouch")
	}
	// The vouch is read-only: the journal is append-only history with no
	// consume-once semantics to disturb.
	plantWebView2RestartJournal(t, 4242, time.Now())
	if !webView2RestartJournalFreshForLifecycle(4242) {
		t.Fatal("vouch setup lost")
	}
	if _, err := os.Stat(newWebView2RecoveryJournal().path); err != nil {
		t.Fatalf("the vouch must not consume the journal line: %v", err)
	}
}

// 验收①反向钉：豁免不外溢——同 PID 的新鲜 journal 也豁免不了 webview2_restarting
// 以外的相位（wedged/starting 是真异常死亡，无其他 sink，照写报告——
// task1018-fix2 语义不回退）。
func TestWebView2VouchDoesNotExcuseOtherPhases(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	plantWebView2RestartJournal(t, 4242, time.Now().Add(-time.Minute))

	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "wedged", PID: 4242, Version: "v1.38.2", Channel: "stable"},
		{Phase: "starting", PID: 4242, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 2 {
		t.Fatalf("non-webview2 phases with a fresh same-PID line wrote %d/2 reports, want 2 (the vouch is phase-gated; wedged/unknown keep their only sink)", got)
	}
}

// 验收③C 档语义修正钉：kind=0 实际执行 restart，报表 recovery 必须如实命名；
// renderer 相位不受影响（空 recovery 仍 not_applicable、成功 reload 仍原名）。
func TestWebView2BrowserFailureReportsRestartRecovery(t *testing.T) {
	report, outcome := webView2NativeFailureReport(webView2NativeEvent{
		Kind: 0, Reason: 3, ReasonAvailable: true, ExitCode: -1073741819, ExitCodeAvailable: true,
	}, "155.0.4283.62", false)
	if report.WebRuntime == nil || report.WebRuntime.Recovery != webView2RecoveryBrowserRestart {
		recovery := "<nil>"
		if report.WebRuntime != nil {
			recovery = report.WebRuntime.Recovery
		}
		t.Fatalf("browser report recovery = %q, want %q (tl1530 C 档: the restart actually executed must not read as not_applicable)", recovery, webView2RecoveryBrowserRestart)
	}
	if outcome != "fatal_app_exit" {
		t.Fatalf("outcome = %q, want fatal_app_exit (the semantic fix must not touch the classification)", outcome)
	}
	// The externally-terminated kill (issue #63 shape) gets the same honest
	// naming — the restart flow is what the state machine would answer with.
	external, _ := webView2NativeFailureReport(webView2NativeEvent{
		Kind: 0, Reason: 0, ReasonAvailable: true, ExitCode: exitCodeTerminatedExternally, ExitCodeAvailable: true,
	}, "155.0.4283.62", false)
	if external.WebRuntime.Recovery != webView2RecoveryBrowserRestart {
		t.Fatalf("terminated-externally recovery = %q, want %q", external.WebRuntime.Recovery, webView2RecoveryBrowserRestart)
	}
	// Renderer scope control: the reload vocabulary stays untouched there.
	rendererEmpty, _ := webView2NativeFailureReport(webView2NativeEvent{Kind: 1}, "155.0.4283.62", false)
	if rendererEmpty.WebRuntime.Recovery != webView2RecoveryNotApplicable {
		t.Fatalf("renderer empty-recovery = %q, want %q (scope control)", rendererEmpty.WebRuntime.Recovery, webView2RecoveryNotApplicable)
	}
	rendererReload, _ := webView2NativeFailureReport(webView2NativeEvent{Kind: 1, Recovery: webView2RecoverySucceeded}, "155.0.4283.62", false)
	if rendererReload.WebRuntime.Recovery != webView2RecoverySucceeded {
		t.Fatalf("renderer reload-succeeded = %q, want %q (scope control)", rendererReload.WebRuntime.Recovery, webView2RecoverySucceeded)
	}
}
