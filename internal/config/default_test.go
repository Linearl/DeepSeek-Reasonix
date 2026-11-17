package config

import "testing"

func TestDefaultCompactRatioIsEightyPercent(t *testing.T) {
	if got := Default().Agent.CompactRatio; got != 0.80 {
		t.Fatalf("compact ratio = %v, want 0.80", got)
	}
}

func TestDefaultRetiredAutoPlanCompatibilityOff(t *testing.T) {
	if got := Default().Agent.AutoPlan; got != "off" {
		t.Fatalf("default auto_plan = %q, want off", got)
	}
}

func TestDefaultReasoningLanguageAuto(t *testing.T) {
	if got := Default().ReasoningLanguage(); got != "auto" {
		t.Fatalf("default reasoning_language = %q, want auto", got)
	}
}

func TestDefaultBotRunsWithoutStepLimit(t *testing.T) {
	if got := Default().Bot.MaxSteps; got != 0 {
		t.Fatalf("default bot max_steps = %d, want continuous 0", got)
	}
}

func TestDefaultDeepSeekUsesChatWithIndependentSearch(t *testing.T) {
	cfg := Default()
	for _, name := range []string{"deepseek-flash", "deepseek-pro"} {
		provider, ok := cfg.Provider(name)
		if !ok {
			t.Fatalf("default provider %q is missing", name)
		}
		if provider.Kind != "openai" || provider.BaseURL != "https://api.deepseek.com" {
			t.Fatalf("default provider %q = kind %q base_url %q, want openai %q", name, provider.Kind, provider.BaseURL, "https://api.deepseek.com")
		}
		if !EffectiveIndependentWebSearch(provider) {
			t.Fatalf("default provider %q web_search = false, want true", name)
		}
	}
}

func TestDefaultDesktopAppearanceAutoGraphite(t *testing.T) {
	cfg := Default()
	if got := cfg.DesktopTheme(); got != "auto" {
		t.Fatalf("default desktop theme = %q, want auto", got)
	}
	if got := cfg.DesktopThemeStyle(); got != "" {
		t.Fatalf("default desktop theme style = %q, want empty so frontend resolves graphite", got)
	}
	if got := cfg.DesktopTerminalTheme(); got != "auto" {
		t.Fatalf("default desktop terminal theme = %q, want auto", got)
	}
}

// TestDesktopTelemetryAndMetricsOffByForkPolicy (tl1665, 2026-11-18) pins the
// fork's product decision「我们不做遥测」at the single gate every reporting site
// reads: the desktop must not send the anonymous launch ping nor aggregate
// desktop metrics.
//
// The two config keys stay read-only compatibility keys — an old config that
// still says `telemetry = true` must keep loading, but it must not re-enable
// anything. That is why this test writes the key directly instead of calling a
// setter (tl1665 removed the setters: the switches are gone, so nothing writes
// these keys any more).
func TestDesktopTelemetryAndMetricsOffByForkPolicy(t *testing.T) {
	cfg := Default()
	if cfg.DesktopTelemetry() {
		t.Fatal("default desktop telemetry = true, want false (the fork does not do telemetry)")
	}
	if cfg.DesktopMetrics() {
		t.Fatal("default desktop metrics = true, want false (the fork does not do telemetry)")
	}
	// A stale opt-in left in an old config file must not turn anything back on.
	enabled := true
	cfg.Desktop.Telemetry = &enabled
	cfg.Desktop.Metrics = &enabled
	if cfg.DesktopTelemetry() {
		t.Fatal("desktop telemetry = true with a stale telemetry = true key, want false")
	}
	if cfg.DesktopMetrics() {
		t.Fatal("desktop metrics = true with a stale metrics = true key, want false")
	}
	// The keys are still parsed (they are not rejected as unknown), which is
	// what keeps old config files loading.
	if cfg.Desktop.Telemetry == nil || cfg.Desktop.Metrics == nil {
		t.Fatal("the compatibility keys must stay parsed so old config files keep loading")
	}
}
