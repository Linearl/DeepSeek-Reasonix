package main

// tl1383: the removal-guard heal. The 2026-11-04 08:23/09:08 archive refusals
// repeated forever with holder_class=unknown holder_pid=0 — a lease refusal
// with NO holder identity left the archive path with no lever: regular archive
// refused, force-archive had no verdict to act on, and the copy misdirected
// the user to a window that did not exist. The heal reclaims the residue form
// through the OS-lock-arbitrated primitive; these tests pin the gate and the
// orchestration, and the contract that a live holder is never grabbed.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

func TestSessionLeaseHolderIdentityMissingBranches(t *testing.T) {
	if !sessionLeaseHolderIdentityMissing(&agent.SessionLeaseError{Path: "p", Info: nil}) {
		t.Fatal("nil info must read as identity-missing")
	}
	if !sessionLeaseHolderIdentityMissing(&agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 0}}) {
		t.Fatal("pid-0 info must read as identity-missing")
	}
	if sessionLeaseHolderIdentityMissing(&agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 42544}}) {
		t.Fatal("named pid must NOT read as identity-missing")
	}
	if sessionLeaseHolderIdentityMissing(errors.New("unrelated failure")) {
		t.Fatal("non-lease errors are never identity-missing")
	}
	if sessionLeaseHolderIdentityMissing(nil) {
		t.Fatal("nil error is never identity-missing")
	}
}

func TestSessionLeaseHealCandidateBranches(t *testing.T) {
	selfPID := os.Getpid()
	// Identity-missing refusals are always heal candidates (the lock take
	// inside the reclaim stays the real arbiter).
	if !sessionLeaseHealCandidate("p", &agent.SessionLeaseError{Path: "p"}) {
		t.Fatal("identity-missing refusal must be a heal candidate")
	}
	// A refusal naming a live foreign holder is never a candidate: its identity
	// is present, so the 1367-fix contract copy stays authoritative.
	if sessionLeaseHealCandidate("p", &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: selfPID + 1}}) {
		t.Fatal("foreign-pid refusal must not be a heal candidate")
	}
	// A self-named refusal whose identity record still EXISTS is not a
	// candidate: a live self holder keeps its record, and the self copy is the
	// correct guidance.
	liveSelfDir := t.TempDir()
	liveSelfPath := filepath.Join(liveSelfDir, "live-self.jsonl")
	if err := agent.SaveSessionLeaseInfo(liveSelfPath, agent.SessionLeaseInfo{
		SessionPath: liveSelfPath,
		PID:         selfPID,
		WriterID:    "writer-test",
	}); err != nil {
		t.Fatalf("stage live self record: %v", err)
	}
	if sessionLeaseHealCandidate(liveSelfPath, &agent.SessionLeaseError{Path: liveSelfPath, Info: &agent.SessionLeaseInfo{PID: selfPID}}) {
		t.Fatal("self refusal with a present record must not be a heal candidate")
	}
	// The incident form: self-named refusal with the identity record GONE —
	// live holders keep the record present, so this is an orphaned entry.
	orphanDir := t.TempDir()
	orphanPath := filepath.Join(orphanDir, "orphan.jsonl")
	if _, err := agent.LoadSessionLeaseInfo(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: orphan path must have no record, got %v", err)
	}
	if !sessionLeaseHealCandidate(orphanPath, &agent.SessionLeaseError{Path: orphanPath, Info: &agent.SessionLeaseInfo{PID: selfPID}}) {
		t.Fatal("self refusal with a missing record must be a heal candidate")
	}
}

