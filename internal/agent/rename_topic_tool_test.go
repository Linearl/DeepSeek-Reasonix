package agent

// 任务 949：rename_topic 工具验收——按 topic_id/contact_id 定址改名，钉住三条：
// ①改名不换 contact_id/topic_id（453 先例）②目录/列表可见新名 ③返回体带
// 旧名+新名+引用同步提示。宿主写入用 desktop 同款的 agent 侧 sidecar 写
// （RenameSession 写 CustomTitle，目录读 Custom标题优先——任务780 同源），
// 所以「list 可见新名」是真扫描断言，不是桩回显。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

func seedRenameTopicSession(t *testing.T, dir, name, topicID, contactID, title string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeEmpty(t, path)
	if err := UpdateBranchMeta(path, false, func(m *BranchMeta) error {
		m.TopicID = topicID
		m.ContactID = contactID
		m.TopicTitle = title
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

// renameTopicHost 是 desktop renameCollabSession 的 agent 侧等价桩：写
// CustomTitle sidecar（目录读它优先），其余（topic 标题库/侧栏事件）属宿主面。
func renameTopicHost(t *testing.T, topicID, sessionPath, title string) (RenameSessionResult, error) {
	t.Helper()
	if err := RenameSession(sessionPath, title); err != nil {
		return RenameSessionResult{}, err
	}
	return RenameSessionResult{TopicID: topicID, SessionPath: sessionPath, Title: title, ListingSynced: true}, nil
}

func renameTopicFixture(t *testing.T) (SessionCollabConfig, string) {
	t.Helper()
	dir := t.TempDir()
	seedRenameTopicSession(t, dir, "dev1.jsonl", "topic-dev1", "ct_dev1", "开发-1-工具引擎")
	seedRenameTopicSession(t, dir, "audit4.jsonl", "topic-audit4", "ct_audit4", "审计员-4-通用")
	return SessionCollabConfig{Enabled: true, SessionDir: dir, WorkspaceRoot: dir}, dir
}

// findIdentityByContact 读的就是 list_addressable_sessions 的数据源
// （scanAddressable），所以这里的 title 断言 = 「list 可见新名」。
func findIdentityByContact(t *testing.T, dir, contactID string) (sessioncollab.Identity, bool) {
	t.Helper()
	for _, id := range scanAddressable(dir, dir) {
		if id.ContactID == contactID {
			return id, true
		}
	}
	return sessioncollab.Identity{}, false
}

// 949 验收核心：按 contact_id 改名后，contact_id/topic_id/sessionPath 三个
// 定址键逐字节不变，目录里可见的是新名，旧名消失；返回体带旧名+新名。
func TestRenameTopicToolRenamesByContactIDAndKeepsIDs(t *testing.T) {
	cfg, dir := renameTopicFixture(t)

	before, ok := findIdentityByContact(t, dir, "ct_dev1")
	if !ok {
		t.Fatal("fixture session ct_dev1 missing from the directory")
	}
	if before.Title != "开发-1-工具引擎" {
		t.Fatalf("before title = %q", before.Title)
	}

	host := func(topicID, sessionPath, title string) (RenameSessionResult, error) {
		return renameTopicHost(t, topicID, sessionPath, title)
	}
	tool := NewRenameTopicTool(cfg, host)
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"contact_id":"ct_dev1","title":"开发专员-工具引擎"}`))
	if err != nil {
		t.Fatalf("rename_topic: %v", err)
	}

	// ① ID 不变断言：改名前后的定址键完全一致。
	after, ok := findIdentityByContact(t, dir, "ct_dev1")
	if !ok {
		t.Fatal("contact_id ct_dev1 disappeared after the rename")
	}
	if after.ContactID != before.ContactID || after.TopicID != before.TopicID || after.SessionPath != before.SessionPath {
		t.Fatalf("ID changed on rename:\nbefore %+v\nafter  %+v", before, after)
	}
	// ② title 更新断言：目录（list_addressable_sessions 的数据源）可见新名。
	if after.Title != "开发专员-工具引擎" {
		t.Fatalf("directory title = %q, want 开发专员-工具引擎", after.Title)
	}

	// list_addressable_sessions 真跑一遍：按新名能查到、按旧名查不到。
	list := NewListAddressableSessionsTool(cfg)
	page, err := list.Execute(context.Background(), json.RawMessage(`{"query":"开发专员-工具引擎"}`))
	if err != nil {
		t.Fatalf("list by new title: %v", err)
	}
	if !strings.Contains(page, "ct_dev1") {
		t.Fatalf("list by new title must show the renamed session, got %s", page)
	}
	oldPage, err := list.Execute(context.Background(), json.RawMessage(`{"query":"开发-1-工具引擎"}`))
	if err != nil {
		t.Fatalf("list by old title: %v", err)
	}
	if strings.Contains(oldPage, "ct_dev1") {
		t.Fatalf("list by OLD title must not return the renamed session, got %s", oldPage)
	}

	// ③ 旧名新名返回断言 + 引用同步提示。
	var payload struct {
		Status string              `json:"status"`
		Result RenameSessionResult `json:"result"`
		Note   string              `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.Status != "renamed" {
		t.Fatalf("status = %q, want renamed", payload.Status)
	}
	if payload.Result.Previous != "开发-1-工具引擎" {
		t.Fatalf("previousTitle = %q, want the OLD title", payload.Result.Previous)
	}
	if payload.Result.Title != "开发专员-工具引擎" {
		t.Fatalf("title = %q, want the NEW title", payload.Result.Title)
	}
	if payload.Result.ContactID != "ct_dev1" || payload.Result.TopicID != "topic-dev1" {
		t.Fatalf("receipt ids = %+v, want unchanged ct_dev1/topic-dev1", payload.Result)
	}
	if !strings.Contains(payload.Note, "unchanged") || !strings.Contains(payload.Note, "OLD title") {
		t.Fatalf("note must state ID stability and the reference-update hint, got %q", payload.Note)
	}

	// 邻居会话不受影响。
	other, ok := findIdentityByContact(t, dir, "ct_audit4")
	if !ok || other.Title != "审计员-4-通用" {
		t.Fatalf("untouched session changed: ok=%v title=%q", ok, other.Title)
	}
}

// 按 topic_id 定址同路径生效（协调线按主题组织，常常只有 topic_id 在手）。
func TestRenameTopicToolRenamesByTopicID(t *testing.T) {
	cfg, dir := renameTopicFixture(t)
	host := func(topicID, sessionPath, title string) (RenameSessionResult, error) {
		return renameTopicHost(t, topicID, sessionPath, title)
	}
	tool := NewRenameTopicTool(cfg, host)
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"topic_id":"topic-audit4","title":"审计专员-通用"}`))
	if err != nil {
		t.Fatalf("rename_topic by topic_id: %v", err)
	}
	var payload struct {
		Result RenameSessionResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.Result.ContactID != "ct_audit4" || payload.Result.TopicID != "topic-audit4" {
		t.Fatalf("receipt ids = %+v, want ct_audit4/topic-audit4 unchanged", payload.Result)
	}
	after, ok := findIdentityByContact(t, dir, "ct_audit4")
	if !ok || after.Title != "审计专员-通用" {
		t.Fatalf("directory after rename: ok=%v title=%q", ok, after.Title)
	}
	if after.TopicID != "topic-audit4" {
		t.Fatalf("topic_id changed: %q", after.TopicID)
	}
}

// 参数与能力面：缺 id / 空 title / 两 id 指向不同会话 / 未知 id / 宿主无能力，
// 全部在触达宿主前或宿主缺位时给可操作拒绝（宿主回调不得被误触发）。
func TestRenameTopicToolValidations(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"neither id", `{"title":"新名"}`, "pass topic_id or contact_id"},
		{"blank title", `{"contact_id":"ct_dev1","title":"   "}`, "title is required"},
		{"mismatched ids", `{"contact_id":"ct_dev1","title":"新名","topic_id":"topic-audit4"}`, "different sessions"},
		{"unknown id", `{"contact_id":"ct_nope","title":"新名"}`, "not in the contact directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := renameTopicFixture(t)
			tool := NewRenameTopicTool(cfg, func(topicID, sessionPath, title string) (RenameSessionResult, error) {
				t.Fatal("host callback must not run for a refused request")
				return RenameSessionResult{}, nil
			})
			_, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

// 宿主无能力（CLI/测试 host 未接线）时如实拒绝，不谎报成功。
func TestRenameTopicToolRefusesWithoutHostCapability(t *testing.T) {
	cfg, _ := renameTopicFixture(t)
	tool := NewRenameTopicTool(cfg, nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"contact_id":"ct_dev1","title":"新名"}`))
	if err == nil || !strings.Contains(err.Error(), "cannot rename") {
		t.Fatalf("nil host must be refused, got %v", err)
	}
}
