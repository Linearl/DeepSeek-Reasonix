package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"

	fileencoding "reasonix/internal/fileutil/encoding"
)

func TestStartupDeepSeekProtocolMigrationMigratesLegacyOpenAIProvidersAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	raw := `# keep this user comment
config_version = 4
default_model = "deepseek-flash/deepseek-v4-flash"
future_top_level = "preserve-me"

[[providers]]
name        = "deepseek-flash"
kind        = "openai" # legacy wire
base_url    = "https://api.deepseek.com"
model       = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
balance_url = "https://api.deepseek.com/user/balance"
context_window = 1000000

[[providers]]
name        = "deepseek-pro"
kind        = "openai"
base_url    = "https://api.deepseek.com/"
model       = "deepseek-v4-pro"
api_key_env = "DEEPSEEK_API_KEY"

[[providers]]
name = "other"
kind = "openai"
base_url = "https://gateway.example/v1"
model = "other-model"
api_key_env = "OTHER_KEY"
future_provider_field = "untouched"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	// The startup chain (boot/boot.go) is the surviving migration entry; the
	// old automatic Messages migration it replaced is gone (tl1635).
	changed, err := ApplyUserConfigUpgradesOnStartup(path)
	if err != nil {
		t.Fatalf("ApplyUserConfigUpgradesOnStartup: %v", err)
	}
	if !changed {
		t.Fatal("legacy official providers were not migrated by the startup chain")
	}
	updatedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(updatedBytes)

	cfg, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
	// The unrelated gateway provider must survive the migration untouched.
	other, ok := cfg.Provider("other")
	if !ok || other.BaseURL != "https://gateway.example/v1" || other.Kind != "openai" || other.APIKeyEnv != "OTHER_KEY" {
		t.Fatalf("unrelated provider was touched: %+v", other)
	}
	var versionHeader struct {
		ConfigVersion int `toml:"config_version"`
	}
	if _, err := toml.Decode(updated, &versionHeader); err != nil {
		t.Fatalf("decode migrated config: %v", err)
	}
	if versionHeader.ConfigVersion != Default().ConfigVersion {
		t.Fatalf("config_version = %d, want %d after startup migration", versionHeader.ConfigVersion, Default().ConfigVersion)
	}
	for _, name := range []string{"deepseek-flash", "deepseek-pro"} {
		entry, ok := cfg.Provider(name)
		if !ok {
			t.Fatalf("migrated provider %q missing", name)
		}
		// Current expected end state: the official openai-compatible transport,
		// NOT the retired anthropic Messages endpoint the removed automatic
		// migration used to write.
		if entry.Kind != "openai" || entry.BaseURL != "https://api.deepseek.com" {
			t.Errorf("migrated provider %q protocol = %s/%s, want openai/https://api.deepseek.com", name, entry.Kind, entry.BaseURL)
		}
		model := strings.TrimSpace(entry.Default)
		if model == "" {
			model = strings.TrimSpace(entry.Model)
		}
		resolved, ok := cfg.ResolveModel(name + "/" + model)
		if !ok {
			t.Fatalf("migrated provider %q model %q did not resolve", name, model)
		}
		cap := EffortCapabilityForEntry(resolved)
		if len(cap.Levels) == 0 {
			t.Errorf("migrated provider %q effort capability = %+v", name, cap)
		}
	}

	beforeSecondRun := string(updatedBytes)
	changed, err = ApplyUserConfigUpgradesOnStartup(path)
	if err != nil {
		t.Fatalf("second startup migration: %v", err)
	}
	if changed {
		t.Fatal("second startup migration unexpectedly reported a change")
	}
	afterSecondRun, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSecondRun) != beforeSecondRun {
		t.Fatal("idempotent startup migration rewrote the config on its second run")
	}
}

func TestStartupDeepSeekMigrationReportsMalformedConfigWithoutRewriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	raw := `[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"

[[plugins]]
name = "windows-mcp"
command = "C:\Users\reasonix\mcp.exe"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := ApplyUserConfigUpgradesOnStartup(path)
	if err == nil {
		t.Fatal("startup migration accepted malformed config")
	}
	if !IsDeepSeekProtocolConfigParseError(err) {
		t.Fatalf("startup migration error type = %T, want TOML parse error", err)
	}
	if changed {
		t.Fatal("startup migration reported changing malformed config")
	}
	next, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(next) != raw {
		t.Fatalf("startup migration rewrote malformed config:\n%s", next)
	}

	cfg, err := LoadForRootReadOnly(t.TempDir())
	if err != nil {
		t.Fatalf("resilient config load: %v", err)
	}
	if !cfg.HasLoadWarnings() {
		t.Fatal("resilient config loader did not expose the malformed config")
	}

	if _, err := UpgradeDeepSeekProviderProtocol(path, "deepseek"); err == nil {
		t.Fatal("explicit upgrade accepted malformed config")
	}
}