// The heal only widens what can SUCCEED. With the save lock genuinely held (a
// real in-flight writer), the guard refusal survives the heal attempt intact:
// same lease error, still wrapped for the busy surfaces, still forceable.
func TestAcquireSessionRemovalGuardLiveSaveLockSurvivesHeal(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(dir, "tl1383-live-save-lock.jsonl")
	if err := os.WriteFile(sessionPath, []byte("turn\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	unlock, err := agent.TryLockSessionFileForTest(sessionPath)
	if err != nil {
		t.Skipf("save lock seam unavailable on this platform: %v", err)
	}
	defer unlock()

	_, guardErr := acquireSessionRemovalGuard(sessionPath)
	if !errors.Is(guardErr, agent.ErrSessionLeaseHeld) {
		t.Fatalf("held save lock error = %v, want ErrSessionLeaseHeld family", guardErr)
	}
	var busyErr *sessionLeaseHeldBusyError
	if !errors.As(guardErr, &busyErr) {
		t.Fatalf("held save lock error = %T, want sessionLeaseHeldBusyError wrap", guardErr)
	}
	if !sessionLeaseHolderIdentityMissing(guardErr) {
		t.Fatal("held save lock refusal must read as identity-missing (Info nil)")
	}
	var forceable *topicArchiveForceableError
	if !errors.As(markTopicArchiveForceable(guardErr), &forceable) {
		t.Fatal("the identity-missing refusal family must stay forceable")
	}
}

// The adopt-and-convert branch: an identity-missing refusal on a path whose
// locks are free is healed into a working removal guard in ONE call, and the
// heal leaves no registry residue behind after the guard releases.
func TestHealUnidentifiedLeaseHolderAdoptsFreeLockResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(dir, "tl1383-heal-adopt.jsonl")
	if err := os.WriteFile(sessionPath, []byte("turn\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	// Stage the residue: a registry-orphan-style refusal error with no
	// identity record anywhere on disk.
	cause := &agent.SessionLeaseError{Path: sessionPath}
	guard, healErr := healUnidentifiedLeaseHolder(sessionPath, cause)
	if healErr != nil {
		t.Fatalf("heal on free residue = %v, want adopted guard", healErr)
	}
	if guard == nil {
		t.Fatal("heal returned no guard")
	}
	// The adopted guard owns the locks: the lease lock file exists with this
	// process's published identity while held.
	if _, err := os.Stat(store.SessionLeaseLock(sessionPath)); err != nil {
		t.Fatalf("healed guard must hold a published lease lock, stat err %v", err)
	}
	if err := guard.RemoveSidecarsAndRelease(); err != nil {
		t.Fatalf("guard release: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseLock(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("released guard leaves the lease lock behind: %v", err)
	}
}

// tl1383 验收文案面：身份缺失的拒绝渲染 leftover 文案，不再回退
// 「another window」；分类落到 errSessionBusyUnidentifiedHolder 且保持
// forceable 通道（强制归档是残留形态的两个正解之一）。
func TestUnidentifiedHolderBusyDetailAndForceable(t *testing.T) {
	err := &sessionLeaseHeldBusyError{cause: &agent.SessionLeaseError{Path: "p"}}
	if detail := err.Error(); detail != errSessionBusyUnidentifiedHolder.Error() {
		t.Fatalf("identity-missing detail = %q, want the leftover copy", detail)
	}
	if !errors.Is(err, errSessionBusyUnidentifiedHolder) {
		t.Fatal("identity-missing busy error must classify as errSessionBusyUnidentifiedHolder")
	}
	if errors.Is(err, errSessionBusyElsewhere) {
		t.Fatal("identity-missing busy error must not classify as the another-window sentinel")
	}
	var forceable *topicArchiveForceableError
	if !errors.As(markTopicArchiveForceable(err), &forceable) {
		t.Fatal("identity-missing refusal must stay forceable")
	}
	if got := sessionLeaseBusyDetail(&agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 0}}); got != errSessionBusyUnidentifiedHolder.Error() {
		t.Fatalf("pid-0 detail = %q, want the leftover copy", got)
	}
}

// A live (locked) residue is never grabbed: with the lease lock held by a
// still-open handle, the heal must fail and leave the refusal as-is.
func TestHealUnidentifiedLeaseHolderNeverGrabsLiveLock(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(dir, "tl1383-live-lease.jsonl")
	if err := os.WriteFile(sessionPath, []byte("turn\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("stage live lease: %v", err)
	}
	defer lease.Release()
	// Identity-missing synthetic cause (the corrupt-record form): the heal is
	// attempted but the live lock keeps the reclaim failing.
	cause := &agent.SessionLeaseError{Path: sessionPath}
	if _, healErr := healUnidentifiedLeaseHolder(sessionPath, cause); healErr == nil {
		t.Fatal("heal must not adopt a live holder's lease")
	}
	// The live lease is untouched and still held by this process.
	if !agent.SessionLeaseHeldByCurrentRuntime(sessionPath) {
		t.Fatal("live lease lost its active ownership after a refused heal")
	}
}

// TestLeaseBusyCopyThreeFormContract pins the tl1392 copy discipline across
// the three refusal shapes a user can land in:
//
//  1. the regular identity-missing refusal names force archive (the repair
//     that exists) and no longer promises the restart — restart only provably
//     clears the disk-residue shapes, which keep their own copy (zombie);
//  2. the zombie (disk-residue) copy keeps the restart recommendation;
//  3. the after-force copy never recommends force again (the user is already
//     inside the force flow — the package-73 self-referential loop) and names
//     the report-a-log path instead.
func TestLeaseBusyCopyThreeFormContract(t *testing.T) {
	regular := errSessionBusyUnidentifiedHolder.Error()
	if !strings.Contains(regular, "use force archive") {
		t.Fatalf("regular unidentified-holder copy lost the force repair: %s", regular)
	}
	if strings.Contains(regular, "restart Reasonix") {
		t.Fatalf("regular unidentified-holder copy still promises a restart (restart only provably clears disk residue): %s", regular)
	}

	zombie := errSessionBusyZombie.Error()
	if !strings.Contains(zombie, "restart Reasonix") {
		t.Fatalf("disk-residue (zombie) copy lost the restart repair: %s", zombie)
	}

	afterForce := errSessionBusyUnidentifiedHolderAfterForce.Error()
	if strings.Contains(strings.ToLower(afterForce), "use force archive") {
		t.Fatalf("after-force copy recommends force again (self-referential loop): %s", afterForce)
	}
	if !strings.Contains(afterForce, "desktop.log") {
		t.Fatalf("after-force copy lost the report-a-log path: %s", afterForce)
	}
	if !strings.Contains(afterForce, "force archive cleared") {
		t.Fatalf("after-force copy lost its FE sentinel stem (sessionBusyError.ts matches it): %s", afterForce)
	}
}

// TestTrashTopicForceAfterForceRefusalRendersReportCopy is the after-force
// acceptance ②: when a force archive still refuses in the identity-missing
// form (here: a REAL cross-process holder whose identity record was torn —
// nothing force may grab), the returned error carries the after-force copy,
// stays in the lease-held family for logs, and is NOT marked forceable (the
// frontend must not re-offer the button the user just clicked).
func TestTrashTopicForceAfterForceRefusalRendersReportCopy(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_force_after_force_copy"
	sessionPath := stageForceArchiveTopic(t, "force-after.jsonl", topicID, "Force after copy")

	releaseHolder := spawnForeignLeaseHolder(t, sessionPath)
	key := sessionRuntimeKey(sessionPath)
	// Corrupt the holder's identity records IN PLACE (the lock-file path stays
	// live and genuinely locked — nothing force may grab). Deleting the lock
	// file would rebuild the namespace entry and bypass the stale lock; the
	// corrupt form keeps every refusal on the identity-missing branch while
	// the lock take keeps failing — the shape that survives force and lands in
	// the after-force copy.
	corruptLeaseLockContent(t, store.SessionLeaseLock(key))
	_ = os.Remove(store.SessionLeaseInfo(key))

	app := NewApp()
	forceErr := app.TrashTopicForce(topicID)
	if forceErr == nil {
		releaseHolder()
		t.Fatal("force archive must not succeed against a live external holder")
	}
	defer releaseHolder()

	if !errors.Is(forceErr, errSessionBusyUnidentifiedHolderAfterForce) {
		t.Fatalf("after-force refusal = %v, want the after-force sentinel", forceErr)
	}
	if !errors.Is(forceErr, agent.ErrSessionLeaseHeld) {
		t.Fatalf("after-force refusal lost the lease-held chain: %v", forceErr)
	}
	if text := forceErr.Error(); strings.Contains(strings.ToLower(text), "use force archive") {
		t.Fatalf("after-force refusal recommends force again: %s", text)
	}
	if strings.HasPrefix(forceErr.Error(), topicArchiveForceablePrefix) {
		t.Fatal("after-force refusal must not be marked forceable — the user just clicked force")
	}
	// Nothing was grabbed: the external holder's lock still blocks the session
	// (InspectSessionLease reads corrupt here by design — the identity was
	// torn in place — so the busy probe is the lock's honest verdict).
	if !agent.SessionLeaseHeldByOtherRuntime(sessionPath) {
		t.Fatal("holder lock disturbed by force: the session no longer reports busy")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("failed force removed the session: %v", err)
	}
}
