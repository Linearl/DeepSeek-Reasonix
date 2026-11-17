package agent

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1651 修复③验收矩阵（先写矩阵后落地；A1 load 修复正式迁移通道）：
//
//	#     验收项                                                     钉
//	R3-1  修复改写场景：stored==H(preRepair) → 迁移成功+restored+      TestTL1651R3LoadRepairMigration/a_migrated_and_restored
//	    投影服务+sidecar 落盘新 hash+迁移日志在场
//	R3-2  把关门1（B1）：盘上前缀被第三方真改写（preRepair 不匹配       TestTL1651R3LoadRepairMigration/b_third_party_rewrite_fail_closed
//	    stored）→ 不迁移，fail-closed（tl1203 防线不削弱）
//	R3-3  把关门2（共享首条指纹）：修复形态动了首条 → 拒绝迁移          TestTL1651R3LoadRepairMigration/c_first_fingerprint_gate
//	R3-4  sidecar 与盘上字节不同代（stored 对不上 preRepair）→ 拒绝     TestTL1651R3LoadRepairMigration/d_stale_sidecar_refused
//	R3-5  legacy 迁移先例零回退（先 legacy 后 load-repair 的次序）      既有 TestProjection 族回归（定向面全绿判据）
//
// 机制说明：B1 门本身就是「防真改写合法化」的主防线——第三方改写改的是盘上
// 字节，修复前读取（rawMessages）就不再匹配 stored，门必不过；共享首条指纹
// 门补一层「修复永不触碰开头」的结构锚。

// tl1651R3InterruptedTranscript is a transcript whose last assistant turn was
// cut off mid-tool-call (dangling call, no result) — the auto-resume shape
// that NormalizeSession repairs by backfilling the call name and fabricating
// the placeholder tool result.
func tl1651R3InterruptedTranscript() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: "", ToolCalls: []provider.ToolCall{{ID: "call1", Name: "", Arguments: "{}"}}},
		{Role: provider.RoleUser, Content: "next"},
	}
}

