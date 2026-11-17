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

// tl1651 主修①验收矩阵（先写矩阵后落地）：
//
//	#      验收项                                                钉
//	R1-1   主路径无 sidecar + 备份目录有旧 sidecar → 加载不读备份   TestTL1651R1BackupSidecarNotReadByMainPath/a
//	R1-2   主路径有合法 sidecar → 零行为变化（restored）            TestTL1651R1BackupSidecarNotReadByMainPath/b
//	R1-3   加载侧 sidecar 来源路径日志在场（Debug 锚带 path）        TestTL1651R1SidecarSourcePathLogged
//	R1-4   继承通道刷新 UpdatedAt + 继承留痕日志在场                 TestTL1651R1InheritedSidecarRestampsUpdatedAt
//	R1-5   继承仍守 hash 门（不合法不继承，先例零回退）              TestTL1651R1InheritedSidecarRestampsUpdatedAt/hash-gate
//
// 排查定案（20261117，详见报告 §排查清单）：主加载路径只有 SessionContext
// 派生的单一确定路径（projection.go ContextStatePath），不存在「备份/副本目录
// 回退链」；会话列表单层扫描不进备份目录。「读到年代错乱 sidecar」的实通道=
// 继承链（copyValidContextProjection）保留父会话 UpdatedAt 且零留痕——本文件
// R1-4 钉住修复；R1-1/R1-3 把「只认主路径」从隐式事实升格为断言+可观测。

// tl1651R1StaleBackup writes a pre-repair backup directory beside the session
// path the way backupSessionFiles does: <stem>.pre-repair-<UTC>/ holding a
// copy of the transcript plus the sidecar it was sealed with. The sidecar's
// UpdatedAt is stamped ten days back — the 2026-10-26 incident reading.
func tl1651R1StaleBackup(t *testing.T, sessionPath string, msgs []provider.Message) string {
	t.Helper()
	stem := strings.TrimSuffix(sessionPath, ".jsonl")
	backupDir := stem + ".pre-repair-20261116T064538"
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 备份里的旧转录副本（名字与主转录完全一致——备份目录复制的真实形态）。
	var lines []byte
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(append(lines, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(backupDir, filepath.Base(sessionPath)), lines, 0o644); err != nil {
		t.Fatal(err)
	}
	// 备份里的旧 sidecar：真实写入口（SaveCompactionState 对备份内转录路径派生
	// 备份内 context.json），UpdatedAt 手工回拨十天，模拟「年代错乱」读数。
	backupTranscript := filepath.Join(backupDir, filepath.Base(sessionPath))
	stale := CompactionState{
		SchemaVersion:  compactionStateSchemaV4,
		UpdatedAt:      time.Now().UTC().Add(-10 * 24 * time.Hour),
		PromptCacheKey: promptCacheKey("ws", BranchID(sessionPath), "m"),
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}},
			CoveredCount:      2,
			CoveredPrefixHash: coveredPrefixHash(msgs, 2),
		},
	}
	if err := SaveCompactionState(backupTranscript, stale); err != nil {
		t.Fatal(err)
	}
	return backupDir
}

func TestTL1651R1BackupSidecarNotReadByMainPath(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: "done"},
	}

	t.Run("a_backup_sidecar_invisible_to_main_load", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "s.jsonl")
		backupDir := tl1651R1StaleBackup(t, path, msgs)
		buf, restore := tl1651TestCapture(t, slog.LevelDebug)
		defer restore()

		sess := NewSession("sys")
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
		a := New(nil, nil, sess, Options{
			SessionPath: path,
			WorkspaceID: "ws",
			ModelRef:    "m",
		}, event.Discard)

		if a.sess.checkpointState == "restored" {
			t.Fatal("main path has no sidecar; the stale backup copy must not restore the projection")
		}
		log := buf.String()
		// 加载锚只应落在主路径 context.json 的「不存在」上：任何 sidecar loaded
		// 锚都不得指向备份目录里的文件。
		for _, line := range strings.Split(log, "\n") {
			if strings.Contains(line, "context sidecar loaded") && strings.Contains(line, "pre-repair") {
				t.Fatalf("backup sidecar was read by the main load path: %s", line)
			}
		}
		// 备份目录不得被主加载路径污染：加载后备份内 context.json 仍是加载前的
		// 旧字节（UpdatedAt 未被改写）。
		stalePath := filepath.Join(backupDir, filepath.Base(path))
		stalePath = strings.TrimSuffix(stalePath, ".jsonl") + ".context.json"
		b, err := os.ReadFile(stalePath)
		if err != nil {
			t.Fatal(err)
		}
		var onDisk CompactionState
		if err := json.Unmarshal(b, &onDisk); err != nil {
			t.Fatal(err)
		}
		if !onDisk.UpdatedAt.Before(time.Now().UTC().Add(-9 * 24 * time.Hour)) {
			t.Fatalf("backup sidecar was touched by the main load path: updated_at=%v", onDisk.UpdatedAt)
		}
	})

	t.Run("b_valid_main_sidecar_zero_behavior_change", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "s.jsonl")
		full := append([]provider.Message(nil), msgs...)
		full = append(full, provider.Message{Role: provider.RoleUser, Content: "next"})
		key := promptCacheKey("ws", BranchID(path), "m")
		stored := CompactionState{
			SchemaVersion:  compactionStateSchemaV4,
			PromptCacheKey: key,
			UpdatedAt:      time.Now().UTC().Add(-time.Hour),
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
				CoveredCount:      3,
				CoveredPrefixHash: coveredPrefixHash(full, 3),
				PinnedContextHash: pinnedContextCoverageHash(full, 3),
			},
		}
		if err := SaveCompactionState(path, stored); err != nil {
			t.Fatal(err)
		}
		// 旁边放一个旧备份——合法主 sidecar 在场时行为必须与无备份时完全一致。
		tl1651R1StaleBackup(t, path, msgs)

		sess := NewSession("sys")
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "next"})
		a := New(nil, nil, sess, Options{
			SessionPath: path,
			WorkspaceID: "ws",
			ModelRef:    "m",
		}, event.Discard)

		if a.sess.checkpointState != "restored" {
			t.Fatalf("valid main sidecar must restore, checkpointState=%q", a.sess.checkpointState)
		}
	})
}

