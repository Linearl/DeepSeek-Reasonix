package main

// tl1383: the archive path for residue forms.
//  - A target whose transcript file is gone (the tl1363 zombie-entry family:
//    a binding to a lost or already-trashed session) must not be refused —
//    nothing exists to protect or move; the leftover lease record is torn and
//    the topic archives straight through.
//  - The force sweep must act on the verdict=None form (absent/unreadable
//    holder record — the 2026-11-04 holder_class=unknown pid=0 refusals) via
//    the free-lock-arbitrated primitives, never on a live holder.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

func TestSessionTrashTargetFileMissingBranches(t *testing.T) {
	if sessionTrashTargetFileMissing("") {
		t.Fatal("empty path is never a missing-file target")
	}
	present := filepath.Join(t.TempDir(), "present.jsonl")
	if err := os.WriteFile(present, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if sessionTrashTargetFileMissing(present) {
		t.Fatal("existing file must not read as missing")
	}
	if !sessionTrashTargetFileMissing(filepath.Join(t.TempDir(), "absent.jsonl")) {
		t.Fatal("absent file must read as missing")
	}
}

// A missing-file target records a nil guard (nothing to acquire) and tears the
// leftover lease record through the free-lock-arbitrated primitive — a corrupt
// identity record beside a vanished transcript is pure residue.
func TestAcquireTopicArchiveOwnershipSkipsMissingFileTarget(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(dir, "tl1383-missing-target.jsonl")
	// Residue: an unreadable identity record beside the vanished transcript.
	if err := os.WriteFile(sessionPath+".lease.json", []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt lease record: %v", err)
	}
	targets := []topicTrashTarget{{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)}}
	if !sessionTrashTargetFileMissing(sessionPath) {
		t.Fatal("precondition: the target transcript must be missing")
	}

	batch, err := acquireTopicArchiveOwnership(targets, nil)
	if err != nil {
		t.Fatalf("ownership for a missing-file target = %v, want nil guard acquisition", err)
	}
	if guard := batch.take(sessionPath); guard != nil {
		t.Fatal("a missing-file target must record a nil guard, not acquire locks")
	}
	if _, err := os.Stat(sessionPath + ".lease.json"); !os.IsNotExist(err) {
		t.Fatalf("leftover lease record survived the missing-file archive path: %v", err)
	}
}

// The force sweep's None verdict: an absent/unreadable holder record is torn
// through ClearStaleSessionLeaseInfo (free-lock-only) and the zombie sweep
// runs; a live holder of the lock file survives both.
func TestReclaimTopicStaleLeaseHoldersNoneVerdict(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(dir, "tl1383-none-verdict.jsonl")
	if err := os.WriteFile(sessionPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(sessionPath+".lease.json", []byte(""), 0o600); err != nil {
		t.Fatalf("write corrupt lease record: %v", err)
	}
	app := &App{}
	app.reclaimTopicStaleLeaseHolders([]topicTrashTarget{{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)}})
	if _, err := os.Stat(sessionPath + ".lease.json"); !os.IsNotExist(err) {
		t.Fatalf("unreadable holder record survived the None-verdict sweep: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("the sweep must never touch the transcript: %v", err)
	}

	// A live holder keeps its lock and its record: the sweep no-ops.
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("stage live lease: %v", err)
	}
	defer lease.Release()
	if err := os.WriteFile(sessionPath+".lease.json", []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt record beside live lease: %v", err)
	}
	app.reclaimTopicStaleLeaseHolders([]topicTrashTarget{{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)}})
	if _, err := os.Stat(sessionPath + ".lease.json"); err != nil {
		t.Fatalf("the sweep must not tear a record beside a live lock: %v", err)
	}
	if !agent.SessionLeaseHeldByCurrentRuntime(sessionPath) {
		t.Fatal("the live lease lost its ownership during a None-verdict sweep")
	}
}

// 验收①：带 unreadable-holder 残留记录的强制归档一次成功——sweep 撕残留、
// guard 直取、会话入回收站、topic 注册清掉（侧栏条目消失）。
func TestTrashTopicForceOneCallWithUnreadableHolderRecord(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	topicID := "topic_tl1383_unreadable"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Unreadable holder"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, dir, "tl1383-unreadable.jsonl", topicID, "Unreadable holder", projectRoot, "keep me", time.Now())
	if err := os.WriteFile(sessionPath+".lease.json", []byte("torn-by-crash"), 0o600); err != nil {
		t.Fatalf("write unreadable lease record: %v", err)
	}

	if err := NewApp().TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce with an unreadable holder record = %v, want one-call success", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("archived session still on disk: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared (sidebar entry gone)", got)
	}
	if _, err := os.Stat(sessionPath + ".lease.json"); !os.IsNotExist(err) {
		t.Fatalf("unreadable holder record survived the archive: %v", err)
	}
}
