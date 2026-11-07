package main

// tl1367 line 4 acceptance: force archive must actually unlock stale lease
// holders instead of merely skipping the UI confirm. The stale forms (a dead
// holder pid, a pid reused by an unrelated program, this window's own zombie
// runtime) must complete the cleanup in ONE TrashTopicForce call; a live
// holder running this application is real occupation and must never be
// grabbed silently (refusal keeps the holder identity, tl1367 line 1).

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/store"

	"golang.org/x/sys/windows"
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

// corruptLeaseLockContent overwrites the lease lock file's owner-identity
// region in place (identity probes read corrupt) while every handle and the
// byte-range lock stay exactly where they are. It must go through CreateFile
// with the full share mask: Go's os.OpenFile requests a share mode without
// DELETE, which collides with the lock handle's DELETE access and fails with
// a sharing violation — the same file the identity reader opens fine. The
// write starts at offset 1 (sessionLeaseOwnerOffset, where the identity
// reader begins): the holder's LockFileEx pins byte 0, and a write there
// fails with ERROR_LOCK_VIOLATION.
func corruptLeaseLockContent(t *testing.T, lockPath string) {
	t.Helper()
	pathp, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		t.Fatalf("lock path utf16: %v", err)
	}
	handle, err := windows.CreateFile(pathp,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open lease lock for corrupting: %v", err)
	}
	f := os.NewFile(uintptr(handle), lockPath)
	if _, err := f.WriteAt([]byte("{torn-garbage"), 1); err != nil {
		f.Close()
		t.Fatalf("corrupt lease lock content: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close lease lock: %v", err)
	}
}

// spawnForeignLeaseHolder starts a real second process holding the session's
// OS lease lock (the topic-archive foreign-lease helper), so the refusals
// below are arbitrated by a genuine cross-process lock instead of a mock.
// The returned func waits for the helper to exit after releasing it.
func spawnForeignLeaseHolder(t *testing.T, sessionPath string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTrashTopicForeignLeaseHelper$")
	cmd.Env = append(os.Environ(),
		"REASONIX_TOPIC_ARCHIVE_LEASE_HELPER=1",
		"REASONIX_TOPIC_ARCHIVE_LEASE_PATH="+sessionPath,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("lease helper stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("lease helper stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lease helper: %v", err)
	}
	release := func() {
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("lease helper exit: %v (%s)", err, stderr.String())
		}
	}
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "ready\n" {
		stdin.Close()
		cmd.Wait()
		t.Fatalf("lease helper readiness = %q, err=%v (%s)", ready, err, stderr.String())
	}
	return release
}

