package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
// ③同一次启动先写后弹，新写文件名必然高于水位——水位拦不住本启动新写的误报，
// (3) 份积压反复占位。
//
// Task 1018（20261020 装机实测）：10-19 手动重启的 wedged 残留（watchdog 强杀、
// 零 panic）驻留队列，两次更新重启反复面呈；1018 修法 = 源头读后即清，退役面
// 扩到全部 native.lifecycle 报告。
//
// Task 1018-fix2（20261020 协调线定案）：1018 连 abnormal_phase 也静音是过修——
// wedged（watchdog 强杀）与 unknown 没有其他崩溃上报通道，静音等于吞真崩溃报告。
// 回退为：计划退出（marker 自证 + benign 相位）仍读后即清，abnormal_phase 恢复
// 写报告；退役面收回 benign-only，wedged 件留队交水位 + flush（理由见
// crash_pending.go）。「反复面呈」由 1002 stale 归属 + 水位负责。

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

// 修法回归钉（1018 × 1018-fix2，积压清理场景）：native.lifecycle 积压报告按
// 相位分流——benign（shutting_down/healthy）已判定非崩溃，快照时退役；wedged
// 是崩溃证据（1018-fix2 恢复），留队交水位与 flush；真实 panic 一字节不动。
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
	// 2 benign 相位退役 + wedged 与真实 panic 留队 = 2 份。
	if got := pendingReportCount(t); got != 2 {
		t.Fatalf("queue holds %d reports after the sweep, want 2 (benign backlog retires; wedged + real panic stay — task 1018-fix2)", got)
	}
	snap := app.PendingCrashSnapshot()
	if snap.Count != 2 || len(snap.Reports) != 2 {
		t.Fatalf("snapshot = %d/%d, want 2/2 (wedged evidence + real panic both surface)", snap.Count, len(snap.Reports))
	}
	sawRealPanic, sawWedged := false, false
	for _, path := range pendingCrashPaths() {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var r crashReport
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		switch {
		case r.Source == "go":
			sawRealPanic = true
		case r.Source == "native.lifecycle":
			if r.ExitPhase == "shutting_down" || r.ExitPhase == "healthy" {
				t.Fatalf("benign-phase backlog must retire, not remain: %s", r.ExitPhase)
			}
			if r.ExitPhase != "wedged" {
				t.Fatalf("unexpected surviving lifecycle phase %q", r.ExitPhase)
			}
			sawWedged = true
		default:
			t.Fatalf("unexpected surviving source %q label %q", r.Source, r.Label)
		}
	}
	if !sawRealPanic || !sawWedged {
		t.Fatalf("survivors = panic:%v wedged:%v, want both (wedged is crash evidence — task 1018-fix2)", sawRealPanic, sawWedged)
	}
	sawSurfacedPanic := false
	for _, payload := range snap.Reports {
		var parsed frontendCrashPayload
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Source == "go" && parsed.Label == panicReport.Label {
			sawSurfacedPanic = true
		}
	}
	if !sawSurfacedPanic {
		t.Fatal("the real panic report must surface")
	}
}

