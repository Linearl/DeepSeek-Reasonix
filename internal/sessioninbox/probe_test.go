package sessioninbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/filelock"
)

// enqueueProbeItem writes one durable queued follow-up through the real Store
// path and closes it, leaving a manifest exactly as a delivered collab message
// would: queued item, no runtime attached (任务 887 的第三态原料).
func enqueueProbeItem(t *testing.T, sessionPath string, collabMsgID string) InboxItemMeta {
	t.Helper()
	st, err := Open(sessionPath, Limits{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	rec, err := st.Enqueue(EnqueueRequest{
		Intent: IntentFollowup,
		Envelope: PromptEnvelope{
			DisplayText: "probe queued followup",
			RawText:     "probe queued followup",
			SubmitText:  "probe queued followup",
			CollabMsgID: collabMsgID,
			CollabMailTo: "contact-self",
		},
		Source: "collab:contact-peer",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	meta, _, err := st.ReadItem(rec.ItemID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	st.Close()
	return meta
}

// 验收④ 的反面（探测面）：无 inbox manifest 的会话必须被识别为「无排队」，
// 且探测绝不能给它建目录——否则每轮反查都在污染磁盘。
func TestProbeSnapshotNoManifestNoSideEffect(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "never-used.jsonl")
	snap, found, err := ProbeSnapshot(sessionPath)
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if found {
		t.Fatalf("expected found=false for a session without inbox manifest, got %+v", snap)
	}
	inboxDir := filepath.Join(dir, "never-used.inbox")
	if _, serr := os.Stat(inboxDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("probe must never create the inbox directory, stat=%v", serr)
	}
	// empty session path: same contract, no panic.
	if _, found, err := ProbeSnapshot("  "); err != nil || found {
		t.Fatalf("empty path must be a quiet miss, found=%v err=%v", found, err)
	}
}

// 验收① 的探测前置：真实 Store 写入的排队 follow-up 能被只读探测原样读出
// （队首、collab 回执坐标、来源），且探测后 manifest 字节不变（零写入）。
func TestProbeSnapshotReadsQueuedFollowupReadOnly(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "dormant.jsonl")
	want := enqueueProbeItem(t, sessionPath, "msg_probe_1")

	manifestPath := filepath.Join(dir, "dormant.inbox", "manifest.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest before probe: %v", err)
	}

	snap, found, err := ProbeSnapshot(sessionPath)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !found {
		t.Fatal("expected found=true after enqueue")
	}

	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest after probe: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("probe must not mutate the manifest (bytes changed)")
	}

	if len(snap.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(snap.Items))
	}
	head := snap.Items[0]
	if head.ID != want.ID {
		t.Fatalf("head id = %q, want %q", head.ID, want.ID)
	}
	if head.State != StateQueued {
		t.Fatalf("head state = %s, want queued", head.State)
	}
	if head.CollabMsgID != "msg_probe_1" || head.CollabMailTo != "contact-self" {
		t.Fatalf("receipt coords lost: %+v", head)
	}
	if head.Source != "collab:contact-peer" {
		t.Fatalf("source = %q", head.Source)
	}
	if snap.SessionPath != sessionPath {
		t.Fatalf("snapshot session path = %q, want %q", snap.SessionPath, sessionPath)
	}
}

// pause 来源必须原样透传：709 桥按 UserPaused / autoPaused 分流，
// 探测替桥做决策就会越界。
func TestProbeSnapshotReportsPauseProvenance(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "paused.jsonl")
	enqueueProbeItem(t, sessionPath, "msg_probe_2")

	st, err := Open(sessionPath, Limits{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.SetPaused(true); err != nil {
		t.Fatalf("auto pause: %v", err)
	}
	if err := st.SetUserPaused(true); err != nil {
		t.Fatalf("user pause: %v", err)
	}
	st.Close()

	snap, found, err := ProbeSnapshot(sessionPath)
	if err != nil || !found {
		t.Fatalf("probe: found=%v err=%v", found, err)
	}
	if !snap.Paused || !snap.UserPaused {
		t.Fatalf("pause provenance lost: paused=%v userPaused=%v", snap.Paused, snap.UserPaused)
	}
}

// 写者持锁期间探测必须让路（ErrProbeBusy），不能阻塞、更不能读到半截——
// Windows 上 rename-over-open 会让写者提交失败，这把锁是双向保护。
func TestProbeSnapshotBusyWhileWriterHoldsLock(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "busy.jsonl")
	enqueueProbeItem(t, sessionPath, "msg_probe_3")

	lockPath := filepath.Join(dir, "busy.inbox", "transaction.lock")
	release, err := filelock.Acquire(t.Context(), lockPath)
	if err != nil {
		t.Fatalf("acquire writer lock: %v", err)
	}
	defer release()

	_, _, err = ProbeSnapshot(sessionPath)
	if !errors.Is(err, ErrProbeBusy) {
		t.Fatalf("want ErrProbeBusy while writer holds the lock, got %v", err)
	}
}

// 损坏 manifest：返回 error 但**不 quarantine、不改文件**——清道夫是
// Store.Open 的职责，只读探测代行会把「看一眼」变成「动手术」。
func TestProbeSnapshotCorruptManifestIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "corrupt.jsonl")
	enqueueProbeItem(t, sessionPath, "msg_probe_4")

	manifestPath := filepath.Join(dir, "corrupt.inbox", "manifest.json")
	garbage := []byte("{not json")
	if err := os.WriteFile(manifestPath, garbage, 0o600); err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}

	_, found, err := ProbeSnapshot(sessionPath)
	if err == nil {
		t.Fatalf("want error for corrupt manifest, found=%v", found)
	}
	after, rerr := os.ReadFile(manifestPath)
	if rerr != nil {
		t.Fatalf("manifest must survive the probe: %v", rerr)
	}
	if string(after) != string(garbage) {
		t.Fatal("probe must not quarantine or rewrite a corrupt manifest")
	}
	if _, serr := os.Stat(filepath.Join(dir, "corrupt.inbox", quarantineName)); serr == nil {
		t.Fatal("probe must never quarantine — that is Store.Open's job")
	}
}
