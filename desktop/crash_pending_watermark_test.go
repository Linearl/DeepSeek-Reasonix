package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ── task 763: seen-watermark for the startup crash-analysis entry ───────────
// 21.5 装机实测：上游 503 令 crash-pending 积压无法清空，分析入口 count>0 即弹，
// 每次启动（含更新重启）都重弹积压——更新重启被读成「被误判为崩溃」。水位修法：
// 只面呈比上次新的报告；积压仍在上传与设置页诊断里，真崩溃（新文件）照常弹。

// writeLegacyPendingCrash plants the pre-queue single-file report shape.
func writeLegacyPendingCrash(t *testing.T, site string) {
	t.Helper()
	report := baseCrashReport("crash")
	report.SchemaVersion = 2
	report.Source = "go"
	report.Label = site
	report.Message = "[go panic] " + site
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingCrashPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The 21.5 incident shape: a backlog that cannot drain must not re-surface the
// crash banner on every launch. First snapshot surfaces it once and advances
// the watermark; the next launch's snapshot must come back empty.
func TestCrashPromptWatermarkSuppressesSeenBacklogOnRelaunch(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("763-backlog-old", "boom", []byte("stack"))
	writePendingCrash("763-backlog-new", "boom", []byte("stack"))

	first := NewApp()
	first.snapshotPendingCrashForAnalysis()
	if got := first.PendingCrashSnapshot(); got.Count != 2 || len(got.Reports) != 2 {
		t.Fatalf("first launch: count=%d reports=%d, want 2/2 (backlog surfaces once)", got.Count, len(got.Reports))
	}

	second := NewApp()
	second.snapshotPendingCrashForAnalysis()
	got := second.PendingCrashSnapshot()
	if got.Count != 0 || len(got.Reports) != 0 {
		t.Fatalf("second launch: count=%d reports=%d, want 0/0 (seen backlog must not re-surface)", got.Count, len(got.Reports))
	}
	if remaining := len(pendingCrashPaths()); remaining != 2 {
		t.Fatalf("watermark must not touch the queue, %d files left, want 2 (upload/diagnostics own them)", remaining)
	}
}

// A REAL crash after the watermark writes a fresh queue file; the next launch
// surfaces exactly that report — the banner stays a real-crash instrument.
func TestCrashPromptWatermarkSurfacesNewCrashAfterWatermark(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("763-seen", "old boom", []byte("stack"))
	NewApp().snapshotPendingCrashForAnalysis()

	writePendingCrash("763-fresh-crash", "new boom", []byte("stack"))
	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	got := app.PendingCrashSnapshot()
	if got.Count != 1 || len(got.Reports) != 1 {
		t.Fatalf("count=%d reports=%d, want 1/1 (only the fresh crash)", got.Count, len(got.Reports))
	}
	var payload frontendCrashPayload
	if err := json.Unmarshal([]byte(got.Reports[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Message, "763-fresh-crash") {
		t.Errorf("surfaced report must be the fresh crash, got: %s", payload.Message)
	}
}

// A corrupt watermark must fail open to the old surface-everything behavior —
// evidence suppression is never the failure direction.
func TestCrashPromptWatermarkCorruptFailsOpen(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("763-corrupt-wm", "boom", []byte("stack"))
	if err := os.MkdirAll(crashPromptWatermarkPath()+".d", 0o700); err != nil {
		t.Skipf("watermark path blocked: %v", err)
	}
	if err := os.RemoveAll(crashPromptWatermarkPath() + ".d"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(crashPromptWatermarkPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	if got := app.PendingCrashSnapshot(); got.Count != 1 {
		t.Fatalf("count=%d, want 1 (corrupt watermark fails open)", got.Count)
	}
}

// The pre-queue legacy single file predates the naming scheme: it always
// surfaces, watermark or not.
func TestCrashPromptLegacyPendingFileAlwaysSurfaces(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("763-queue-seen", "boom", []byte("stack"))
	NewApp().snapshotPendingCrashForAnalysis() // watermark now covers the queue file

	writeLegacyPendingCrash(t, "763-legacy")
	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	got := app.PendingCrashSnapshot()
	if got.Count != 1 || len(got.Reports) != 1 {
		t.Fatalf("count=%d reports=%d, want 1/1 (legacy file always fresh)", got.Count, len(got.Reports))
	}
	var payload frontendCrashPayload
	if err := json.Unmarshal([]byte(got.Reports[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Message, "763-legacy") {
		t.Errorf("surfaced report must be the legacy file, got: %s", payload.Message)
	}
}

// The read-model fallback exists for processes that never snapshotted (dev
// flows, tests). A process that DID snapshot with nothing fresh must not fall
// back — the fallback would resurrect the suppressed backlog every launch and
// undo the whole fix.
func TestCrashPromptSnapshotTakenStopsFallbackResurrection(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("763-fallback-seen", "boom", []byte("stack"))
	app := NewApp()
	app.snapshotPendingCrashForAnalysis() // surfaces the fresh report this boot
	if got := app.PendingCrashSnapshot(); got.Count != 1 {
		t.Fatalf("count=%d, want 1: the boot snapshot surfaces the fresh report once", got.Count)
	}
	writePendingCrash("763-fallback-fresh", "boom2", []byte("stack2")) // written after boot
	if got := app.PendingCrashSnapshot(); got.Count != 1 {
		t.Fatalf("count=%d, want 1: snapshotted process must not live-read the queue", got.Count)
	}
	fresh := NewApp() // never snapshotted: today's fallback behavior, unfiltered
	if got := fresh.PendingCrashSnapshot(); got.Count != 2 {
		t.Fatalf("count=%d, want 2: never-snapshotted process keeps the live fallback", got.Count)
	}
}
