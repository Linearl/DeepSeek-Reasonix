package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── task 847: 763 复发#2 回归钉 ─────────────────────────────────────────────
// 0911（2026-10-11）装机实测：更新重启后弹「检测到上次运行遗留的崩溃报告(3)」，
// panic 文案为 desktopLifecycleReport 的 "A per-process lifecycle record
// remained after its desktop process stopped"。归因三件：①377 gate 默认 false
// 且装机配置显式 false——clean-shutdown 残留照样写成 crash 报告；②763 首修的
// 更新打标豁免只接了 shutdown retire-superseded 检查，未接 lifecycle 报告面；
// ③同一次启动 recordPreviousRunDiagnostics（startupStartMonitors）先写、
// snapshotPendingCrashForAnalysis（startupKickTabRestore）后弹，新写文件名必
// 然高于水位——水位拦不住本启动新写的误报，(3) 份积压反复占位。
//
// Task 1018（20261020 装机实测）：10-19 手动重启的 wedged 残留报告（watchdog
// 强杀退出、desktop.log 零 panic）驻留 crash-pending 队列，在 10-20 两次更新
// 重启反复面呈「检测到上次运行遗留的崩溃报告(1)」。修法 = 源头读后即清：启动
// 扫描消费的无主 record 一律不再写崩溃报告（任何相位、任何 marker 状态），
// 积压退役面同步扩到全部 native.lifecycle 报告。三重回归钉随之演进：
// 更新重启 marker 语义（peek 不消费，保留）+ 全相位读后即清（本文件主钉）+
// 启动扫描退役全部 lifecycle 积压；外加 1018 复发场景的端到端钉（无主 wedged
// 残留零弹窗）与真 panic 不误杀钉。

