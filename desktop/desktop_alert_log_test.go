package main

// tl 1097 验收测试——告警日志分级保留（B 方案）。
//
// 覆盖面（对应任务书第五节验收标准）：
//  ① 分级隔离：INFO/WARN/ERROR 各写一条 ⇒ 全量三条都在、告警日志只有
//     WARN+ERROR；
//  ② 时间档生效：8 天前的告警滚动份在 retention=7 时被删、=30/永久时保留；
//  ③ 大小档生效：总量超硬顶时从最老删起直到 ≤ 硬顶；=无限制时不因大小删除；
//  ④ 两档冲突以大小为准：时间未到期但总量超硬顶 ⇒ 仍删最老；
//  ⑤ 默认值：全新 config 断言时间档=7 天、大小档=100MB；
//  ⑥ 全量日志不受配置影响：rollingLogWriter 默认参数下 25 份行为回归；
//  ⑦ 不误删：清理只碰 desktop-alerts.log 家族，不碰 repair/、diagnostics/、
//     inbox-recovery.jsonl 或用户文件；
//  ⑧ 行解析：alertLineIsWarnOrError 对 WARN/ERROR 为真、对 INFO/DEBUG/畸形行
//     为假。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

func TestDesktopAlertLogRetentionDefaults(t *testing.T) {
	cfg := config.Default()
	if got := desktopAlertLogRetentionDaysEffective(cfg); got != 7 {
		t.Fatalf("default retention = %d days, want 7", got)
	}
	if got := desktopAlertLogMaxTotalBytesEffective(cfg); got != 100<<20 {
		t.Fatalf("default max total = %d bytes, want %d (100MB)", got, int64(100<<20))
	}
	// 永久 = 不按时间删（0）；无限制 = 不按大小删（-1）。
	if err := cfg.SetDesktopAlertLogRetentionDays(-1); err != nil {
		t.Fatalf("set retention -1: %v", err)
	}
	if got := desktopAlertLogRetentionDaysEffective(cfg); got != 0 {
		t.Fatalf("forever retention = %d days, want 0 (no time-based prune)", got)
	}
	if err := cfg.SetDesktopAlertLogMaxTotalMB(-1); err != nil {
		t.Fatalf("set max total -1: %v", err)
	}
	if got := desktopAlertLogMaxTotalBytesEffective(cfg); got != -1 {
		t.Fatalf("unlimited max total = %d, want -1 (no size-based prune)", got)
	}
}

func TestDesktopAlertLogSettersClamp(t *testing.T) {
	cfg := config.Default()
	cases := []struct{ in, want int }{{-5, -1}, {-1, -1}, {0, 0}, {30, 30}, {200, 90}}
	for _, tc := range cases {
		if err := cfg.SetDesktopAlertLogRetentionDays(tc.in); err != nil {
			t.Fatalf("set retention %d: %v", tc.in, err)
		}
		if got := cfg.Desktop.DesktopAlertLogRetentionDays; got != tc.want {
			t.Fatalf("retention %d stored %d, want %d", tc.in, got, tc.want)
		}
	}
	sizeCases := []struct{ in, want int }{{-5, -1}, {-1, -1}, {0, 0}, {300, 300}, {999999, 10240}}
	for _, tc := range sizeCases {
		if err := cfg.SetDesktopAlertLogMaxTotalMB(tc.in); err != nil {
			t.Fatalf("set max total %d: %v", tc.in, err)
		}
		if got := cfg.Desktop.DesktopAlertLogMaxTotalMB; got != tc.want {
			t.Fatalf("max total %d stored %d, want %d", tc.in, got, tc.want)
		}
	}
}

// writeTestAlertFile 在 dir 下写入 size 字节的告警滚动份，并把 mtime 改成
// ageAgo 之前（模拟「上次会话遗留的超期文件」）。
func writeTestAlertFile(t *testing.T, dir string, name string, size int, ageAgo time.Duration) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mtime := time.Now().Add(-ageAgo)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

func listFamilyNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := listAlertLogFamily(dir)
	if err != nil {
		t.Fatalf("list family: %v", err)
	}
	names := make(map[string]bool, len(files))
	for _, f := range files {
		names[filepath.Base(f.path)] = true
	}
	return names
}

func TestPruneAlertLogTimeRetention(t *testing.T) {
	dir := t.TempDir()
	// 8 天前的滚动份（retention=7 应删）、1 天前的滚动份（应留）。
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".1", 1<<10, 8*24*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".2", 1<<10, 24*time.Hour)

	// retention=7 天：超期的 .1 被删，.2 保留。
	removed, err := pruneAlertLogFamily(dir, 7, -1, time.Now())
	if err != nil {
		t.Fatalf("prune retention 7: %v", err)
	}
	if len(removed) != 1 || filepath.Base(removed[0]) != desktopAlertLogFileName+".1" {
		t.Fatalf("retention=7 removed %v, want exactly [.1]", removed)
	}
	names := listFamilyNames(t, dir)
	if names[desktopAlertLogFileName+".1"] {
		t.Fatalf(".1 should have been deleted by retention=7")
	}
	if !names[desktopAlertLogFileName+".2"] {
		t.Fatalf(".2 should survive retention=7")
	}

	// retention=30 天：.1（8 天前）不再超期，两份都保留。
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".1", 1<<10, 8*24*time.Hour)
	removed, err = pruneAlertLogFamily(dir, 30, -1, time.Now())
	if err != nil {
		t.Fatalf("prune retention 30: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("retention=30 removed %v, want none", removed)
	}

	// retention=-1（永久）→ 翻译为 0 天：不做时间清理，两份都保留。
	removed, err = pruneAlertLogFamily(dir, 0, -1, time.Now())
	if err != nil {
		t.Fatalf("prune forever: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("forever removed %v, want none", removed)
	}
	if !listFamilyNames(t, dir)[desktopAlertLogFileName+".1"] || !listFamilyNames(t, dir)[desktopAlertLogFileName+".2"] {
		t.Fatalf("forever must keep every rolled file")
	}
}

func TestPruneAlertLogSizeCeiling(t *testing.T) {
	dir := t.TempDir()
	// 3 份 × 40KB = 120KB；硬顶 100KB ⇒ 从最老删起，删 1 份（40KB）后 80KB ≤ 100KB。
	// 用不同 mtime 保证「从最老删起」顺序确定：.1 最老、.3 最新。
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".1", 40<<10, 72*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".2", 40<<10, 48*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".3", 40<<10, 24*time.Hour)
	// 当前活跃文件（非滚动份）也造出来，确保它不被删（句柄持有 + 追加语义）。
	writeTestAlertFile(t, dir, desktopAlertLogFileName, 10<<10, time.Hour)

	removed, err := pruneAlertLogFamily(dir, -1, 100<<10, time.Now())
	if err != nil {
		t.Fatalf("prune size: %v", err)
	}
	if len(removed) != 1 || filepath.Base(removed[0]) != desktopAlertLogFileName+".1" {
		t.Fatalf("size prune removed %v, want exactly [.1] (oldest)", removed)
	}
	names := listFamilyNames(t, dir)
	if names[desktopAlertLogFileName+".1"] {
		t.Fatalf(".1 (oldest) should have been deleted first")
	}
	if !names[desktopAlertLogFileName+".2"] || !names[desktopAlertLogFileName+".3"] {
		t.Fatalf(".2/.3 should survive a single-file trim")
	}
	if !names[desktopAlertLogFileName] {
		t.Fatalf("current (non-rolled) file must never be deleted by size prune")
	}

	// 无限制（-1）：不因大小删除任何文件。
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".1", 40<<10, 72*time.Hour)
	removed, err = pruneAlertLogFamily(dir, -1, -1, time.Now())
	if err != nil {
		t.Fatalf("prune unlimited: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("unlimited removed %v, want none", removed)
	}
	// .1（重写）+ .2 + .3 + 当前文件 = 4 份。
	if len(listFamilyNames(t, dir)) != 4 {
		t.Fatalf("unlimited must keep all 4 files, got %d", len(listFamilyNames(t, dir)))
	}
}