func TestTL1651R3LoadRepairMigration(t *testing.T) {
	t.Run("a_migrated_and_restored", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "s.jsonl")
		pre := tl1651R3InterruptedTranscript()
		// 盘上写「修复前」字节 + 按修复前字节封的 sidecar。
		if err := writeSessionMessages(path, pre); err != nil {
			t.Fatal(err)
		}
		key := promptCacheKey("ws", BranchID(path), "m")
		stored := CompactionState{
			SchemaVersion:  compactionStateSchemaV4,
			PromptCacheKey: key,
			UpdatedAt:      time.Now().UTC(),
			Projection: ContextProjection{
				Messages:             []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
				TranscriptVersion:    4,
				ProjectionVersion:    6,
				CoveredCount:         len(pre),
				CoveredPrefixHash:    coveredPrefixHash(pre, len(pre)),
				PinnedContextHash:    pinnedContextCoverageHash(pre, len(pre)),
				CoveredMessageHashes: coveredMessageFingerprints(pre, len(pre)),
			},
		}
		if err := SaveCompactionState(path, stored); err != nil {
			t.Fatal(err)
		}

		buf, restore := tl1651TestCapture(t, slog.LevelDebug)
		defer restore()

		// LoadSession 走修复（normalizeDirty=true，rawMessages=pre），agent 以
		// 修复后形态做首载校验——修复③前这里是 hash_mismatch 全量兜底。
		sess, err := LoadSession(path)
		if err != nil {
			t.Fatal(err)
		}
		if !sess.normalizedDirty {
			t.Fatal("fixture broken: interrupted turn was not repaired on load")
		}
		a := New(nil, nil, sess, Options{
			SessionPath: path,
			WorkspaceID: "ws",
			ModelRef:    "m",
		}, event.Discard)

		if a.sess.checkpointState != "restored" {
			t.Fatalf("load-repaired fold must migrate and restore, checkpointState=%q, log:\n%s", a.sess.checkpointState, buf.String())
		}
		if a.currentProjectionVersion() == 0 {
			t.Fatal("migrated projection must serve the wire (projection_version > 0)")
		}
		log := buf.String()
		if !strings.Contains(log, "load-repaired covered prefix migrated") {
			t.Fatalf("migration trail missing, log:\n%s", log)
		}
		if strings.Contains(log, "reason=hash_mismatch") {
			t.Fatalf("migrated fold must not trip the mismatch drop, log:\n%s", log)
		}
		// 迁移后的 sidecar 落盘（persistCompactionStateLocked）：下次重启直接
		// 服务，不再 rebuild。
		b, err := os.ReadFile(ContextStatePath(path))
		if err != nil {
			t.Fatal(err)
		}
		var landed CompactionState
		if err := json.Unmarshal(b, &landed); err != nil {
			t.Fatal(err)
		}
		cur := sess.Snapshot()
		if want := coveredPrefixHash(cur, landed.Projection.CoveredCount); landed.Projection.CoveredPrefixHash != want {
			t.Fatalf("republished sidecar hash must anchor the REPAIRED transcript, got %q want %q", landed.Projection.CoveredPrefixHash, want)
		}
	})

	t.Run("b_third_party_rewrite_fail_closed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "s.jsonl")
		// 盘上前缀被第三方真改写（tl1644 家族）：well-formed，修复不动它，
		// rawMessages 与修复后一致 → preRepair 为空语义等价；stored 锚的是
		// 改写前的旧字节 → B1 门必不过。
		onDisk := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "task-REWRITTEN-BY-THIRD-PARTY"},
			{Role: provider.RoleAssistant, Content: "done"},
			{Role: provider.RoleUser, Content: "next"},
		}
		if err := writeSessionMessages(path, onDisk); err != nil {
			t.Fatal(err)
		}
		original := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "task"},
			{Role: provider.RoleAssistant, Content: "done"},
			{Role: provider.RoleUser, Content: "next"},
		}
		key := promptCacheKey("ws", BranchID(path), "m")
		stored := CompactionState{
			SchemaVersion:  compactionStateSchemaV4,
			PromptCacheKey: key,
			UpdatedAt:      time.Now().UTC(),
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
				CoveredCount:      len(original),
				CoveredPrefixHash: coveredPrefixHash(original, len(original)),
				PinnedContextHash: pinnedContextCoverageHash(original, len(original)),
			},
		}
		if err := SaveCompactionState(path, stored); err != nil {
			t.Fatal(err)
		}

		buf, restore := tl1651TestCapture(t, slog.LevelDebug)
		defer restore()

		sess, err := LoadSession(path)
		if err != nil {
			t.Fatal(err)
		}
		if sess.normalizedDirty {
			t.Fatal("fixture broken: well-formed rewrite must not need repair")
		}
		a := New(nil, nil, sess, Options{
			SessionPath: path,
			WorkspaceID: "ws",
			ModelRef:    "m",
		}, event.Discard)

		if a.sess.checkpointState == "restored" {
			t.Fatal("a third-party prefix rewrite must stay fail-closed")
		}
		log := buf.String()
		if strings.Contains(log, "load-repaired covered prefix migrated") {
			t.Fatalf("third-party rewrite must never be legalized by migration, log:\n%s", log)
		}
		if !strings.Contains(log, "reason=hash_mismatch") {
			t.Fatalf("expected the tl1651 hash_mismatch drop verdict, log:\n%s", log)
		}
	})

	t.Run("c_first_fingerprint_gate", func(t *testing.T) {
		// 修复形态动了首条（共享首条指纹门）：拒绝迁移。
		pre := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys-v1"},
			{Role: provider.RoleUser, Content: "task"},
		}
		cur := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys-v2-REPAIRED-HEAD"},
			{Role: provider.RoleUser, Content: "task"},
		}
		st := CompactionState{
			SchemaVersion: compactionStateSchemaV4,
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys-v1"}},
				CoveredCount:      len(pre),
				CoveredPrefixHash: coveredPrefixHash(pre, len(pre)),
			},
		}
		if migrateLoadRepairedPrefixHash(&st, cur, pre) {
			t.Fatal("a repair that reshapes the leading message must not migrate")
		}
		if st.Projection.CoveredPrefixHash != coveredPrefixHash(pre, len(pre)) {
			t.Fatal("refused migration must leave the stored hash untouched")
		}
	})

	t.Run("d_stale_sidecar_refused", func(t *testing.T) {
		// sidecar 与盘上修复前字节不同代（更老的一代）：B1 门拒绝。
		pre := tl1651R3InterruptedTranscript()
		cur := NormalizeSession(pre)
		st := CompactionState{
			SchemaVersion: compactionStateSchemaV4,
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}},
				CoveredCount:      len(pre),
				CoveredPrefixHash: coveredPrefixHash(pre[:1], 1), // 另一代的 hash
			},
		}
		if migrateLoadRepairedPrefixHash(&st, cur, pre) {
			t.Fatal("a sidecar from another generation must not migrate")
		}
	})
}