// plantMarkerAt writes a marker with an explicit timestamp so tests can plant
// stale ones; writeMarkerForTest (update_restart_marker_test.go) covers the
// fresh-write path.
func plantMarkerAt(t *testing.T, at time.Time, pid int) {
	t.Helper()
	body, err := json.Marshal(updateRestartMarker{At: at.Unix(), PID: pid, Reason: "publish", Version: "v1.39.0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(updateRestartMarkerPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updateRestartMarkerPath(), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The peek is a vouch, not a consume: the auto-resume gate owns the marker's
// read-and-delete, and a diagnostics peek must never disturb that.
func TestUpdateRestartMarkerPeekVouchesWithoutConsuming(t *testing.T) {
	isolateDesktopUserDirs(t)
	plantMarkerAt(t, time.Now().Add(-time.Minute), 4242)

	if !updateRestartMarkerFreshForLifecycle(4242) {
		t.Fatal("a fresh marker from the same PID must vouch for the residue")
	}
	if _, err := os.Stat(updateRestartMarkerPath()); err != nil {
		t.Fatalf("peek must not consume the marker: %v", err)
	}
}

func TestUpdateRestartMarkerPeekRejectsMismatchedEvidence(t *testing.T) {
	isolateDesktopUserDirs(t)
	plantMarkerAt(t, time.Now().Add(-time.Minute), 4242)

	// A record from another PID is someone else's death — it keeps reporting.
	if updateRestartMarkerFreshForLifecycle(9999) {
		t.Fatal("a PID mismatch must never vouch")
	}
	// A stale leftover marker (crashed tab restore, long ago) vouches for
	// nothing: the fresh window bounds the handoff it may explain.
	plantMarkerAt(t, time.Now().Add(-updateRestartMarkerFreshWindow-time.Minute), 4242)
	if updateRestartMarkerFreshForLifecycle(4242) {
		t.Fatal("a stale marker must never vouch")
	}
	// No marker (manual restart / plain launch) — the common case reports on.
	if err := os.Remove(updateRestartMarkerPath()); err != nil {
		t.Fatal(err)
	}
	if updateRestartMarkerFreshForLifecycle(4242) {
		t.Fatal("without a marker nothing may vouch")
	}
	// A degenerate observation PID can never match, by construction.
	plantMarkerAt(t, time.Now(), 4242)
	if updateRestartMarkerFreshForLifecycle(0) {
		t.Fatal("a non-positive PID must never vouch")
	}
}

// 修法回归钉（1018 演进）：更新重启场景（873：marker 作者本人的 clean/mid-
// shutdown/wedged 相位）与无主他人 PID 残留 alike——一律读后即清零报告。
// marker 的自证语义保留在分类层（update_restart 处置），报告面已无差别。
func TestUpdateRestartExemptionSuppressesCleanShutdownResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	plantMarkerAt(t, time.Now().Add(-time.Minute), 4242)

	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "shutting_down", PID: 4242, Version: "v1.38.2", Channel: "stable"},
		{Phase: "healthy", PID: 4242, Version: "v1.38.2", Channel: "stable"},
		{Phase: "wedged", PID: 4242, Version: "v1.38.2", Channel: "stable"},
		{Phase: "shutting_down", PID: 9999, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("pending reports = %d, want 0 (task 1018: every disposition — marker-vouched or orphan — is read-and-clear)", got)
	}
}

// 修法回归钉（1018 演进，积压清理场景）：native.lifecycle 积压报告（benign
// 与 wedged 相位 alike——源头停写后队列里全部是 1018 前旧积压）在启动快照时
// 退役；真实 panic 报告一字节不动。
func TestStartupSweepRetiresBenignLifecycleBacklog(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	plant := func(r crashReport) {
		t.Helper()
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		dir := pendingCrashDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "847-plant-"+r.Label+".json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	benign := baseCrashReport("crash")
	benign.SchemaVersion = 3
	benign.Source = "native.lifecycle"
	benign.ExitPhase = "shutting_down"
	benign.Label = "desktop.abnormal_exit.v2"
	benignHealthy := benign
	benignHealthy.Label = "desktop.abnormal_exit.v2-benign-healthy"
	benignHealthy.ExitPhase = "healthy"
	wedged := benign
	wedged.Label = "desktop.abnormal_exit.v2-wedged"
	wedged.ExitPhase = "wedged"
	panicReport := baseCrashReport("crash")
	panicReport.SchemaVersion = 2
	panicReport.Source = "go"
	panicReport.Label = "847-real-panic"
	plant(benign)
	plant(benignHealthy)
	plant(wedged)
	plant(panicReport)

	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("queue holds %d reports after the sweep, want 1 (every native.lifecycle backlog retires; only the real panic stays)", got)
	}
	snap := app.PendingCrashSnapshot()
	if snap.Count != 1 || len(snap.Reports) != 1 {
		t.Fatalf("snapshot = %d/%d, want 1/1 (only the real panic surfaces)", snap.Count, len(snap.Reports))
	}
	for _, payload := range snap.Reports {
		var parsed frontendCrashPayload
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Source == "native.lifecycle" {
			t.Fatalf("judged-non-crash report must never surface: %s", parsed.Label)
		}
	}
}

// 1018 复发场景端到端钉（验收①）：先清后弹的完整启动时序
// （recordPreviousRunDiagnostics → snapshotPendingCrashForAnalysis）对无主
// 残留必须零弹窗——10-20 08:55/09:44 两次更新重启弹的正是 10-19 wedged 无主
// 残留的驻留报告。marker 有无只影响处置分类，不影响零弹窗。同场景下真实
// panic 报告照常面呈（验收③，不误杀）。
func TestFalseCrashUpdateRestartScenarioEndToEnd(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllCrashesAndMarker(t))

	// The 10-19 shape: a manual restart's watchdog-forced quit left a wedged
	// orphan record (no update-restart marker, gate off, desktop.log zero
	// panic) — the exact record that re-surfaced the banner on 10-20.
	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "wedged", PID: 15380, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("orphan residue wrote %d reports, want 0 (task 1018 read-and-clear)", got)
	}
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("orphan-residue relaunch surfaced %d reports, want 0 (the false-crash banner must not come back)", got.Count)
	}

	// 验收③ control: a real panic report planted in the queue surfaces exactly
	// the same boot — the read-and-clear must not mask genuine crash evidence.
	panicReport := baseCrashReport("crash")
	panicReport.SchemaVersion = 2
	panicReport.Source = "go"
	panicReport.Label = "1018-real-panic"
	body, err := json.Marshal(panicReport)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pendingCrashDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingCrashDir(), "1018-real-panic.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	relaunch := NewApp()
	relaunch.lifecycle.previousRuns = app.lifecycle.previousRuns
	relaunch.recordPreviousRunDiagnostics()
	relaunch.snapshotPendingCrashForAnalysis()
	snap := relaunch.PendingCrashSnapshot()
	if snap.Count != 1 || len(snap.Reports) != 1 {
		t.Fatalf("real panic surfaced %d/%d, want 1/1 (acceptance ③: a genuine panic still reaches the banner)", snap.Count, len(snap.Reports))
	}
	var parsed frontendCrashPayload
	if err := json.Unmarshal([]byte(snap.Reports[0]), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Label != "1018-real-panic" {
		t.Fatalf("surfaced label = %q, want the real panic report", parsed.Label)
	}
}

