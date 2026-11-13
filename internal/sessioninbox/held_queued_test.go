package sessioninbox

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// tl1577 刀②（dormant/停滞 可视化，纯派生，零行为）：被 Paused/Readonly 门
// 挡在派发之外的 StateQueued 行数必须作为 HeldQueued 出现在快照状态面。
// tl1339 报告 §2.1#5 的停滞形状此前只在泵的 WARN 里可见（tl1339min 刀①），
// 状态消费方（面板 / serve GET /inbox / 只读探测的 dormant 会话）看不到
// 「门后有几件活」。验收：paused/dormant 形状在 UI/状态可见。

func enqueueHeldQueuedItem(t *testing.T, s *Store, text string) {
	t.Helper()
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: text}}); err != nil {
		t.Fatalf("enqueue %q: %v", text, err)
	}
}

// 门后滞留数：门开时为 0（JSON 不上线），门关时逐件计数，暂停来源（自动 /
// 用户）不改变读数——读数回答的是「卡了几件」，不是「谁持的门」。
func TestSnapshotHeldQueuedTracksGateHiddenWork(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	enqueueHeldQueuedItem(t, s, "held one")
	enqueueHeldQueuedItem(t, s, "held two")

	snap := s.Snapshot()
	if snap.HeldQueued != 0 {
		t.Fatalf("open gate must report zero held work, got %d", snap.HeldQueued)
	}
	if body, merr := json.Marshal(snap); merr != nil || strings.Contains(string(body), "heldQueued") {
		t.Fatalf("zero held work must be omitted from the status payload, body = %s err = %v", body, merr)
	}

	// 自动暂停形状（709 恢复暂停，UserPaused=false）。
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.HeldQueued != 2 {
		t.Fatalf("auto-paused gate must hold 2 queued rows, got %d", snap.HeldQueued)
	}
	if body, merr := json.Marshal(s.Snapshot()); merr != nil || !strings.Contains(string(body), `"heldQueued":2`) {
		t.Fatalf("parked work must be visible on the status payload, body = %s err = %v", body, merr)
	}

	// 用户持有同一扇门：读数不变（谁持门看 UserPaused，卡几件看 HeldQueued）。
	if err := s.SetUserPaused(true); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.HeldQueued != 2 || !snap.UserPaused {
		t.Fatalf("user-held gate must keep counting parked work, snapshot = %+v", snap)
	}

	// 门一开读数即归零：resume 之后停滞信号消失（与派发恢复同一边界）。
	if _, err := s.ResumeAutoPause(); err != nil { // no-op: user held
		t.Fatal(err)
	}
	if err := s.SetUserPaused(false); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.HeldQueued != 0 {
		t.Fatalf("lifted gate must report zero held work, got %d", snap.HeldQueued)
	}
}

// 只读门（schema 降级）同样藏活：即使没有 Paused，readonly 也让 NextQueued
// 返回空（ops.go 同一判定），读数必须如实反映。
func TestSnapshotHeldQueuedCountsReadonlyGate(t *testing.T) {
	man := &manifest{
		SchemaVersion: SchemaVersion,
		RunID:         "run-held",
		Items: []InboxItemMeta{
			{ID: "q1", State: StateQueued},
			{ID: "q2", State: StateQueued},
			{ID: "r1", State: StateRunning},
			{ID: "u1", State: StateUncertain},
		},
	}
	s := &Store{session: filepath.Join(t.TempDir(), "ro.jsonl"), man: man, limits: Limits{}.withDefaults()}

	snap := s.snapshotFromManifestLocked(man, true)
	if !snap.Readonly || snap.HeldQueued != 2 {
		t.Fatalf("readonly gate must hold exactly the queued rows (2), snapshot = %+v", snap)
	}
	snap = s.snapshotFromManifestLocked(man, false)
	if snap.HeldQueued != 0 {
		t.Fatalf("ungated manifest must report zero held work, got %d", snap.HeldQueued)
	}
	man.Paused = true
	snap = s.snapshotFromManifestLocked(man, false)
	if snap.HeldQueued != 2 {
		t.Fatalf("paused gate must hold exactly the queued rows (2), got %d", snap.HeldQueued)
	}
}

// dormant 反查面（887 第三态：有排队活、本进程无 runtime）：只读探测拿到的
// 快照必须携带同一读数——守护/面板在不拉起 runtime 的前提下就能看见
// 「这个会话门后卡着活」。
func TestProbeSnapshotCarriesHeldQueued(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "dormant.jsonl")
	st, err := Open(sessionPath, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	enqueueHeldQueuedItem(t, st, "parked in a dormant manifest")
	if err := st.SetPaused(true); err != nil { // 自动暂停形状，不带 UserPaused
		t.Fatal(err)
	}
	st.Close()

	snap, found, err := ProbeSnapshot(sessionPath)
	if err != nil || !found {
		t.Fatalf("probe must find the dormant manifest, found = %v err = %v", found, err)
	}
	if snap.HeldQueued != 1 {
		t.Fatalf("dormant probe must surface the parked count, snapshot = %+v", snap)
	}
	if !snap.Paused || snap.UserPaused {
		t.Fatalf("provenance must stay intact on the probe surface, snapshot = %+v", snap)
	}
}
