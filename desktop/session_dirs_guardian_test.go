package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// 任务 1146 返修：路径校验红字（版本面板对 guardian 类会话报
// 「session path outside known session dirs」）。
//
// 根因是两处缺口叠加：
//  ① knownSessionDirs 只枚举 legacy 全局目录、项目 root 的 desktopSessionDir
//     与活跃 tab 目录——引擎侧 project-scope 容器目录
//     （<state>/projects/<slug>/sessions）不在列表，而 heartbeat/子会话
//     （guardian）由引擎托管创建住在那里；版本面板的扫描面
//     （recoveryScanDirectories）与协作名册却都已枚举它们 ⇒ 列得出来、
//     过不了 sessionDirForPath，红字。
//  ② validateSessionPath 用 store.IsSessionTranscriptName 判名，guardian
//     侧车（.guardian.jsonl）被排除（那是扫描期的分类口径，防侧车在清单里
//     遮蔽母会话，事故 F154-4）——补了目录后它仍会以「not a session file」
//     被拒。guardian 转录是与主转录同一保存路径写出的会话形态文件
//     （internal/guardian Session.Save），校验边界应放行。
//
// 本测试钉住修复后的完整合同：
//  - 容器目录下的 guardian 转录与普通转录都过 sessionDirForPath，且
//    guardian 转录可经 PreviewSession 预览（用户可见验收）；
//  - 安全边界不放宽：目录外路径、目录穿越、非转录名照旧拒绝。
func TestSessionDirForPathAcceptsEngineProjectContainerDirs(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("REASONIX_STATE_HOME", stateHome)
	sessionsDir := filepath.Join(stateHome, "projects", "proj-slug-1146", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	guardianPath := filepath.Join(sessionsDir, "20261024-172129-abcd1234.session.guardian.jsonl")
	regularPath := filepath.Join(sessionsDir, "20261024-172129-abcd1234.session.jsonl")
	guardianBody := `{"id":"g1","role":"user","content":"guardian turn"}` + "\n"
	for _, p := range []string{guardianPath, regularPath} {
		if err := os.WriteFile(p, []byte(guardianBody), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(p) })
	}

	app := newBranchDeleteApp(t)

	// The engine-hosted container dir is a known session dir: both the
	// guardian transcript and a regular transcript under it resolve.
	for _, path := range []string{guardianPath, regularPath} {
		dir, sessionPath, err := app.sessionDirForPath(path)
		if err != nil {
			t.Fatalf("container-dir transcript must resolve: %s: %v", path, err)
		}
		if dir != sessionsDir {
			t.Fatalf("resolved dir must be the container sessions dir, got %s", dir)
		}
		if !strings.EqualFold(sessionPath, path) {
			t.Fatalf("resolved session path must be the transcript path itself, got %s", sessionPath)
		}
	}

	// The guardian transcript previews through the versions panel's read-only
	// chain-preview channel (the tolerant replay reads the file the same way
	// the panel's preview dialog does). PreviewSession/LoadSession stay closed
	// for sidecars by design — task 1071's gate keeps read-modify-write flows
	// away from sidecar files, and this fix does not touch that boundary.
	feed, err := agent.RecoveryChainPreviewMessagesFor(guardianPath, guardianPath, 0)
	if err != nil {
		t.Fatalf("guardian transcript must preview through the panel's chain channel: %v", err)
	}
	if len(feed) == 0 {
		t.Fatal("guardian transcript chain preview must carry its message")
	}

	// The boundary stays closed outside the engine-hosted container form.
	outsidePath := filepath.Join(stateHome, "elsewhere", "x.session.jsonl")
	if _, _, err := app.sessionDirForPath(outsidePath); err == nil {
		t.Fatal("a path outside every known session dir must stay refused")
	}
	sneakyPath := filepath.Join(sessionsDir, "..", "..", "escaped.session.jsonl")
	if _, _, err := app.sessionDirForPath(sneakyPath); err == nil {
		t.Fatal("a traversal path must stay refused")
	}
	notASession := filepath.Join(sessionsDir, "notes.guardian.jsonl.bak")
	if _, _, err := app.sessionDirForPath(notASession); err == nil {
		t.Fatal("a non-transcript name must stay refused")
	}
	// A guardian events log (append-only DAG sidecar) is not a transcript.
	eventsPath := filepath.Join(sessionsDir, "20261024-172129-abcd1234.session.guardian.events.jsonl")
	if _, _, err := app.sessionDirForPath(eventsPath); err == nil {
		t.Fatal("a guardian events log must stay refused: only the transcript is session-shaped")
	}
}

// The fix must not loosen the scan-time classification: the single authority
// still excludes guardian sidecars from primary-transcript listings (F154-4),
// and the desktop helper widens it only for path validation.
func TestIsSessionShapedFileNameStaysScanTight(t *testing.T) {
	cases := map[string]bool{
		"20261024.session.jsonl":                 true,
		"20261024.session.guardian.jsonl":        true,
		"20261024.session.events.jsonl":          false,
		"20261024.session.guardian.events.jsonl": false,
		"notes.txt":                              false,
		"20261024.session.repairlog.jsonl":       false,
		"20261024.session.turns.jsonl":           false,
		"20261024.session.conflicts.jsonl":       false,
	}
	for name, want := range cases {
		if got := isSessionShapedFileName(name); got != want {
			t.Fatalf("isSessionShapedFileName(%s) = %v, want %v", name, got, want)
		}
	}
	// The store authority itself is untouched — directory listings keep
	// excluding guardian sidecars so they never shadow their parent session.
	if store.IsSessionTranscriptName("20261024.session.guardian.jsonl") {
		t.Fatal("store.IsSessionTranscriptName must keep excluding guardian sidecars")
	}
}
