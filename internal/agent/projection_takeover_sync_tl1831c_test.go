package agent

import (
	"errors"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1831c 阶段③验收：rev-lag takeover 整文件重写落地后，投影层必须在当场
// 收到协同信号——活 fold 仍有效 → 重发 sidecar（磁盘 (transcript, sidecar)
// 对保持 hash 一致）；内存无有效 fold → 磁盘上描述旧字节的 sidecar（常常是
// 死 writer 的封存，08:58 现场）被删除并走有序重建——下次启动见到的是
// 「无 sidecar → 新建」或「快路径 restored」，永远不再是「旧 sidecar × 新
// transcript 的 hash_mismatch drop 现场」。
//
// 时序钉：信号在 SaveAdoptOverStaleDisk 内、SaveRewrite 落盘之后同步触发
// （transcript 先落、sidecar 后写的水位序）；hook body 在 save 锁外自取
// compactionMu。

// tl1831cTakeoverFixture builds the tl1409 dead-writer shape on top of a live
// session: live saves a base transcript, a second writer advances the disk and
// dies, the live session grows past it and hits the diverged conflict.
func tl1831cTakeoverFixture(t *testing.T, path string, live *Session) *SessionSnapshotConflictError {
	t.Helper()
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	dead, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession (dead writer): %v", err)
	}
	dead.Add(provider.Message{Role: provider.RoleUser, Content: "dead-writer-tail"})
	if err := dead.SaveSnapshot(path); err != nil {
		t.Fatalf("dead writer save: %v", err)
	}
	live.Add(provider.Message{Role: provider.RoleUser, Content: "live4"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "live5"})
	err = live.SaveSnapshot(path)
	var conflict *SessionSnapshotConflictError
	if !errors.As(err, &conflict) || conflict == nil || conflict.Kind != SessionSnapshotConflictDiverged {
		t.Fatalf("live save err = %v, want diverged conflict", err)
	}
	return conflict
}

// TestTL1831cTakeoverRepublishesValidFold: the common continuation shape —
// the live process's own fold still describes exactly the transcript it just
// rewrote to disk, while the DISK sidecar is a foreign writer's seal (the
// 08:58 disk state). The sync must republish the live seal, so the next
// startup restores VALID instead of tripping hash_mismatch.
func TestTL1831cTakeoverRepublishesValidFold(t *testing.T) {
	path := schemaOneSessionPath(t, "takeover-sync-valid.jsonl")
	live := NewSession("sys")
	bindSessionWriter(t, live, path)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "m2"})
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m3"})

	// The live fold (valid against the live canonical, covered = 4).
	seal := CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: 311,
		PromptCacheKey:    promptCacheKey("workspace", BranchID(path), "provider/model"),
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				{Role: provider.RoleUser, Content: "[compacted context]"},
			},
			TranscriptVersion:    311,
			ProjectionVersion:    5,
			CoveredCount:         4,
			CoveredPrefixHash:    coveredPrefixHash(live.Snapshot(), 4),
			CoveredMessageHashes: coveredMessageFingerprints(live.Snapshot(), 4),
		},
	}
	if err := SaveCompactionState(path, seal); err != nil {
		t.Fatalf("SaveCompactionState (live seal): %v", err)
	}
	// The agent binds the session (installs the sync hook) and restores the
	// fold as valid.
	bound := New(nil, tool.NewRegistry(), live, Options{
		SessionPath: path,
		WorkspaceID: "workspace",
		ModelRef:    "provider/model",
	}, event.Discard)
	if got := bound.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state before takeover = %q, want restored", got)
	}

	conflict := tl1831cTakeoverFixture(t, path, live)

	// The disk sidecar meanwhile became a FOREIGN writer's seal (sealed
	// against the dead writer's transcript — the 08:58 disk state).
	deadView, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession (dead transcript): %v", err)
	}
	foreign := CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: 999,
		PromptCacheKey:    seal.PromptCacheKey,
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				{Role: provider.RoleUser, Content: "[dead writer fold]"},
			},
			TranscriptVersion:    999,
			ProjectionVersion:    9,
			CoveredCount:         len(deadView.Snapshot()),
			CoveredPrefixHash:    coveredPrefixHash(deadView.Snapshot(), len(deadView.Snapshot())),
			CoveredMessageHashes: coveredMessageFingerprints(deadView.Snapshot(), len(deadView.Snapshot())),
		},
	}
	if err := SaveCompactionState(path, foreign); err != nil {
		t.Fatalf("SaveCompactionState (foreign seal): %v", err)
	}

	// The takeover commits; the sync fires inside it.
	if err := live.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		t.Fatalf("SaveAdoptOverStaleDisk: %v", err)
	}

	// The disk sidecar is the LIVE seal again (republished, hash-consistent
	// with the rewritten transcript).
	persisted, ok, err := LoadCompactionState(path)
	if err != nil || !ok {
		t.Fatalf("LoadCompactionState after takeover: ok=%v err=%v", ok, err)
	}
	if persisted.Projection.ProjectionVersion != 5 {
		t.Fatalf("persisted projection version = %d, want 5 (the live seal, not the foreign 9)", persisted.Projection.ProjectionVersion)
	}
	if want := coveredPrefixHash(live.Snapshot(), 4); persisted.Projection.CoveredPrefixHash != want {
		t.Fatalf("persisted covered hash = %s, want live-canonical %s", shortHash8(persisted.Projection.CoveredPrefixHash), shortHash8(want))
	}
	// Next startup: the fold restores VALID — the mismatch scene is gone.
	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state after restart = %q, want restored (no hash_mismatch drop)", got)
	}
}

