package agent

import (
	"path/filepath"
	"testing"
	"time"
)

// tl1372 A1：折叠免报的对账证据面。LatestFoldRepairTime 从 repairlog 取最近
// 一次「完成」的投影折叠时间；跳过/失败的折叠没动过 transcript 字节，解释不
// 任何缺席文本，不得参与免报。

func writeFoldRecord(t *testing.T, sessionPath, at, status string) {
	t.Helper()
	writeRepairLogRecord(sessionPath, RepairRecord{
		At:      at,
		Path:    sessionPath,
		Actions: []SelfHealAction{{Action: HealActionFold, Status: status}},
		Before:  "projection_overflow",
		After:   "healthy",
	})
}

func TestLatestFoldRepairTimeMissingLog(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "sess.jsonl")
	if got, ok := LatestFoldRepairTime(sessionPath); ok || !got.IsZero() {
		t.Fatalf("missing repairlog must yield no fold evidence, got (%v, %v)", got, ok)
	}
}

func TestLatestFoldRepairTimeOnlyCompletedFoldsCount(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "sess.jsonl")
	// 只有剥图（非折叠）→ 无折叠证据。
	writeRepairLogRecord(sessionPath, RepairRecord{
		At:      "2026-11-03T04:18:00Z",
		Path:    sessionPath,
		Actions: []SelfHealAction{{Action: HealActionStripImages, Status: "done"}},
		Before:  "projection_overflow",
		After:   "healthy",
	})
	if got, ok := LatestFoldRepairTime(sessionPath); ok || !got.IsZero() {
		t.Fatalf("strip_images-only log must yield no fold evidence, got (%v, %v)", got, ok)
	}
	// 折叠被跳过 → 不算。
	writeFoldRecord(t, sessionPath, "2026-11-03T05:00:00Z", "skipped")
	if _, ok := LatestFoldRepairTime(sessionPath); ok {
		t.Fatal("a skipped fold must not count as fold evidence")
	}
	// 完成折叠 → 取其时间。
	writeFoldRecord(t, sessionPath, "2026-11-03T08:18:00Z", "done")
	got, ok := LatestFoldRepairTime(sessionPath)
	if !ok {
		t.Fatal("completed fold must yield evidence")
	}
	want, _ := time.Parse(time.RFC3339, "2026-11-03T08:18:00Z")
	if !got.Equal(want) {
		t.Fatalf("LatestFoldRepairTime = %v, want %v", got, want)
	}
	// 失败的折叠晚于完成的折叠 → 仍取完成那次。
	writeFoldRecord(t, sessionPath, "2026-11-03T09:00:00Z", "failed")
	if got, _ := LatestFoldRepairTime(sessionPath); !got.Equal(want) {
		t.Fatalf("a failed fold must not advance the latest fold time, got %v", got)
	}
	// 再落一次更晚的完成 → 取最新。
	writeFoldRecord(t, sessionPath, "2026-11-03T09:40:00Z", "done")
	got, _ = LatestFoldRepairTime(sessionPath)
	want2, _ := time.Parse(time.RFC3339, "2026-11-03T09:40:00Z")
	if !got.Equal(want2) {
		t.Fatalf("LatestFoldRepairTime = %v, want latest completed fold %v", got, want2)
	}
}

// 损坏的 At 行跳过而非致命：免报证据缺失时保守继续上报。
func TestLatestFoldRepairTimeSkipsUnparsableAt(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "sess.jsonl")
	writeRepairLogRecord(sessionPath, RepairRecord{
		At:      "not-a-timestamp",
		Path:    sessionPath,
		Actions: []SelfHealAction{{Action: HealActionFold, Status: "done"}},
	})
	writeFoldRecord(t, sessionPath, "2026-11-03T10:00:00Z", "done")
	got, ok := LatestFoldRepairTime(sessionPath)
	if !ok {
		t.Fatal("a good record after a corrupt one must still yield evidence")
	}
	want, _ := time.Parse(time.RFC3339, "2026-11-03T10:00:00Z")
	if !got.Equal(want) {
		t.Fatalf("LatestFoldRepairTime = %v, want %v", got, want)
	}
}
