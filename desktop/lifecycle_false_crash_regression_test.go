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
// 三重回归钉：更新重启豁免（marker）+ clean-shutdown gate 语义（377 既有测试
// 守护，本文件不重复）+ 启动扫描退役已判定非崩溃的积压；外加 0911 复发场景的
// 端到端钉（先写后弹的完整时序必须回到零弹窗）。

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

// 修法①回归钉（更新重启场景）：gate=false（装机默认，配置文件渲染显式 false）
// 时，更新重启退出进程自己的 clean-shutdown 残留不再写成 crash 报告；他人 PID
// 的残留照写——豁免面是「marker 作者本人的退出相位」。task 873 把 marker 自证
// 提到相位判定之前：宽限期强制退出会把记录留在 mid-shutdown 相位（非 benign），
// 原顺序让 marker 没机会自证；豁免的语义本位是「计划内更新退出的作者本人残留」，
// 相位只是 377 gate（clean 相位免报）的管辖区。marker 作者的 wedged 相位同样
// 由 marker 背书——计划退出途中被 watchdog 强杀仍是计划退出的一部分。
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
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("pending reports = %d, want 1 (another PID's residue); the marker writer's own phases — clean or mid-shutdown or wedged — stay suppressed (task 873: marker vouch precedes the phase gate)", got)
	}
}

// 修法③回归钉（积压清理场景）：已判定非崩溃的遗留报告（native.lifecycle +
// shutting_down/healthy）在启动快照时退役——不再面呈、不再占位；wedged 与
// 真实 panic 报告一字节不动。
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
	if got := pendingReportCount(t); got != 2 {
		t.Fatalf("queue holds %d reports after the sweep, want 2 (wedged lifecycle + real panic stay)", got)
	}
	snap := app.PendingCrashSnapshot()
	if snap.Count != 2 || len(snap.Reports) != 2 {
		t.Fatalf("snapshot = %d/%d, want 2/2 (only the survivors surface)", snap.Count, len(snap.Reports))
	}
	for _, payload := range snap.Reports {
		var parsed frontendCrashPayload
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Source == "native.lifecycle" && (parsed.Label == "desktop.abnormal_exit.v2" || parsed.Label == "desktop.abnormal_exit.v2-benign-healthy") {
			t.Fatalf("judged-non-crash report must never surface: %s", parsed.Label)
		}
	}
}

// 0911 复发场景端到端钉：先写后弹的完整启动时序（recordPreviousRunDiagnostics
// → snapshotPendingCrashForAnalysis）在更新重启条件下必须回到零弹窗；同一条件
// 去掉 marker 则照弹——证明豁免来自打标而不是巧合。
func TestFalseCrashUpdateRestartScenarioEndToEnd(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllCrashesAndMarker(t))

	// The 0911 shape: the update restart left the exiting process's own
	// clean-shutdown lifecycle residue behind, gate off (装机显式 false).
	plantMarkerAt(t, time.Now().Add(-time.Minute), 777)
	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "shutting_down", PID: 777, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("update-restart relaunch surfaced %d reports, want 0 (the 0911 false-crash banner must not come back)", got.Count)
	}

	// Control: the same residue WITHOUT the marker is nobody's planned exit —
	// it IS written to the queue exactly as before (the marker vouch is what
	// keeps the write from happening, not a coincidence). The prompt face is
	// still covered there by the sweep (task 847 ③ retires the judged residue
	// before the snapshot), so the banner never returns on either path — but
	// the write/no-write line between the two runs is the marker's doing.
	removeAllPendingCrashes()
	if err := os.Remove(updateRestartMarkerPath()); err != nil {
		t.Fatal(err)
	}
	control := NewApp()
	control.lifecycle.previousRuns = app.lifecycle.previousRuns
	control.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("without the marker the residue must still be WRITTEN (queue = %d, want 1)", got)
	}
	control.snapshotPendingCrashForAnalysis()
	if got := control.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("the sweep must still retire the judged residue from the prompt (count = %d, want 0)", got.Count)
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
// 根本没机会自证。1756 启动弹「检测到上次运行遗留的崩溃报告(1)」。修法 =
// marker 检查提到相位判定之前：计划内更新重启的退出（marker 作者本人的任何
// 相位残留）一律先过 marker 自证；PID 不匹配/过期 marker/无 marker 的语义不
// 变（照写报告），377 gate 只管 clean 相位，语义不越界。
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
		// 控制组：无 marker 保护的残留照写。
		{Phase: "closing_shared_hosts", PID: 8642, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("pending reports = %d, want 1 (the marker writer's mid-shutdown phases stay suppressed; the markerless PID's residue keeps reporting)", got)
	}
}
