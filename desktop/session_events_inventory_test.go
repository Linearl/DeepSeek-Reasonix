package main

import (
	"testing"

	"reasonix/internal/config"
)

// 任务 373 R4 备料：inventory 行级处置可观测的判定语义。ReclaimBytes 是 fold
// 的回收上限（events − live，向下取 0，log 比 live 还小的会话修了反而没得拿）；
// LiveOverCap 只在 auto 模式且有 cap 时成立——live 内容本身超 cap 的会话，
// fold 后的 log（≈live）仍超 cap，修复不可能把它修到 cap 之下。
func TestSessionEventsEntryJudgment(t *testing.T) {
	mib := int64(1) << 20

	// 常规膨胀行：回收上限 = events − live；不标 LiveOverCap（cap 未超）。
	entry := SessionEventsEntry{EventsBytes: 100 * mib, LiveBytes: 30 * mib}
	sessionEventsEntryJudgment(&entry, config.EventsAutoRotationAuto, 50)
	if entry.ReclaimBytes != 70*mib {
		t.Fatalf("reclaimBytes = %d, want %d (events - live)", entry.ReclaimBytes, 70*mib)
	}
	if entry.LiveOverCap {
		t.Fatal("30MiB live under a 50MiB cap must not be marked repair-futile")
	}

	// live 超 cap（auto 模式）：修复无效类，即使 events 本身很大。
	entry = SessionEventsEntry{EventsBytes: 95 * mib, LiveBytes: 58 * mib}
	sessionEventsEntryJudgment(&entry, config.EventsAutoRotationAuto, 50)
	if entry.ReclaimBytes != 37*mib {
		t.Fatalf("reclaimBytes = %d, want %d", entry.ReclaimBytes, 37*mib)
	}
	if !entry.LiveOverCap {
		t.Fatal("58MiB live over a 50MiB cap must be marked repair-futile in auto mode")
	}

	// 同样的 live 超 cap，但模式/口径不满足时不得标记：
	// manual（无 cap 口径）与 cap=0（cap 关闭）。
	entry = SessionEventsEntry{EventsBytes: 95 * mib, LiveBytes: 58 * mib}
	sessionEventsEntryJudgment(&entry, config.EventsAutoRotationManual, 50)
	if entry.LiveOverCap {
		t.Fatal("manual mode has no cap judgment; LiveOverCap must stay false")
	}
	entry = SessionEventsEntry{EventsBytes: 95 * mib, LiveBytes: 58 * mib}
	sessionEventsEntryJudgment(&entry, config.EventsAutoRotationAuto, 0)
	if entry.LiveOverCap {
		t.Fatal("cap 0 (disabled) must never mark LiveOverCap")
	}

	// log 比 live 还小（重放后兼容文件更大的边缘形态）：回收上限取 0，
	// 不出现负数误导排序。
	entry = SessionEventsEntry{EventsBytes: 10 * mib, LiveBytes: 30 * mib}
	sessionEventsEntryJudgment(&entry, config.EventsAutoRotationAuto, 50)
	if entry.ReclaimBytes != 0 {
		t.Fatalf("reclaimBytes = %d, want 0 (log smaller than live)", entry.ReclaimBytes)
	}
}
