package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

// 任务 1368 线2：版本副本面板的「删除选中对话」——整对话级清除的后端面。
//
// 验收（tl1368 正文）：勾选行点「删除选中对话」→ 主文件 + 全部副本 + 侧车
// 入回收站，侧栏与面板均消失；回收站可还原。本文件钉三点：
//  ① 有副本会话：主文件（含事件日志与侧车）与全部副本+副本侧车全部离开
//     活动目录，分别落进两条既有可恢复回收站（session trash 的
//     trashSessionArtifacts 链路 + 副本的 TrashRecoveryBranchForced 链路）；
//     RestoreSession 能把主文件+侧车整体还原。
//  ② orphan 主线（主文件已不在）：只归档副本，MainTrashed=false，不报错。
//  ③ 已知会话目录之外路径拒绝（与 DeleteRecoveryBranch 同一门禁）。
//
// 复用链路声明（不造新机制）：副本走 1064 forced 归档，主文件走
// deleteSession 的 trashSessionArtifacts（主文件+全部侧车一枚举内进出回收站）。

func newConversationDeleteApp(t *testing.T) *App {
	t.Helper()
	return &App{tabs: map[string]*WorkspaceTab{}, detachedSessions: map[string]*WorkspaceTab{}}
}

// writeConversationFixture drops a main transcript with its meta sidecar and
// two recovery-named copies beside it, so both archive paths have real files
// to move.
func writeConversationFixture(t *testing.T, dir, stem string) (mainPath string, copyPaths []string) {
	t.Helper()
	mainPath = filepath.Join(dir, stem+".jsonl")
	if err := os.WriteFile(mainPath, []byte(`{"id":"m1","role":"user","content":"main line"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	metaPath := mainPath + ".meta"
	if err := os.WriteFile(metaPath, []byte(`{"topic_title":"tl1368 fixture"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, suffix := range []string{"recovery-0123456789abcdef", "recovery-fedcba9876543210"} {
		copyPath := filepath.Join(dir, stem+"-"+suffix+".jsonl")
		if err := os.WriteFile(copyPath, []byte(`{"id":"c`+string(rune('0'+i))+`","role":"user","content":"forked"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		copyPaths = append(copyPaths, copyPath)
	}
	return mainPath, copyPaths
}

func TestDeleteConversationWithCopiesArchivesEverything(t *testing.T) {
	// sessionDirForPath only trusts known session dirs — the same contract
	// the versions panel operates under in production (1064 test precedent).
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stem := "sess-conv-del-1368-a"
	mainPath, copyPaths := writeConversationFixture(t, dir, stem)
	metaPath := mainPath + ".meta"
	t.Cleanup(func() {
		for _, p := range append([]string{mainPath, metaPath}, copyPaths...) {
			_ = os.Remove(p)
		}
	})

	app := newConversationDeleteApp(t)
	report, err := app.DeleteConversationWithCopies(mainPath)
	if err != nil {
		t.Fatalf("delete with copies must succeed: %v", err)
	}
	if !report.MainTrashed {
		t.Fatal("report must say the main transcript was trashed")
	}
	if report.CopiesArchived != len(copyPaths) {
		t.Fatalf("report must count both copies, got %d", report.CopiesArchived)
	}
	for _, p := range append([]string{mainPath, metaPath}, copyPaths...) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("every live artifact must be gone from the session directory: %s (err=%v)", p, err)
		}
	}

	// Recoverability, both sides:
	//  - copies land under the directory's recoverable .trash (1064 layout);
	//  - the main transcript + sidecars restore through the existing
	//    RestoreSession path, which reads back the same artifact enumeration
	//    trashSessionArtifacts wrote.
	trashRoot := filepath.Join(dir, ".trash")
	for _, copyPath := range copyPaths {
		found := false
		_ = filepath.Walk(trashRoot, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && filepath.Base(p) == filepath.Base(copyPath) {
				found = true
			}
			return nil
		})
		if !found {
			t.Fatalf("the copy must remain recoverable under the session .trash: %s", copyPath)
		}
	}
	// RestoreSession takes the trash-entry path (what ListTrashedSessions
	// returns): dir/.trash/<itemDir>/<key>, with itemDir named after the key
	// on a first delete.
	trashedPath := filepath.Join(dir, ".trash", stem+".jsonl", stem+".jsonl")
	if err := app.RestoreSession(trashedPath); err != nil {
		t.Fatalf("the trashed main transcript must restore: %v", err)
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("restored main transcript must be back: %v", err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("restored meta sidecar must be back with the transcript: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(metaPath)
	})
}

func TestDeleteConversationWithCopiesOrphanMain(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stem := "sess-conv-del-1368-orphan"
	// The main is already gone; only the copies exist (the versions panel
	// renders such groups as orphan rows).
	_, copyPaths := writeConversationFixture(t, dir, stem)
	mainPath := filepath.Join(dir, stem+".jsonl")
	if err := os.Remove(mainPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, p := range copyPaths {
			_ = os.Remove(p)
		}
	})

	app := newConversationDeleteApp(t)
	report, err := app.DeleteConversationWithCopies(mainPath)
	if err != nil {
		t.Fatalf("an orphan group deletes its copies without error: %v", err)
	}
	if report.MainTrashed {
		t.Fatal("a missing main must not be reported as trashed")
	}
	if report.CopiesArchived != len(copyPaths) {
		t.Fatalf("both copies must be archived, got %d", report.CopiesArchived)
	}
	for _, p := range copyPaths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("the copy must be gone from the live directory: %s", p)
		}
	}
}

func TestDeleteConversationWithCopiesRefusesUnknownDir(t *testing.T) {
	app := newConversationDeleteApp(t)
	if _, err := app.DeleteConversationWithCopies(filepath.Join(t.TempDir(), "outside.jsonl")); err == nil {
		t.Fatal("a path outside the known session dirs must be refused")
	}
}
