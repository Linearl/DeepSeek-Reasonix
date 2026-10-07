package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestBashTimeoutSecondsDefaultsToSafetyCap(t *testing.T) {
	cfg := Default()
	if cfg.Tools.BashTimeoutSeconds != nil {
		t.Fatalf("default raw bash timeout = %v, want nil", *cfg.Tools.BashTimeoutSeconds)
	}
	if got := cfg.BashTimeoutSeconds(); got != 300 {
		t.Fatalf("BashTimeoutSeconds() = %d, want 300 (fork default)", got)
	}
}

func TestBashTimeoutSecondsAllowsExplicitZero(t *testing.T) {
	cfg := Default()
	cfg.Tools.BashTimeoutSeconds = intPtr(0)
	if got := cfg.BashTimeoutSeconds(); got != 0 {
		t.Fatalf("BashTimeoutSeconds() = %d, want 0", got)
	}
}

func TestBashTimeoutSecondsParsesExplicitZero(t *testing.T) {
	cfg := Default()
	if _, err := toml.Decode("[tools]\nbash_timeout_seconds = 0\n", cfg); err != nil {
		t.Fatalf("decode explicit zero: %v", err)
	}
	if cfg.Tools.BashTimeoutSeconds == nil {
		t.Fatal("explicit zero decoded as nil")
	}
	if got := cfg.BashTimeoutSeconds(); got != 0 {
		t.Fatalf("BashTimeoutSeconds() = %d, want 0", got)
	}
}

func TestBashTimeoutSecondsFallsBackForNegative(t *testing.T) {
	cfg := Default()
	cfg.Tools.BashTimeoutSeconds = intPtr(-1)
	if got := cfg.BashTimeoutSeconds(); got != 300 {
		t.Fatalf("BashTimeoutSeconds() = %d, want 300 (fork default)", got)
	}
}

func TestMCPCallTimeoutSecondsDefaultsToSafetyCap(t *testing.T) {
	cfg := Default()
	if cfg.Tools.MCPCallTimeoutSeconds != nil {
		t.Fatalf("default raw MCP call timeout = %v, want nil", *cfg.Tools.MCPCallTimeoutSeconds)
	}
	if got := cfg.MCPCallTimeoutSeconds(); got != 300 {
		t.Fatalf("MCPCallTimeoutSeconds() = %d, want 300", got)
	}
}

func TestMCPCallTimeoutSecondsExplicitPositive(t *testing.T) {
	cfg := Default()
	if _, err := toml.Decode("[tools]\nmcp_call_timeout_seconds = 600\n", cfg); err != nil {
		t.Fatalf("decode MCP timeout: %v", err)
	}
	if cfg.Tools.MCPCallTimeoutSeconds == nil {
		t.Fatal("explicit MCP timeout decoded as nil")
	}
	if got := cfg.MCPCallTimeoutSeconds(); got != 600 {
		t.Fatalf("MCPCallTimeoutSeconds() = %d, want 600", got)
	}
}

func TestMCPCallTimeoutSecondsFallsBackForZeroOrNegative(t *testing.T) {
	cfg := Default()
	cfg.Tools.MCPCallTimeoutSeconds = intPtr(0)
	if got := cfg.MCPCallTimeoutSeconds(); got != 300 {
		t.Fatalf("zero MCPCallTimeoutSeconds() = %d, want 300", got)
	}
	cfg.Tools.MCPCallTimeoutSeconds = intPtr(-1)
	if got := cfg.MCPCallTimeoutSeconds(); got != 300 {
		t.Fatalf("negative MCPCallTimeoutSeconds() = %d, want 300", got)
	}
}

func TestMCPStartupTimeoutSecondsDefaultsToBackgroundSafetyCap(t *testing.T) {
	cfg := Default()
	if cfg.Tools.MCPStartupTimeoutSeconds != nil {
		t.Fatalf("default raw MCP startup timeout = %v, want nil", *cfg.Tools.MCPStartupTimeoutSeconds)
	}
	if got := cfg.MCPStartupTimeoutSeconds(); got != 30 {
		t.Fatalf("MCPStartupTimeoutSeconds() = %d, want 30", got)
	}
}

