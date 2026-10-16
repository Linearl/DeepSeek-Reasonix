package config

import (
	"strings"
	"testing"
)

// 任务 916（存储版本锁定 v3）config render 档位断言。
//
// 锁定发生在设置面板（前端禁用非 v3 档），后端渲染面必须保持宽容：
// 已存 v4 档的升级用户配置要原样渲染往返，否则下一次保存会把他的档位
// 静默改写成 v3——那等于替用户做了降级决定，还丢掉重启提示的依据。
// 这三组断言共同守住验收第 4 条（带 v4 数据的升级用户路径不被破坏）。

// TestRenderKeepsSessionStorageLockNote pins the rendered comment marking the
// panel lock, on both render paths (top-level RenderTOML and the project delta).
func TestRenderKeepsSessionStorageLockNote(t *testing.T) {
	for name, out := range map[string]string{
		"RenderTOML":             RenderTOML(Default()),
		"RenderTOMLProjectDelta": RenderTOMLProjectDelta(Default()),
	} {
		if !strings.Contains(out, "session_storage = \"v3_only\"") {
			t.Errorf("%s: default renders session_storage = \"v3_only\", got no such line", name)
		}
		if !strings.Contains(out, "settings-locked per task 916") {
			t.Errorf("%s: session_storage comment lost the 916 lock marker", name)
		}
		// The comment still lists every mode: a hand-editor must keep seeing the
		// full scale (the lock is a panel affordance, not a config-format change).
		for _, mode := range SessionStorageModes {
			if !strings.Contains(out, mode) {
				t.Errorf("%s: session_storage comment no longer lists mode %q", name, mode)
			}
		}
	}
}

// TestRenderPreservesStoredV4Mode is the upgrade-user guard: a config already
// sitting on a v4 stage renders that exact stage back out — the lock must not
// rewrite stored v4/v5-era values to v3 on save.
func TestRenderPreservesStoredV4Mode(t *testing.T) {
	for _, stored := range []string{
		SessionStorageDualWriteReadV3,
		SessionStorageDualWriteReadV4,
		SessionStorageV4Only,
	} {
		cfg := Default()
		if err := cfg.SetSessionStorage(stored); err != nil {
			t.Fatalf("SetSessionStorage(%q) = %v", stored, err)
		}
		out := RenderTOML(cfg)
		want := "session_storage = \"" + stored + "\""
		if !strings.Contains(out, want) {
			t.Errorf("stored mode %q not rendered: want line %q", stored, want)
		}
	}
}

// TestLabTierKeepsSessionStorageEntry pins the experiment-switch surface: the
// sessionStore card (which hosts the 805 migration routes) keeps its lab tier
// registration, so the card — and with it the migration guidance for v4-data
// users — stays reachable from the settings page. Shrinking the tier table
// without an entry here would hide the card for everyone.
func TestLabTierKeepsSessionStorageEntry(t *testing.T) {
	found := false
	for _, entry := range labFeatureTiers {
		if entry.id == "sessionStorage" {
			found = true
			if entry.tier == LabTierRetired {
				t.Errorf("sessionStorage lab tier = retired; the card hosts the migration routes and must stay reachable")
			}
			if len(entry.renderKeys) == 0 || entry.renderKeys[0] != "session_storage" {
				t.Errorf("sessionStorage lab render keys = %v, want first key session_storage", entry.renderKeys)
			}
		}
	}
	if !found {
		t.Fatal("labFeatureTiers has no sessionStorage entry — the storage card (and its migration section) would vanish from settings")
	}
}
