package config

import (
	"strings"
	"testing"
)

// tl-1218：multi_edit 三条增强（错误详情增强 / 证据门连坐收窄 / 跳过失败项）
// 的 config 层语义钉。三个键均为普通默认关 bool（铁律 2）：零值（及 nil
// config）= 行为与基线逐字节一致；显式 setter 翻转读取面；渲染层无条件
// 渲染（81/123 丢档教训——omit-on-off 会在下次渲染把已开的开关静默弹回）。
//
// 验收矩阵（三开关独立，两态逐态）：
//
//	键            缺省      setter(true)  setter(false)
//	①error_detail 关        开            关
//	②narrow       关        开            关
//	③skip_failed  关        开            关
func TestMultiEditEnhancementSwitchesDefaultOff(t *testing.T) {
	c := &Config{}
	if c.MultiEditErrorDetailEnabled() {
		t.Error("experimental_multi_edit_error_detail must default to off")
	}
	if c.MultiEditEvidenceNarrowEnabled() {
		t.Error("experimental_multi_edit_evidence_narrow must default to off")
	}
	if c.MultiEditSkipFailedEnabled() {
		t.Error("experimental_multi_edit_skip_failed must default to off")
	}
	var nilCfg *Config
	if nilCfg.MultiEditErrorDetailEnabled() || nilCfg.MultiEditEvidenceNarrowEnabled() || nilCfg.MultiEditSkipFailedEnabled() {
		t.Fatal("nil config must read as off on all three faces")
	}
}

func TestMultiEditEnhancementSwitchSetterRoundTrip(t *testing.T) {
	c := &Config{}
	for _, tc := range []struct {
		name string
		set  func(bool) error
		get  func() bool
		toml string
	}{
		{name: "error_detail", set: c.SetExperimentalMultiEditErrorDetail, get: c.MultiEditErrorDetailEnabled, toml: "experimental_multi_edit_error_detail"},
		{name: "evidence_narrow", set: c.SetExperimentalMultiEditEvidenceNarrow, get: c.MultiEditEvidenceNarrowEnabled, toml: "experimental_multi_edit_evidence_narrow"},
		{name: "skip_failed", set: c.SetExperimentalMultiEditSkipFailed, get: c.MultiEditSkipFailedEnabled, toml: "experimental_multi_edit_skip_failed"},
	} {
		if err := tc.set(true); err != nil {
			t.Fatalf("%s: set(true): %v", tc.name, err)
		}
		if !tc.get() {
			t.Errorf("%s: set(true) must light the switch", tc.name)
		}
		if err := tc.set(false); err != nil {
			t.Fatalf("%s: set(false): %v", tc.name, err)
		}
		if tc.get() {
			t.Errorf("%s: set(false) must turn the switch off", tc.name)
		}
	}
}

// TestMultiEditEnhancementSwitchesRenderUnconditionally pins the render face:
// the three keys render on EVERY save (explicit render, lost-save lesson) and
// the rendered value follows the stored field both ways.
func TestMultiEditEnhancementSwitchesRenderUnconditionally(t *testing.T) {
	c := &Config{}
	off := RenderTOML(c)
	for _, key := range []string{"experimental_multi_edit_error_detail", "experimental_multi_edit_evidence_narrow", "experimental_multi_edit_skip_failed"} {
		if !strings.Contains(off, key+" = false") {
			t.Errorf("render must carry %s = false even at defaults (lost-save lesson), got:\n%s", key, off)
		}
	}
	if err := c.SetExperimentalMultiEditErrorDetail(true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetExperimentalMultiEditEvidenceNarrow(true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetExperimentalMultiEditSkipFailed(true); err != nil {
		t.Fatal(err)
	}
	on := RenderTOML(c)
	for _, key := range []string{"experimental_multi_edit_error_detail", "experimental_multi_edit_evidence_narrow", "experimental_multi_edit_skip_failed"} {
		if !strings.Contains(on, key+" = true") {
			t.Errorf("render must carry %s = true after the setter, got:\n%s", key, on)
		}
	}
}
