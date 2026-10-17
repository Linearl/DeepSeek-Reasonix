package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
)

// 任务 938 Go 面验收：hover 读口 + 编辑写口。钉三件事——
// ① 写口真正落到 sidecar（purpose 即通讯录看到的那份）；
// ② 写口保留 task-348 结构化字段（identity/duties 不因自由文本编辑被抹）；
// ③ 空路径/不存在会话被拒绝，不产生孤儿 sidecar。

func newPurposeTestSession(t *testing.T) (*App, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	sessionPath, err := createEmptySessionFile(desktopSessionDir(root), "collab")
	if err != nil {
		t.Fatal(err)
	}
	return app, sessionPath
}

func TestSetSessionPurposeWritesSidecar(t *testing.T) {
	app, sessionPath := newPurposeTestSession(t)

	if err := app.SetSessionPurpose(sessionPath, "React 前端专家"); err != nil {
		t.Fatalf("SetSessionPurpose: %v", err)
	}

	stored, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("branch meta: ok=%v err=%v", ok, err)
	}
	if stored.Purpose != "React 前端专家" {
		t.Fatalf("sidecar purpose = %q, want React 前端专家", stored.Purpose)
	}
	if stored.ContactID == "" {
		t.Fatal("purpose write must mint a contact id (task 141 chain)")
	}

	got, err := app.GetSessionPurpose(sessionPath)
	if err != nil {
		t.Fatalf("GetSessionPurpose: %v", err)
	}
	if got != "React 前端专家" {
		t.Fatalf("GetSessionPurpose = %q, want React 前端专家", got)
	}
}

// 编辑自由文本职责不得抹掉 task-348 结构化字段：SetSessionPurpose 走
// SetSessionDuty(nil, nil) 的 presence 语义，identity 与 duties 原样保留。
func TestSetSessionPurposeKeepsStructuredFields(t *testing.T) {
	app, sessionPath := newPurposeTestSession(t)

	duties := []string{"写前端", "维护组件库"}
	if _, err := agent.SetSessionDuty(sessionPath, "旧职责", &agent.SessionIdentity{Type: "sub", Domain: "开发"}, &duties); err != nil {
		t.Fatalf("seed structured fields: %v", err)
	}

	if err := app.SetSessionPurpose(sessionPath, "新职责"); err != nil {
		t.Fatalf("SetSessionPurpose: %v", err)
	}

	stored, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("branch meta: ok=%v err=%v", ok, err)
	}
	if stored.Purpose != "新职责" {
		t.Fatalf("purpose = %q, want 新职责", stored.Purpose)
	}
	if stored.IdentityType != "sub" || stored.IdentityDomain != "开发" {
		t.Fatalf("identity = %q/%q, want sub/开发 (presence semantics must keep it)", stored.IdentityType, stored.IdentityDomain)
	}
	if len(stored.Duties) != 2 || stored.Duties[0] != "写前端" || stored.Duties[1] != "维护组件库" {
		t.Fatalf("duties = %#v, want the seeded list untouched", stored.Duties)
	}
}

// 空职责是合法编辑意图（弹窗里删光文本保存 = 清除登记），但空路径与不存在
// 的会话必须拒绝——绝不为不存在的会话造孤儿 sidecar。
func TestSetSessionPurposeRejectsInvalidTargets(t *testing.T) {
	app, sessionPath := newPurposeTestSession(t)

	if err := app.SetSessionPurpose("   ", "职责"); err == nil {
		t.Fatal("empty session path must be refused")
	}
	ghost := filepath.Join(filepath.Dir(sessionPath), "ghost-session.jsonl")
	if err := app.SetSessionPurpose(ghost, "职责"); err == nil {
		t.Fatal("a nonexistent session must be refused")
	}
	if _, err := os.Stat(ghost + ".meta"); !os.IsNotExist(err) {
		t.Fatalf("refused write must not create an orphan sidecar: stat err=%v", err)
	}

	// 清空职责：写口允许空字符串，读口回空串。
	if err := app.SetSessionPurpose(sessionPath, "临时职责"); err != nil {
		t.Fatalf("seed purpose: %v", err)
	}
	if err := app.SetSessionPurpose(sessionPath, ""); err != nil {
		t.Fatalf("clear purpose: %v", err)
	}
	got, err := app.GetSessionPurpose(sessionPath)
	if err != nil {
		t.Fatalf("GetSessionPurpose after clear: %v", err)
	}
	if got != "" {
		t.Fatalf("purpose after clear = %q, want empty", got)
	}

	if _, err := app.GetSessionPurpose("  "); err == nil {
		t.Fatal("empty session path on read must be refused")
	}
}