func TestUpgradeDeepSeekProviderProtocolWritesThroughSymlinkAndPreservesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared-config.toml")
	link := filepath.Join(dir, "config.toml")
	raw := `[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`
	if err := os.WriteFile(target, []byte(raw), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	changed, err := UpgradeDeepSeekProviderProtocol(link, "deepseek")
	if err != nil {
		t.Fatalf("UpgradeDeepSeekProviderProtocol: %v", err)
	}
	if !changed {
		t.Fatal("symlinked DeepSeek provider was not upgraded")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("logical config link was replaced: info=%v err=%v", info, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Fatalf("migrated target mode = %04o, want 0640", got)
		}
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `kind = "anthropic"`) ||
		!strings.Contains(string(got), `base_url = "https://api.deepseek.com/anthropic"`) {
		t.Fatalf("symlink target was not upgraded:\n%s", got)
	}
}

func TestApplyUserConfigUpgradesOnStartupSerializesConcurrentDeepSeekUpgrades(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	raw := `[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	const workers = 12
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			_, err := ApplyUserConfigUpgradesOnStartup(path)
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent startup migration: %v", err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed Config
	if _, err := toml.Decode(string(got), &parsed); err != nil {
		t.Fatalf("concurrently migrated config is invalid: %v\n%s", err, got)
	}
	if parsed.ConfigVersion != Default().ConfigVersion || len(parsed.Providers) != 1 ||
		parsed.Providers[0].Kind != "openai" || parsed.Providers[0].BaseURL != "https://api.deepseek.com" {
		t.Fatalf("concurrent startup migration produced a corrupt or partial config:\n%s", got)
	}
	if _, err := LoadForEditReadOnlyStrict(path); err != nil {
		t.Fatalf("concurrently migrated config is invalid: %v", err)
	}
}

func TestDeepSeekProtocolUpgradeAvailabilityUsesUserConfigSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("# unrelated user settings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if CanUpgradeDeepSeekProviderProtocolUserConfig("deepseek") {
		t.Fatal("an unrelated user config must not expose an upgrade for a project-only provider")
	}

	raw := `[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if !CanUpgradeDeepSeekProviderProtocolUserConfig("deepseek") {
		t.Fatal("eligible user-global provider did not expose the grouped upgrade")
	}
	if CanUpgradeDeepSeekProviderProtocolUserConfig("unrelated") {
		t.Fatal("an unrelated provider target unexpectedly exposed the DeepSeek upgrade")
	}
}

func TestStartupDeepSeekMigrationHandlesLegacyUTF16Config(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	raw := `# preserve UTF-16 configuration
[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`
	encoded := fileencoding.Encode(raw, fileencoding.UTF16LE)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := ApplyUserConfigUpgradesOnStartup(path)
	if err != nil || !changed {
		t.Fatalf("ApplyUserConfigUpgradesOnStartup on UTF-16 legacy: changed=%v err=%v", changed, err)
	}
	// The startup render path (SaveTo) re-encodes as UTF-8 — long-standing
	// behavior for every pre-7 config it upgrades. What must hold is that the
	// UTF-16 source is decoded correctly and the migrated config stays valid.
	cfg, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatalf("migrated UTF-16 config is invalid: %v", err)
	}
	entry, ok := cfg.Provider("deepseek-flash")
	if !ok || entry.Kind != "openai" || entry.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("migrated UTF-16 provider = %+v, want openai/https://api.deepseek.com", entry)
	}
}

