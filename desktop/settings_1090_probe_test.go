package main

import (
	"testing"
	"time"

	"reasonix/internal/config"
)

// 任务 1090 诊断：Settings() 链最慢段——provider 视图构建的逐 key 凭据
// 解析——复刻计时。root 恒为空（与生产「无激活工作区」同型）。
func TestSettingsProviderBuildTimingProbe1090(t *testing.T) {
	root := ""
	start := time.Now()
	resolver := config.NewCredentialResolverForRoot(root)
	rev := providerCredentialsRevision()
	n := 0
	for range 50 {
		entries, _, err := officialProviderTemplate("deepseek", "USD")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			_ = providerViewFromEntryForRootWithResolverAndCredentials(entry, true, false, root, resolver, rev)
			n++
		}
	}
	total := time.Since(start)
	t.Logf("provider views: %d builds in %v -> %v per view", n, total, total/time.Duration(n))
}
