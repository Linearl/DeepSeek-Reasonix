package agent

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1651 修复④验收矩阵（先写矩阵后落地；lineage_key_changed 观测补全）：
//
//	#     验收项                                                    钉
//	R4-1  key 被拒时丢弃行带逐段 diff（workspace/branch/model 的      TestTL1651R4LineageKeySegmentDiffLogged/rejected_key_segments
//	    stored_*/current_* + changed_segments）
//	R4-2  key 匹配（绿路径）零新增字段、零行为变化                    TestTL1651R4LineageKeySegmentDiffLogged/matching_key_unchanged
//	R4-3  纯函数：单段变化 → changed_segments 精确命名该段            TestTL1651R4LineageKeySegmentDiffLogged/pure_single_segment
//
// 9865/9866 核对结论（进报告，本单不改码）：「effort 不重建」的 per-request
// fast path 在 controller 未附着的 tab（冷载恢复态）与 provider entry 读失败
// 两个出口结构性不可用——tl1653 已给这两个静默出口补了 fast-skipped-* 留痕，
// declined 观测缺口已闭合；effort 切换实际走 rebuild 的机制链（durable
// snapshot 冲突 → recovery fork 重定向 path → lineage key 的 branch 段变化）
// 由 R4-1 的逐段 diff 在下次复现时直接定案。

func TestTL1651R4LineageKeySegmentDiffLogged(t *testing.T) {
	fixture := func(t *testing.T, dir, storedKey string, mutateTranscript bool) *Agent {
		t.Helper()
		path := filepath.Join(dir, "s.jsonl")
		full := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "task"},
			{Role: provider.RoleAssistant, Content: "done"},
		}
		st := CompactionState{
			SchemaVersion:  compactionStateSchemaV4,
			PromptCacheKey: storedKey,
			UpdatedAt:      time.Now().UTC(),
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
				ProjectionVersion: 6,
				CoveredCount:      len(full),
				CoveredPrefixHash: coveredPrefixHash(full, len(full)),
				PinnedContextHash: pinnedContextCoverageHash(full, len(full)),
			},
		}
		if err := SaveCompactionState(path, st); err != nil {
			t.Fatal(err)
		}
		sess := NewSession("sys")
		user := "task"
		if mutateTranscript {
			// 内容改写 → projectionContentValid=false → rebind 不救，落
			// lineage_key_changed 丢弃（任务638：内容有效时 key 变化走 rebind）。
			user = "task-REWRITTEN"
		}
		sess.Add(provider.Message{Role: provider.RoleUser, Content: user})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
		return New(nil, nil, sess, Options{
			SessionPath: path,
			WorkspaceID: "ws",
			ModelRef:    "m",
		}, event.Discard)
	}

	t.Run("rejected_key_segments", func(t *testing.T) {
		dir := t.TempDir()
		// 写入侧 key 带 branch/model 两段旧值；当前 lineage 同 workspace 但
		// branch+model 都变了（sessionPathAfterSnapshot recovery 重定向的形态）。
		storedKey := "ws|old-branch|old-model"
		buf, restore := tl1651TestCapture(t, slog.LevelInfo)
		defer restore()
		fixture(t, dir, storedKey, true)
		log := buf.String()
		if !strings.Contains(log, "reason=lineage_key_changed") {
			t.Fatalf("expected the lineage_key_changed drop, log:\n%s", log)
		}
		for _, field := range []string{
			"stored_workspace=ws", "current_workspace=ws",
			"stored_branch=old-branch", "current_branch=",
			"stored_model=old-model", "current_model=",
			"changed_segments=branch+model",
		} {
			if !strings.Contains(log, field) {
				t.Fatalf("per-segment diff field %q missing, log:\n%s", field, log)
			}
		}
	})

	t.Run("matching_key_unchanged", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "s.jsonl")
		currentKey := promptCacheKey("ws", BranchID(path), "m")
		buf, restore := tl1651TestCapture(t, slog.LevelInfo)
		defer restore()
		a := fixture(t, dir, currentKey, false)
		if a.sess.checkpointState != "restored" {
			t.Fatalf("matching lineage must restore, state=%q log:\n%s", a.sess.checkpointState, buf.String())
		}
		log := buf.String()
		if strings.Contains(log, "stored_workspace") {
			t.Fatalf("green path must carry no diff fields, log:\n%s", log)
		}
		if strings.Contains(log, "lineage_key_changed") {
			t.Fatalf("matching key must not drop, log:\n%s", log)
		}
	})

	t.Run("pure_single_segment", func(t *testing.T) {
		args := lineageKeyDiffArgs("ws|sess-a|model-a", "ws|sess-b|model-a")
		m := map[string]string{}
		for i := 0; i+1 < len(args); i += 2 {
			m[args[i].(string)] = args[i+1].(string)
		}
		if m["changed_segments"] != "branch" {
			t.Fatalf("single branch move must name only branch, got %q (args=%v)", m["changed_segments"], args)
		}
		if m["stored_branch"] != "sess-a" || m["current_branch"] != "sess-b" {
			t.Fatalf("branch segments wrong: %v", m)
		}
		// model 段含 "|" 的 key：SplitN(3) 保证模型 ref 完整保留。
		args2 := lineageKeyDiffArgs("ws|b|gateway/model|x", "ws|b|gateway/model|y")
		m2 := map[string]string{}
		for i := 0; i+1 < len(args2); i += 2 {
			m2[args2[i].(string)] = args2[i+1].(string)
		}
		if m2["stored_model"] != "gateway/model|x" || m2["changed_segments"] != "model" {
			t.Fatalf("model segment with pipes must survive SplitN, got %v", m2)
		}
	})
}