func TestDeepSeekProtocolMigrationSupportsInlineProviderArrays(t *testing.T) {
	raw := `config_version = 4
future_prompt = """
[not-a-section]
providers = [{ name = "quoted-example", kind = "openai" }]
"""
providers = [{ name = "deepseek-flash", kind = "openai", base_url = "https://api.deepseek.com", model = "deepseek-v4-flash", api_key_env = "DEEPSEEK_API_KEY" }, { name = "other", kind = "openai", base_url = "https://gateway.example/v1", model = "other-model", api_key_env = "OTHER_KEY", headers = { X-Trace = "keep,=#value" } }, { name = "local", kind = "ollama", model = "local-model" }]
`
	next, changed, err := rewriteLegacyDeepSeekProtocol(raw, "deepseek")
	if err != nil {
		t.Fatalf("rewrite inline providers: %v", err)
	}
	if !changed {
		t.Fatal("eligible inline DeepSeek provider was not migrated")
	}
	for _, want := range []string{
		`kind = "anthropic"`,
		`base_url = "https://api.deepseek.com/anthropic"`,
		`headers = { X-Trace = "keep,=#value" }`,
		`base_url = "https://gateway.example/v1"`,
		`{ name = "local", kind = "ollama", model = "local-model" }`,
		`providers = [{ name = "quoted-example", kind = "openai" }]`,
	} {
		if !strings.Contains(next, want) {
			t.Errorf("inline migration dropped %q:\n%s", want, next)
		}
	}
	var decoded Config
	if _, err := toml.Decode(next, &decoded); err != nil {
		t.Fatalf("migrated inline TOML is invalid: %v\n%s", err, next)
	}
	if len(decoded.Providers) != 3 || decoded.Providers[0].Kind != "anthropic" || decoded.Providers[1].Kind != "openai" {
		t.Fatalf("migrated inline providers = %+v", decoded.Providers)
	}
	again, changed, err := rewriteLegacyDeepSeekProtocol(next, "deepseek")
	if err != nil {
		t.Fatalf("second inline migration: %v", err)
	}
	if changed || again != next {
		t.Fatal("inline provider migration is not idempotent")
	}
}

func TestManualDeepSeekProtocolUpgradeSupportsMultilineInlineArray(t *testing.T) {
	raw := `providers = [
  { name = "deepseek-pro", kind = 'openai', base_url = 'https://api.deepseek.com/v1', model = "deepseek-v4-pro", api_key_env = "CUSTOM_KEY", headers = { X-Route = "keep,=#route" }, future_capability = true },
]
`
	next, changed, err := rewriteLegacyDeepSeekProtocol(raw, "deepseek")
	if err != nil {
		t.Fatalf("manual inline upgrade: %v", err)
	}
	if !changed || !strings.Contains(next, `kind = "anthropic"`) ||
		!strings.Contains(next, `base_url = "https://api.deepseek.com/anthropic"`) {
		t.Fatalf("manual inline upgrade mismatch:\n%s", next)
	}
	for _, want := range []string{
		`api_key_env = "CUSTOM_KEY"`,
		`headers = { X-Route = "keep,=#route" }`,
		`future_capability = true`,
	} {
		if !strings.Contains(next, want) {
			t.Errorf("manual inline upgrade dropped %q:\n%s", want, next)
		}
	}
}

func TestManualDeepSeekProtocolUpgradePreservesCapabilitiesAndUnknownFields(t *testing.T) {
	raw := `[[providers]]
name = "deepseek-flash"
kind = 'openai'
base_url = 'https://api.deepseek.com'
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
vision = true
future_capability = "keep"

[[providers]]
name = "deepseek-pro"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-pro"
api_key_env = "DEEPSEEK_API_KEY"
reasoning_protocol = "none"
`
	next, changed, err := rewriteLegacyDeepSeekProtocol(raw, "deepseek")
	if err != nil {
		t.Fatalf("rewriteLegacyDeepSeekProtocol: %v", err)
	}
	if !changed || strings.Count(next, `kind = "anthropic"`) != 2 ||
		strings.Count(next, `base_url = "https://api.deepseek.com/anthropic"`) != 2 {
		t.Fatalf("manual family upgrade mismatch:\n%s", next)
	}
	for _, preserved := range []string{
		`vision = true`,
		`future_capability = "keep"`,
		`reasoning_protocol = "none"`,
	} {
		if !strings.Contains(next, preserved) {
			t.Errorf("manual upgrade dropped %q:\n%s", preserved, next)
		}
	}
}

