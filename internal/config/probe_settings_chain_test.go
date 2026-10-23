package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// 任务 1090 诊断探针（第二轮：模拟真实用户体量）——复刻 desktop Settings()
// 链三段开销。生产环境的 .env 有 N 个 key，逐 key 的 envFileValue 都整读
// +godotenv 解析 +detectDotEnvDuplicateKeys 再读一遍 = 每 key 3 次文件读，
// 总量 O(N×文件大小)。这里用「N 个 key 的夹具 .env」把该乘法效应量化，
// CI 安全（纯临时目录）。指针 receiver 只借根路径解析（生产为空/真实根）。
func TestSettingsChainScaledProbe1090(t *testing.T) {
	const nKeys = 40
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials")
	{
		content := ""
		for i := range nKeys {
			content += fmt.Sprintf("PROBE_API_KEY_%02d=sk-probe-%02d0123456789\n", i, i)
		}
		if err := os.WriteFile(credPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	{
		cfgPath := filepath.Join(dir, "config.toml")
		content := ""
		for i := range nKeys {
			content += fmt.Sprintf("[[providers]]\nname = \"probe%02d\"\napi_key_env = \"PROBE_API_KEY_%02d\"\nbase_url = \"https://example.invalid/v1\"\nmodel = \"m\"\n", i, i)
		}
		if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		cfg, err := LoadForEditWithoutCredentialsReadOnlyStrict(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("user-config decode (%d providers): %v", len(cfg.Providers), time.Since(start))

		// 逐 key 凭据解析（Settings() 复刻）：每个 key 至少 3 次整文件读。
		start = time.Now()
		resolver := NewCredentialResolverForRoot(dir)
		keys := credentialEnvNamesFromConfig(cfg)
		for _, k := range keys {
			resolver.ResolveGlobalFirst(k)
		}
		total := time.Since(start)
		t.Logf("credential resolve %d keys against %v-file: total %v (~%v/key; ~3 file reads/key)",
			len(keys), runtime.GOOS, total, total/time.Duration(len(keys)))

		// 空解析器对照（无凭证文件路径时的快路径）。
		start = time.Now()
		empty := NewCredentialResolverForRoot(t.TempDir())
		for _, k := range keys {
			empty.ResolveGlobalFirst(k)
		}
		t.Logf("credential resolve %d keys (no store): total %v", len(keys), time.Since(start))
	}
}
