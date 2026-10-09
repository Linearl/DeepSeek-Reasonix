package sessioninbox

import (
	"errors"
	"path/filepath"
	"testing"
)

// 任务709：暂停来源标记。自动暂停（恢复/重开遗留/错误防护）不得被记成用户
// 暂停；用户暂停只经 SetUserPaused 留痕，且系统侧的 ResumeAutoPause 不可触
// 碰——这是空闲开轮桥「stale 会话唤醒」与「用户持有不越」的分界线。

func TestAutomaticPauseStaysWakeable(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "leftover"}}); err != nil {
		t.Fatal(err)
	}
	// 重开遗留路径：PauseIfPending 是自动暂停。
	if err := s.PauseIfPending(); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if !snap.Paused || snap.UserPaused {
		t.Fatalf("automatic pause must not be marked user-held: %+v", snap)
	}
	// 恢复路径：SetPaused(true) 同样是自动暂停。
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); !snap.Paused || snap.UserPaused {
		t.Fatalf("recovery pause must not be marked user-held: %+v", snap)
	}
	resumed, err := s.ResumeAutoPause()
	if err != nil || !resumed {
		t.Fatalf("ResumeAutoPause must clear an automatic pause, got %v, %v", resumed, err)
	}
	if snap := s.Snapshot(); snap.Paused || snap.UserPaused {
		t.Fatalf("automatic pause must be gone after resume: %+v", snap)
	}
	// 幂等：没有自动暂停时再调是 no-op。
	if resumed, err := s.ResumeAutoPause(); resumed || err != nil {
		t.Fatalf("second ResumeAutoPause must be a no-op, got %v, %v", resumed, err)
	}
}

func TestUserPauseIsUntouchableByAutoResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "work"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserPaused(true); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if !snap.Paused || !snap.UserPaused {
		t.Fatalf("user pause must carry provenance: %+v", snap)
	}
	resumed, err := s.ResumeAutoPause()
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("ResumeAutoPause must never clear a user-held pause")
	}
	if snap := s.Snapshot(); !snap.Paused || !snap.UserPaused {
		t.Fatalf("user pause must survive the auto-resume attempt: %+v", snap)
	}
	// 用户自己恢复：Paused 与来源位一并清除。
	if err := s.SetUserPaused(false); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.Paused || snap.UserPaused {
		t.Fatalf("user resume must clear the pause and its provenance: %+v", snap)
	}
}

func TestUserPauseWinsRaceAgainstAutoResume(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "work"}}); err != nil {
		t.Fatal(err)
	}
	// 自动暂停在先，用户随后在桥的快照与唤醒之间按下暂停：
	// ResumeAutoPause 必须原样返回，不得覆盖用户意志。
	if err := s.PauseIfPending(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserPaused(true); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeAutoPause()
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("a pause the user took over must not be auto-resumed")
	}
	if snap := s.Snapshot(); !snap.Paused || !snap.UserPaused {
		t.Fatalf("user takeover must hold: %+v", snap)
	}
}

func TestClearPauseIfEmptyClearsProvenance(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "solo"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserPaused(true); err != nil {
		t.Fatal(err)
	}
	// 唯一项被取消清场：暂停与来源位一并消失（与既有 clearPauseIfEmpty 语义一致）。
	if err := s.DeletePendingOrAcceptedItem(rec.ItemID); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.Paused || snap.UserPaused {
		t.Fatalf("empty inbox must not stay paused: %+v", snap)
	}
}

func TestOldManifestWithoutProvenanceDecodesAutomatic(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.jsonl"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "legacy"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if !snap.Paused || snap.UserPaused {
		t.Fatalf("pre-709 paused manifest (no userPaused field) must decode as automatic: %+v", snap)
	}
	resumed, err := s.ResumeAutoPause()
	if err != nil || !resumed {
		t.Fatalf("legacy pause must stay wakeable, got %v, %v", resumed, err)
	}
}

// ErrPaused 语义回归：桥侧把 ErrPaused 归为竞态类的前提是它确实可 errors.Is。
func TestErrPausedIsComparable(t *testing.T) {
	if !errors.Is(ErrPaused, ErrPaused) {
		t.Fatal("ErrPaused must be usable with errors.Is")
	}
}
