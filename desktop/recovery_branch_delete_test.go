package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// 任务 1064：面板手动删除分支的两条后端路径。
//
// 验收（1064 正文）：非当前版本行加删除入口 + 复选框批量选中/批量删除；安
// 全约束①当前选中版本不可选不可删 ②删除走回收语义（可反悔）③修复记录/备
// 份关联引用处理；删除后列表即时刷新。本测试钉住后端面：
//  ① 副本分支删除 = forced 归档（不要求覆盖证明），落进可恢复 .trash；
//  ② head 删除 = retire 标记，当前选中 head 拒绝；
//  ③ 主线转录本体拒绝走副本删除路径（面板只允许删分支）。

func newBranchDeleteApp(t *testing.T) *App {
	t.Helper()
	return &App{tabs: map[string]*WorkspaceTab{}, detachedSessions: map[string]*WorkspaceTab{}}
}

// writeRecoveryCopyFixture drops a recovery-named transcript beside main so the
// forced-archive path has a real file to move.
func writeRecoveryCopyFixture(t *testing.T, dir, mainName, copyName string) (mainPath, copyPath string) {
	t.Helper()
	mainPath = filepath.Join(dir, mainName)
	copyPath = filepath.Join(dir, copyName)
	if err := os.WriteFile(mainPath, []byte(`{"id":"m1","role":"user","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte(`{"id":"c1","role":"user","content":"forked"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return mainPath, copyPath
}

func TestDeleteRecoveryBranchArchivesWithoutCoverageProof(t *testing.T) {
	// sessionDirForPath only trusts known session dirs, so the fixture lives
	// under the app's real global session dir (the same contract the versions
	// panel operates under in production).
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath, copyPath := writeRecoveryCopyFixture(t, dir, "sess-branch-del-1.jsonl", "sess-branch-del-1-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})
	if !agent.LooksLikeRecoveryFilename(copyPath) {
		t.Fatalf("fixture must look like a recovery copy: %s", copyPath)
	}

	app := newBranchDeleteApp(t)
	if err := app.DeleteRecoveryBranch(copyPath); err != nil {
		t.Fatalf("forced archive of a diverged branch must succeed (the user chose it): %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("archived branch file must be gone from the live directory, stat err=%v", err)
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the main transcript must be untouched: %v", err)
	}
	// Recoverability: the archive lands under the directory's .trash, not deleted.
	trashHits := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(p) == filepath.Base(copyPath) {
			trashHits++
		}
		return nil
	})
	if trashHits == 0 {
		t.Fatal("the discarded branch must remain recoverable somewhere under the session directory")
	}
}

func TestDeleteRecoveryBranchRefusesPlainTranscript(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath, _ := writeRecoveryCopyFixture(t, dir, "sess-branch-del-2.jsonl", "sess-branch-del-2-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() { _ = os.Remove(mainPath) })

	app := newBranchDeleteApp(t)
	if err := app.DeleteRecoveryBranch(mainPath); err == nil {
		t.Fatal("deleting the main transcript through the branch path must be refused")
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the main transcript must survive a refused call: %v", err)
	}
}

func TestDeleteSessionVersionHeadRefusesSelectedHead(t *testing.T) {
	// sessionDirForPath only trusts known session dirs, so the fixture lives
	// under the app's real global session dir (the same contract the versions
	// panel operates under in production).
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "heads-branch-del-3.jsonl")
	t.Cleanup(func() { _ = os.Remove(path) })
	// Build the schema-2 fixture through the agent's own session save (the same
	// shape internal/agent's head tests use), so ForkHead records real markers.
	s := agent.NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	fork, err := s.ForkHead(path, s.Snapshot()[1].ID, agent.HeadKindFork, "side")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := agent.ListSessionHeads(path)
	if err != nil || len(selected) != 2 {
		t.Fatalf("heads=%+v err=%v", selected, err)
	}
	current := ""
	for _, h := range selected {
		if h.Selected {
			current = h.ID
		}
	}
	if current == "" {
		t.Fatal("fixture must have a selected head")
	}

	app := newBranchDeleteApp(t)
	if err := app.DeleteSessionVersionHead(path, current); err == nil {
		t.Fatal("the current version must not be deletable (1064 constraint ①)")
	}

	// Switch off the fork, then the fork head becomes deletable.
	if err := agent.SelectSessionHead(path, agent.SessionMainHead); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteSessionVersionHead(path, fork); err != nil {
		t.Fatalf("retire of a non-current head must succeed: %v", err)
	}
	heads, err := agent.ListSessionHeads(path)
	if err != nil {
		t.Fatal(err)
	}
	retired := false
	for _, h := range heads {
		if h.ID == fork && h.Retired {
			retired = true
		}
	}
	if !retired {
		t.Fatalf("the deleted head must read back as retired: %+v", heads)
	}
	if err := app.DeleteSessionVersionHead(path, "no-such-head"); err == nil {
		t.Fatal("an unknown head must be refused")
	} else if !errors.Is(err, agent.ErrSessionHeadUnknown) {
		t.Fatalf("unknown-head error must wrap ErrSessionHeadUnknown, got %v", err)
	}
}
