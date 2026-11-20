package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/jobs"
	"reasonix/internal/permission"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1722 验收矩阵的测试落点。矩阵：两语义（trash 默认 / permanent 显式）
// × 五工件（主转录 / store 侧车全集 / ACP meta / subagents / jobs artifacts）
// × 四调用点（sessionDelete 主路径、sessionDelete 双胞胎、
// ReconcileCleanupPending 补收、delayedDeleteSessionFiles 延迟）。

// acpTrashBuckets lists the trash bucket directories under dir.
func acpTrashBuckets(t *testing.T, dir string) []string {
	t.Helper()
	root := filepath.Join(dir, acpSessionTrashDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read trash root: %v", err)
	}
	var buckets []string
	for _, entry := range entries {
		if entry.IsDir() {
			buckets = append(buckets, filepath.Join(root, entry.Name()))
		}
	}
	return buckets
}

// singleTrashBucket asserts exactly one trash bucket exists and returns it.
func singleTrashBucket(t *testing.T, dir string) string {
	t.Helper()
	buckets := acpTrashBuckets(t, dir)
	if len(buckets) != 1 {
		t.Fatalf("trash buckets = %v, want exactly 1", buckets)
	}
	return buckets[0]
}

// assertNoTrashBucket asserts the session directory carries no trash bucket —
// the hard-delete path must never leave one behind.
func assertNoTrashBucket(t *testing.T, dir string) {
	t.Helper()
	if buckets := acpTrashBuckets(t, dir); len(buckets) != 0 {
		t.Fatalf("hard delete left trash bucket(s): %v", buckets)
	}
}

// assertTrashMetaKey decodes the bucket's .trash-meta.json and asserts the
// desktop-compatible shape ({"key": ..., "deletedAt": ...}) carries the
// original transcript name — the field the desktop restore chain keys off.
func assertTrashMetaKey(t *testing.T, bucket, wantKey string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bucket, acpSessionTrashMetaFile))
	if err != nil {
		t.Fatalf("read trash meta: %v", err)
	}
	var meta acpTrashedSessionMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatalf("decode trash meta %s: %v", b, err)
	}
	if meta.Key != wantKey {
		t.Fatalf("trash meta key = %q, want %q", meta.Key, wantKey)
	}
	if meta.DeletedAt <= 0 {
		t.Fatalf("trash meta deletedAt = %d, want positive", meta.DeletedAt)
	}
}