func TestDeepSeekProtocolMigrationSupportsQuotedKeys(t *testing.T) {
	raw := `[[providers]]
"name" = "deepseek-flash"
'kind' = 'openai'
"base_url" = 'https://api.deepseek.com'
'model' = "deepseek-v4-flash"
"api_key_env" = "DEEPSEEK_API_KEY"
`
	next, changed, err := rewriteLegacyDeepSeekProtocol(raw, "deepseek")
	if err != nil {
		t.Fatalf("rewrite quoted-key provider: %v", err)
	}
	if !changed {
		t.Fatal("quoted-key provider was not migrated")
	}
	for _, want := range []string{
		`'kind' = "anthropic"`,
		`"base_url" = "https://api.deepseek.com/anthropic"`,
	} {
		if !strings.Contains(next, want) {
			t.Errorf("migration changed or dropped %q:\n%s", want, next)
		}
	}
	var decoded Config
	if _, err := toml.Decode(next, &decoded); err != nil {
		t.Fatalf("migrated quoted-key TOML is invalid: %v\n%s", err, next)
	}
	if len(decoded.Providers) != 1 || decoded.Providers[0].Kind != "anthropic" || decoded.Providers[0].BaseURL != deepSeekAnthropicBaseURL {
		t.Fatalf("migrated quoted-key provider = %+v", decoded.Providers)
	}
}

func TestDeepSeekProtocolMigrationSupportsQuotedProviderTableHeaders(t *testing.T) {
	raw := `[["providers"]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"

[['providers']]
name = "deepseek-pro"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-pro"
api_key_env = "DEEPSEEK_API_KEY"
`
	next, changed, err := rewriteLegacyDeepSeekProtocol(raw, "deepseek")
	if err != nil {
		t.Fatalf("rewrite quoted provider table headers: %v", err)
	}
	if !changed || strings.Count(next, `kind = "anthropic"`) != 2 ||
		strings.Count(next, `base_url = "https://api.deepseek.com/anthropic"`) != 2 {
		t.Fatalf("quoted provider table headers were not migrated:\n%s", next)
	}
	for _, header := range []string{`[["providers"]]`, `[['providers']]`} {
		if !strings.Contains(next, header) {
			t.Errorf("migration changed provider table header %q:\n%s", header, next)
		}
	}
	var decoded Config
	if _, err := toml.Decode(next, &decoded); err != nil {
		t.Fatalf("migrated quoted-header TOML is invalid: %v\n%s", err, next)
	}
	if len(decoded.Providers) != 2 || decoded.Providers[0].Kind != "anthropic" || decoded.Providers[1].Kind != "anthropic" {
		t.Fatalf("migrated quoted-header providers = %+v", decoded.Providers)
	}
}

func TestManualDeepSeekProtocolUpgradeSkipsMultilineProviderText(t *testing.T) {
	raw := `[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
description = '''
kind = "example"
base_url = "https://example.invalid"
'''
`
	next, changed, err := rewriteLegacyDeepSeekProtocol(raw, "deepseek")
	if err != nil {
		t.Fatalf("manual multiline upgrade: %v", err)
	}
	if !changed || !strings.Contains(next, `kind = "anthropic"`) || !strings.Contains(next, `base_url = "https://api.deepseek.com/anthropic"`) {
		t.Fatalf("manual multiline upgrade mismatch:\n%s", next)
	}
	for _, want := range []string{
		`kind = "example"`,
		`base_url = "https://example.invalid"`,
	} {
		if !strings.Contains(next, want) {
			t.Errorf("multiline provider text changed or dropped %q:\n%s", want, next)
		}
	}
	var decoded Config
	if _, err := toml.Decode(next, &decoded); err != nil {
		t.Fatalf("migrated multiline TOML is invalid: %v\n%s", err, next)
	}
	if len(decoded.Providers) != 1 || decoded.Providers[0].Kind != "anthropic" {
		t.Fatalf("migrated multiline provider = %+v", decoded.Providers)
	}
}

