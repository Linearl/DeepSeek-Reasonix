package agent

import (
	"slices"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 1010 需求 4：rotation 不得把选中 head 重钉回「更旧且更短」的链。
// 现场（20261019 ai-passport 21:03）：选中停在 10-16 的 rewind 头
// 01M51VB（6611 条），并发头 01M59JNK 已长到 7087 条且活动到 21:03，
// rotation 的 select reason=rotation 把后续所有打开都压回旧链。

// dagDominanceFixture builds: main chain of baseMsgs messages, then a
// concurrent head optionally pinned by an explicit select, then more messages
// on main.
func dagDominanceFixture(t *testing.T, path string, pin bool) (base time.Time) {
	t.Helper()
	base = time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	for i := 0; i < 4; i++ {
		m := dagMsg(provider.RoleUser, "m", string(rune('A'+i)))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second)))
		parent = m.ID
	}
	// 旧头从 A1 分叉（现场里的 rewind 头）；pin 时附带显式选中。
	if pin {
		entries = append(entries,
			sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "old", From: "A", Kind: HeadKindConcurrent, Writer: "w1", At: base.Add(time.Hour)},
			sessionDAGEntry{Type: sessionDAGTypeSelect, Head: "old", Reason: HeadKindRewind, At: base.Add(time.Hour)},
		)
	} else {
		entries = append(entries,
			sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "old", From: "A", Kind: HeadKindConcurrent, Writer: "w1", At: base.Add(time.Hour)},
		)
	}
	// 之后主链继续生长：更长且更新（现场里的并发头）。
	for i := 0; i < 6; i++ {
		m := dagMsg(provider.RoleUser, "new", string(rune('N'+i)))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(48*time.Hour+time.Duration(i)*time.Second)))
		parent = m.ID
	}
	dagAppend(t, path, entries...)
	return base
}

func TestRotationDropsSelectPinDominatedByLongerNewerHead(t *testing.T) {
	path := dagTestSession(t)
	base := dagDominanceFixture(t, path, true)
	st := dagReplay(t, path)
	if st.selected != "old" {
		t.Fatalf("selected = %q, want the pinned old head", st.selected)
	}
	oldLen := len(st.chainIDs("old"))
	mainLen := len(st.chainIDs(SessionMainHead))
	if mainLen <= oldLen || !st.heads[SessionMainHead].lastActivity.After(st.heads["old"].lastActivity) {
		t.Fatalf("fixture must be dominated: old=%d main=%d", oldLen, mainLen)
	}
	entries, err := buildRotatedSessionDAG(st, base.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("build rotated: %v", err)
	}
	for _, e := range entries {
		if e.Type == sessionDAGTypeSelect {
			t.Fatalf("rotation re-pinned select %+v; a pin strictly dominated by a longer, newer head must be dropped", e)
		}
	}
	// 旋转丢掉 pin 之后，下一次回放（无 select 条目）按活动回落到主链——
	// 现场「10-16→10-19 丢失」的解。
	unpinned := dagTestSession(t)
	dagDominanceFixture(t, unpinned, false)
	if got := dagReplay(t, unpinned).selectedHead(); got != SessionMainHead {
		t.Fatalf("open without the pin = %q, want the dominant main chain", got)
	}
}

func TestRotationKeepsNonDominatedSelectPin(t *testing.T) {
	path := dagTestSession(t)
	base := dagDominanceFixture(t, path, true)
	st := dagReplay(t, path)
	// 把选中切到主链：主链就是最长且最新的，rotation 必须照常重钉（字节稳定）。
	st.selected = SessionMainHead
	entries, err := buildRotatedSessionDAG(st, base.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("build rotated: %v", err)
	}
	hasSelect := slices.ContainsFunc(entries, func(e sessionDAGEntry) bool { return e.Type == sessionDAGTypeSelect })
	if !hasSelect {
		t.Fatal("a non-dominated selection must keep its rotation re-pin")
	}
}

func TestLoadHeadEventsSelectedHeadBehindReadings(t *testing.T) {
	path := dagTestSession(t)
	dagDominanceFixture(t, path, true)
	st := dagReplay(t, path)
	events := loadHeadEvents(st, "old", "")
	behind := false
	for _, ev := range events {
		if ev.Kind != HeadEventSelectedHeadBehind {
			continue
		}
		behind = true
		if ev.HeadID != "old" || ev.OtherHeadID != SessionMainHead {
			t.Fatalf("behind event heads = %q/%q", ev.HeadID, ev.OtherHeadID)
		}
		if ev.Messages != len(st.chainIDs("old")) || ev.OtherMessages != len(st.chainIDs(SessionMainHead)) {
			t.Fatalf("behind readings = %d/%d, want the chain lengths", ev.Messages, ev.OtherMessages)
		}
	}
	if !behind {
		t.Fatalf("open on a dominated head must surface readings, got %+v", events)
	}
	// 打开在主链上：无落后事件。
	for _, ev := range loadHeadEvents(st, SessionMainHead, "") {
		if ev.Kind == HeadEventSelectedHeadBehind {
			t.Fatalf("the dominant head must not be flagged behind: %+v", ev)
		}
	}
}
