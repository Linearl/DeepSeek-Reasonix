package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLog = `time=2026-10-13T16:26:49.269+08:00 level=INFO msg="desktop: effort read breakdown" tab=tab_old stage="switch-tab:ancillary effort" total_ms=223 reconcile_ms=223 snapshot_ms=0 resolve_ms=0 memo_ms=0 reconcile_walk_ms=0 memo_hit=false snapshot_wait_ms=0 snapshot_stat_ms=0 snapshot_load_ms=0 snapshot_reloaded=false after_timeout=false
time=2026-10-14T10:51:23.485+08:00 level=WARN msg="desktop: effort read timed out; serving cached value" tab=tab_a limit=2s cache_age=1m52.545s
time=2026-10-14T10:53:51.654+08:00 level=WARN msg="desktop: effort read timed out; serving cached value" tab=tab_b limit=2s cache_age=6m35.664s
time=2026-10-14T11:43:17.773+08:00 level=INFO msg="desktop: effort read breakdown" tab=tab_x stage="switch-tab:ancillary effort" total_ms=261314 reconcile_ms=191828 memo_ms=100 reconcile_walk_ms=191728 snapshot_ms=69485 resolve_ms=0 memo_hit=false snapshot_wait_ms=12 snapshot_stat_ms=33 snapshot_load_ms=69440 snapshot_reloaded=true after_timeout=true
time=2026-10-14T16:30:00.000+08:00 level=INFO msg="desktop: session reconcile slow" tab=tab_x total_ms=990 phase=path phase_ms=990
time=2026-10-14T18:34:08.989+08:00 level=INFO msg="desktop: effort read breakdown" tab=tab_y stage="switch-tab:ancillary effort" total_ms=10350 reconcile_ms=10277 memo_ms=50 reconcile_walk_ms=10227 snapshot_ms=72 resolve_ms=0 memo_hit=false snapshot_wait_ms=0 snapshot_stat_ms=9 snapshot_load_ms=60 snapshot_reloaded=false after_timeout=true
`

func TestScanCountsTimeoutsByDayAndHour(t *testing.T) {
	days, hours, slow, err := scan(strings.NewReader(sampleLog), 2000, "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if days["2026-10-14"] != 2 || len(days) != 1 {
		t.Fatalf("day counts = %v, want 2026-10-14:2 only", days)
	}
	if hours["2026-10-14 10"] != 2 || len(hours) != 1 {
		t.Fatalf("day+hour counts = %v, want 2026-10-14 10:2 only", hours)
	}
	// Only breakdowns at/above minMs; the 223ms line is below it.
	if len(slow) != 2 {
		t.Fatalf("slow reads = %d, want 2", len(slow))
	}
	if slow[0].totalMs != 261314 || slow[1].totalMs != 10350 {
		t.Fatalf("slow reads not sorted desc: %+v", slow)
	}
	if slow[0].memoMs != 100 || slow[0].walkMs != 191728 || slow[0].snapshotMs != 69485 {
		t.Fatalf("segment split misparsed: %+v", slow[0])
	}
	if !slow[0].afterTimeout || slow[0].tab != "tab_x" {
		t.Fatalf("after_timeout/tab misparsed: %+v", slow[0])
	}
	if !strings.HasPrefix(slow[0].at, "2026-10-14T11:43:17") {
		t.Fatalf("timestamp assembly = %q", slow[0].at)
	}
}

func TestScanSinceFiltersEarlierDays(t *testing.T) {
	days, _, _, err := scan(strings.NewReader(sampleLog), 2000, "2026-10-14")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(days) != 1 || days["2026-10-14"] != 2 {
		t.Fatalf("since-filtered days = %v, want only 2026-10-14:2", days)
	}
}

func TestExpandArgsPicksLogRotationsFromDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"desktop.log", "desktop.log.1", "desktop.log.2", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	files := expandArgs([]string{dir})
	if len(files) != 3 {
		t.Fatalf("expandArgs = %v, want the three desktop.log files", files)
	}
	for _, f := range files {
		if !strings.Contains(filepath.Base(f), "desktop.log") {
			t.Fatalf("non-log file included: %s", f)
		}
	}
	// A missing path contributes nothing instead of failing the run.
	if got := expandArgs([]string{filepath.Join(dir, "does-not-exist")}); len(got) != 0 {
		t.Fatalf("missing path expanded to %v, want none", got)
	}
}
