package boot

import (
	"net/http"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

// NewProvider builds a provider.Provider from a configured entry. Exported so
// custom assemblers (e.g. the ACP per-session factory) can reuse it without
// going through the full Build.
func NewProvider(e *config.ProviderEntry) (provider.Provider, error) {
	return NewProviderWithProxy(e, netclient.ProxySpec{Mode: netclient.ModeAuto})
}

// NewProviderWithProxy builds a provider.Provider with the configured ordinary
// network proxy settings.
func NewProviderWithProxy(e *config.ProviderEntry, proxy netclient.ProxySpec) (provider.Provider, error) {
	return NewProviderWithProxyAndModelInfo(e, proxy, nil)
}

// NewProviderWithProxyAndModelInfo builds a provider while preserving the
// adapter-resolved metadata for the exact model instance.
func NewProviderWithProxyAndModelInfo(e *config.ProviderEntry, proxy netclient.ProxySpec, modelInfo *provider.ModelInfo) (provider.Provider, error) {
	return newProviderWithSearchMode(e, proxy, modelInfo, true, nil)
}

// NewProviderWithProxyConfig is the config-aware variant (task 1056): it takes
// the loaded *config.Config so the [network.retry_budget] lab switch (default
// off) can stamp its transport knob into the provider's Extra. Callers that
// have a *config.Config should prefer this over NewProviderWithProxy — the
// legacy wrapper stays signature-compatible for callers with no config in
// scope (passing nil is safe: providerBudgetFromConfig treats nil as "switch
// off", today's behaviour).
func NewProviderWithProxyConfig(e *config.ProviderEntry, proxy netclient.ProxySpec, cfg *config.Config) (provider.Provider, error) {
	return newProviderWithSearchMode(e, proxy, nil, true, cfg)
}

// clientSearch suppresses new native searches while retaining the adapter's
// ability to read and replay existing native search history.
func newProviderWithSearchMode(e *config.ProviderEntry, proxy netclient.ProxySpec, modelInfo *provider.ModelInfo, clientSearch bool, cfg *config.Config) (provider.Provider, error) {
	if err := config.ValidateProviderEndpoint(e); err != nil {
		return nil, err
	}
	if err := config.ReasoningCapabilityForEntry(e).Validate(e.Model, config.EffectiveEffort(e)); err != nil {
		return nil, err
	}
	// Task 1008: the audio gate rides the same resolution as vision — the
	// resolver folds the per-model override into the modality set and applies
	// the Anthropic protocol blacklist. Adapters with a non-nil ModelInfo
	// re-derive it from SupportsInput(audio), so both paths agree.
	audioInput := false
	if modelInfo != nil {
		audioInput = modelInfo.SupportsInput(provider.ModalityAudio)
	} else {
		resolved := config.NewModelCapabilityResolver().Resolve(e)
		modelInfo = &resolved.ModelInfo
		audioInput = resolved.AudioInputEnabled
	}
	var tunnelClient *http.Client
	if endpoint := e.CredentialProxyURL(); endpoint != "" {
		var err error
		tunnelClient, err = netclient.NewModelCredentialProxyClient(endpoint, e.APIKey())
		if err != nil {
			return nil, err
		}
	}
	// 任务1056-B: the retry-budget lab switch (default off) stamps the
	// transport-budget knob into Extra so the concrete dialer (openai/
	// anthropic/responses newHTTPClient) can pick up a short dial timeout and
	// force a fresh DNS lookup per attempt. Off-state: no key, no change.
	extra := map[string]any{
		"api_key_env":        e.APIKeyEnv,
		"api_key_source":     e.APIKeySourceLabel(),
		"thinking":           e.Thinking,
		"effort":             config.EffectiveEffort(e),
		"supported_efforts":  e.SupportedEfforts,
		"reasoning_protocol": config.ReasoningProtocolForEntry(e),
		"max_output_tokens":  e.MaxOutputTokens,
		"chat_url":           e.ChatURL,
		"request_url":        e.RequestURL,
		"headers":            e.Headers,
		"extra_body":         e.ExtraBody,
		"auth_header":        e.AuthHeader,
		"proxy_spec":         proxy,
		"vision":             config.EffectiveVision(e),
		"audio":              audioInput,
		"vision_detail":      e.VisionDetail,
		"web_search":         config.EffectiveWebSearch(e),
		"client_web_search":  clientSearch,
		"reject_redirects":   !clientSearch,
		"mode":               e.ResponsesMode,
		// Keep nil as nil so the responses provider can vendor-detect its
		// default instead of accidentally treating every endpoint as stateful.
		"stateful": e.ResponsesStateful,
	}
	if budget := providerBudgetFromConfig(cfg); budget != nil {
		extra["retry_budget"] = *budget
	}
	// 任务 tl1327: the [network.dial_gate] family (default on, built-in
	// defaults) reaches the process-wide dial gate from the same config-aware
	// construction path as the retry budget above. All-default config stamps
	// nothing, so the gate's breaker state survives provider rebuilds.
	applyDialGateFromConfig(cfg)
	// 任务1308 (收窄于 831×1308 裁决 2026-11-03): live credential re-auth — the
	// HTTP adapters read this hook and SendWithRetry calls it exactly once,
	// only after the accepted snapshot's key was actually rejected with a 401,
	// so an externally rotated credential still self-heals instead of
	// 401-looping until a rebuild. There is NO proactive refresh: a running
	// turn keeps the boot-accepted snapshot's model+key even when config or
	// credentials change mid-run — 配置变更轮内冻结、下一回合生效 (contract
	// pin: TestModelSettingsChildCreatedAfterSaveInheritsAcceptedRunSnapshot).
	// Entries without an api_key_env get no hook (auth failures keep their
	// fast-fail). Probe overrides are excluded too (KeyIsProbeOverride): the
	// caller handed over the exact draft credential to send
	// (WithAPIKeyForProbe), and a re-read would silently swap it for the
	// env/store value — the settings probe then tests the wrong key (desktop
	// TestProviderDraftProbesDoNotPersistCredentialsOrConfiguration 稳定红根因).
	if refresher := providerCredentialRefresher(e); refresher != nil && !e.KeyIsProbeOverride() {
		extra["reauth"] = refresher
	}
	return provider.New(e.Kind, provider.Config{
		HTTPClient: tunnelClient,
		Name:       e.Name, DisplayName: e.DisplayName, Protocol: e.Kind,
		BaseURL: e.BaseURL, Model: e.Model, APIKey: e.APIKey(), ModelInfo: modelInfo,
		Extra: extra,
	})
}

// providerCredentialRefresher builds the 任务1308 live re-auth hook for an
// entry: each call re-reads the global credentials store exactly where config
// load resolved from, fresh and uncached. The adapters invoke it only on a 401
// rejection (831×1308 收窄: no proactive polling), so a credential rotated
// after boot is picked up on the failure it causes, while a turn that has not
// been rejected keeps its accepted snapshot key. Nil for entries without an
// api_key_env — those keep the fixed build-time key behaviour.
func providerCredentialRefresher(e *config.ProviderEntry) func() (string, bool) {
	if e == nil {
		return nil
	}
	key := strings.TrimSpace(e.APIKeyEnv)
	if key == "" {
		return nil
	}
	return func() (string, bool) {
		return config.RefreshProviderCredential(key)
	}
}

// providerBudgetFromConfig resolves the task-1056-B retry-budget lab switch to
// the transport-knob half of the budget. Nil (no Extra key) when the switch is
// off, unset, or no config is available — the default-off contract every
// other piece of this task depends on.
func providerBudgetFromConfig(cfg *config.Config) *provider.TransportBudget {
	if cfg == nil {
		return nil
	}
	b := cfg.NetworkRetryBudget()
	if !b.Enabled {
		return nil
	}
	return &provider.TransportBudget{DialTimeout: b.DialTimeout, ForceDNSRefresh: true}
}

// applyDialGateFromConfig hands the [network.dial_gate] family (任务 tl1327) to
// the process-wide dial gate. The gate itself is on by default with built-in
// defaults, so an all-default family stamps nothing — the process gate is left
// exactly as constructed (no breaker-state reset across provider rebuilds).
// Only an explicit override (disabled = true or any knob) swaps the gate;
// swaps are idempotent, so per-provider construction is safe. nil config
// (legacy assemblers, desktop probes) stamps nothing.
func applyDialGateFromConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	d := cfg.NetworkDialGate()
	if !d.Disabled && d.MaxConcurrent <= 0 && d.FailureThreshold <= 0 && d.Cooldown <= 0 {
		return
	}
	settings := provider.DialGateSettings{
		MaxConcurrent:    d.MaxConcurrent,
		FailureThreshold: d.FailureThreshold,
		Cooldown:         d.Cooldown,
	}
	if d.Disabled {
		settings.Disabled = &d.Disabled
	}
	provider.ConfigureDialGate(settings)
}