// TestTL1831cTakeoverRemovesForeignSidecarAndRebuilds: the 08:58 shape
// itself — no usable in-memory fold, and the disk sidecar is the dead
// writer's seal. The sync must REMOVE it (ordered rebuild precondition) so
// the next startup sees "no sidecar → fresh fold" instead of the mismatch
// drop. The rebuild kick rides invalidateProjection's standard arm path
// (provider-gated in unit tests; exercised by the tl1203/任务549 suites).
func TestTL1831cTakeoverRemovesForeignSidecarAndRebuilds(t *testing.T) {
	path := schemaOneSessionPath(t, "takeover-sync-foreign.jsonl")
	live := NewSession("sys")
	bindSessionWriter(t, live, path)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	live.Add(provider.Message{Role: provider.RoleAssistant, Content: "m2"})
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m3"})

	conflict := tl1831cTakeoverFixture(t, path, live)

	// Dead writer's own seal on disk — sealed against the PRE-takeover
	// transcript, guaranteed stale once the rewrite lands.
	deadView, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession (dead transcript): %v", err)
	}
	deadSeal := CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: 777,
		PromptCacheKey:    promptCacheKey("workspace", BranchID(path), "provider/model"),
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				{Role: provider.RoleUser, Content: "[dead writer fold]"},
			},
			TranscriptVersion:    777,
			ProjectionVersion:    3,
			CoveredCount:         len(deadView.Snapshot()),
			CoveredPrefixHash:    coveredPrefixHash(deadView.Snapshot(), len(deadView.Snapshot())),
			CoveredMessageHashes: coveredMessageFingerprints(deadView.Snapshot(), len(deadView.Snapshot())),
		},
	}
	if err := SaveCompactionState(path, deadSeal); err != nil {
		t.Fatalf("SaveCompactionState (dead seal): %v", err)
	}

	// The live agent has NO usable fold (never loaded one); the takeover must
	// still clean the disk sidecar — that is the signal's whole point.
	bound := New(nil, tool.NewRegistry(), live, Options{
		SessionPath: path,
		WorkspaceID: "workspace",
		ModelRef:    "provider/model",
	}, event.Discard)
	if got := bound.ContextMaintenanceSnapshot().CheckpointState; got != "none" {
		t.Fatalf("checkpoint state before takeover = %q, want none", got)
	}
	if _, ok, err := LoadCompactionState(path); err != nil || !ok {
		t.Fatalf("dead seal must exist before the takeover: ok=%v err=%v", ok, err)
	}

	if err := live.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		t.Fatalf("SaveAdoptOverStaleDisk: %v", err)
	}

	// The stale sidecar is gone; the next startup builds fresh instead of
	// litigating the mismatch.
	if _, ok, err := LoadCompactionState(path); err != nil || ok {
		t.Fatalf("LoadCompactionState after takeover: ok=%v err=%v, want absent (removed by the sync)", ok, err)
	}
	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "none" {
		t.Fatalf("checkpoint state after restart = %q, want none (fresh fold, no mismatch scene)", got)
	}
	visible := reopened.modelVisibleMessages()
	if len(visible) != 6 {
		t.Fatalf("visible messages = %d, want the 6-message canonical (fresh fold)", len(visible))
	}
}

// TestTL1831cTakeoverSyncNilHookNoOp: a plain Session (no agent) keeps the
// nil hook — takeover behavior byte-identical to pre-tl1831c.
func TestTL1831cTakeoverSyncNilHookNoOp(t *testing.T) {
	path := schemaOneSessionPath(t, "takeover-sync-nil.jsonl")
	live := NewSession("sys")
	bindSessionWriter(t, live, path)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "m1"})
	conflict := tl1831cTakeoverFixture(t, path, live)
	if err := live.SaveAdoptOverStaleDisk(path, conflict); err != nil {
		t.Fatalf("SaveAdoptOverStaleDisk: %v", err)
	}
	// No panic, no sidecar write — the takeover itself committed.
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// live transcript = system + m1 + live4 + live5.
	if got := loaded.Snapshot(); len(got) != 4 || got[3].Content != "live5" {
		t.Fatalf("reload = %d messages, want the live transcript (system + 3)", len(got))
	}
	if _, ok, _ := LoadCompactionState(path); ok {
		t.Fatal("no sidecar expected on a session that never had one")
	}
}