// 1018 复发场景端到端钉（积压面）：1018 之前写进队列的 wedged 残留报告在下一
// 次启动快照时退役——装机上已驻留的积压不用等 14 天保留期，首次启动即清。
func TestStaleWedgedLifecycleBacklogRetiresOnNextBoot(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	stale := desktopLifecycleReport(desktopLifecycleObservation{
		Version: "v1.38.2", Channel: "stable", Phase: "wedged",
		PID: 15380, DetectedAt: "2026-10-19T01:40:11Z",
	})
	body, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pendingCrashDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingCrashDir(), "1792374012977358300-55140-1.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("stale wedged backlog surfaced %d reports, want 0 (the 10-20 banner must not come back)", got.Count)
	}
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("stale wedged backlog still on disk: %d reports, want 0", got)
	}
}

func removeAllCrashesAndMarker(t *testing.T) func() {
	return func() {
		removeAllPendingCrashes()
		_ = os.Remove(updateRestartMarkerPath())
	}
}

// ── task 873: 847 家族第三形态回归钉 ────────────────────────────────────────
// 1706（2026-10-13 17:14 execute）装机走宽限期超时强制路径：强杀退出把记录留
// 在 mid-shutdown 相位（closing_*/waiting_runtime_locks），847 的 marker 豁免
// 检查被 `!lifecycleNoiseBenign` 分支短路——非 benign 相位直接写报告，marker
// 根本没机会自证。1756 启动弹「检测到上次运行遗留的崩溃报告(1)」。873 修法 =
// marker 检查提到相位判定之前；1018 演进后 marker 有无只影响处置分类——全
// 相位、全 marker 状态读后即清，报告面零写入。
func TestUpdateRestartMarkerVouchesForMidShutdownPhaseResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	plantMarkerAt(t, time.Now().Add(-time.Minute), 5150)

	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "closing_shared_hosts", PID: 5150, Version: "v1.38.2", Channel: "stable"},
		{Phase: "waiting_runtime_locks", PID: 5150, Version: "v1.38.2", Channel: "stable"},
		{Phase: "closing_tab:abc", PID: 5150, Version: "v1.38.2", Channel: "stable"},
		// 控制组：无 marker 保护的残留同样读后即清（1018 起报告面无差别）。
		{Phase: "closing_shared_hosts", PID: 8642, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 0 {
		t.Fatalf("pending reports = %d, want 0 (task 1018: marker and markerless residue alike are read-and-clear)", got)
	}
}
