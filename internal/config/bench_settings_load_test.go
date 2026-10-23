package config

import (
	"os"
	"testing"
	"time"
)

// 任务 1090 诊断：实测真实用户 config.toml 的只读解码耗时（设置页/实验室页
// 共享链路）。路径由 REASONIX_BENCH_CONFIG 指定；未设置时静默跳过（CI 不带
// 真实配置，这不是 SKIP 静默——路径由本机诊断会话显式给出）。
func TestUserConfigLoadTimingProbe(t *testing.T) {
	path := os.Getenv("REASONIX_BENCH_CONFIG")
	if path == "" {
		t.Skip("REASONIX_BENCH_CONFIG not set")
	}
	start := time.Now()
	cfg, err := LoadForEditWithoutCredentialsReadOnlyStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("LoadForEditWithoutCredentialsReadOnlyStrict %s: %v providers=%d", path, time.Since(start), len(cfg.Providers))
}
