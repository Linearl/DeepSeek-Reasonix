package main

// tl1323 回归钉（审计 low ④）：同为「新建空会话」，侧栏 topic 打开的 mint
// 分支（openTopicTabWithActivation，sessionPath==""）给 createEmptySessionFile
// 传的 model 实参是 ""，而 EnsureBlankTab/createTabEntryWithID 传
// desktopNewSessionDefaults 的 model——文件名里的 model 提示两条路径分叉，
// topic 侧新建的会话恒跌泛化 "-session" 名。修后同源：model 标签来自同一个
// desktopNewSessionDefaults（文件名只是提示，不回读；命名契约对照
// TestEnsureBlankTabInheritsActiveTabLocalSettings 已钉住 EnsureBlankTab 侧）。
//
// Zero SKIP：前置失败即 t.Fatal。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

func TestOpenTopicNewSessionFilenameStampsDefaultModel(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.LoadForEdit(config.UserConfigPath())
	if err := cfg.SetDefaultModel("deepseek-pro/deepseek-v4-pro"); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenGlobalTab("topic_tl1323_model_label")
	if err != nil {
		t.Fatalf("OpenGlobalTab: %v", err)
	}
	app.mu.RLock()
	tab := app.tabs[meta.ID]
	app.mu.RUnlock()
	if tab == nil {
		t.Fatalf("tab %q not found after open", meta.ID)
	}
	if tab.SessionPath == "" {
		t.Fatal("topic-open mint path must produce a session file")
	}
	base := filepath.Base(tab.SessionPath)
	if !strings.Contains(base, "deepseek-v4-pro") {
		t.Fatalf("topic-open minted session name = %q, want default-model stamp from desktopNewSessionDefaults (same source as EnsureBlankTab)", base)
	}
	if strings.Contains(base, "-session.jsonl") {
		t.Fatalf("topic-open minted session name = %q, generic -session fallback must be gone", base)
	}
}
