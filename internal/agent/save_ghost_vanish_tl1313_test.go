package agent

// tl1313 regression pins: the snapshot no-op fast path used to re-verify only
// the event log and the CAS ledger, so a transcript checkpoint removed out of
// band left saves reporting "save-verified" forever (the 20261031 ghost-save
// incident: desktop.log carried noop=save-verified saves while canonical
// probes logged the file missing). Three pins:
//  1. a baseline-known transcript that vanished must refuse the no-op and be
//     recreated by the next save (the ghost itself);
//  2. a checkpoint-less baseline (schema-2 events-authoritative shape) must
//     keep the no-op standing — the fix may not tax legitimate absence;
//  3. the vanish WARN fires exactly once per disappearance episode and
//     re-arms after the file is seen again.

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// seedVanishSession saves two messages to a fresh schema-1 session so the
// baseline records checkpointBytes>0 and logTail>0.
func seedVanishSession(t *testing.T, path string) *Session {
	t.Helper()
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := s.Save(path); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("seed save must publish the transcript: stat err=%v", err)
	}
	if info, err := os.Stat(store.SessionEventLog(path)); err != nil || info.Size() == 0 {
		t.Fatalf("seed save must publish the event log: stat err=%v", err)
	}
	return s
}

func captureVanishLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestSnapshotRefusedWhenTranscriptVanished is the data-integrity pin for the
// tl1313 incident class: with the event log and ledger intact but the
// transcript checkpoint deleted out of band, the next save must put the
// transcript back on disk (WAL heal) — a vanished checkpoint must never stay
// vanished while its writer lives.
func TestSnapshotRefusedWhenTranscriptVanished(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ghost.jsonl")
	s := seedVanishSession(t, path)
	if reason := s.snapshotNoOpReason(path); reason == "" {
		t.Fatalf("precondition: intact baseline must arm the no-op")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}
	// 版本未动（模拟 20261031 现场：写者内存态与基线一致，唯一变化是盘上
	// 文件被外部删除）——修复前该保存会命中 no-op 直接跳过（ghost-save）。
	if err := s.Save(path); err != nil {
		t.Fatalf("save after vanish: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("transcript must be recreated by the save after vanish (pre-fix this save was a ghost no-op): %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("recreated transcript is empty")
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload after recreate: %v", err)
	}
	if len(reloaded.Messages) != 2 || reloaded.Messages[0].Content != "hello" {
		t.Fatalf("recreated transcript lost history: %d messages", len(reloaded.Messages))
	}
}

// TestSnapshotSkipStandsForCheckpointlessBaseline pins the no-over-defense
// side: when the baseline itself recorded no checkpoint (checkpointBytes==0 —
// the schema-2 events-authoritative shape whose .jsonl never materializes),
// transcript absence stays legitimate and the no-op must keep standing.
func TestSnapshotSkipStandsForCheckpointlessBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema2shape.jsonl")
	s := seedVanishSession(t, path)

	// 重录基线为「无检查点」形态：删掉转录后按生产记录函数重录基线
	// （checkpointBytes=0，events 与台账仍在）。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load branch meta after seed: ok=%v err=%v", ok, err)
	}
	digest, err := digestSessionMessages(s.Messages)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	s.setPersistedBaseline(path, digest, s.version, meta.Revision, true, true, 0, s.Messages)
	if reason := s.snapshotNoOpReason(path); reason != "save-verified" {
		t.Fatalf("checkpoint-less baseline must keep the no-op standing, got reason=%q", reason)
	}

	// 对照组：基线记录了检查点（>0）而盘上缺失 ⇒ 门必须拒绝（正是修的洞）。
	s2 := seedVanishSession(t, filepath.Join(dir, "ghost2.jsonl"))
	if err := os.Remove(filepath.Join(dir, "ghost2.jsonl")); err != nil {
		t.Fatalf("remove transcript 2: %v", err)
	}
	if reason := s2.snapshotNoOpReason(filepath.Join(dir, "ghost2.jsonl")); reason != "" {
		t.Fatalf("vanished baseline-known transcript must refuse the no-op, got reason=%q", reason)
	}
}