func TestMCPStartupTimeoutSecondsExplicitPositive(t *testing.T) {
	cfg := Default()
	if _, err := toml.Decode("[tools]\nmcp_startup_timeout_seconds = 45\n", cfg); err != nil {
		t.Fatalf("decode MCP startup timeout: %v", err)
	}
	if got := cfg.MCPStartupTimeoutSeconds(); got != 45 {
		t.Fatalf("MCPStartupTimeoutSeconds() = %d, want 45", got)
	}
}

func TestMCPStartupTimeoutSecondsFallsBackForZeroOrNegative(t *testing.T) {
	cfg := Default()
	cfg.Tools.MCPStartupTimeoutSeconds = intPtr(0)
	if got := cfg.MCPStartupTimeoutSeconds(); got != 30 {
		t.Fatalf("zero MCPStartupTimeoutSeconds() = %d, want 30", got)
	}
	cfg.Tools.MCPStartupTimeoutSeconds = intPtr(-1)
	if got := cfg.MCPStartupTimeoutSeconds(); got != 30 {
		t.Fatalf("negative MCPStartupTimeoutSeconds() = %d, want 30", got)
	}
}

func TestBackgroundJobStalledWarningSecondsDefault(t *testing.T) {
	cfg := Default()
	if cfg.Tools.BackgroundJobs.StalledWarningSeconds != nil {
		t.Fatalf("default raw stalled warning = %v, want nil", *cfg.Tools.BackgroundJobs.StalledWarningSeconds)
	}
	if got := cfg.BackgroundJobStalledWarningSeconds(); got != 900 {
		t.Fatalf("BackgroundJobStalledWarningSeconds() = %d, want 900", got)
	}
}

func TestBackgroundJobStalledWarningSecondsAllowsExplicitZero(t *testing.T) {
	cfg := Default()
	cfg.Tools.BackgroundJobs.StalledWarningSeconds = intPtr(0)
	if got := cfg.BackgroundJobStalledWarningSeconds(); got != 0 {
		t.Fatalf("BackgroundJobStalledWarningSeconds() = %d, want 0", got)
	}
}

func TestBackgroundJobStalledWarningSecondsParsesExplicitZero(t *testing.T) {
	cfg := Default()
	if _, err := toml.Decode("[tools.background_jobs]\nstalled_warning_seconds = 0\n", cfg); err != nil {
		t.Fatalf("decode explicit zero: %v", err)
	}
	if cfg.Tools.BackgroundJobs.StalledWarningSeconds == nil {
		t.Fatal("explicit zero decoded as nil")
	}
	if got := cfg.BackgroundJobStalledWarningSeconds(); got != 0 {
		t.Fatalf("BackgroundJobStalledWarningSeconds() = %d, want 0", got)
	}
}

func TestBackgroundJobStalledWarningSecondsBounds(t *testing.T) {
	cfg := Default()
	cfg.Tools.BackgroundJobs.StalledWarningSeconds = intPtr(-1)
	if got := cfg.BackgroundJobStalledWarningSeconds(); got != 900 {
		t.Fatalf("negative BackgroundJobStalledWarningSeconds() = %d, want 900", got)
	}
	cfg.Tools.BackgroundJobs.StalledWarningSeconds = intPtr(90000)
	if got := cfg.BackgroundJobStalledWarningSeconds(); got != 86400 {
		t.Fatalf("oversized BackgroundJobStalledWarningSeconds() = %d, want 86400", got)
	}
}

