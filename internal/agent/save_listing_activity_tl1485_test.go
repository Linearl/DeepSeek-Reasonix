package agent

// tl1485 regression pins: the sidebar's relative time comes exclusively from
// the branch sidecar's UpdatedAt (agent listing → sessioncatalog reconcile →
// catalog_topics.last_activity_at), but the only writer that ever stamped it
// as turn activity (Controller.SnapshotActivity, markActivity=true) has zero
// production callers — every commit publishes its listing through
// persistSessionListingProjection with touch=false, so UpdatedAt froze at
// whatever meta-only writer stamped last. Sessions receiving messages without
// a stamped sidecar (collab delivery, detached/serve writes, cross-session
// appends) displayed "1个月" for days-old messages (2026-11-07 用户截图:
// 人类专属调研员2/调研员1 同族 collab 会话,盘上最后消息 11-05、UpdatedAt 停在
// 10-31)。修复=提交漏斗自己发布转录的最后消息时间;修复回放(解码转录)盖同
// 一真相,存量冻结行在 repair 时自愈。四枚钉(自包含:基线可编译,前两枚基线红):
//  1. 内容提交把冻结的 UpdatedAt 抬到本次最后消息时间(修前红);
//  2. 时间戳只升不降(rewind/移植旧消息不得倒拨,与下游 keep-max 一致);
//  3. listing repair 回放对存量会话盖同一消息级时间(修前红);
//  4. 全无时间戳的 legacy 转录保留边车现值(缺证据不乱盖)。

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// TestContentCommitRaisesFrozenUpdatedAtToLastMessageTL1485 is the incident
// pin: a sidecar frozen by past meta-only stamps must be raised to the
// transcript's own last-message time by the next content commit.
func TestContentCommitRaisesFrozenUpdatedAtToLastMessageTL1485(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.jsonl")
	now := time.Now()
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello", CreatedAt: now.Add(-45 * 24 * time.Hour).UnixMilli()})
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	// 模拟现场冻结边车:meta-only 写入者把 UpdatedAt 盖在 30 天前,后续提交
	// 一律 touch=false 原样保留(tl1485 的病灶)。
	frozen := now.Add(-30 * 24 * time.Hour)
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{ID: BranchID(path), UpdatedAt: frozen}); err != nil {
		t.Fatalf("freeze sidecar: %v", err)
	}
	msgAt := now.Add(-2 * 24 * time.Hour)
	s.Add(provider.Message{Role: provider.RoleUser, Content: "fresh message", CreatedAt: msgAt.UnixMilli()})
	if err := s.Save(path); err != nil {
		t.Fatalf("commit save: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if got := meta.UpdatedAt.UnixMilli(); got < msgAt.Add(-2*time.Hour).UnixMilli() || got > now.Add(2*time.Hour).UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the committed last-message time (~%v); the frozen value %v leaked through the commit funnel", meta.UpdatedAt, msgAt, frozen)
	}
}

// TestContentCommitNeverLowersUpdatedAtTL1485 pins the monotonicity side: a
// commit replaying older messages (rewind, recovery graft) must not drag the
// displayed activity backward — the downstream catalog keeps max anyway.
func TestContentCommitNeverLowersUpdatedAtTL1485(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rewind.jsonl")
	now := time.Now()
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "old", CreatedAt: now.Add(-25 * 24 * time.Hour).UnixMilli()})
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	fresh := now.Add(-1 * time.Hour)
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{ID: BranchID(path), UpdatedAt: fresh}); err != nil {
		t.Fatalf("set sidecar: %v", err)
	}
	// rewind 形态:重放的历史比现存 UpdatedAt 更旧。
	s.Add(provider.Message{Role: provider.RoleUser, Content: "replayed older message", CreatedAt: now.Add(-20 * 24 * time.Hour).UnixMilli()})
	if err := s.Save(path); err != nil {
		t.Fatalf("commit save: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if got := meta.UpdatedAt.UnixMilli(); got < fresh.Add(-time.Minute).UnixMilli() {
		t.Fatalf("UpdatedAt = %v moved backward below %v on an older-message commit", meta.UpdatedAt, fresh)
	}
}

// TestContentCommitWithoutTimestampsPreservesUpdatedAtTL1485 pins the fallback
// side: a transcript whose messages carry no CreatedAt (legacy replay) must
// keep whatever the sidecar holds — no evidence, no stamp.
func TestContentCommitWithoutTimestampsPreservesUpdatedAtTL1485(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.jsonl")
	now := time.Now()
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "legacy message without timestamps"})
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	stamped := now.Add(-10 * 24 * time.Hour)
	if err := SaveBranchMetaPreserveUpdated(path, BranchMeta{ID: BranchID(path), UpdatedAt: stamped}); err != nil {
		t.Fatalf("set sidecar: %v", err)
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "another legacy message"})
	if err := s.Save(path); err != nil {
		t.Fatalf("commit save: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: ok=%v err=%v", ok, err)
	}
	if got := meta.UpdatedAt.UnixMilli(); got < stamped.Add(-time.Minute).UnixMilli() || got > stamped.Add(time.Minute).UnixMilli() {
		t.Fatalf("UpdatedAt = %v, want the preserved sidecar value %v — a timestamp-less commit must not stamp", meta.UpdatedAt, stamped)
	}
}

// TestListingRepairReplayStampsUpdatedAtTL1485 is the stock-heal pin: the
// repair worker decodes the transcript anyway, so existing frozen rows heal
// with the same message-derived truth instead of waiting for their next use.
func TestListingRepairReplayStampsUpdatedAtTL1485(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	msgAt := time.Now().Add(-2 * 24 * time.Hour)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "question", CreatedAt: msgAt.Add(-time.Hour).UnixMilli()},
		{Role: provider.RoleAssistant, Content: "answer", CreatedAt: msgAt.UnixMilli()},
	}
	writeSessionFile(t, path, msgs)
	before, err := sessionRepairContentFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, state, repairable, err := loadSessionDisplayMessagesContextUnlocked(context.Background(), path)
	if err != nil || !repairable {
		t.Fatalf("decode repairable=%v err=%v", repairable, err)
	}
	frozen := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := commitSessionListingReplay(context.Background(), path, BranchMeta{ID: BranchID(path), UpdatedAt: frozen}, before, decoded, state, SessionListingRepairResult{}); err != nil {
		t.Fatalf("repair commit: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta after repair: ok=%v err=%v", ok, err)
	}
	if got := meta.UpdatedAt.UnixMilli(); got < msgAt.Add(-2*time.Hour).UnixMilli() || got > time.Now().Add(2*time.Hour).UnixMilli() {
		t.Fatalf("after repair UpdatedAt = %v, want the transcript's last-message time (~%v); frozen %v survived the replay", meta.UpdatedAt, msgAt, frozen)
	}
}
