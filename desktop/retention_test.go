package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/installlayout"
)

// 任务835：desktop 启动链的保留清理接线钉。
//
// 合同：能跑到启动链（本地服务起立）= 新版本已成功启动 = 「更新成功确认」
// 的进程内证据——此刻清理才动手，更新失败（新进程没起来）永远不丢回滚源。
// 本文件钉两个面：
//  1. 版本化安装根上，启动清理把超额版本树收敛到 ≤4 且 active 保留；
//  2. 无版本化布局（portable 形态）是静默 no-op。

func writeRetentionFixture(t *testing.T, versions []string, active string) string {
	t.Helper()
	root := t.TempDir()
	versionsDir := filepath.Join(root, installlayout.VersionsDirName)
	for _, v := range versions {
		dir := filepath.Join(versionsDir, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, installlayout.DesktopBinaryName()), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(installlayout.CurrentPointer{SchemaVersion: installlayout.CurrentSchemaVersion, ActiveVersion: active, ActiveDir: installlayout.VersionDirRelative(active)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, installlayout.CurrentFileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEnforceInstallRetentionConvergesVersionedRoot(t *testing.T) {
	root := writeRetentionFixture(t, []string{
		"v1.38.3-20261011-1000",
		"v1.38.3-20261012-1100",
		"v1.38.3-20261012-1748",
		"v1.38.3-20261013-0014",
		"v1.38.3-20261013-0911",
	}, "v1.38.3-20261013-0911")
	// 一枚超龄 replaced 残壳，验证启动清理顺带扫掉。
	versionsDir := filepath.Join(root, installlayout.VersionsDirName)
	shell := filepath.Join(versionsDir, "v1.38.3-20261011-1000.replaced-stale")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-26 * time.Hour)
	if err := os.Chtimes(shell, old, old); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	oldRoot := retentionInstallRoot
	retentionInstallRoot = func() string { return root }
	t.Cleanup(func() { retentionInstallRoot = oldRoot })

	app.enforceInstallRetention()

	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		t.Fatal(err)
	}
	trees := 0
	for _, e := range entries {
		if e.IsDir() && installlayout.ValidateVersionName(e.Name()) == nil {
			trees++
		}
	}
	if trees > installlayout.RetentionKeepHistory+1 {
		t.Fatalf("versions after startup retention = %d, want ≤ %d", trees, installlayout.RetentionKeepHistory+1)
	}
	if _, err := os.Stat(shell); !os.IsNotExist(err) {
		t.Fatalf("the stale replaced shell must not survive a startup pass, stat err = %v", err)
	}
	active := filepath.Join(versionsDir, "v1.38.3-20261013-0911")
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("the running version must never be pruned: %v", err)
	}
}

func TestEnforceInstallRetentionSkipsPortableLayout(t *testing.T) {
	app := &App{}
	oldRoot := retentionInstallRoot
	retentionInstallRoot = func() string { return "" }
	t.Cleanup(func() { retentionInstallRoot = oldRoot })

	// 不得 panic，不得触碰任何目录。
	app.enforceInstallRetention()
}
