package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/config"
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

// ── task 1002: 847 家族新形态——迟到认领的陈旧 record 不再面呈 ──────────────
// 20261019 1038 / 20261020 1058 装机实锤：一个更早异常死亡（watchdog 强杀）
// 留下的 record 在其自然后继启动时未被认领（Windows 认领竞争落败 / PID 复用
// 判活误真），跨若干次启动后才被某次更新重启的新进程认领——此刻盘上 marker
// 属于别的进程，PID 交叉自证正确拒绝，陈旧 record 被写成崩溃报告并面呈
// 「检测到上次运行遗留的崩溃报告」，用户在更新后看到即读成「更新崩了」。
// 修法 = consume-pass 时戳：某 record 的最后一次相位写入早于「上一次启动的
// 认领扫描」，则本次启动不是它的直接后继，「上次运行」的说法不成立——退役
// 不写报告（认领已把文件从盘上删除），带命名日志 + metrics（1545 反静默）。

// plantConsumePassAt writes a consume-pass stamp at an explicit time so tests
// can simulate an intermediate boot's pass.
func plantConsumePassAt(t *testing.T, at time.Time) {
	t.Helper()
	body, err := json.Marshal(lifecycleConsumePassStamp{
		SchemaVersion: lifecycleConsumePassSchema,
		RunID:         "prior-boot",
		At:            at.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lifecycleConsumePassPathForRoot(config.MemoryUserDir())), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lifecycleConsumePassPathForRoot(config.MemoryUserDir()), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// 0911→1038 形态端到端：更新重启的启动（盘上有他人 PID 的新鲜 marker）认领
// 到一份 57 分钟前强杀留下的 wedged record，而 10 分钟前的中间启动已经过手
// 目录——该 record 退役不写报告；同一次扫描里「上次运行」的直接后继死亡
// （相位写入晚于那次过手）照写——退役只吞陈旧件，不吞真崩溃。
func TestStaleLifecycleRecordFromIntermediatePassRetired(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllCrashesAndMarker(t))

	// The update-restart boot's marker belongs to another PID — the vouch
	// correctly refuses the stale record (this is the 847/873 boundary).
	plantMarkerAt(t, time.Now().Add(-time.Minute), 777)
	priorPass := time.Now().Add(-10 * time.Minute)
	plantConsumePassAt(t, priorPass)

	app := NewApp()
	app.lifecycle.priorPassAt = priorPass
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		// The 20261019 shape: an earlier force-kill's record, claimed late.
		{Phase: "wedged", PID: 15380, Version: "v1.38.2", Channel: "stable",
			UpdatedAt: time.Now().Add(-57 * time.Minute).Format(time.RFC3339Nano)},
		// Control: the genuinely previous run's death (its last write is NEWER
		// than the intermediate pass) keeps reporting in the same scan.
		{Phase: "closing_tab:tab_x", PID: 9999, Version: "v1.38.2", Channel: "stable",
			UpdatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano)},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("pending reports = %d, want 1 (the stale 57-min-old record retires; the direct successor's death still reports)", got)
	}
}

// 真崩溃路径零回归：无中间过手（首次安装/长时间关机后首启）时，任何相位的
// record 照写报告——时戳缺失/不可解析同样 fail-open 照写，修复不得吞真崩溃。
func TestDirectSuccessorCrashStillReported(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	reported := func(name string, priorPassAt time.Time, updatedAt string) {
		t.Helper()
		app := NewApp()
		app.lifecycle.priorPassAt = priorPassAt
		app.lifecycle.previousRuns = []desktopLifecycleObservation{
			{Phase: "wedged", PID: 4242, Version: "v1.38.2", Channel: "stable", UpdatedAt: updatedAt},
		}
		app.recordPreviousRunDiagnostics()
		if got := pendingReportCount(t); got != 1 {
			t.Fatalf("%s: pending reports = %d, want 1 (the crash must keep reporting)", name, got)
		}
		removeAllPendingCrashes()
	}

	now := time.Now()
	// No stamp at all: first install or a machine that only ever booted once —
	// the boot IS the direct successor (also covers the overnight-crash boot).
	reported("no prior pass", time.Time{}, now.Add(-time.Hour).Format(time.RFC3339Nano))
	// A prior pass that predates the record's last write: the record was born
	// after that pass, so this boot is the first to see it.
	reported("pass older than record", now.Add(-2*time.Hour), now.Add(-time.Minute).Format(time.RFC3339Nano))
	// Fail-open: an unparseable timestamp can never prove staleness.
	reported("unparseable updatedAt", now.Add(-10*time.Minute), "not-a-time")
	// Fail-open: a record without a timestamp (degenerate writer) reports.
	reported("empty updatedAt", now.Add(-10*time.Minute), "")
}

// 时戳管道回归钉：consumePrevious 先读旧戳（存进 priorPassAt 交给报告面）、
// 后写新戳；戳文件因 schema 不同不被当 lifecycle record 消费。
func TestConsumePassStampRoundTrip(t *testing.T) {
	root := t.TempDir()
	dir := desktopLifecycleDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	prior := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	body, err := json.Marshal(lifecycleConsumePassStamp{
		SchemaVersion: lifecycleConsumePassSchema,
		RunID:         "prior-boot",
		At:            prior.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	stampPath := filepath.Join(dir, lifecycleConsumePassFile)
	if err := os.WriteFile(stampPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	tracker := newDesktopLifecycleTracker(root, "v1.39.0", "stable")
	if err := tracker.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tracker.clean)
	_ = tracker.consumePrevious(true)

	if tracker.priorPassAt.IsZero() || !tracker.priorPassAt.Equal(prior) {
		t.Fatalf("priorPassAt = %v, want the planted stamp %v", tracker.priorPassAt, prior)
	}
	survived, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatalf("consume-pass stamp must survive the pass (not consumed as a record): %v", err)
	}
	var stamp lifecycleConsumePassStamp
	if json.Unmarshal(survived, &stamp) != nil || stamp.SchemaVersion != lifecycleConsumePassSchema {
		t.Fatalf("stamp unreadable after the pass: %s", survived)
	}
	at, err := time.Parse(time.RFC3339Nano, stamp.At)
	if err != nil || !at.After(prior) {
		t.Fatalf("stamp after the pass = %q (err=%v), want a time after %v", stamp.At, err, prior)
	}
}
