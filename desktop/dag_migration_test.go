package main

// tl1227: DAG 迁移动作卡的后端纯逻辑单测。
//
// 只测"不必真的跑迁移"的部分：快照树识别、目录收集的分类（活跃会话 vs 自愈
// 快照）、标签截断。真正的迁移路径由 internal/agent.MigrateSessionDAGLog 的
// 既有测试覆盖，这里不重复造第二个真相源。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDagMigrationIsSnapshot(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		path string
		want bool
	}{
		{"proj" + sep + "sessions" + sep + "20261022-074436-collab.jsonl", false},
		{"proj" + sep + "sessions" + sep + "20261022-074436-collab.events.jsonl", false},
		// 修复链生成的全量快照目录：任何一段带 .pre-repair- 都算。
		{"proj" + sep + "sessions" + sep + "20261022-074436-collab.pre-repair-20261030T144135" + sep + "s.jsonl", true},
		{"pre-repair-x" + sep + "s.jsonl", true},
	}
	for _, c := range cases {
		if got := dagMigrationIsSnapshot(c.path); got != c.want {
			t.Errorf("dagMigrationIsSnapshot(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestDagMigrationCollectClassifies 证明"备份树不进迁移面"这条纪律在收集阶段
// 就生效——否则每次 apply 都会改写修复的回滚凭据。
func TestDagMigrationCollectClassifies(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "proj", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 两个真实会话
	write(filepath.Join(sessions, "s1.events.jsonl"))
	write(filepath.Join(sessions, "s2.events.jsonl"))
	// 一个自愈快照树（同名会话的修复前副本）
	snapshot := filepath.Join(sessions, "s1.pre-repair-20261030T144135")
	write(filepath.Join(snapshot, "s1.events.jsonl"))
	// 非 events 文件不该被收
	write(filepath.Join(sessions, "s1.turns.jsonl"))

	targets, backups, warnings, err := dagMigrationCollect(root)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected walk warnings: %v", warnings)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d (%v), want 2", len(targets), targets)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %d (%v), want 1 (the pre-repair snapshot)", len(backups), backups)
	}
	// 收集结果必须是 transcript 路径（去掉 .events 后缀），迁移 API 由它再派生
	// 侧车。
	for _, p := range targets {
		if strings.HasSuffix(p, ".events.jsonl") {
			t.Errorf("target %q must be the transcript path, not the events log", p)
		}
		if !strings.HasSuffix(p, ".jsonl") {
			t.Errorf("target %q must end in .jsonl", p)
		}
	}
}

func TestDagMigrationCollectMissingRoot(t *testing.T) {
	_, _, _, err := dagMigrationCollect(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected an error for a missing projects root")
	}
}

func TestDagMigrationLabelTruncates(t *testing.T) {
	// 正好 32 字符：不截断（阀值是 > 32）。
	const exact32 = "20261022-074436.918398400-collab"
	if len(exact32) != 32 {
		t.Fatalf("fixture drifted: len = %d, want 32", len(exact32))
	}
	if got := dagMigrationLabel("/x/sessions/" + exact32 + ".jsonl"); got != exact32 {
		t.Errorf("label = %q, want the untruncated 32-char name", got)
	}
	if got := dagMigrationLabel("/x/sessions/" + exact32 + "-extra.jsonl"); got != exact32+"~" {
		t.Errorf("label = %q, want %q", got, exact32+"~")
	}
	if got := dagMigrationLabel("/x/sessions/short.jsonl"); got != "short" {
		t.Errorf("label = %q, want %q", got, "short")
	}
}

func TestDagMigrationFirstLine(t *testing.T) {
	if got := dagMigrationFirstLine("boom\nstack line 1\nstack line 2"); got != "boom" {
		t.Errorf("firstLine = %q, want %q", got, "boom")
	}
	if got := dagMigrationFirstLine("single"); got != "single" {
		t.Errorf("firstLine = %q, want %q", got, "single")
	}
}
