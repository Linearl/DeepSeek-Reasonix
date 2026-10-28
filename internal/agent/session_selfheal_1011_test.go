package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 1011：会话自愈单元。检测只读（session_health.go）、修复先备份再动手
// （session_selfheal.go）、健康会话零文件改动、修复可追溯（repairlog）。

// hashSessionTree fingerprints every regular file in the session directory so
// the zero-write acceptance (健康会话零改动) is byte-exact, not mtime-guessy.
func hashSessionTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(blob)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

// selfHealFixture builds a schema-2 session whose selected head is dominated
// by a longer, newer main chain, with a .jsonl checkpoint stuck at the old
// watermark (the ai-passport incident shape in miniature). The main chain
// grows 60 messages past the fork so the watermark gap clears the 50-message
// notice threshold (任务 1010 的阈值，检测器共用).
func selfHealFixture(t *testing.T) (path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "heal.jsonl")
	base := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	// 旧链 10 条（将被选中）。
	for i := 0; i < 10; i++ {
		m := dagMsg(provider.RoleUser, fmt.Sprintf("old-%d", i), fmt.Sprintf("O%d", i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second)))
		parent = m.ID
	}
	// 主链分叉后继续生长：更长（60 条）且更新（+48h）。
	for i := 0; i < 60; i++ {
		m := dagMsg(provider.RoleUser, fmt.Sprintf("new-%d", i), fmt.Sprintf("N%02d", i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(48*time.Hour+time.Duration(i)*time.Second)))
		parent = m.ID
	}
	entries = append(entries,
		sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "old", From: "O9", Kind: HeadKindConcurrent, Writer: "w1", At: base.Add(48 * time.Hour)},
		sessionDAGEntry{Type: sessionDAGTypeSelect, Head: "old", Reason: HeadKindRewind, At: base.Add(48 * time.Hour)},
	)
	dagAppend(t, path, entries...)

	// 物化检查点停在旧水位：只有旧链的 10 条，meta 也停在旧时刻
	// （现场形态：jsonl/meta 停 10-16，events 到 10-19）。
	st := dagReplay(t, path)
	if st.selectedHead() != "old" {
		t.Fatalf("fixture selected = %q, want old", st.selectedHead())
	}
	msgs, _ := st.materialize("old")
	if len(msgs) != 10 {
		t.Fatalf("fixture old chain = %d messages, want 10", len(msgs))
	}
	if err := writeJSONLCheckpointAtomic(path, msgs, nil); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.UpdatedAt = base.Add(9 * time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func Test1011HealthFlagsHeadLagAndWatermarkGap(t *testing.T) {
	path := selfHealFixture(t)
	report := InspectSessionHealth(path, HealthInspectOptions{})
	if report.Healthy {
		t.Fatalf("dominated fixture must be unhealthy: %+v", report)
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	if !codes[HealthHeadLag] {
		t.Fatalf("want head_lag issue, got %+v", report.Issues)
	}
	if !codes[HealthWatermarkLag] {
		t.Fatalf("want watermark_lag issue, got %+v", report.Issues)
	}
	if report.SuggestedHeadID != SessionMainHead {
		t.Fatalf("suggested head = %q, want main (longer and newer)", report.SuggestedHeadID)
	}
	if report.SelectedHeadID != "old" {
		t.Fatalf("selected head = %q, want old", report.SelectedHeadID)
	}
}

func Test1011HealthySessionInspectAndHealTouchNothing(t *testing.T) {
	path := selfHealFixture(t)
	// 先把会话修健康：head 重选 + 补水。
	if _, err := SelfHealSession(path, SelfHealOptions{PromoteHead: true, Hydrate: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	before := hashSessionTree(t, dir)

	report := InspectSessionHealth(path, HealthInspectOptions{})
	if !report.Healthy {
		t.Fatalf("repaired session must inspect healthy: %+v", report.Issues)
	}
	heal, err := SelfHealSession(path, SelfHealOptions{PromoteHead: true, Hydrate: true, Diet: true})
	if err != nil {
		t.Fatal(err)
	}
	if heal.Repaired {
		t.Fatalf("healthy session must not be repaired: %+v", heal)
	}
	if heal.BackupDir != "" {
		t.Fatalf("healthy session must not grow a backup dir: %q", heal.BackupDir)
	}
	after := hashSessionTree(t, dir)
	for name, sum := range before {
		if after[name] != sum {
			t.Fatalf("healthy session file %s changed (验收硬条目: 健康会话零改动)", name)
		}
	}
	if len(after) != len(before) {
		t.Fatalf("healthy session grew new files: before=%d after=%d", len(before), len(after))
	}
}

func Test1011SelfHealPromotesHeadAndHydratesCheckpoint(t *testing.T) {
	path := selfHealFixture(t)
	report, err := SelfHealSession(path, SelfHealOptions{PromoteHead: true, Hydrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatal("expected a repair")
	}
	if report.HeadTo != SessionMainHead || report.HeadFrom != "old" {
		t.Fatalf("head switch = %s -> %s, want old -> main", report.HeadFrom, report.HeadTo)
	}
	// 补水后：jsonl 行数 == 主链长度，水位差消失。
	st := dagReplay(t, path)
	mainLen := len(st.chainIDs(SessionMainHead))
	materialized, err := countJSONLLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if materialized != mainLen {
		t.Fatalf("hydrated checkpoint lines = %d, want %d (水位差根治)", materialized, mainLen)
	}
	// 备份目录存在且含原 .jsonl。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	backupSeen, logSeen := false, false
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, selfHealBackupSuffix) {
			backupSeen = true
		}
		if strings.HasSuffix(name, selfHealRepairLogSuffix) {
			logSeen = true
		}
	}
	if !backupSeen {
		t.Fatal("repair must leave a backup dir")
	}
	if !logSeen {
		t.Fatal("repair must leave a repair log")
	}
	records, err := ListSessionRepairRecordsFor(path)
	if err != nil || len(records) != 1 {
		t.Fatalf("repair records = %d err=%v, want 1", len(records), err)
	}
	if records[0].BackupDir == "" || records[0].After != "healthy" {
		t.Fatalf("repair record incomplete: %+v", records[0])
	}
	// 复检健康。
	if got := InspectSessionHealth(path, HealthInspectOptions{}); !got.Healthy {
		t.Fatalf("post-heal inspection must be healthy: %+v", got.Issues)
	}
}

func Test1011SelfHealDietStripsImagesAndFolds(t *testing.T) {
	t.Skip("tl1227 DAG 读退役: selfheal diet 读回经已删读面；存量 DAG 日志由 MigrateSessionDAGLog 一次性迁移为 schema-1，读侧单路径化后本场景不再可达")
	path := filepath.Join(t.TempDir(), "diet.jsonl")
	// 一条链：一条巨大图片消息 + 大量旧文本 + 几条新消息。
	base := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	bigImage := "data:image/png;base64," + strings.Repeat("A", 2<<20) // 2 MiB
	m := dagMsg(provider.RoleUser, "look", "IMG1")
	m.Images = []string{bigImage}
	entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base))
	parent = m.ID
	for i := 0; i < 40; i++ {
		m := dagMsg(provider.RoleAssistant, strings.Repeat("x", 40000), fmt.Sprintf("F%d", i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i+1)*time.Second)))
		parent = m.ID
	}
	for i := 0; i < 3; i++ {
		m := dagMsg(provider.RoleUser, fmt.Sprintf("tail-%d", i), fmt.Sprintf("T%d", i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Hour+time.Duration(i)*time.Second)))
		parent = m.ID
	}
	dagAppend(t, path, entries...)

	const tinyWindow = 200_000 // 远小于 40 条大文本 + 2MiB 图
	before := InspectSessionHealth(path, HealthInspectOptions{WindowTokens: tinyWindow})
	if before.Healthy {
		t.Fatalf("fixture must overflow: %+v", before)
	}
	report, err := SelfHealSession(path, SelfHealOptions{Diet: true, WindowTokens: tinyWindow})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Repaired {
		t.Fatal("expected repair")
	}
	status := map[string]string{}
	for _, a := range report.Actions {
		status[a.Action] = a.Status
	}
	if status[HealActionStripImages] != "done" {
		t.Fatalf("strip_images must run: %+v", report.Actions)
	}
	if status[HealActionFold] != "done" {
		t.Fatalf("fold must run: %+v", report.Actions)
	}
	after := InspectSessionHealth(path, HealthInspectOptions{WindowTokens: tinyWindow})
	if !after.Healthy {
		t.Fatalf("diet must bring the projection under the window: %+v", after.Issues)
	}
	// 剥图占位与折叠占位可读。
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Snapshot()
	foundFold := false
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "已省略") && strings.Contains(msg.Content, "未调用模型") {
			foundFold = true
		}
		for _, img := range msg.Images {
			if strings.HasPrefix(img, "data:") {
				t.Fatalf("embedded image survived the diet: %d bytes", len(img))
			}
			if strings.Contains(img, "original in repair backup") {
				foundFold = true // stripped marker present
			}
		}
	}
	if !foundFold {
		t.Fatalf("fold placeholder or strip marker missing in %d messages", len(msgs))
	}
}

