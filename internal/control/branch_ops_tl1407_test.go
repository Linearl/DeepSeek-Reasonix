package control

import (
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1407 源头②:fork/branch 两处 `agent.NewSession("")` + Messages 覆盖——
// 父会话无头则子会话永远无头(20260927-020815 collab 群聊老会话无头,后代全
// 无头,代际遗传)。子会话必须在父前缀缺头时补身份头。
//
// 现场还原约束:不传 Options.SystemPrompt——构造期 newControllerPromptState
// 会顺手修掉无头会话,传了就会掩盖无头父会话现场。

func tl1407HeadlessParentCtrl(t *testing.T, dir string) *Controller {
	t.Helper()
	sess := agent.NewSession("") // 无 system 头
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "first prompt"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "first answer"})
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, SessionDir: dir, Label: "tl1407"})
	c.SetSessionPath(filepath.Join(dir, "parent.jsonl"))
	return c
}

func tl1407LoadedChildFirstRole(t *testing.T, path string) (string, string) {
	t.Helper()
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	msgs := loaded.Snapshot()
	if len(msgs) == 0 {
		t.Fatalf("child file %s is empty", path)
	}
	return string(msgs[0].Role), msgs[0].Content
}

// TestTL1407BranchSeedsHeaderOntoHeadlessParent pins branchTip: a headless
// parent must not raise a headless child.
func TestTL1407BranchSeedsHeaderOntoHeadlessParent(t *testing.T) {
	dir := schemaOneTempDir(t)
	c := tl1407HeadlessParentCtrl(t, dir)

	if _, err := c.Branch("tl1407-branch"); err != nil {
		t.Fatal(err)
	}
	role, content := tl1407LoadedChildFirstRole(t, c.SessionPath())
	if role != string(provider.RoleSystem) || content == "" {
		t.Fatalf("branched child lost the identity header: first role=%q content=%q", role, content)
	}
}

// TestTL1407ForkSeedsHeaderOntoHeadlessParent pins forkNamedReady (the /branch
// <turn> path) with the same generational guarantee.
func TestTL1407ForkSeedsHeaderOntoHeadlessParent(t *testing.T) {
	dir := schemaOneTempDir(t)
	c := tl1407HeadlessParentCtrl(t, dir)
	c.checkpoints.mu.Lock()
	c.checkpoints.bound[1] = 2 // fork at the first turn boundary keeps both messages
	c.checkpoints.mu.Unlock()

	if _, err := c.ForkNamed(1, "tl1407-fork"); err != nil {
		t.Fatal(err)
	}
	role, content := tl1407LoadedChildFirstRole(t, c.SessionPath())
	if role != string(provider.RoleSystem) || content == "" {
		t.Fatalf("forked child lost the identity header: first role=%q content=%q", role, content)
	}
}
