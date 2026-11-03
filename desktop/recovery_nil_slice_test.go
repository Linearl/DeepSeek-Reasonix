package main

// 任务 1039r2 回归钉：面板绑定不得把 nil 切片发给前端。
//
// 1036 装机实测（GC开发专员会话）：bridge nil 切片序列化成 JSON null，前端
// 渲染树读 .length 同步抛错，SectionBoundary 接成分区失败横幅「设置加载失败。
// Cannot read properties of null (reading 'length')」。两处根：
//  ① ListRecoveryChains——全部候选的探测/重放都以非限额错误失败时
//     set.Chains 保持 nil（这里用「探测判出 DAG、重放撞未知条目类型硬错误」
//     的 events 日志夹具复现该路径）；
//  ② ListSessionRepairRecordsFor——修复日志不存在（常态）返回 nil, nil。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestListRecoveryChainsChainsNotNilWhenAllCandidatesRefused 钉：候选读不出
// 时，成功响应里 chains 不得是 null（nil 切片直发前端会崩渲染树）。tl1355
// 起读面语义显式更新：候选不再因读不出被跳过（旧硬错误 skip 面），而是保
// 留一行仅文件身份的降级行（任务 1039 的「保留行、计数全零、不重放」语义
// 在内容扫描世界里的统一形态——面板看得见哪一份读不了）；本测试随之改钉
// ①非 null ②降级身份行 ③零读数不产生推荐。
func TestListRecoveryChainsChainsNotNilWhenAllCandidatesRefused(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "sess1.jsonl")
	// 转录本体中段夹一行残缺字节（且无 events 日志可自愈重建）：checkpoint
	// 解码硬错误——严格快照与 tolerant 兜底都读不出（后者同走该解码器），
	// 候选以仅文件身份的降级行保留。
	// （原夹具的「未知条目类型硬错误」在今天的重放里被前向兼容跳过；空
	// checkpoint 还会被自愈重建兜住——中段残缺行才是稳定的双拒源。）
	if err := os.WriteFile(main, []byte("{\"id\":\"m1\",\"role\":\"user\",\"content\":\"hi\"}\n{not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := (&App{}).ListRecoveryChains(main)
	if err != nil {
		t.Fatalf("ListRecoveryChains returned error: %v", err)
	}
	if set.Chains == nil {
		t.Fatal("set.Chains is nil: nil slice serializes to JSON null and crashes the panel render (.length on null)")
	}
	b, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"chains":null`) {
		t.Fatalf("response marshals chains as null: %s", b)
	}
	// 降级行：保留该候选、Degraded 标记；条数由纯行计数填真实值（2 个完整
	// 行，协调线输入①——降级行不再是无读数身份行），轮次/预览不可得。
	if len(set.Chains) != 1 {
		t.Fatalf("the unreadable candidate must stay listed, got %d chains: %s", len(set.Chains), b)
	}
	row := set.Chains[0]
	if !row.Degraded || row.MessageCount != 2 || row.Turns != 0 || row.Preview != "" {
		t.Fatalf("the unreadable candidate must be a degraded row with quick-count readings: %+v", row)
	}
	// 零读数不产生推荐（1019/1123 语义）：更长/更新留空。
	if set.LongestPath != "" || set.NewestPath != "" {
		t.Fatalf("zero-reading rows must not be recommended as longest/newest, got longest=%q newest=%q", set.LongestPath, set.NewestPath)
	}
}

// TestListSessionRepairRecordsForMissingLogReturnsEmptyArray 钉：修复日志不
// 存在（无修复历史的常态）时，绑定层必须回空数组而不是 nil 切片。
func TestListSessionRepairRecordsForMissingLogReturnsEmptyArray(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	dir := filepath.Join(stateHome, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "sess1.jsonl")
	if err := os.WriteFile(main, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := (&App{}).ListSessionRepairRecordsFor(main)
	if err != nil {
		t.Fatalf("ListSessionRepairRecordsFor returned error: %v", err)
	}
	if recs == nil {
		t.Fatal("nil slice reached the bridge: JSON null crashes the panel render (.length on null)")
	}
	b, err := json.Marshal(recs)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("expected empty array, got: %s", b)
	}
}
