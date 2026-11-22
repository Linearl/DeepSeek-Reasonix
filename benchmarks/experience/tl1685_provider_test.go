package main

// tl1685 ① 回归钉：首响按 provider 分层。ledger 本身不带 model 信息
// （eventwire.Event 无 modelRef），provider 取自会话 meta 侧车
// （<session>.jsonl.meta 的 model 字段，canonical "provider/model"）——
// 与 internal/agent.SetBranchModelPreserveUpdated 写入的同一份文件。
// 缺 meta / 无 model / 损坏一律归 "unknown"，不得阻断首响扫描。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeProviderFixture(t *testing.T, dir, ledger, metaJSON string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ledger), []byte(strings.Join([]string{
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"turn_started","status":"in_progress","createdAt":%d}`, 1750000000000),
		fmt.Sprintf(`{"recordType":"event","schemaVersion":2,"turnId":"t1","kind":"text","status":"in_progress","createdAt":%d}`, 1750000031000),
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if metaJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, strings.ReplaceAll(ledger, ".turns.jsonl", ".jsonl")+".meta"), []byte(metaJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionProviderOf(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "a.turns.jsonl")
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl.meta"), []byte(`{"id":"a","model":"mimo-api/mimo-v2.5-pro"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sessionProviderOf(ledger); got != "mimo-api" {
		t.Fatalf("provider = %q，want mimo-api（canonical ref 前缀）", got)
	}
	// 无 "/" 的裸 model：整串即 provider。
	if err := os.WriteFile(filepath.Join(dir, "b.jsonl.meta"), []byte(`{"id":"b","model":"legacy-model"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sessionProviderOf(filepath.Join(dir, "b.turns.jsonl")); got != "legacy-model" {
		t.Fatalf("provider = %q，want legacy-model", got)
	}
	// meta 无 model 字段 / meta 缺失 / 损坏：一律 unknown。
	if err := os.WriteFile(filepath.Join(dir, "c.jsonl.meta"), []byte(`{"id":"c"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d.jsonl.meta"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"c", "d", "e"} {
		if got := sessionProviderOf(filepath.Join(dir, name+".turns.jsonl")); got != providerUnknown {
			t.Fatalf("%s provider = %q，want unknown（不得阻断扫描）", name, got)
		}
	}
}

func TestScanLedgersAttachesProvider(t *testing.T) {
	dir := t.TempDir()
	writeProviderFixture(t, dir, "mimo.turns.jsonl", `{"id":"m","model":"mimo-api/mimo-v2.5-pro"}`)
	writeProviderFixture(t, dir, "ds.turns.jsonl", `{"id":"d","model":"deepseek/deepseek-v4"}`)
	writeProviderFixture(t, dir, "bare.turns.jsonl", "")
	now := time.UnixMilli(1750000360000)
	scan, err := scanLedgers([]string{dir}, 72*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Turns) != 3 {
		t.Fatalf("turns = %d，want 3", len(scan.Turns))
	}
	got := map[string]string{}
	for _, tn := range scan.Turns {
		got[tn.Session] = tn.Provider
	}
	if got["mimo.turns.jsonl"] != "mimo-api" || got["ds.turns.jsonl"] != "deepseek" || got["bare.turns.jsonl"] != providerUnknown {
		t.Fatalf("provider 归属不符：%v", got)
	}
}

func TestProviderStratificationRows(t *testing.T) {
	turns := []turnSample{
		{Session: "a", TurnID: "1", MS: 10000, Provider: "mimo-api"},
		{Session: "a", TurnID: "2", MS: 20000, Provider: "mimo-api"},
		{Session: "b", TurnID: "3", MS: 5000, Provider: "deepseek"},
		{Session: "c", TurnID: "4", MS: 8000, Provider: ""}, // 空值归 unknown
	}
	rows := providerStratificationRows(turns)
	if len(rows) != 3 {
		t.Fatalf("行数 = %d，want 3（mimo-api/deepseek/unknown 各一行）：%+v", len(rows), rows)
	}
	// 多者在前：mimo-api(n=2) 第一行。
	if rows[0].Metric != "发消息→首响 · provider=mimo-api（分层观测）" || rows[0].N != 2 || rows[0].P95MS != 20000 {
		t.Fatalf("第一行不符：%+v", rows[0])
	}
	for _, r := range rows {
		if r.Verdict != verdictObserve || r.Bench != "first-response" {
			t.Fatalf("分层行应 OBSERVE 且挂 first-response：%+v", r)
		}
		if !strings.Contains(r.Note, "会话级归属") {
			t.Fatalf("分层行应明示会话级归属粒度：%+v", r)
		}
	}
	// 全 unknown 也出分层行（老会话为主的窗口仍可跨期对比）。
	rows = providerStratificationRows([]turnSample{{TurnID: "x", MS: 9000}})
	if len(rows) != 1 || !strings.Contains(rows[0].Metric, providerUnknown) {
		t.Fatalf("unknown 桶应出 1 行：%+v", rows)
	}
	// 无样本不出行。
	if got := providerStratificationRows(nil); got != nil {
		t.Fatalf("无样本应不出行：%+v", got)
	}
}