func TestTL1651R1SidecarSourcePathLogged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	full := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: "done"},
	}
	key := promptCacheKey("ws", BranchID(path), "m")
	stored := CompactionState{
		SchemaVersion:  compactionStateSchemaV4,
		PromptCacheKey: key,
		UpdatedAt:      time.Now().UTC(),
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
			CoveredCount:      3,
			CoveredPrefixHash: coveredPrefixHash(full, 3),
			PinnedContextHash: pinnedContextCoverageHash(full, 3),
		},
	}
	if err := SaveCompactionState(path, stored); err != nil {
		t.Fatal(err)
	}

	buf, restore := tl1651TestCapture(t, slog.LevelDebug)
	defer restore()

	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	New(nil, nil, sess, Options{
		SessionPath: path,
		WorkspaceID: "ws",
		ModelRef:    "m",
	}, event.Discard)

	want := "path=" + ContextStatePath(path)
	log := buf.String()
	if !strings.Contains(log, "context sidecar loaded") {
		t.Fatalf("load anchor missing entirely, log:\n%s", log)
	}
	if !strings.Contains(log, want) {
		t.Fatalf("load anchor must carry the sidecar SOURCE path %q, log:\n%s", want, log)
	}
}

func TestTL1651R1InheritedSidecarRestampsUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.jsonl")
	childPath := filepath.Join(dir, "child.jsonl")
	shared := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: "done"},
	}

	// 父 sidecar：UpdatedAt 回拨到 2026-10-26 形态（非零旧值——SaveCompactionState
	// 只在零值时填 now，修复前的复制会原样带过去）。
	parentSt := CompactionState{
		SchemaVersion:  compactionStateSchemaV4,
		PromptCacheKey: promptCacheKey("ws", BranchID(parentPath), "m"),
		UpdatedAt:      time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC),
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
			CoveredCount:      3,
			CoveredPrefixHash: coveredPrefixHash(shared, 3),
			PinnedContextHash: pinnedContextCoverageHash(shared, 3),
		},
	}
	if err := SaveCompactionState(parentPath, parentSt); err != nil {
		t.Fatal(err)
	}

	buf, restore := tl1651TestCapture(t, slog.LevelInfo)
	defer restore()

	child := NewSession("sys")
	child.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	child.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	child.Add(provider.Message{Role: provider.RoleUser, Content: "extended tail"})
	ok, err := child.CopyValidContextProjection(parentPath, childPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("matching covered prefix must inherit the parent projection")
	}

	// 钉 R1-4a：继承后的 sidecar 不再携带父会话的 10-26 时间戳。
	var landed CompactionState
	b, err := os.ReadFile(ContextStatePath(childPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &landed); err != nil {
		t.Fatal(err)
	}
	if !landed.UpdatedAt.After(time.Now().UTC().Add(-time.Minute)) {
		t.Fatalf("inherited sidecar UpdatedAt must be restamped at copy time, got %v", landed.UpdatedAt)
	}
	// 钉 R1-4b：继承留痕日志在场（来源→目标可追）。
	log := buf.String()
	if !strings.Contains(log, "context projection inherited from parent session") {
		t.Fatalf("inheritance trail missing, log:\n%s", log)
	}
	if !strings.Contains(log, "source="+parentPath) || !strings.Contains(log, "target="+childPath) {
		t.Fatalf("inheritance trail must name source and target, log:\n%s", log)
	}

	// 钉 R1-5：hash 门先例零回退——covered prefix 不匹配的子会话不继承。
	child2Path := filepath.Join(dir, "child2.jsonl")
	child2 := NewSession("sys")
	child2.Add(provider.Message{Role: provider.RoleUser, Content: "DIFFERENT-HISTORY"})
	child2.Add(provider.Message{Role: provider.RoleAssistant, Content: "other"})
	ok2, err := child2.CopyValidContextProjection(parentPath, child2Path)
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("mismatching covered prefix must not inherit")
	}
	if _, okStat, err := LoadCompactionState(child2Path); err != nil || okStat {
		t.Fatalf("no sidecar may land on a hash-gate-rejected target (ok=%v err=%v)", okStat, err)
	}
}
