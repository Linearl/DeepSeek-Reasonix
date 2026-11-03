package main

// tl1367 line 4 acceptance: force archive must actually unlock stale lease
// holders instead of merely skipping the UI confirm. The stale forms (a dead
// holder pid, a pid reused by an unrelated program, this window's own zombie
// runtime) must complete the cleanup in ONE TrashTopicForce call; a live
// holder running this application is real occupation and must never be
// grabbed silently (refusal keeps the holder identity, tl1367 line 1).

import (
	"errors"
	"os"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// stageForceArchiveTopic stages the historical force shape: a topic with one
// on-disk session and no runtime tabs (a session that failed to restore).
func stageForceArchiveTopic(t *testing.T, name, topicID, title string) string {
	t.Helper()
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, title); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	return writeTopicSessionWithPrompt(t, dir, name, topicID, title, projectRoot, "preserve me", time.Now())
}

// TestTrashTopicForceUnlocksInProcessZombieHolder is the line-4 core gap: a
// zombie runtime (bound the lease, never published a controller, no build in
// flight) whose tab carries a different TopicID — the form
// reclaimTopicSelfLeasesLocked cannot see — must not wedge the force archive.
// The regular archive refuses (busy, forceable); the force call reclaims the
// provable zombie and completes the cleanup in ONE call.
func TestTrashTopicForceUnlocksInProcessZombieHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_force_zombie_holder"
	sessionPath := stageForceArchiveTopic(t, "force-zombie.jsonl", topicID, "Force zombie")
	key := sessionRuntimeKey(sessionPath)

	app := NewApp()
	zombie := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "force-zombie")
	zombie.SessionPath = sessionPath
	zombie.sink = &tabEventSink{tabID: zombie.ID, app: app}
	if err := zombie.ensureSessionLease(sessionPath); err != nil {
		t.Fatalf("zombie lease bind: %v", err)
	}
	installNoopRuntimeEvents(app, zombie.sink)
	app.mu.Lock()
	app.tabs[zombie.ID] = zombie
	app.tabOrder = append(app.tabOrder, zombie.ID)
	app.mu.Unlock()
	t.Cleanup(func() { zombie.releaseSessionLease() })

	// The regular archive is refused by the in-process holder (the pre-line-4
	// shape the force path exists for).
	if err := app.TrashTopic(topicID); !errors.Is(err, errSessionBusyElsewhere) {
		t.Fatalf("regular TrashTopic against a zombie holder = %v, want busy", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("refused archive removed the session: %v", err)
	}

	// Force: unlock the provable zombie and finish in one call.
	if err := app.TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce against a zombie holder: %v", err)
	}
	if zombie.sessionLeaseRuntimeKey() != "" {
		t.Fatal("zombie still holds the lease after the force archive")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("force archive left the session in place: %v", err)
	}
	if _, err := agent.LoadSessionLeaseInfo(key); !os.IsNotExist(err) {
		t.Fatalf("lease identity survived the force archive: %v", err)
	}
}

// TestTrashTopicForceCompletesWithDeadPidLeaseRecord is line-4 acceptance ④
// (pid-dead form): a leftover record naming a dead holder must not stop the
// force archive, and the stale identity must be gone together with the
// session when the call returns.
func TestTrashTopicForceCompletesWithDeadPidLeaseRecord(t *testing.T) {
	isolateDesktopUserDirs(t)
	prevAlive := leaseReconcileProcessAlive
	t.Cleanup(func() { leaseReconcileProcessAlive = prevAlive })
	deadPID := 1367100
	leaseReconcileProcessAlive = func(pid int) bool { return pid != deadPID }

	topicID := "topic_force_dead_record"
	sessionPath := stageForceArchiveTopic(t, "force-dead.jsonl", topicID, "Force dead")
	key := sessionRuntimeKey(sessionPath)
	if err := agent.SaveSessionLeaseInfo(sessionPath, agent.SessionLeaseInfo{
		SessionPath: key, WriterID: "writer-crashed", PID: deadPID,
		Hostname: "crash-leftover", AcquiredAt: time.Now().Add(-3 * time.Hour).UTC(),
	}); err != nil {
		t.Fatalf("stage dead-holder record: %v", err)
	}

	app := NewApp()
	if err := app.TrashTopicForce(topicID); err != nil {
		t.Fatalf("force archive under a dead-holder record: %v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("force archive left the session in place: %v", err)
	}
	if _, err := agent.LoadSessionLeaseInfo(key); !os.IsNotExist(err) {
		t.Fatalf("dead-holder record survived the force archive: %v", err)
	}
}

// TestTrashTopicForceCompletesWithReusedPidLeaseRecord is line-4 acceptance ④
// (pid-reused form, the 2026-11-03 incident shape): a record whose pid is
// alive but belongs to an unrelated program is stale per the line-3 verdict;
// force must complete the cleanup in one call and retire the record.
func TestTrashTopicForceCompletesWithReusedPidLeaseRecord(t *testing.T) {
	isolateDesktopUserDirs(t)
	prevAlive := leaseReconcileProcessAlive
	t.Cleanup(func() { leaseReconcileProcessAlive = prevAlive })
	withHolderImageForReconcileTest(t, func(pid int) (string, string) {
		if pid == 1367200 {
			return "indexer-pro.exe", `C:\tools\indexer-pro.exe`
		}
		return "", ""
	})
	reusedPID := 1367200
	leaseReconcileProcessAlive = func(pid int) bool { return pid == reusedPID }

	topicID := "topic_force_reused_record"
	sessionPath := stageForceArchiveTopic(t, "force-reused.jsonl", topicID, "Force reused")
	key := sessionRuntimeKey(sessionPath)
	if err := agent.SaveSessionLeaseInfo(sessionPath, agent.SessionLeaseInfo{
		SessionPath: key, WriterID: "writer-reused", PID: reusedPID,
		Hostname: "reused-leftover", AcquiredAt: time.Now().Add(-2 * time.Hour).UTC(),
	}); err != nil {
		t.Fatalf("stage reused-holder record: %v", err)
	}

	app := NewApp()
	if err := app.TrashTopicForce(topicID); err != nil {
		t.Fatalf("force archive under a reused-pid record: %v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("force archive left the session in place: %v", err)
	}
	if _, err := agent.LoadSessionLeaseInfo(key); !os.IsNotExist(err) {
		t.Fatalf("reused-pid record survived the force archive: %v", err)
	}
}