func Test1011StripImagesKeepsRemoteRefs(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "mix", Images: []string{"data:image/jpeg;base64,QQ==", "https://example.com/i.png", "file-api-123"}},
	}
	out := stripMessageImages(msgs)
	if len(out[0].Images) != 3 {
		t.Fatalf("images count changed: %v", out[0].Images)
	}
	if !strings.HasPrefix(out[0].Images[0], "[image: embedded payload stripped") {
		t.Fatalf("data URL not stripped: %q", out[0].Images[0])
	}
	if out[0].Images[1] != "https://example.com/i.png" || out[0].Images[2] != "file-api-123" {
		t.Fatalf("remote refs must stay untouched: %v", out[0].Images)
	}
	// 原输入未被就地修改。
	if msgs[0].Images[0] != "data:image/jpeg;base64,QQ==" {
		t.Fatal("stripMessageImages must not mutate its input")
	}
}

func Test1011FoldProjectionTailKeepsNewest(t *testing.T) {
	msgs := make([]provider.Message, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: strings.Repeat("z", 4000), ID: fmt.Sprintf("m%d", i)})
	}
	// 预算只够 ~5 条。
	folded, count := foldProjectionTail(msgs, 5000)
	if count <= 0 || count >= len(msgs) {
		t.Fatalf("fold count = %d, want between 1 and %d", count, len(msgs)-1)
	}
	if len(folded) != len(msgs)-count+1 {
		t.Fatalf("folded length = %d, want %d", len(folded), len(msgs)-count+1)
	}
	if !strings.Contains(folded[0].Content, "已省略") {
		t.Fatalf("placeholder missing: %q", folded[0].Content)
	}
	// 折叠后尾部必须是原最新消息（UI 显示最新）。
	if folded[len(folded)-1].ID != "m9" {
		t.Fatalf("newest message lost in fold: tail=%+v", folded[len(folded)-1])
	}
	// 预算充足时不动。
	if same, c := foldProjectionTail(msgs, 1<<30); c != 0 || len(same) != len(msgs) {
		t.Fatalf("generous budget must not fold: count=%d", c)
	}
}