func TestCanUpgradeDeepSeekProviderProtocolRejectsProxyButAllowsExplicitUpgradeOfCustomization(t *testing.T) {
	base := ProviderEntry{
		Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com",
		Model: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY",
	}
	if !CanUpgradeDeepSeekProviderProtocol(&base) {
		t.Fatal("standard official provider should offer manual upgrade")
	}
	proxy := base
	proxy.BaseURL = "https://deepseek.example/v1"
	if CanUpgradeDeepSeekProviderProtocol(&proxy) {
		t.Fatal("proxy endpoint should not offer manual upgrade")
	}
	headers := base
	headers.Headers = map[string]string{"X-Route": "custom"}
	if !CanUpgradeDeepSeekProviderProtocol(&headers) {
		t.Fatal("custom headers should block automatic migration, not the explicit upgrade action")
	}
	versioned := base
	versioned.BaseURL = "https://api.deepseek.com/v1"
	if !CanUpgradeDeepSeekProviderProtocol(&versioned) {
		t.Fatal("the official /v1 compatibility address should offer the explicit upgrade action")
	}
	customKey := base
	customKey.APIKeyEnv = "MY_DEEPSEEK_KEY"
	if !CanUpgradeDeepSeekProviderProtocol(&customKey) {
		t.Fatal("an official provider with a custom key env should offer the explicit upgrade action")
	}
}

func TestNormalizeOfficialDeepSeekModelsAddsProToResponses(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name: "deepseek", Kind: "responses", BaseURL: "https://api.deepseek.com",
		Model: "deepseek-v4-flash",
	}}}

	normalizeOfficialDeepSeekModels(c)
	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("DeepSeek provider missing after normalization")
	}
	if !p.HasModel("deepseek-v4-flash") || !p.HasModel("deepseek-v4-pro") {
		t.Fatalf("Responses models = %v, want Flash and Pro", p.ModelList())
	}
}

func TestNormalizeOfficialDeepSeekResponsesPresetAddsPro(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name: "deepseek-responses", Kind: "responses", BaseURL: "https://api.deepseek.com",
		Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
	}}}

	normalizeOfficialDeepSeekModels(c)
	p, ok := c.Provider("deepseek-responses")
	if !ok {
		t.Fatal("deepseek-responses provider missing after normalization")
	}
	if !p.HasModel("deepseek-v4-flash") || !p.HasModel("deepseek-v4-pro") {
		t.Fatalf("deepseek-responses models = %v, want Flash and Pro", p.ModelList())
	}
	if p.Default != "deepseek-v4-flash" {
		t.Fatalf("default = %q, want deepseek-v4-flash", p.Default)
	}
	flash := p.ModelOverrides["deepseek-v4-flash"]
	if !containsString(flash.SupportedEfforts, "low") {
		t.Fatalf("Flash effort override = %+v", flash)
	}
	pro := p.ModelOverrides["deepseek-v4-pro"]
	if !containsString(pro.SupportedEfforts, "low") || !containsString(pro.SupportedEfforts, "max") {
		t.Fatalf("Pro effort override = %+v", pro)
	}
}

