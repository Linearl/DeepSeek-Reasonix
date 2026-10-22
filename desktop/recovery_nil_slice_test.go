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

// TestListRecoveryChainsChainsNotNilWhenAllCandidatesRefused 钉：所有候选被
// 非限额错误跳过时，成功响应里 chains 必须是 []（JSON "chains":[]），不得是
// null。
func TestListRecoveryChainsChainsNotNilWhenAllCandidatesRefused(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "sess1.jsonl")
	if err := os.WriteFile(main, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// events 日志：首条是合法 DAG 头（探测判 dag=true），次条是未知条目类型
	// （重放硬错误、非限额）——ListSessionHeads 对该候选报错且不降级。
	events := filepath.Join(dir, "sess1.events.jsonl")
	payload := "{\"schema_version\":2,\"type\":\"log\"}\n{\"schema_version\":2,\"type\":\"bogus-1039r2\"}\n"
	if err := os.WriteFile(events, []byte(payload), 0o644); err != nil {
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
	if !strings.Contains(string(b), `"chains":[]`) {
		t.Fatalf("response should carry an explicit empty chains array, got: %s", b)
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
