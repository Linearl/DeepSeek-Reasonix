package config

import (
	"testing"

	"reasonix/internal/provider"
)

// tl1379 数据层修正：mimo 价格表覆盖 v2.6 三型号（用户提供的实时推理牌价，
// per-1M-token 人民币）；模型列表换代后价格键自动对齐，不再触发不可算。

func TestMimoDomesticPricesCoverV26Generation(t *testing.T) {
	prices := mimoDomesticPrices([]string{"mimo-v2.6-flash", "mimo-v2.6-pro", "mimo-v2.6-pro-ultraspeed"})
	flash := prices["mimo-v2.6-flash"]
	if flash == nil || flash.CacheHit != 0.02 || flash.Input != 1 || flash.Output != 2 || flash.Currency != "¥" {
		t.Fatalf("mimo-v2.6-flash price = %+v, want 0.02/1/2 ¥", flash)
	}
	pro := prices["mimo-v2.6-pro"]
	if pro == nil || pro.CacheHit != 0.025 || pro.Input != 3 || pro.Output != 6 || pro.Currency != "¥" {
		t.Fatalf("mimo-v2.6-pro price = %+v, want 0.025/3/6 ¥", pro)
	}
	ultra := prices["mimo-v2.6-pro-ultraspeed"]
	if ultra == nil || ultra.CacheHit != 0.25 || ultra.Input != 30 || ultra.Output != 60 || ultra.Currency != "¥" {
		t.Fatalf("mimo-v2.6-pro-ultraspeed price = %+v, want 0.25/30/60 ¥", ultra)
	}
}

// 手改模型列表（v2.6）+ 旧 v2.5 价格键：normalize 补齐缺失键，自定义价不动。
func TestNormalizeFillsV26PricesForUpgradedModelList(t *testing.T) {
	custom := &provider.Pricing{CacheHit: 9, Input: 9, Output: 9, Currency: "¥"}
	c := &Config{Providers: []ProviderEntry{{
		Name:    "mimo-token-plan",
		Kind:    "openai",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		Models:  []string{"mimo-v2.6-flash", "mimo-v2.6-pro"},
		Default: "mimo-v2.6-flash",
		Prices: map[string]*provider.Pricing{
			"mimo-v2.5": custom, // 历史遗留自定义价必须保留
		},
	}}}
	if !normalizeLegacyMimoProviderCatalogs(c) {
		t.Fatal("normalize should report price backfill")
	}
	p, ok := c.Provider("mimo-token-plan")
	if !ok {
		t.Fatal("mimo-token-plan provider missing")
	}
	for model, want := range map[string][3]float64{
		"mimo-v2.6-flash": {0.02, 1, 2},
		"mimo-v2.6-pro":   {0.025, 3, 6},
	} {
		got := p.PriceForModel(model)
		if got == nil || got.CacheHit != want[0] || got.Input != want[1] || got.Output != want[2] {
			t.Fatalf("%s price = %+v, want %v", model, got, want)
		}
	}
	if kept := p.Prices["mimo-v2.5"]; kept != custom {
		t.Fatalf("custom v2.5 price must stay untouched: %+v", kept)
	}
	// 幂等：第二遍不再报变更。
	if normalizeLegacyMimoProviderCatalogs(c) {
		t.Fatal("second normalize pass must be a no-op")
	}
}

// 全新装机的旧格式配置（无模型列表）仍走 v2.5 目录回填，行为不变。
func TestLegacyEmptyModelCatalogBackfillUnchanged(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name:    "mimo-token-plan",
		Kind:    "openai",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
	}}}
	normalizeLegacyMimoProviderCatalogs(c)
	p, ok := c.Provider("mimo-token-plan")
	if !ok {
		t.Fatal("mimo-token-plan provider missing")
	}
	if p.DefaultModel() != "mimo-v2.5-pro" || p.PriceForModel("mimo-v2.5-pro") == nil {
		t.Fatalf("legacy catalog backfill changed: models=%v prices=%v", p.ModelList(), p.Prices)
	}
}