func TestNormalizeOfficialDeepSeekMultiModelPreservesProviderEfforts(t *testing.T) {
	for _, tc := range []struct {
		name, providerName, kind, baseURL string
	}{
		{name: "responses", providerName: "deepseek-responses", kind: "responses", baseURL: "https://api.deepseek.com"},
		{name: "anthropic", providerName: "deepseek", kind: "anthropic", baseURL: deepSeekAnthropicBaseURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Providers: []ProviderEntry{{
				Name: tc.providerName, Kind: tc.kind, BaseURL: tc.baseURL,
				Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash",
				SupportedEfforts: []string{"disabled", "high"}, DefaultEffort: "high",
			}}}

			normalizeOfficialDeepSeekModels(c)
			for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
				entry, ok := c.ResolveModel(tc.providerName + "/" + model)
				if !ok {
					t.Fatalf("%s did not resolve", model)
				}
				if !stringSlicesEqual(entry.SupportedEfforts, []string{"disabled", "high"}) {
					t.Errorf("%s supported_efforts = %v, want provider-level custom vocabulary", model, entry.SupportedEfforts)
				}
				if _, err := NormalizeEffort(entry, "low"); err == nil {
					t.Errorf("%s unexpectedly accepted low outside provider-level vocabulary", model)
				}
			}
		})
	}
}

func TestNormalizeOfficialDeepSeekProviderEffortsKeepsExplicitModelOverride(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name: "deepseek-responses", Kind: "responses", BaseURL: "https://api.deepseek.com",
		Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash",
		SupportedEfforts: []string{"disabled", "high"}, DefaultEffort: "high",
		ModelOverrides: map[string]ProviderModelOverride{
			"deepseek-v4-pro": {SupportedEfforts: []string{"disabled", "low", "high"}, DefaultEffort: "low"},
		},
	}}}

	normalizeOfficialDeepSeekModels(c)
	pro, ok := c.ResolveModel("deepseek-responses/deepseek-v4-pro")
	if !ok {
		t.Fatal("Pro did not resolve")
	}
	if !stringSlicesEqual(pro.SupportedEfforts, []string{"disabled", "low", "high"}) || pro.DefaultEffort != "low" {
		t.Fatalf("Pro override = %v/%q, want explicit per-model values", pro.SupportedEfforts, pro.DefaultEffort)
	}
}

func TestNormalizeOfficialDeepSeekResponsesDoesNotRestoreUncheckedPro(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]ProviderModelOverride
	}{
		{
			name: "settings uncheck keeps flash override",
			overrides: map[string]ProviderModelOverride{
				"deepseek-v4-flash": {SupportedEfforts: []string{"disabled", "low", "high", "max"}, DefaultEffort: "high"},
			},
		},
		{
			name: "leftover pro override is still treated as curated",
			overrides: map[string]ProviderModelOverride{
				"deepseek-v4-pro": {SupportedEfforts: []string{"disabled", "low", "high", "max"}, DefaultEffort: "high"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Providers: []ProviderEntry{{
				Name: "deepseek-responses", Kind: "responses", BaseURL: "https://api.deepseek.com",
				Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
				ModelOverrides: tc.overrides,
			}}}

			normalizeOfficialDeepSeekModels(c)
			p, ok := c.Provider("deepseek-responses")
			if !ok {
				t.Fatal("deepseek-responses provider missing after normalization")
			}
			if p.HasModel("deepseek-v4-pro") {
				t.Fatalf("unchecked Pro was restored: %v", p.ModelList())
			}
		})
	}
}

func TestNormalizeOfficialDeepSeekResponsesAddsProPriceForLegacyFlashPrice(t *testing.T) {
	flash := deepSeekV4FlashPriceUSD()
	c := &Config{Providers: []ProviderEntry{{
		Name: "deepseek-responses", Kind: "responses", BaseURL: "https://api.deepseek.com",
		Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
		Price: flash,
	}}}

	normalizeOfficialDeepSeekModels(c)
	p, ok := c.Provider("deepseek-responses")
	if !ok {
		t.Fatal("deepseek-responses provider missing after normalization")
	}
	if !p.HasModel("deepseek-v4-pro") {
		t.Fatalf("Responses models = %v, want Flash and Pro", p.ModelList())
	}
	if got := p.PriceForModel("deepseek-v4-flash"); !samePricing(got, flash) {
		t.Fatalf("Flash price = %+v, want legacy singular price %+v", got, flash)
	}
	if got := p.PriceForModel("deepseek-v4-pro"); !samePricing(got, deepSeekV4ProPriceUSD()) {
		t.Fatalf("Pro price = %+v, want official Pro list price", got)
	}
}
