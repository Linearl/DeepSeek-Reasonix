package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// tl1197（1074 家族第 3 形态）: 携带 topic 身份但无会话文件的 global 打开
// （openGlobalTab 链）必须跟随既有 project binding，绝不在 global 目录新建
// 同 topic 空壳——那是 20261025 用户两次截图的「剧情核对」跨区影子的
// 磁盘种子（global 空壳被扫描后把项目对话画进 Global 未分组区）。
func TestOpenGlobalTabFollowsProjectBoundTopicInsteadOfMintingShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "ai_passport"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_20261025-152234_t1197"
	projectSessionDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(projectSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir project sessions: %v", err)
	}
	writeTopicSession(t, projectSessionDir, "20261025-152313.841311900-owner.jsonl", topicID, "剧情核对", projectRoot)

	globalSessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	before := sessionJSONLNames(t, globalSessionDir)

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenGlobalTab(topicID)
	if err != nil {
		t.Fatalf("OpenGlobalTab: %v", err)
	}
	if meta.Scope != "project" {
		t.Fatalf("opened tab scope = %q, want project (the topic's binding is the authority)", meta.Scope)
	}
	if !sameProjectRoot(meta.WorkspaceRoot, projectRoot) {
		t.Fatalf("opened tab root = %q, want %q", meta.WorkspaceRoot, projectRoot)
	}
	after := sessionJSONLNames(t, globalSessionDir)
	if len(after) != len(before) {
		t.Fatalf("global sessions dir grew %v -> %v, want no minted same-topic shell", before, after)
	}
}

// 存量 global 空壳在场时，同一 topic 的打开必须选内容承载者（正主），
// 而不是激活空壳——与 findTopicSessionAcrossScopes 的 #7305 选优规则一致。
func TestOpenGlobalTabPrefersContentBearingOwnerOverLeftoverShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "cooking_action_recognition"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	topicID := "topic_20261022-074604_t1197"

	globalSessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	// 存量空壳：0 字节、scope=global、同 topic（用户机器上 20261023 空壳的同构）。
	shellPath := filepath.Join(globalSessionDir, "20261023-060524.344349700-session.jsonl")
	if err := os.WriteFile(shellPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	if err := saveBranchMetaForTest(shellPath, "global", "", topicID, "烹饪复盘分享-续"); err != nil {
		t.Fatal(err)
	}

	projectSessionDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(projectSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir project sessions: %v", err)
	}
	ownerPath := writeTopicSession(t, projectSessionDir, "20261022-074607.708825100-owner.jsonl", topicID, "烹饪复盘分享-续", projectRoot)

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenGlobalTab(topicID)
	if err != nil {
		t.Fatalf("OpenGlobalTab: %v", err)
	}
	if meta.Scope != "project" {
		t.Fatalf("opened tab scope = %q, want project (content-bearing owner wins)", meta.Scope)
	}
	if meta.SessionPath != ownerPath {
		t.Fatalf("opened session = %q, want owner %q", meta.SessionPath, ownerPath)
	}
}

// 全世界都没有该 topic 会话的真新话题，新建空壳的原能力保持不变。
func TestOpenGlobalTabStillMintsShellForBrandNewTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenGlobalTab("topic_t1197_brand_new")
	if err != nil {
		t.Fatalf("OpenGlobalTab: %v", err)
	}
	if meta.Scope != "global" {
		t.Fatalf("brand-new topic opened as %q, want global", meta.Scope)
	}
	globalSessionDir := desktopSessionDir(globalWorkspaceRoot())
	names := sessionJSONLNames(t, globalSessionDir)
	if len(names) != 1 {
		t.Fatalf("global sessions dir = %v, want exactly the minted shell", names)
	}
}

func sessionJSONLNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, e.Name())
		}
	}
	return out
}

func saveBranchMetaForTest(path, scope, workspaceRoot, topicID, topicTitle string) error {
	return agent.SaveBranchMeta(path, agent.BranchMeta{
		CreatedAt:     time.Now().Add(-time.Minute),
		UpdatedAt:     time.Now(),
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		TopicID:       topicID,
		TopicTitle:    topicTitle,
	})
}
