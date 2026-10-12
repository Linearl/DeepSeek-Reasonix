package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// 任务 801 Phase 2 固化工具测试。验收标准（方案文档修订六/七 + Phase 2）：
// list 增量与阈值判定；write 结构化条目格式合规、DP 编号自增、5 版本备份轮转、
// 游标推进、源记忆（由 source 实现保证）不被删除。

type fakeSolidifySource struct {
	items []DecisionSolidifyItem
}

func (f fakeSolidifySource) DecisionMemoriesSince(since time.Time) []DecisionSolidifyItem {
	var out []DecisionSolidifyItem
	for _, it := range f.items {
		if it.UpdatedAt.After(since) {
			out = append(out, it)
		}
	}
	return out
}

func writeArgs(action string, patterns ...map[string]string) json.RawMessage {
	in := map[string]any{"action": action}
	if patterns != nil {
		in["patterns"] = patterns
	}
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestDecisionSolidifyListIncrementalAndThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision-patterns.md")
	base := time.Now().UTC().Add(-2 * time.Hour)
	src := fakeSolidifySource{items: []DecisionSolidifyItem{
		{Name: "decision-old-aaa", Title: "old", Body: "body-old", UpdatedAt: base},
		{Name: "decision-new-bbb", Title: "new", Body: "body-new", UpdatedAt: base.Add(time.Hour)},
	}}
	tl := NewDecisionSolidifyTool(src, path, 5, false)
	// 首次 list：无游标 → 全量 2 条，未达阈值 5。
	out, err := tl.Execute(context.Background(), writeArgs("list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var view struct {
		PendingCount int  `json:"pendingCount"`
		Threshold    int  `json:"threshold"`
		ThresholdMet bool `json:"thresholdMet"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if view.PendingCount != 2 || view.Threshold != 5 || view.ThresholdMet {
		t.Fatalf("first list = %+v", view)
	}
	// 固化一批后游标推进：再 list 只剩新条目。
	if _, err := tl.Execute(context.Background(), writeArgs("write", map[string]string{
		"scene": "上游新增机制", "decision": "不自动跟随", "reason": "按我方需求判断", "source": "测试来源",
	})); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err = tl.Execute(context.Background(), writeArgs("list"))
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.PendingCount != 0 {
		t.Fatalf("cursor must exclude solidified memories, pending=%d", view.PendingCount)
	}
}

func TestDecisionSolidifyWriteFormatAndNumbering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision-patterns.md")
	tl := NewDecisionSolidifyTool(fakeSolidifySource{}, path, 5, false)
	if _, err := tl.Execute(context.Background(), writeArgs("write", map[string]string{
		"scene":    "上游新增机制与我方需求冲突时",
		"decision": "不自动跟随，按我方需求独立判断",
		"reason":   "判据是我方需求而非上游动作",
		"source":   "会话 fork开发-新6 压缩窗口 #3 的 3 条记忆",
	})); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := tl.Execute(context.Background(), writeArgs("write", map[string]string{
		"scene": "实验特性引发异常", "decision": "优先怀疑实验特性", "reason": "2026-10-05 六条判据之二", "source": "记忆 decision-x-123",
	})); err != nil {
		t.Fatalf("second write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, "## DP-0001（") || !strings.Contains(content, "## DP-0002（") {
		t.Fatalf("DP numbering must increment: %s", content)
	}
	for _, field := range []string{"- 场景：", "- 决策：", "- 理由：", "- 来源："} {
		if !strings.Contains(content, field) {
			t.Fatalf("entry must carry the four-field structure, missing %q:\n%s", field, content)
		}
	}
	if !strings.Contains(content, "# 决策模式") {
		t.Fatalf("first write must create the file header:\n%s", content)
	}
	if !strings.Contains(content, decisionSolidifyMetaMark) {
		t.Fatalf("cursor meta comment must be present:\n%s", content)
	}
	// 只允许一条游标行。
	if got := strings.Count(content, decisionSolidifyMetaMark); got != 1 {
		t.Fatalf("exactly one cursor meta line expected, got %d", got)
	}
}

func TestDecisionSolidifyBackupRotationFiveVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision-patterns.md")
	tl := NewDecisionSolidifyTool(fakeSolidifySource{}, path, 5, false)
	for i := 0; i < 7; i++ {
		if _, err := tl.Execute(context.Background(), writeArgs("write", map[string]string{
			"scene": "s", "decision": "d", "reason": "r", "source": "src",
		})); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	// 7 次写入 → 当前 + 5 份备份（bak-1 是第 6 版内容，bak-5 是第 2 版内容；
	// 第 1 版被轮转出链）。
	for _, bak := range []string{".bak-1", ".bak-2", ".bak-3", ".bak-4", ".bak-5"} {
		if _, err := os.Stat(path + bak); err != nil {
			t.Fatalf("backup %s must exist after 7 writes: %v", bak, err)
		}
	}
	// bak 内容不重号：bak-1 含 DP-0006，bak-5 含 DP-0002。
	raw1, err := os.ReadFile(path + ".bak-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw1), "## DP-0006") {
		t.Fatalf(".bak-1 must hold the previous generation, got:\n%s", raw1)
	}
	raw5, err := os.ReadFile(path + ".bak-5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw5), "## DP-0002") {
		t.Fatalf(".bak-5 must hold the fifth generation, got:\n%s", raw5)
	}
	if _, err := os.Stat(path + ".bak-6"); !os.IsNotExist(err) {
		t.Fatalf("no .bak-6 may leak: %v", err)
	}
	// 每代文件的编号从该代起点续排：当前文件是 7 次写入后的 DP-0001..0007。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`## DP-(\d{4})`)
	if got := len(re.FindAllString(string(raw), -1)); got != 7 {
		t.Fatalf("current file must hold 7 entries, got %d", got)
	}
}

func TestDecisionSolidifyWriteValidationAndUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision-patterns.md")
	tl := NewDecisionSolidifyTool(fakeSolidifySource{}, path, 5, false)
	if _, err := tl.Execute(context.Background(), writeArgs("write")); err == nil {
		t.Fatal("write without patterns must fail")
	}
	if _, err := tl.Execute(context.Background(), writeArgs("write", map[string]string{
		"decision": "缺场景", "reason": "r", "source": "s",
	})); err == nil {
		t.Fatal("write without scene must fail")
	}
	if _, err := tl.Execute(context.Background(), writeArgs("bogus")); err == nil {
		t.Fatal("unknown action must fail")
	}
	// 未接线（开关未开 → boot 不注册）：报不可用而不是半成功。
	dead := NewDecisionSolidifyTool(nil, "", 5, false)
	if _, err := dead.Execute(context.Background(), writeArgs("list")); err == nil {
		t.Fatal("unwired tool must report unavailable")
	}
}