func Test1011LoadTimeoutCriterionFires(t *testing.T) {
	path := selfHealFixture(t)
	report := InspectSessionHealthContext(context.Background(), path, HealthInspectOptions{LoadTimeout: 1 * time.Nanosecond})
	// 纳秒预算下重放必然超时（或极小概率跑完——两种都合法，但超时路径必须
	// 报 load_timeout 而不是崩溃）。
	if !report.Healthy {
		found := false
		for _, issue := range report.Issues {
			if issue.Code == HealthLoadTimeout {
				found = true
			}
		}
		if !found {
			t.Fatalf("timeout must surface as load_timeout: %+v", report.Issues)
		}
	}
}

func Test1011SessionFileStem(t *testing.T) {
	cases := map[string]string{
		"/x/dag.jsonl":                "dag",
		"/x/dag.jsonl.meta":           "dag",
		"/x/dag.events.jsonl":         "dag",
		"/x/dag.events.jsonl.damaged": "dag",
		"/x/dag.repairlog.jsonl":      "dag",
		"/x/dag.context.json":         "dag",
		"/x/dag.pre-repair-20261019":  "dag.pre-repair-20261019",
	}
	for in, want := range cases {
		if got := sessionFileStem(in); got != want {
			t.Fatalf("sessionFileStem(%q) = %q, want %q", in, got, want)
		}
	}
}