// 任务553：idle-session wake 四键的默认/显式/钳制三态。
func TestBackgroundJobWakeAccessorsDefaultOff(t *testing.T) {
	cfg := Default()
	if cfg.BackgroundJobWakeIdleSession() {
		t.Fatal("wake_idle_session must default to false (iron rule 2)")
	}
	if got := cfg.BackgroundJobWakeMaxTurnsPerWindow(); got != 3 {
		t.Fatalf("default wake_max_turns_per_window = %d, want 3", got)
	}
	if got := cfg.BackgroundJobWakeWindowSeconds(); got != 600 {
		t.Fatalf("default wake_window_seconds = %d, want 600", got)
	}
	if got := cfg.BackgroundJobWakeThrottleSeconds(); got != 60 {
		t.Fatalf("default wake_throttle_seconds = %d, want 60", got)
	}
}

func TestBackgroundJobWakeAccessorsExplicitAndClamped(t *testing.T) {
	cfg := Default()
	cfg.Tools.BackgroundJobs.WakeIdleSession = boolPtr(true)
	cfg.Tools.BackgroundJobs.WakeMaxTurnsPerWindow = intPtr(0) // 0 也回默认：预算不可经配置拆除
	cfg.Tools.BackgroundJobs.WakeWindowSeconds = intPtr(-1)
	cfg.Tools.BackgroundJobs.WakeThrottleSeconds = intPtr(999999)
	if !cfg.BackgroundJobWakeIdleSession() {
		t.Fatal("explicit wake_idle_session = true did not resolve")
	}
	if got := cfg.BackgroundJobWakeMaxTurnsPerWindow(); got != 3 {
		t.Fatalf("zero wake_max_turns_per_window = %d, want the default 3", got)
	}
	if got := cfg.BackgroundJobWakeWindowSeconds(); got != 600 {
		t.Fatalf("negative wake_window_seconds = %d, want the default 600", got)
	}
	if got := cfg.BackgroundJobWakeThrottleSeconds(); got != 86400 {
		t.Fatalf("oversized wake_throttle_seconds = %d, want 86400", got)
	}
}

// 任务553：diff 视图必须独立发射 wake 键——只设 wake 不设 stalled 时不得丢键，
// 且输出是合法 TOML（单表头）。
func TestRenderTOMLProjectDeltaEmitsWakeKeysWithoutStalled(t *testing.T) {
	custom := Default()
	custom.Tools.BackgroundJobs.WakeIdleSession = boolPtr(true)
	custom.Tools.BackgroundJobs.WakeThrottleSeconds = intPtr(120)

	delta := RenderTOMLProjectDelta(custom)
	if !strings.Contains(delta, "wake_idle_session = true") {
		t.Fatalf("project delta dropped wake_idle_session:\n%s", delta)
	}
	if !strings.Contains(delta, "wake_throttle_seconds = 120") {
		t.Fatalf("project delta dropped wake_throttle_seconds:\n%s", delta)
	}
	if strings.Count(delta, "[tools.background_jobs]") != 1 {
		t.Fatalf("project delta must emit the section header exactly once:\n%s", delta)
	}
	var got Config
	if _, err := toml.Decode(delta, &got); err != nil {
		t.Fatalf("delta TOML does not parse: %v\n%s", err, delta)
	}
	if got.Tools.BackgroundJobs.WakeIdleSession == nil || !*got.Tools.BackgroundJobs.WakeIdleSession {
		t.Fatal("wake_idle_session did not round-trip through the delta render")
	}
}

// 任务553：全量视图始终带着四键的解析值（含默认），且可解析回读。
func TestRenderTOMLFullViewCarriesWakeKeys(t *testing.T) {
	rendered := RenderTOML(Default())
	for _, key := range []string{
		"wake_idle_session = false",
		"wake_max_turns_per_window = 3",
		"wake_window_seconds = 600",
		"wake_throttle_seconds = 60",
	} {
		if !strings.Contains(rendered, key) {
			t.Fatalf("full render missing %q:\n%s", key, rendered)
		}
	}
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("full render does not parse: %v", err)
	}
}