func TestPruneAlertLogTimeSizeConflictSizeWins(t *testing.T) {
	dir := t.TempDir()
	// 「时间未到期但总量超硬顶」：2 天前的两份各 40KB（retention=7 天不触发
	// 时间清理），总量 80KB + 当前 30KB = 110KB > 硬顶 100KB ⇒ 仍按大小档删
	// 最老一份。
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".1", 40<<10, 48*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".2", 40<<10, 36*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName, 30<<10, time.Hour)

	removed, err := pruneAlertLogFamily(dir, 7, 100<<10, time.Now())
	if err != nil {
		t.Fatalf("prune conflict: %v", err)
	}
	if len(removed) != 1 || filepath.Base(removed[0]) != desktopAlertLogFileName+".1" {
		t.Fatalf("conflict removed %v, want exactly [.1] (oldest, size wins)", removed)
	}
	names := listFamilyNames(t, dir)
	if names[desktopAlertLogFileName+".1"] {
		t.Fatalf(".1 should have been deleted (size ceiling wins over unexpired time)")
	}
	if !names[desktopAlertLogFileName+".2"] || !names[desktopAlertLogFileName] {
		t.Fatalf(".2 (still within ceiling) and current file must survive")
	}
}

func TestPruneAlertLogDoesNotTouchUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	// 无关文件：repair/ 子目录、diagnostics/ 子目录、inbox-recovery.jsonl（tl
	// 1094 家族，自带 5MB×2 固定上限）、用户自放文件。
	if err := os.MkdirAll(filepath.Join(dir, "repair"), 0o700); err != nil {
		t.Fatalf("mkdir repair: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "diagnostics"), 0o700); err != nil {
		t.Fatalf("mkdir diagnostics: %v", err)
	}
	unrelated := []string{
		filepath.Join(dir, "repair", "session.json"),
		filepath.Join(dir, "diagnostics", "heap.pprof"),
		filepath.Join(dir, "inbox-recovery.jsonl"),
		filepath.Join(dir, "user-notes.txt"),
	}
	for _, p := range unrelated {
		if err := os.WriteFile(p, []byte("keep me"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	// 告警家族：超期 + 超量，两档都触发。
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".1", 40<<10, 8*24*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName+".2", 40<<10, 48*time.Hour)
	writeTestAlertFile(t, dir, desktopAlertLogFileName, 40<<10, time.Hour)

	removed, err := pruneAlertLogFamily(dir, 7, 100<<10, time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	for _, p := range removed {
		if !strings.HasPrefix(filepath.Base(p), desktopAlertLogFileName) {
			t.Fatalf("prune removed a non-alert-log file: %s", p)
		}
	}
	for _, p := range unrelated {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unrelated file %s was deleted: %v", p, err)
		}
	}
}

func TestAlertLineIsWarnOrError(t *testing.T) {
	warn := []byte("time=2026-10-23T10:00:00.000+08:00 level=WARN msg=\"desktop: topic archive metadata reconciliation remains pending\"\n")
	errLine := []byte("time=2026-10-23T10:00:00.000+08:00 level=ERROR msg=\"desktop: snapshot all tabs failed\" tab=abc err=boom\n")
	info := []byte("time=2026-10-23T10:00:00.000+08:00 level=INFO msg=\"desktop: settings view loaded\" ms=3\n")
	debug := []byte("time=2026-10-23T10:00:00.000+08:00 level=DEBUG msg=\"serve: pool tick\" n=1\n")
	malformed := []byte("not a slog line at all\n")
	levelInMsg := []byte("time=2026-10-23T10:00:00.000+08:00 level=INFO msg=\"user reported level=ERROR in a quote\" ok=true\n")

	if !alertLineIsWarnOrError(warn) {
		t.Fatalf("WARN line must classify as alert")
	}
	if !alertLineIsWarnOrError(errLine) {
		t.Fatalf("ERROR line must classify as alert")
	}
	if alertLineIsWarnOrError(info) {
		t.Fatalf("INFO line must NOT classify as alert")
	}
	if alertLineIsWarnOrError(debug) {
		t.Fatalf("DEBUG line must NOT classify as alert")
	}
	if alertLineIsWarnOrError(malformed) {
		t.Fatalf("malformed line must NOT classify as alert")
	}
	if alertLineIsWarnOrError(levelInMsg) {
		t.Fatalf("INFO line whose msg contains level=ERROR must NOT classify as alert")
	}
}

func TestAlertLogLevelWriterFiltersAndWrites(t *testing.T) {
	dir := t.TempDir()
	full := newRollingLogWriter(filepath.Join(dir, "desktop.log"))
	defer full.Close()
	alertW := &alertLogLevelWriter{w: newRollingLogWriterWithMax(filepath.Join(dir, desktopAlertLogFileName), desktopAlertLogMaxSizeBytes, desktopAlertLogMaxBackups)}
	// 必须在读文件前 Close——Windows 下未关闭句柄会让 t.TempDir 的 RemoveAll
	// 报「文件被另一进程占用」（cleanup 阶段失败会把测试标红，即使断言全过）。
	defer alertW.w.Close()

	warn := []byte("time=2026-10-23T10:00:00.000+08:00 level=WARN msg=\"warn line\"\n")
	info := []byte("time=2026-10-23T10:00:00.000+08:00 level=INFO msg=\"info line\"\n")
	errLine := []byte("time=2026-10-23T10:00:00.000+08:00 level=ERROR msg=\"error line\"\n")

	// 全量 writer 收全部三条（模拟 composite 里的 full 链）。
	for _, p := range [][]byte{info, warn, errLine} {
		if _, err := full.Write(p); err != nil {
			t.Fatalf("full write: %v", err)
		}
	}
	// 告警 writer 只收 WARN/ERROR。
	for _, p := range [][]byte{info, warn, errLine} {
		if _, err := alertW.Write(p); err != nil {
			t.Fatalf("alert write: %v", err)
		}
	}

	fullData, err := os.ReadFile(filepath.Join(dir, "desktop.log"))
	if err != nil {
		t.Fatalf("read full log: %v", err)
	}
	if got := strings.Count(string(fullData), "msg="); got != 3 {
		t.Fatalf("full log lines = %d, want 3", got)
	}
	alertData, err := os.ReadFile(filepath.Join(dir, desktopAlertLogFileName))
	if err != nil {
		t.Fatalf("read alert log: %v", err)
	}
	alertStr := string(alertData)
	if !strings.Contains(alertStr, "warn line") || !strings.Contains(alertStr, "error line") {
		t.Fatalf("alert log must contain WARN and ERROR lines, got: %q", alertStr)
	}
	if strings.Contains(alertStr, "info line") {
		t.Fatalf("alert log must NOT contain INFO line, got: %q", alertStr)
	}
}

func TestFullLogWriterDefaultParamsUnchanged(t *testing.T) {
	// 回归钉：newRollingLogWriter（无显式参数）必须与桌面全量日志的既有行为
	// 一致——4MB/份触发滚动，25 份封顶（desktopLogMaxSizeBytes /
	// desktopLogMaxBackups 两个常量是「行为等价」的锚点，不是本件新常量）。
	dir := t.TempDir()
	w := newRollingLogWriter(filepath.Join(dir, "desktop.log"))
	defer w.Close()
	if w.effectiveMaxSizeBytes() != desktopLogMaxSizeBytes || w.effectiveMaxBackups() != desktopLogMaxBackups {
		t.Fatalf("default rollingLogWriter params changed: size=%d backups=%d",
			w.effectiveMaxSizeBytes(), w.effectiveMaxBackups())
	}
	if desktopLogMaxSizeBytes != 4<<20 || desktopLogMaxBackups != 25 {
		t.Fatalf("full-log constants drifted: size=%d backups=%d (want 4MB / 25)",
			desktopLogMaxSizeBytes, desktopLogMaxBackups)
	}
}
