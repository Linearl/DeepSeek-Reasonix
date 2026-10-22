package config

// 任务1049: [serve.pool] 把 serve 池的 spawn/idle 超时暴露成 config 键。
// 解码测试钉死键名拼写（加载对未知键只告警不报错，键名写错 = 静默回退
// 池默认值，不会失败）；round-trip 测试钉死渲染行（缺渲染行 = 下一次
// 设置保存把用户写的键擦掉，task 432/468 同类损失）。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServePoolTimeoutKeysDecode(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[serve.pool]\nspawn_timeout_seconds = 45\nidle_timeout_seconds = 1200\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Serve.Pool.SpawnTimeoutSeconds; got != 45 {
		t.Fatalf("serve.pool.spawn_timeout_seconds = %d, want 45", got)
	}
	if got := cfg.Serve.Pool.IdleTimeoutSeconds; got != 1200 {
		t.Fatalf("serve.pool.idle_timeout_seconds = %d, want 1200", got)
	}
}

func TestServePoolAbsentKeysKeepZero(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Serve.Pool.SpawnTimeoutSeconds != 0 || cfg.Serve.Pool.IdleTimeoutSeconds != 0 {
		t.Fatalf("absent [serve.pool] decoded nonzero: %+v", cfg.Serve.Pool)
	}
}

func TestServePoolRoundTripSurvivesRender(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("REASONIX_HOME", home)

	cfg, err := LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Serve.Pool.SpawnTimeoutSeconds = 60
	cfg.Serve.Pool.IdleTimeoutSeconds = 1800
	rendered := RenderTOMLForScope(cfg, RenderScopeUser)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}

	reread, err := LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := reread.Serve.Pool.SpawnTimeoutSeconds; got != 60 {
		t.Fatalf("after render round-trip spawn_timeout_seconds = %d, want 60 (render row missing = wipe bug)", got)
	}
	if got := reread.Serve.Pool.IdleTimeoutSeconds; got != 1800 {
		t.Fatalf("after render round-trip idle_timeout_seconds = %d, want 1800", got)
	}
}
