package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// tl1793r2 写面钉：recovery 副本出生即继承原对话标题。20261125 现场：回收站
// 恢复的副本在版本管理显示文件字符串而回收站族渲染出中文名——副本创建时
// 桌面回调的 tab 标题为空（旧包产物形态），副本 meta 两级标题全空，显示链
// 只剩 stem 兜底。写面根治 = prepareRecoveryBranchMetaLocked 按显示链同序
// （custom_title 先、topic_title 后）从原对话 meta 补继承，只填空位。

func writeBranchMetaTitles(t *testing.T, transcriptPath, customTitle, topicTitle string) {
	t.Helper()
	meta := BranchMeta{ID: BranchID(transcriptPath)}
	if customTitle != "" {
		meta.CustomTitle = customTitle
	}
	if topicTitle != "" {
		meta.TopicTitle = topicTitle
	}
	if err := saveBranchMeta(transcriptPath, meta, false); err != nil {
		t.Fatalf("save parent meta: %v", err)
	}
}

// 写面①：副本 meta 无标题而原对话有 topic_title——继承落位（真机缺失面）。
func TestRecoveryBranchMetaInheritsParentTopicTitle(t *testing.T) {
	dir := t.TempDir()
	originalPath := filepath.Join(dir, "original.jsonl")
	writeBranchMetaTitles(t, originalPath, "", "Heartbeat:玫瑰窃贼完整版")

	recovery := NewSession("sys")
	recovery.Add(provider.Message{Role: provider.RoleUser, Content: "local question"})
	info, err := recovery.SaveShutdownRecoveryBranch(RecoveryBranchOptions{OriginalPath: originalPath})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok, err := LoadBranchMeta(info.Path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.TopicTitle != "Heartbeat:玫瑰窃贼完整版" {
		t.Fatalf("copy must inherit the parent topic_title at birth: got %q", meta.TopicTitle)
	}
}

// 写面②优先级与覆盖面：原对话 custom_title+topic_title 并存、副本全空——
// 两级都按显示链同序继承；后续重写（existing ledger 路径）不漂移。
func TestRecoveryBranchMetaInheritsBothTitleFieldsInChainOrder(t *testing.T) {
	dir := t.TempDir()
	originalPath := filepath.Join(dir, "original.jsonl")
	writeBranchMetaTitles(t, originalPath, "qwen部署", "旧自动标题")

	recovery := NewSession("sys")
	recovery.Add(provider.Message{Role: provider.RoleUser, Content: "local question"})
	info, err := recovery.SaveShutdownRecoveryBranch(RecoveryBranchOptions{OriginalPath: originalPath})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok, err := LoadBranchMeta(info.Path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.CustomTitle != "qwen部署" || meta.TopicTitle != "旧自动标题" {
		t.Fatalf("copy must inherit both fields: custom=%q topic=%q", meta.CustomTitle, meta.TopicTitle)
	}
}

// 写面③只填空位：回调已带 topic_title（tabSessionRecoveryMeta 正常态）时不被
// 父对话字段覆盖——继承永不覆盖已有值。
func TestRecoveryBranchMetaKeepsCallerSuppliedTitles(t *testing.T) {
	dir := t.TempDir()
	originalPath := filepath.Join(dir, "original.jsonl")
	writeBranchMetaTitles(t, originalPath, "父对话改名", "父对话自动题")

	recovery := NewSession("sys")
	recovery.Add(provider.Message{Role: provider.RoleUser, Content: "local question"})
	info, err := recovery.SaveShutdownRecoveryBranch(RecoveryBranchOptions{
		OriginalPath: originalPath,
		BranchMeta:   BranchMeta{TopicTitle: "tab 带来的标题"},
	})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok, err := LoadBranchMeta(info.Path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.TopicTitle != "tab 带来的标题" {
		t.Fatalf("caller-supplied topic_title must survive: got %q", meta.TopicTitle)
	}
	if meta.CustomTitle != "父对话改名" {
		t.Fatalf("empty copy custom_title must inherit the parent rename: got %q", meta.CustomTitle)
	}
}

// 写面④原对话无 meta/无标题——继承放弃、副本照常落盘（救援写不被阻塞）。
func TestRecoveryBranchMetaWithoutParentTitlesStaysClean(t *testing.T) {
	dir := t.TempDir()
	originalPath := filepath.Join(dir, "original.jsonl")

	recovery := NewSession("sys")
	recovery.Add(provider.Message{Role: provider.RoleUser, Content: "local question"})
	info, err := recovery.SaveShutdownRecoveryBranch(RecoveryBranchOptions{OriginalPath: originalPath})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok, err := LoadBranchMeta(info.Path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta ok=%v err=%v", ok, err)
	}
	if meta.CustomTitle != "" || meta.TopicTitle != "" {
		t.Fatalf("no parent titles to inherit: custom=%q topic=%q", meta.CustomTitle, meta.TopicTitle)
	}
	if !LooksLikeRecoveryFilename(info.Path) {
		t.Fatalf("recovery copy path shape drifted: %q", info.Path)
	}
}