// TestTrashTopicForceRefusesLiveForeignHolderWithoutGrabbing pins the line-4
// real-occupation contract: a live holder running this application is never
// grabbed by force. The force call must refuse with the busy family, leave
// the holder's lock and identity record untouched, and keep the session
// intact — while the regular archive succeeds once the holder walks away.
func TestTrashTopicForceRefusesLiveForeignHolderWithoutGrabbing(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_force_live_foreign"
	sessionPath := stageForceArchiveTopic(t, "force-live.jsonl", topicID, "Force live")
	key := sessionRuntimeKey(sessionPath)

	releaseHolder := spawnForeignLeaseHolder(t, sessionPath)
	info, err := agent.LoadSessionLeaseInfo(key)
	if err != nil || info == nil || info.PID <= 0 {
		t.Fatalf("holder record after spawn: %v (%v)", info, err)
	}
	holderPID := info.PID

	app := NewApp()
	forceErr := app.TrashTopicForce(topicID)
	if forceErr == nil {
		releaseHolder()
		t.Fatal("force archive must not succeed against a live foreign holder")
	}
	if !errors.Is(forceErr, errSessionBusyElsewhere) {
		t.Fatalf("force against a live holder = %v, want the live busy family", forceErr)
	}
	// Not grabbed: the holder's lock and identity record are untouched.
	if _, locked, err := agent.InspectSessionLease(key); err != nil || !locked {
		releaseHolder()
		t.Fatalf("holder lock disturbed by force: locked=%v err=%v", locked, err)
	}
	if got, err := agent.LoadSessionLeaseInfo(key); err != nil || got == nil || got.PID != holderPID {
		releaseHolder()
		t.Fatalf("holder identity record disturbed by force: %v (%v)", got, err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		releaseHolder()
		t.Fatalf("failed force removed the session: %v", err)
	}
	if agent.IsCleanupPending(sessionPath) {
		releaseHolder()
		t.Fatal("failed force published a cleanup-pending marker")
	}

	// The holder walks away; the regular archive then succeeds — nothing was
	// stolen or corrupted by the refused force call.
	releaseHolder()
	if err := app.TrashTopic(topicID); err != nil {
		t.Fatalf("regular archive after holder release: %v", err)
	}
}

// TestTrashTopicForceNeverStealsLiveLockBehindStaleVerdict pins the honest
// stale boundary: when the holder verdict reads stale (the injected probe
// models the torn-record shape — a live lock whose record can no longer be
// trusted) but the OS lock is REALLY held by a live process, the stale-record
// retirement must refuse (the clear primitive is verify-free-then-remove) and
// the force archive must fail cleanly instead of faking success — the holder's
// record, the live lock, and the session all survive; the regular archive
// succeeds after the holder leaves.
func TestTrashTopicForceNeverStealsLiveLockBehindStaleVerdict(t *testing.T) {
	isolateDesktopUserDirs(t)
	prevAlive := leaseReconcileProcessAlive
	prevDetailAlive := desktopLeaseAlive
	t.Cleanup(func() {
		leaseReconcileProcessAlive = prevAlive
		desktopLeaseAlive = prevDetailAlive
	})

	topicID := "topic_force_stale_verdict_live_lock"
	sessionPath := stageForceArchiveTopic(t, "force-stale-live.jsonl", topicID, "Force stale live")
	key := sessionRuntimeKey(sessionPath)

	releaseHolder := spawnForeignLeaseHolder(t, sessionPath)
	info, err := agent.LoadSessionLeaseInfo(key)
	if err != nil || info == nil || info.PID <= 0 {
		releaseHolder()
		t.Fatalf("holder record after spawn: %v (%v)", info, err)
	}
	holderPID := info.PID
	// The liveness seam now calls the live holder stale — the torn-record
	// shape: our verdict says "leftover", the lock says "someone alive".
	deadProbe := func(pid int) bool { return pid != holderPID }
	leaseReconcileProcessAlive = deadProbe
	desktopLeaseAlive = deadProbe

	app := NewApp()
	forceErr := app.TrashTopicForce(topicID)
	if forceErr == nil {
		releaseHolder()
		t.Fatal("force archive must not succeed while a live process holds the lock")
	}
	if !errors.Is(forceErr, errSessionBusyZombie) {
		t.Fatalf("force with a stale verdict on a live lock = %v, want the zombie family", forceErr)
	}
	// The stale-verdict retirement was NOT performed through a stolen lock,
	// and the lock is still held by the live holder.
	if got, err := agent.LoadSessionLeaseInfo(key); err != nil || got == nil || got.PID != holderPID {
		releaseHolder()
		t.Fatalf("holder record disturbed during force: %v (%v)", got, err)
	}
	if _, locked, err := agent.InspectSessionLease(key); err != nil || !locked {
		releaseHolder()
		t.Fatalf("live lock disturbed by force: locked=%v err=%v", locked, err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		releaseHolder()
		t.Fatalf("failed force removed the session: %v", err)
	}
	if agent.IsCleanupPending(sessionPath) {
		releaseHolder()
		t.Fatal("failed force published a cleanup-pending marker")
	}

	releaseHolder()
	if err := app.TrashTopic(topicID); err != nil {
		t.Fatalf("regular archive after holder release: %v", err)
	}
}


// forceCloseStoppableController is a running controller that actually stops on
// Cancel, so stop_and_close observes idle within the grace window and closes
// the tab WITHOUT detaching (the detach path returns early and keeps the
// lease alive — the shape force-archive must not rely on).
type forceCloseStoppableController struct {
	backgroundRuntimeController
}

func (c *forceCloseStoppableController) Cancel() {
	c.status.Running = false
	c.turnCancel++
}

// GoalStatus is reached by the archive teardown; the embedded stub's nil
// SessionAPI interface would panic here.
func (c *forceCloseStoppableController) GoalStatus() string { return control.GoalStatusStopped }

// tl1367-fix defect B acceptance ④: force-archiving a session whose tab is
// open and RUNNING in THIS window must close that tab (stop its run, save
// first, release its lease) as part of the same force click — bouncing the
// user back with "close that tab first" is exactly the dead-end guidance the
// 2026-11-04 00:10 incident exposed. After the close, the archive completes
// in ONE call.
func TestTrashTopicForceClosesOpenTopicTabAndArchives(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_force_open_tab"
	sessionPath := stageForceArchiveTopic(t, "force-open-tab.jsonl", topicID, "Force open tab")

	app := NewApp()
	ctrl := &forceCloseStoppableController{}
	ctrl.status = control.RuntimeStatus{Running: true, Cancellable: true}
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), topicID, "force-open-tab")
	tab.SessionPath = sessionPath
	tab.Ctrl = ctrl
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	if err := tab.ensureSessionLease(sessionPath); err != nil {
		t.Fatalf("tab lease bind: %v", err)
	}
	installNoopRuntimeEvents(app, tab.sink)
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = append(app.tabOrder, tab.ID)
	app.mu.Unlock()
	t.Cleanup(func() { tab.releaseSessionLease() })

	// The regular archive is refused at the active-work gate: the session is
	// running in this window's tab.
	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicHasActiveWork) {
		t.Fatalf("regular TrashTopic against a running tab = %v, want errTopicHasActiveWork", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("refused archive removed the session: %v", err)
	}
	if ctrl.turnCancel != 0 {
		t.Fatal("the refused archive must not cancel the running turn")
	}

	// Force: the tab stops and closes (lease released with it), then the
	// archive completes — one force click, no manual tab closing.
	if err := app.TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce with the tab open and running: %v", err)
	}
	if ctrl.turnCancel == 0 {
		t.Fatal("force archive never stopped the tab's running turn")
	}
	app.mu.RLock()
	_, tabStillOpen := app.tabs[tab.ID]
	app.mu.RUnlock()
	if tabStillOpen {
		t.Fatal("force archive left the topic's tab open; the running tab must close as part of the force click")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("archived session still on disk: %v", err)
	}
}