// writeTrashFixtureSession writes one session with all five artifact families
// the acceptance matrix pins: the transcript itself, a store sidecar (event
// log), the ACP metadata sidecar, the checkpoint directory, the sub-agent
// artifacts, and the jobs artifact directory. Returns the transcript path and
// the sub-agent ref.
func writeTrashFixtureSession(t *testing.T, dir, id string) (string, string) {
	t.Helper()
	sessionPath := filepath.Join(dir, id+".jsonl")
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "trash me"})
	if err := s.SaveSnapshot(sessionPath); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := saveACPMeta(sessionPath, acpSessionMeta{Cwd: dir, Model: "test-model"}); err != nil {
		t.Fatalf("saveACPmeta: %v", err)
	}
	if err := os.WriteFile(store.SessionConflictLog(sessionPath), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write conflict log: %v", err)
	}
	ckptDir := store.SessionCheckpointDir(sessionPath)
	if err := os.MkdirAll(ckptDir, 0o755); err != nil {
		t.Fatalf("mkdir checkpoint dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ckptDir, "ckpt-1"), []byte("snapshot"), 0o644); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	jobsDir := jobs.ArtifactDir(sessionPath)
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		t.Fatalf("mkdir jobs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobsDir, "bash-1.log"), []byte("output"), 0o644); err != nil {
		t.Fatalf("write job log: %v", err)
	}
	ref := "sa_20260102_030405_000000000_aabbccddeeff"
	writeACPSubagentArtifact(t, dir, ref, agent.BranchID(sessionPath))
	return sessionPath, ref
}

// assertFamilyGoneFromLive asserts all five artifact families left the live
// session directory (the "原位消失" half of the trash acceptance pin).
func assertFamilyGoneFromLive(t *testing.T, sessionPath, subagentRef string) {
	t.Helper()
	for _, p := range []string{
		sessionPath,
		acpMetaPath(sessionPath),
		store.SessionConflictLog(sessionPath),
		store.SessionCheckpointDir(sessionPath),
		jobs.ArtifactDir(sessionPath),
		filepath.Join(filepath.Dir(sessionPath), "subagents", subagentRef+".jsonl"),
		filepath.Join(filepath.Dir(sessionPath), "subagents", subagentRef+".meta.json"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("artifact still live after trash: %s (err=%v)", p, err)
		}
	}
}

// TestTrashSessionFilesBucketsAllArtifacts is the trash×five-artifacts cell of
// the acceptance matrix: every family leaves the live directory and lands in
// one restore-shaped bucket, nothing is hard-deleted.
func TestTrashSessionFilesBucketsAllArtifacts(t *testing.T) {
	dir := t.TempDir()
	sessionPath, ref := writeTrashFixtureSession(t, dir, "mysession")
	orig, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}

	if err := trashSessionFiles(sessionPath); err != nil {
		t.Fatalf("trashSessionFiles: %v", err)
	}

	assertFamilyGoneFromLive(t, sessionPath, ref)
	bucket := singleTrashBucket(t, dir)
	for _, name := range []string{
		"mysession.jsonl",
		"mysession.acp.json",
		"mysession.conflicts.jsonl",
		"mysession.ckpt",
		"mysession.jobs",
		acpSessionTrashMetaFile,
	} {
		if _, err := os.Stat(filepath.Join(bucket, name)); err != nil {
			t.Fatalf("trashed artifact %s missing from bucket: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bucket, "subagents", ref+".jsonl")); err != nil {
		t.Fatalf("subagent jsonl missing from bucket: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bucket, "subagents", ref+".meta.json")); err != nil {
		t.Fatalf("subagent meta missing from bucket: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bucket, "mysession.jobs", "bash-1.log")); err != nil {
		t.Fatalf("job log missing from bucket: %v", err)
	}
	assertTrashMetaKey(t, bucket, "mysession.jsonl")
	// Restore-shaped and byte-faithful: the bucket transcript is the original
	// bytes under the original name (the desktop restore chain's round trip).
	got, err := os.ReadFile(filepath.Join(bucket, "mysession.jsonl"))
	if err != nil {
		t.Fatalf("read bucket transcript: %v", err)
	}
	if string(got) != string(orig) {
		t.Fatal("bucket transcript differs from the original bytes")
	}
}

// TestDeleteSessionFilesDefaultsToTrash pins the default: a zero-value mode
// (the no-field wire shape) recovers, it must not hard-delete.
func TestDeleteSessionFilesDefaultsToTrash(t *testing.T) {
	dir := t.TempDir()
	sessionPath, ref := writeTrashFixtureSession(t, dir, "defaults")

	if err := deleteSessionFiles(sessionPath, sessionDeleteMode("")); err != nil {
		t.Fatalf("deleteSessionFiles(\"\"): %v", err)
	}
	assertFamilyGoneFromLive(t, sessionPath, ref)
	singleTrashBucket(t, dir)
}

// TestDeleteSessionFilesPermanentHardDeletesAllArtifacts is the
// permanent×five-artifacts cell: only the explicit mode reaches the hard path,
// and it leaves no bucket behind.
func TestDeleteSessionFilesPermanentHardDeletesAllArtifacts(t *testing.T) {
	dir := t.TempDir()
	sessionPath, ref := writeTrashFixtureSession(t, dir, "gone")

	if err := deleteSessionFiles(sessionPath, sessionDeleteModePermanent); err != nil {
		t.Fatalf("deleteSessionFiles(permanent): %v", err)
	}
	assertFamilyGoneFromLive(t, sessionPath, ref)
	assertNoTrashBucket(t, dir)
	if _, err := agent.LoadSession(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("LoadSession after permanent delete = %v, want IsNotExist", err)
	}
}

// TestSessionDeleteModeFromWire pins the bypass-proof resolution: only the
// literal case-sensitive "permanent" reaches the hard-delete path; the empty
// field and "trash" recover; anything else (typo, casing variant) is rejected
// instead of silently downgraded or escalated.
func TestSessionDeleteModeFromWire(t *testing.T) {
	cases := []struct {
		raw     string
		want    sessionDeleteMode
		wantErr bool
	}{
		{raw: "", want: sessionDeleteModeTrash},
		{raw: "trash", want: sessionDeleteModeTrash},
		{raw: "permanent", want: sessionDeleteModePermanent},
		{raw: "Permanent", wantErr: true},
		{raw: "PERMANENT", wantErr: true},
		{raw: "perm", wantErr: true},
		{raw: "hard", wantErr: true},
	}
	for _, tc := range cases {
		got, err := sessionDeleteModeFromWire(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("sessionDeleteModeFromWire(%q) = %q, want error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sessionDeleteModeFromWire(%q): %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sessionDeleteModeFromWire(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestCleanupPendingDeleteMode pins the marker-operation mapping: the shared
// "delete" spelling recovers (desktop delete = ACP default), the permanent
// chain's spelling and the clear teardown hard-delete — the same split the
// desktop reconciler applies.
func TestCleanupPendingDeleteMode(t *testing.T) {
	if got := cleanupPendingDeleteMode("delete"); got != sessionDeleteModeTrash {
		t.Fatalf("cleanupPendingDeleteMode(delete) = %q, want trash", got)
	}
	for _, op := range []string{"delete-permanent", "clear", "", "unknown"} {
		if got := cleanupPendingDeleteMode(op); got != sessionDeleteModePermanent {
			t.Fatalf("cleanupPendingDeleteMode(%q) = %q, want permanent", op, got)
		}
	}
}

// TestCleanupPendingOperationFor pins the write-side spelling so a marker
// written by this process reconciles with the semantics it was asked for.
func TestCleanupPendingOperationFor(t *testing.T) {
	if got := cleanupPendingOperationFor(sessionDeleteModeTrash); got != "delete" {
		t.Fatalf("cleanupPendingOperationFor(trash) = %q, want delete", got)
	}
	if got := cleanupPendingOperationFor(sessionDeleteModePermanent); got != "delete-permanent" {
		t.Fatalf("cleanupPendingOperationFor(permanent) = %q, want delete-permanent", got)
	}
}

// TestDelayedDeleteFollowsRequestMode pins the delayed call site: the
// teardown-timeout goroutine keeps the original request's semantics.
func TestDelayedDeleteFollowsRequestMode(t *testing.T) {
	trashDir := t.TempDir()
	trashPath, trashRef := writeTrashFixtureSession(t, trashDir, "delayed-trash")
	delayedDeleteSessionFiles(trashPath, sessionDeleteModeTrash, control.SessionDestroyHandle{})
	assertFamilyGoneFromLive(t, trashPath, trashRef)
	singleTrashBucket(t, trashDir)

	permDir := t.TempDir()
	permPath, permRef := writeTrashFixtureSession(t, permDir, "delayed-perm")
	delayedDeleteSessionFiles(permPath, sessionDeleteModePermanent, control.SessionDestroyHandle{})
	assertFamilyGoneFromLive(t, permPath, permRef)
	assertNoTrashBucket(t, permDir)
}

// TestSessionDeleteRPCDeletionMode drives session/delete over the RPC surface:
// the default request recovers into a bucket, the explicit permanent request
// hard-deletes, and an unknown mode is invalid params — the wire cannot
// accidentally reach the hard path.
func TestSessionDeleteRPCDeletionMode(t *testing.T) {
	run := func(t *testing.T) (*rpcClient, func(), string) {
		t.Helper()
		dir := t.TempDir()
		factory := &e2eFactory{
			prov: &scriptedProvider{name: "fake", responses: [][]provider.Chunk{
				{{Type: provider.ChunkText, Text: "unused"}, {Type: provider.ChunkDone}},
			}},
			tool:       fakeTool{name: "peek", ro: true, out: "unused"},
			policy:     permission.New("ask", nil, nil, nil),
			sessionDir: dir,
		}
		client, stop := startServer(t, factory)
		sessionPath, _ := writeTrashFixtureSession(t, dir, "rpcdel")
		return client, stop, sessionPath
	}

	t.Run("default_trashes", func(t *testing.T) {
		client, stop, sessionPath := run(t)
		defer stop()
		client.call(t, "initialize", InitializeParams{ProtocolVersion: 1})
		resp := client.call(t, "session/delete", SessionDeleteParams{SessionID: "rpcdel"})
		if resp.Error != nil {
			t.Fatalf("session/delete errored: %+v", resp.Error)
		}
		if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
			t.Fatalf("transcript still live after default delete (err=%v)", err)
		}
		singleTrashBucket(t, filepath.Dir(sessionPath))
	})

	t.Run("permanent_hard_deletes", func(t *testing.T) {
		client, stop, sessionPath := run(t)
		defer stop()
		client.call(t, "initialize", InitializeParams{ProtocolVersion: 1})
		resp := client.call(t, "session/delete", SessionDeleteParams{SessionID: "rpcdel", DeletionMode: "permanent"})
		if resp.Error != nil {
			t.Fatalf("session/delete errored: %+v", resp.Error)
		}
		if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
			t.Fatalf("transcript still live after permanent delete (err=%v)", err)
		}
		assertNoTrashBucket(t, filepath.Dir(sessionPath))
	})

	t.Run("unknown_mode_rejected", func(t *testing.T) {
		client, stop, sessionPath := run(t)
		defer stop()
		client.call(t, "initialize", InitializeParams{ProtocolVersion: 1})
		resp := client.call(t, "session/delete", SessionDeleteParams{SessionID: "rpcdel", DeletionMode: "Permanent"})
		if resp.Error == nil {
			t.Fatal("session/delete with casing-variant mode should fail")
		}
		if resp.Error.Code != ErrInvalidParams {
			t.Fatalf("error code = %d, want invalid params", resp.Error.Code)
		}
		if _, err := os.Stat(sessionPath); err != nil {
			t.Fatalf("rejected request must not touch the session: stat err = %v", err)
		}
	})
}

// TestSessionDeleteTwinPathFollowsMode pins the second call site: a recovered
// session's id-keyed twin is disposed with the same semantics as the active
// recovery transcript — both recover together under the default.
func TestSessionDeleteTwinPathFollowsMode(t *testing.T) {
	dir := t.TempDir()
	factory := &e2eFactory{
		prov: &scriptedProvider{name: "fake", responses: [][]provider.Chunk{
			{{Type: provider.ChunkText, Text: "unused"}, {Type: provider.ChunkDone}},
		}},
		tool:       fakeTool{name: "peek", ro: true, out: "unused"},
		policy:     permission.New("ask", nil, nil, nil),
		sessionDir: dir,
	}
	client, stop := startServer(t, factory)
	defer stop()
	client.call(t, "initialize", InitializeParams{ProtocolVersion: 1})

	id := "twinsen"
	twinPath := transcriptPath(dir, id)
	// The id-keyed twin holds the redirect; the live transcript sits on a
	// recovery branch, the shape sessionDelete resolves and then deletes.
	activePath := filepath.Join(dir, id+"-recovery-abcd.jsonl")
	if err := os.WriteFile(activePath, []byte(`{"role":"user","content":"active"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveACPMeta(activePath, acpSessionMeta{SessionID: id, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	if err := saveACPMeta(twinPath, acpSessionMeta{SessionID: id, Cwd: dir, ActiveTranscript: filepath.Base(activePath)}); err != nil {
		t.Fatal(err)
	}

	resp := client.call(t, "session/delete", SessionDeleteParams{SessionID: id})
	if resp.Error != nil {
		t.Fatalf("session/delete errored: %+v", resp.Error)
	}
	for _, p := range []string{activePath, twinPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("twin %s still live after delete (err=%v)", p, err)
		}
	}
	buckets := acpTrashBuckets(t, dir)
	if len(buckets) != 2 {
		t.Fatalf("trash buckets = %d, want 2 (active + twin both recover)", len(buckets))
	}
	seen := map[string]bool{}
	for _, bucket := range buckets {
		b, err := os.ReadFile(filepath.Join(bucket, acpSessionTrashMetaFile))
		if err != nil {
			t.Fatalf("read trash meta: %v", err)
		}
		var meta acpTrashedSessionMeta
		if err := json.Unmarshal(b, &meta); err != nil {
			t.Fatalf("decode trash meta: %v", err)
		}
		seen[meta.Key] = true
	}
	if !seen[filepath.Base(activePath)] || !seen[filepath.Base(twinPath)] {
		t.Fatalf("bucket keys = %v, want both %q and %q", seen, filepath.Base(activePath), filepath.Base(twinPath))
	}
}