// TestVanishWarnOncePerEpisode pins the alarm semantics: one WARN per
// disappearance episode, armed on the first save that sees the file gone and
// cleared by the first save that sees it back (heal, recreation, restore —
// regardless of who brought it). A continuing remover must not recreate the
// 847-lines-per-day WARN storm class (task 710). The both-files-gone shape
// also pins the fail-closed contract: the save surfaces
// ErrSessionExternallyRemoved instead of writing into the void.
func TestVanishWarnOncePerEpisode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "episodes.jsonl")
	s := seedVanishSession(t, path)
	buf := captureVanishLogs(t)

	// Episode 1: transcript + event log both vanish out of band → 下一笔保存
	// 告警各一次，且不再「成功」——外部删除必须走 ErrSessionExternallyRemoved
	// 的恢复流，而不是 ghost-save。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}
	if err := os.Remove(store.SessionEventLog(path)); err != nil {
		t.Fatalf("remove event log: %v", err)
	}
	err := s.Save(path)
	if !errors.Is(err, ErrSessionExternallyRemoved) {
		t.Fatalf("both files removed: save must fail closed with ErrSessionExternallyRemoved, got %v", err)
	}
	if got := strings.Count(buf.String(), "transcript vanished on disk"); got != 1 {
		t.Fatalf("transcript vanish WARN count = %d, want 1; log=%s", got, buf.String())
	}
	if got := strings.Count(buf.String(), "event log vanished on disk"); got != 1 {
		t.Fatalf("event log vanish WARN count = %d, want 1; log=%s", got, buf.String())
	}

	// 同一 episode 内的后续保存不重复告警（闩未清：保存仍在失败）。
	err = s.Save(path)
	if !errors.Is(err, ErrSessionExternallyRemoved) {
		t.Fatalf("still-removed session must keep failing closed, got %v", err)
	}
	if got := strings.Count(buf.String(), "transcript vanished on disk"); got != 1 {
		t.Fatalf("transcript WARN must not repeat within the episode, count = %d", got)
	}
	if got := strings.Count(buf.String(), "event log vanished on disk"); got != 1 {
		t.Fatalf("event log WARN must not repeat within the episode, count = %d", got)
	}

	// 恢复：盘上内容回来了（生产里是 recovery fork / 用户找回），下一笔成功
	// 保存重录基线并清闩。
	reseed := seedVanishSession(t, path)
	_ = reseed
	if err := s.Save(path); err != nil {
		t.Fatalf("save after disk restored: %v", err)
	}
	t.Logf("DEBUG persisted after restore: checkpointBytes=%d logTail=%d revision=%d armed=%s",
		s.persisted.checkpointBytes, s.persisted.logTail, s.persisted.revision, s.snapshotNoOpReason(path))
	if got := strings.Count(buf.String(), "transcript vanished on disk"); got != 1 {
		t.Fatalf("restore must not warn, count = %d", got)
	}

	// Episode 2: 再次删除（只删转录）→ 重新告警（闩在「文件回来了」的目击时
	// 已清），heal 随后从 WAL 无声重建。闩在边沿检测语义下保持武装，直到下一
	// 笔保存目击文件在场才静默清掉。
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove transcript (episode 2): %v", err)
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("save after second vanish: %v", err)
	}
	if got := strings.Count(buf.String(), "transcript vanished on disk"); got != 2 {
		t.Fatalf("transcript vanish WARN count after re-removal = %d, want 2 (episode re-armed); log=%s", got, buf.String())
	}
	// 文件已被 heal 重建：下一笔保存目击在场 → 静默清闩，无第三条 WARN。
	if err := s.Save(path); err != nil {
		t.Fatalf("save after heal: %v", err)
	}
	if got := strings.Count(buf.String(), "transcript vanished on disk"); got != 2 {
		t.Fatalf("healed-back sighting must not warn, count = %d", got)
	}
}

// TestNoGhostSkipOnIntactBaseline pins the guard against over-defense on the
// healthy path: an intact session keeps the no-op armed after the change
// (#6607 fast path preserved).
func TestNoGhostSkipOnIntactBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "healthy.jsonl")
	s := seedVanishSession(t, path)
	if reason := s.snapshotNoOpReason(path); reason != "save-verified" {
		t.Fatalf("intact baseline must keep save-verified, got %q", reason)
	}
	if !s.snapshotUpToDate(path) {
		t.Fatal("intact session must stay up-to-date for the snapshot skip")
	}
}