// TestTrashTopicForceCompletesWithUnboundOrphanLeaseHolder is the tl1392
// package-73 incident form: a live lease object with NO tab binding whose disk
// record was torn away. Every pre-tl1392 lever misses it — the tab sweep
// matches holders through tabs, the zombie sweep requires a bound lease, and
// reclaim cannot win the OS lock the object still holds — so "tear the disk
// record" succeeded while the next acquire kept refusing, forever. The
// acceptance core: ONE force call completes the archive, no loop.
func TestTrashTopicForceCompletesWithUnboundOrphanLeaseHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_force_orphan_holder"
	sessionPath := stageForceArchiveTopic(t, "force-orphan.jsonl", topicID, "Force orphan")
	key := sessionRuntimeKey(sessionPath)

	// The orphan: acquired, registered in the agent live-lease registry, its
	// identity record torn from under it — the lock file's CONTENT is garbage
	// (torn publish, the tl1383 corrupt family) so every identity probe reads
	// "unidentifiable", while the object's OS lock handle stays very much
	// alive on the same, still-named lock file. The corrupt-record form is the
	// one the tl1383 heal cannot cross — reclaim keeps failing the lock take —
	// and it is exactly what the orphan sweep exists for. No App tab ever
	// references the object; it stays reachable only through the registry,
	// which is what the sweep consults.
	if _, err := agent.TryAcquireSessionLease(sessionPath); err != nil {
		t.Fatalf("orphan acquire: %v", err)
	}
	corruptLeaseLockContent(t, store.SessionLeaseLock(key))
	if _, err := agent.LoadSessionLeaseInfo(key); err == nil {
		t.Fatal("orphan identity record should be unreadable after the tear")
	}

	app := NewApp()
	// The regular archive either self-heals in one call (tl1383-1392: the heal
	// gate now crosses the torn-record form, so the orphan is swept and the
	// archive converges without escalating) or refuses with the lease-held
	// family — in which case ONE force call must complete the archive (the
	// package-73 loop's first half). Both roads end in the same assertions
	// below: transcript gone, lease state clean.
	if err := app.TrashTopic(topicID); err != nil {
		if !errors.Is(err, agent.ErrSessionLeaseHeld) {
			t.Fatalf("regular TrashTopic against an unbound orphan = %v, want the lease-held family", err)
		}
		if _, err := os.Stat(sessionPath); err != nil {
			t.Fatalf("refused archive removed the session: %v", err)
		}
	}

	// ONE force call (only reached when the regular archive refused): the
	// orphan sweep releases the registered object (its lock goes with it),
	// discards the entry, and the archive completes. No second click, no
	// restart, no loop.
	if err := app.TrashTopicForce(topicID); err != nil {
		t.Fatalf("TrashTopicForce against an unbound orphan lease: %v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("archive left the session in place: %v", err)
	}
	// If any orphan state survived (owner entry or lock handle), this very
	// reacquire would bounce off it — the loop's exact shape. Success here is
	// the whole acceptance.
	reattach, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("reacquire after force = %v, want a clean lease state", err)
	}
	reattach.Release()
}
