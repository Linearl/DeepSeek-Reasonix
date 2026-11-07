package main

// tl1449 回归钉:清空回收站的「多桶+脏形态」组合面。
// 症状②③的后端机制:.trash 里凡是 listTrashedSessionFiles 提取不到记录的桶
// (空壳桶=meta 在 transcript 消失、无 meta 双 transcript 等),既不进 UI 列表、
// 也进不了逐条清空,清空完成后永远躺在磁盘上——UI 计数与磁盘项数从此对不上
// (症状③),「清空了但磁盘还有几十项」的观感即由此而来(症状②;用户当场那轮
// 清空整轮未执行的机制=ManagementSurface 看门狗误杀,前端侧另一钉)。
// 契约:.trash 每个含 transcript 形状条目的桶必须恰好出一条记录;模拟清空
// (对列表逐条 PurgeTrashedSession,与前端 mutate 同构)后 .trash 归零。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

func tl1449StageBucket(t *testing.T, dir, name, meta string, transcripts []string, extraFiles map[string]string) string {
	t.Helper()
	itemDir := filepath.Join(dir, sessionTrashDir, name)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatalf("mkdir bucket %s: %v", name, err)
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(itemDir, sessionTrashMetaFile), []byte(meta), 0o644); err != nil {
			t.Fatalf("write meta %s: %v", name, err)
		}
	}
	for _, key := range transcripts {
		if err := os.WriteFile(filepath.Join(itemDir, key), []byte("turn"), 0o600); err != nil {
			t.Fatalf("write transcript %s in %s: %v", key, name, err)
		}
	}
	for fileName, content := range extraFiles {
		if err := os.WriteFile(filepath.Join(itemDir, fileName), []byte(content), 0o600); err != nil {
			t.Fatalf("write extra %s in %s: %v", fileName, name, err)
		}
	}
	return itemDir
}

func TestClearTrashMixedDirtyShapesListsAndPurgesEveryBucket(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	stamp := time.Now().Format("20060102T150405.000000000")
	buckets := map[string]string{
		// 正常桶:meta 完整 + transcript + sidecar 残留文件(purge 须整桶带走)。
		"good": tl1449StageBucket(t, dir, "good-"+stamp, `{"key":"k-good.jsonl","deletedAt":1}`, []string{"k-good.jsonl"}, map[string]string{"k-good.jsonl.lock": ""}),
		// 无 meta 桶(崩溃于 artifact move 与 meta write 之间,tl1429b 已救)。
		"metaless": tl1449StageBucket(t, dir, "metaless-"+stamp, "", []string{"k-metaless.jsonl"}, nil),
		// 损坏 meta:非法 JSON,须与无 meta 同路兜底。
		"corrupt": tl1449StageBucket(t, dir, "corrupt-"+stamp, "{not json", []string{"k-corrupt.jsonl"}, nil),
		// meta key 无效:合法 JSON 但 key 不是 transcript 名。
		"invalid": tl1449StageBucket(t, dir, "invalid-"+stamp, `{"key":"not-a-transcript"}`, []string{"k-invalid.jsonl"}, nil),
		// 空壳桶:meta 在、transcript 已消失(restore/purge 半途失败的中间态)。
		"shell": tl1449StageBucket(t, dir, "shell-"+stamp, `{"key":"k-shell.jsonl","deletedAt":2}`, nil, nil),
		// 无 meta 双 transcript 桶:兜底「恰 1」放行的盲区形态。
		"dual": tl1449StageBucket(t, dir, "dual-"+stamp, "", []string{"k-dual-a.jsonl", "k-dual-b.jsonl"}, nil),
		// recovery 形状副本桶(tl1429d 识别面):无母本时 coverage 证明必败,
		// UI 按普通桶清,本钉顺带覆盖。
		"recopy": tl1449StageBucket(t, dir, "recopy-"+stamp, `{"key":"k-recovery-abcdef1234567890.jsonl","deletedAt":3}`, []string{"k-recovery-abcdef1234567890.jsonl"}, nil),
	}

	app := NewApp()
	listed := app.ListTrashedSessions()
	// 每桶恰一条记录(7 桶→7 条);shell/dual 曾被静默漏掉。
	covered := map[string]bool{}
	for _, meta := range listed {
		bucketName := filepath.Base(filepath.Dir(meta.Path))
		for name := range buckets {
			if strings.HasPrefix(bucketName, name+"-") {
				covered[name] = true
			}
		}
	}
	for name := range buckets {
		if !covered[name] {
			t.Errorf("bucket %s has no list record — 不可见即不可清,清空后必残留(症状③的计数差)", name)
		}
	}
	if len(listed) != len(buckets) {
		t.Errorf("ListTrashedSessions = %d records for %d buckets, want one per bucket", len(listed), len(buckets))
	}

	// 模拟 UI 清空:对列表逐条 purge(与前端 trashOperations.purgeTrashBatch 同构)。
	for _, meta := range listed {
		if err := app.PurgeTrashedSession(meta.Path); err != nil {
			t.Errorf("PurgeTrashedSession(%s) = %v, want success", filepath.Base(meta.Path), err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, sessionTrashDir))
	if err != nil {
		t.Fatalf("read trash root: %v", err)
	}
	if len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("trash still holds %d buckets after clearing all listed records: %v — 症状②的磁盘残留面", len(entries), names)
	}
}
