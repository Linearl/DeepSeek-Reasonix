package agent

// tl1474 pins for the recovery-coverage verdict memo.
//
// Contract under test:
//  1. A proven verdict (true or false) is served from the memo on the next
//     call with unchanged bytes — observable by holding the branch's save-path
//     lock: a memo hit returns promptly, a re-proof blocks inside LoadSession.
//  2. Any byte movement on the branch re-proves (fresh stamp key), even though
//     the memo could have served the stale verdict.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func tl1474WriteCoveredPair(t *testing.T, dir, parentName, branchName string, msgs []provider.Message, continued bool) (parentPath, branchPath string) {
	t.Helper()
	parentPath = filepath.Join(dir, parentName)
	branchPath = filepath.Join(dir, branchName)

	parentMsgs := msgs
	if continued {
		// Parent moved on; branch stayed at the fork point.
		parentMsgs = append(append([]provider.Message{}, msgs...), provider.Message{Role: provider.RoleUser, Content: "newer turn only the parent has"})
	}
	writeSessionFile(t, parentPath, parentMsgs)
	writeSessionFile(t, branchPath, msgs)

	forkDigest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatalf("digestSessionMessages: %v", err)
	}
	if err := SaveBranchMeta(branchPath, BranchMeta{
		ID:             BranchID(branchPath),
		Revision:       1,
		ContentDigest:  digestString(forkDigest),
		Recovered:      true,
		RecoveryDigest: digestString(forkDigest),
		ParentID:       strings.TrimSuffix(parentName, ".jsonl"),
	}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	return parentPath, branchPath
}

// expectCoveredComplete runs the proof while holding the branch's save-path
// lock and asserts it returns before the timeout (memo hit).
func expectCoveredComplete(t *testing.T, path, parentDir string, meta BranchMeta, want bool) {
	t.Helper()
	unlock, ok := tryLockSessionSavePath(path)
	if !ok {
		t.Fatalf("could not take the save-path lock for the pin")
	}
	defer unlock()

	type result struct {
		verdict   bool
		elapsedMs int64
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		v := recoveryBranchCoveredByParent(path, parentDir, meta)
		done <- result{verdict: v, elapsedMs: time.Since(start).Milliseconds()}
	}()
	select {
	case r := <-done:
		if r.verdict != want {
			t.Fatalf("verdict drift: want %v got %v", want, r.verdict)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("proof re-ran behind a held save-path lock — memo did not serve the verdict (re-proves must not happen for unchanged bytes)")
	}
}

// expectCoveredBlocks runs the proof while holding the branch's save-path lock
// and asserts it does NOT complete before the timeout (memo miss → re-proof).
func expectCoveredBlocks(t *testing.T, path, parentDir string, meta BranchMeta) {
	t.Helper()
	unlock, ok := tryLockSessionSavePath(path)
	if !ok {
		t.Fatalf("could not take the save-path lock for the pin")
	}
	defer unlock()

	done := make(chan bool, 1)
	go func() {
		done <- recoveryBranchCoveredByParent(path, parentDir, meta)
	}()
	select {
	case verdict := <-done:
		t.Fatalf("proof completed behind a held save-path lock (verdict=%v) — expected a re-proof to block", verdict)
	case <-time.After(300 * time.Millisecond):
		// expected: still blocked inside LoadSession
	}
}

func TestTL1474CoverageMemoServesTrueWithoutReproof(t *testing.T) {
	dir := t.TempDir()
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "fork point content"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	_, branchPath := tl1474WriteCoveredPair(t, dir, "20260101-000001-model-parent.jsonl", "20260101-000002-model-recovery-abc.jsonl", msgs, false)
	meta, ok, err := LoadBranchMeta(branchPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	// Warm the memo without the lock; then the locked call must be a hit.
	if v := recoveryBranchCoveredByParent(branchPath, dir, meta); !v {
		t.Fatalf("fixture should prove covered (equal digests), got false")
	}
	expectCoveredComplete(t, branchPath, dir, meta, true)
}

func TestTL1474CoverageMemoServesFalseWithoutReproof(t *testing.T) {
	dir := t.TempDir()
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "fork point content"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	// Branch continued on past the recorded fork digest (its transcript carries
	// a turn the digest predates) → "this is someone's conversation now": a
	// definitive false the memo must serve without re-proving.
	branchPath := filepath.Join(dir, "20260101-000004-model-recovery-def.jsonl")
	branchMsgs := append(append([]provider.Message{}, msgs...), provider.Message{Role: provider.RoleUser, Content: "turn after the fork digest was recorded"})
	writeSessionFile(t, branchPath, branchMsgs)
	forkDigest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatalf("digestSessionMessages: %v", err)
	}
	if err := SaveBranchMeta(branchPath, BranchMeta{
		ID:             BranchID(branchPath),
		Revision:       2,
		ContentDigest:  digestString(tl1474MustDigest(t, branchMsgs)),
		Recovered:      true,
		RecoveryDigest: digestString(forkDigest),
		ParentID:       "20260101-000003-model-parent",
	}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	meta, ok, err := LoadBranchMeta(branchPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	if v := recoveryBranchCoveredByParent(branchPath, dir, meta); v {
		t.Fatalf("fixture should NOT prove covered (branch continued), got true")
	}
	expectCoveredComplete(t, branchPath, dir, meta, false)
}

func tl1474MustDigest(t *testing.T, msgs []provider.Message) [32]byte {
	t.Helper()
	d, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatalf("digestSessionMessages: %v", err)
	}
	return d
}

func TestTL1474CoverageMemoInvalidatedByBranchWrite(t *testing.T) {
	dir := t.TempDir()
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "fork point content"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	_, branchPath := tl1474WriteCoveredPair(t, dir, "20260101-000005-model-parent.jsonl", "20260101-000006-model-recovery-ghi.jsonl", msgs, false)
	meta, ok, err := LoadBranchMeta(branchPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	if v := recoveryBranchCoveredByParent(branchPath, dir, meta); !v {
		t.Fatalf("fixture should prove covered, got false")
	}

	// Append a turn to the branch transcript: the branch moved on, and the
	// stamp key must move with it — the next proof re-runs (blocks behind the
	// held lock) instead of serving the stale true.
	f, err := os.OpenFile(branchPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(provider.Message{Role: provider.RoleUser, Content: "continued on the branch"}); err != nil {
		t.Fatalf("encode append: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	expectCoveredBlocks(t, branchPath, dir, meta)
}