// 1018 复发场景端到端钉（验收①）：先清后弹的完整启动时序。1018-fix2 起，新鲜无主
// wedged 面呈 1 份且只面呈 1 份（水位负责不反复）；同场景真实 panic 照常面呈
// （验收③，不误杀）。
func TestFalseCrashUpdateRestartScenarioEndToEnd(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllCrashesAndMarker(t))

	// The 10-19 shape: a manual restart's watchdog-forced quit left a wedged
	// orphan record (no update-restart marker, gate off, desktop.log zero
	// panic). Under 1018-fix2 this is a genuine abnormal death — it reports.
	app := NewApp()
	app.lifecycle.previousRuns = []desktopLifecycleObservation{
		{Phase: "wedged", PID: 15380, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("orphan residue wrote %d reports, want 1 (wedged is crash evidence — task 1018-fix2)", got)
	}
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 1 {
		t.Fatalf("orphan-residue relaunch surfaced %d reports, want 1", got.Count)
	}
	// 水位负责「不反复」：同一启动再快照一次不重复面呈。
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("re-snapshot surfaced %d reports, want 0 (the watermark suppresses a repeat)", got.Count)
	}

	// 验收③ control: a real panic report planted in the queue surfaces exactly
	// the same boot — the restored write must not crowd out genuine crash evidence.
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
	// 队列文件名是 `<unixnano>-<seq>.json`（writePendingReport）；固定名会排在水位
	// 之下被当成积压，必须用真实时间戳命名才能模拟「本启动新写入的崩溃报告」。
	panicName := strconv.FormatInt(time.Now().UTC().UnixNano(), 10) + "-99999-1.json"
	if err := os.WriteFile(filepath.Join(pendingCrashDir(), panicName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	relaunch := NewApp()
	relaunch.lifecycle.previousRuns = app.lifecycle.previousRuns
	relaunch.recordPreviousRunDiagnostics()
	relaunch.snapshotPendingCrashForAnalysis()
	// 上一次启动面呈过的 wedged 报告被水位压住；本启动新写的 wedged 残留与刚
	// 种入的真实 panic 文件名都高于水位，同为 fresh。此处只钉真实 panic 仍面呈。
	snap := relaunch.PendingCrashSnapshot()
	if snap.Count < 1 {
		t.Fatalf("surfaced %d reports, want the real panic to reach the banner", snap.Count)
	}
	sawPanic := false
	for _, payload := range snap.Reports {
		var parsed frontendCrashPayload
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Label == "1018-real-panic" {
			sawPanic = true
		}
	}
	if !sawPanic {
		t.Fatal("acceptance ③: the real panic must still reach the banner")
	}
}

// 1018 × 1018-fix2 端到端钉（积压面）：wedged 相位是崩溃证据（watchdog 强杀、
// 无其他上报通道），已驻留的 wedged 积压在下一次启动面呈一次——「不反复」由
// 水位（同名文件至多面呈一次）负责，不由退役负责；退役只对 benign 积压成立。
func TestWedgedLifecycleBacklogSurfacesOnceOnNextBoot(t *testing.T) {
	isolateDesktopUserDirs(t)
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	wedged := desktopLifecycleReport(desktopLifecycleObservation{
		Version: "v1.38.2", Channel: "stable", Phase: "wedged",
		PID: 15380, DetectedAt: "2026-10-19T01:40:11Z",
	})
	body, err := json.Marshal(wedged)
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
	if got := app.PendingCrashSnapshot(); got.Count != 1 {
		t.Fatalf("wedged backlog surfaced %d reports, want 1 (wedged is crash evidence — task 1018-fix2)", got.Count)
	}
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("wedged backlog on disk = %d reports, want 1 (retained for the flush to ship)", got)
	}
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("re-snapshot surfaced %d reports, want 0 (the watermark suppresses a repeat)", got.Count)
	}
	nextBoot := NewApp()
	nextBoot.snapshotPendingCrashForAnalysis()
	if got := nextBoot.PendingCrashSnapshot(); got.Count != 0 {
		t.Fatalf("next boot surfaced %d reports, want 0 (the 10-20 banner must not come back every launch)", got.Count)
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
// marker 检查提到相位判定之前；1018-fix2 修正为：marker 自证豁免计划退出
// （静音），无 marker 保护的 mid-shutdown 强杀按异常相位照写。
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
		// 控制组：无 marker 保护的 mid-shutdown 残留按 1018-fix2 属异常相位
		// （计划退出的唯一豁免是 marker 自证），照写 1 份报告。
		{Phase: "closing_shared_hosts", PID: 8642, Version: "v1.38.2", Channel: "stable"},
	}
	app.recordPreviousRunDiagnostics()
	// marker 自证的三条静音 + 无 marker 的异常相位一条照写 = 1 份。
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("pending reports = %d, want 1 (marker vouch quiets its own PID; the markerless mid-shutdown death reports — task 1018-fix2)", got)
	}
	var reportedPID int
	for _, path := range pendingCrashPaths() {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var r crashReport
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		reportedPID = r.ProcessPID
	}
	if reportedPID != 8642 {
		t.Fatalf("reported PID = %d, want 8642 (the markerless residue, not a marker-vouched one)", reportedPID)
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
		// than the intermediate pass) keeps reporting in the same scan. Its phase
		// is wedged — the 1018-fix2 restored reporting class — so it must report.
		{Phase: "wedged", PID: 9999, Version: "v1.38.2", Channel: "stable",
			UpdatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano)},
	}
	app.recordPreviousRunDiagnostics()
	// 三层并集（见文件头）：stale 件退役不写、计划退出两类不触发、fresh+wedged
	// 写 1 份——本用例断言合计恰好 1 份。
	if got := pendingReportCount(t); got != 1 {
		t.Fatalf("pending reports = %d, want 1 (stale record retires silently; the fresh wedged death keeps reporting — task 1018-fix2)", got)
	}
	// 那 1 份必须来自控制组（PID 9999），不能是已退役的 stale 件（PID 15380）。
	var reportedPID int
	for _, path := range pendingCrashPaths() {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var r crashReport
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		if r.Source != "native.lifecycle" {
			t.Fatalf("unexpected report source %q label %q", r.Source, r.Label)
		}
		reportedPID = r.ProcessPID
	}
	if reportedPID != 9999 {
		t.Fatalf("reported PID = %d, want 9999 (the fresh control, not the retired stale record 15380)", reportedPID)
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
