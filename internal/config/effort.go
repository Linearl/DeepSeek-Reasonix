package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/provider/openai"
)

const (
	ReasoningProtocolAuto     = "auto"
	ReasoningProtocolDeepSeek = "deepseek"
	ReasoningProtocolGLM      = "glm"
	ReasoningProtocolKimiK3   = "kimi-k3"
	ReasoningProtocolOpenAI   = "openai"
	ReasoningProtocolNone     = "none"
)

// EffortCapability describes the abstract effort levels a provider/model can set
// through the /effort command.
type EffortCapability struct {
	Supported bool
	Levels    []string
	Default   string
	// AliasFold marks the MiMo compatibility vocabulary (task effortfix2,
	// A-line P2): display surfaces fold it onto the four honest tiers, and
	// ONLY it — the flag is the provider/protocol identity, not a vocabulary
	// shape sniff, so honest xhigh/max sets (anthropic-family, luna) are never
	// folded. Config-declared supported_efforts never set it: a user-authored
	// vocabulary is displayed verbatim.
	AliasFold bool
}

type modelReasoningCapability struct {
	Protocol string
	Levels   []string
	Default  string
	Aliases  map[string]string
}

var modelReasoningCapabilities = map[string]modelReasoningCapability{
	"deepseek-v4-flash": {
		Protocol: ReasoningProtocolDeepSeek,
		Levels:   []string{"disabled", "low", "high", "max"},
		Default:  "high",
		Aliases:  map[string]string{"medium": "high", "xhigh": "high"},
	},
	"deepseek-v4-pro": {
		Protocol: ReasoningProtocolDeepSeek,
		Levels:   []string{"disabled", "low", "high", "max"},
		Default:  "high",
		Aliases:  map[string]string{"medium": "high", "xhigh": "high"},
	},
}

// EffortCapabilityForEntry returns the user-facing /effort levels for a resolved
// provider entry. Provider implementations still decide how a stored effort is
// serialized into requests.
func EffortCapabilityForEntry(e *ProviderEntry) EffortCapability {
	explicitProtocol := explicitReasoningProtocol(e)
	if explicitProtocol == ReasoningProtocolNone {
		return EffortCapability{}
	}
	// Kimi K3 is a complete wire contract, including its fixed effort
	// vocabulary. Keep any persisted supported_efforts metadata dormant while
	// the protocol is selected so switching protocols can restore it later.
	if explicitProtocol == ReasoningProtocolKimiK3 {
		return kimiK3EffortCapability()
	}
	supported := normalizedSupportedEfforts(e)
	if len(supported) > 0 {
		// Hint deliberately drops the +1: len(supported)+1 is an overflow-prone
		// sum for the allocator on 32-bit builds (codeql
		// go/allocation-size-overflow); the "auto" prepend costs at most one
		// amortized growth.
		levels := make([]string, 0, len(supported))
		levels = append(levels, "auto")
		levels = append(levels, supported...)
		def := normalizeEffortLevel(e.DefaultEffort)
		if def == "" || !containsString(supported, def) {
			def = supported[0]
		}
		return EffortCapability{Supported: true, Levels: levels, Default: def}
	}
	switch explicitProtocol {
	case ReasoningProtocolDeepSeek:
		if cap, ok := resolvedModelReasoningCapability(e); ok && cap.Protocol == ReasoningProtocolDeepSeek {
			return effortCapabilityFromModel(cap)
		}
		return deepSeekEffortCapability(e)
	case ReasoningProtocolGLM:
		return glmEffortCapability()
	case ReasoningProtocolOpenAI:
		if isMimoEntry(e) {
			// MiMo documents an 8-level reasoning.effort scale
			// (none|minimal|low|medium|high|xhigh|max|ultra, default
			// "enabled"). See mimoEffortCapability for the wire
			// normalization onto low/medium/high/none.
			return mimoEffortCapability()
		}
		return openAIEffortCapability()
	}
	if cap, ok := resolvedModelReasoningCapability(e); ok {
		return effortCapabilityFromModel(cap)
	}
	// MiMo endpoints: auto-detect effort support even when reasoning_protocol
	// is not explicitly set. All *.xiaomimimo.com hosts (public API and
	// enterprise) are eligible — the vendor documents the same 8-level
	// reasoning.effort scale regardless of endpoint.
	if isMimoEntry(e) {
		return mimoEffortCapability()
	}
	switch ReasoningProtocolForEntry(e) {
	case ReasoningProtocolDeepSeek:
		return deepSeekEffortCapability(e)
	case ReasoningProtocolGLM:
		return glmEffortCapability()
	case ReasoningProtocolKimiK3:
		return kimiK3EffortCapability()
	case ReasoningProtocolOpenAI:
		return openAIEffortCapability()
	}
	switch {
	case isMiniMaxEntry(e):
		// MiniMax-M3 only exposes a binary thinking knob (adaptive|disabled)
		// on its OpenAI-compatible endpoint, so /effort mirrors the API
		// vocabulary verbatim. Default is "adaptive" because the M3 model
		// runs with thinking on out of the box; "auto" means "don't override
		// the model default" (== adaptive for M3).
		return EffortCapability{Supported: true, Levels: []string{"auto", "adaptive", "disabled"}, Default: "adaptive"}
	case isZhipuEntry(e):
		// Zhipu GLM exposes a binary thinking knob (enabled|disabled) on its
		// OpenAI-compatible endpoint and ignores reasoning_effort, so /effort
		// mirrors that vocabulary. Default is "enabled" because GLM runs with
		// thinking on out of the box; "auto" means "don't override the model
		// default" (== enabled for GLM).
		return glmEffortCapability()
	case isLongCatEntry(e):
		// LongCat exposes the same binary thinking vocabulary on its
		// OpenAI-compatible endpoint and documents no reasoning_effort depth scale.
		return EffortCapability{Supported: true, Levels: []string{"auto", "enabled", "disabled"}, Default: "enabled"}
	case isOllamaCloudEntry(e):
		// Ollama Cloud accepts top-level reasoning_effort values low|medium|
		// high|max. "none" means omit the field so the hosted model runs without
		// thinking. Leave auto as the default so existing traffic stays provider-
		// default until the user chooses an effort explicitly.
		return EffortCapability{Supported: true, Levels: []string{"auto", "none", "low", "medium", "high", "max"}, Default: "auto"}
	case e != nil && e.Kind == "anthropic":
		return EffortCapability{Supported: true, Levels: []string{"auto", "low", "medium", "high", "xhigh", "max"}, Default: "auto"}
	default:
		return EffortCapability{}
	}
}

// NormalizeEffort maps a user-supplied /effort level into the value stored in
// config. Empty means auto/provider default.
func NormalizeEffort(e *ProviderEntry, raw string) (string, error) {
	level := normalizeEffortLevel(raw)
	if level == "" {
		return "", fmt.Errorf("usage: /effort auto|<level>")
	}
	if level == "auto" {
		return "", nil
	}
	explicitProtocol := explicitReasoningProtocol(e)
	if explicitProtocol == ReasoningProtocolNone {
		return "", effortNotConfigurableError(e)
	}
	if explicitProtocol == ReasoningProtocolKimiK3 {
		return normalizeKimiK3ReasoningEffort(level)
	}
	supported := normalizedSupportedEfforts(e)
	level = normalizeBuiltInModelEffortAlias(e, supported, level)
	if len(supported) > 0 {
		if containsString(supported, level) {
			return level, nil
		}
		return "", fmt.Errorf("usage: /effort auto|%s", strings.Join(supported, "|"))
	}
	// V4 Flash and Pro expose a real low depth. Keep this model-scoped: generic
	// DeepSeek-compatible endpoints still normalize low to high unless
	// they explicitly advertise a different supported_efforts list.
	if cap, ok := resolvedModelReasoningCapability(e); ok {
		explicit := explicitReasoningProtocol(e)
		if explicit == "" || explicit == cap.Protocol {
			if containsString(cap.Levels, level) {
				return level, nil
			}
			if normalized, ok := cap.Aliases[level]; ok && containsString(cap.Levels, normalized) {
				return normalized, nil
			}
		}
	}
	switch ReasoningProtocolForEntry(e) {
	case ReasoningProtocolDeepSeek:
		switch level {
		case "disabled":
			return "disabled", nil
		case "off": // retired DeepSeek "no thinking" → disabled
			return "disabled", nil
		case "high", "max":
			return level, nil
		case "low", "medium":
			return "high", nil
		case "xhigh":
			return "max", nil
		default:
			return "", fmt.Errorf("usage: /effort auto|disabled|high|max")
		}
	case ReasoningProtocolOpenAI:
		return normalizeOpenAIReasoningEffort(e, level)
	case ReasoningProtocolKimiK3:
		return normalizeKimiK3ReasoningEffort(level)
	case ReasoningProtocolGLM:
		return normalizeGLMEffort(level)
	}
	switch {
	case isMiniMaxEntry(e):
		// The M3 knob is binary; map Anthropic / OpenAI-style levels onto the
		// nearest valid value so a stale /effort high|low still works. "off"
		// is a retired DeepSeek level meaning "no thinking" — on M3 that maps
		// to "disabled" rather than the model default, since M3 actually
		// supports a "thinking off" mode and "off" is the natural request.
		switch level {
		case "adaptive", "disabled":
			return level, nil
		case "off":
			return "disabled", nil
		case "low", "medium", "high":
			return "adaptive", nil
		case "xhigh", "max":
			return "disabled", nil
		default:
			return "", fmt.Errorf("usage: /effort auto|adaptive|disabled")
		}
	case isZhipuEntry(e):
		// GLM's knob is binary (enabled|disabled); map Anthropic / OpenAI-style
		// depth levels onto the nearest valid value so a stale /effort high|low
		// still works. "off" is a retired DeepSeek level meaning "no thinking",
		// which maps to "disabled".
		return normalizeGLMEffort(level)
	case isLongCatEntry(e):
		// LongCat's knob is binary (enabled|disabled); depth-like aliases mean
		// thinking on, while the legacy off spellings disable it.
		switch level {
		case "enabled", "disabled":
			return level, nil
		case "off":
			return "disabled", nil
		case "low", "medium", "high", "xhigh", "max":
			return "enabled", nil
		default:
			return "", fmt.Errorf("usage: /effort auto|enabled|disabled")
		}
	case isOllamaCloudEntry(e):
		switch level {
		case "none", "disabled", "off":
			return "none", nil
		case "low", "medium", "high", "max":
			return level, nil
		case "xhigh":
			return "max", nil
		default:
			return "", fmt.Errorf("usage: /effort auto|none|low|medium|high|max")
		}
	case isMimoEntry(e):
		return normalizeMimoEffort(level)
	case e != nil && e.Kind == "anthropic":
		switch level {
		case "low", "medium", "high", "xhigh", "max":
			return level, nil
		default:
			return "", fmt.Errorf("usage: /effort auto|low|medium|high|xhigh|max")
		}
	default:
		return "", effortNotConfigurableError(e)
	}
}

// EffortDisplay returns the selected /effort level, using "auto" for provider
// default.
func EffortDisplay(e *ProviderEntry) string {
	if e == nil || strings.TrimSpace(e.Effort) == "" {
		return "auto"
	}
	effort := normalizeEffortLevel(e.Effort)
	if explicitReasoningProtocol(e) == ReasoningProtocolKimiK3 && !isKimiK3ReasoningEffort(effort) {
		return "auto"
	}
	return effort
}

// EffectiveEffort resolves the provider-visible effort value. Explicit
// ProviderEntry.Effort wins; otherwise a configured SupportedEfforts list makes
// DefaultEffort (or the first supported level) the runtime default. Empty means
// provider default / omit the provider-specific effort field.
func EffectiveEffort(e *ProviderEntry) string {
	if e == nil {
		return ""
	}
	if effort := normalizeStoredEffort(e.Effort); effort != "" {
		if explicitReasoningProtocol(e) == ReasoningProtocolKimiK3 && !isKimiK3ReasoningEffort(effort) {
			return ""
		}
		// Saved pre-contract aliases (medium/xhigh) keep working on official
		// DeepSeek endpoints: resolve them to the declared high level.
		return migrateStoredDeepSeekEffort(e, effort)
	}
	if explicitReasoningProtocol(e) == ReasoningProtocolKimiK3 {
		return ""
	}
	supported := normalizedSupportedEfforts(e)
	if len(supported) == 0 {
		return ""
	}
	def := normalizeEffortLevel(e.DefaultEffort)
	if def == "" || !containsString(supported, def) {
		return supported[0]
	}
	return def
}

func normalizeEffortConfig(c *Config) {
	if c == nil {
		return
	}
	for i := range c.Providers {
		normalizeProviderEffortFields(&c.Providers[i])
	}
}

func normalizeProviderEffortFields(e *ProviderEntry) {
	if e == nil {
		return
	}
	e.Headers = normalizedProviderHeaders(e.Headers)
	e.Effort = normalizeStoredEffort(e.Effort)
	e.ReasoningProtocol = normalizeReasoningProtocol(e.ReasoningProtocol)
	e.DefaultEffort = normalizeEffortLevel(e.DefaultEffort)
	e.SupportedEfforts = normalizedSupportedEfforts(e)
	e.ModelOverrides = normalizedModelOverrides(e.ModelOverrides)
}

func normalizeStoredEffort(raw string) string {
	level := normalizeEffortLevel(raw)
	if level == "auto" || level == "off" {
		return ""
	}
	return level
}

// ReasoningProtocolForEntry resolves the provider request shape for reasoning
// controls. Explicit config wins, then the model capability registry, then legacy
// endpoint heuristics.
func ReasoningProtocolForEntry(e *ProviderEntry) string {
	if explicit := explicitReasoningProtocol(e); explicit != "" {
		return explicit
	}
	if cap, ok := resolvedModelReasoningCapability(e); ok {
		return cap.Protocol
	}
	if isTokenRhythmGLMEntry(e) {
		return ReasoningProtocolGLM
	}
	if isDeepSeekEntry(e) {
		return ReasoningProtocolDeepSeek
	}
	return ""
}

func explicitReasoningProtocol(e *ProviderEntry) string {
	if e == nil {
		return ""
	}
	protocol := normalizeReasoningProtocol(e.ReasoningProtocol)
	if protocol == ReasoningProtocolAuto {
		return ""
	}
	return protocol
}

func normalizeReasoningProtocol(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ReasoningProtocolAuto:
		return ""
	case ReasoningProtocolDeepSeek, ReasoningProtocolGLM, ReasoningProtocolKimiK3, ReasoningProtocolOpenAI, ReasoningProtocolNone:
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func kimiK3EffortCapability() EffortCapability {
	return EffortCapability{Supported: true, Levels: []string{"auto", "low", "high", "max"}, Default: "max"}
}

// isDeepSeekEntry reports whether the entry points at DeepSeek's API. The
// actual host matching lives in provider/openai so the openai package and
// the config layer stay in lockstep when new gateways are added.
func isDeepSeekEntry(e *ProviderEntry) bool {
	return e != nil && e.Kind == "openai" && openai.IsDeepSeek(e.BaseURL)
}

// isMiniMaxEntry reports whether the entry points at MiniMax's OpenAI-compatible
// endpoint. See openai.IsMiniMax for the host-matching rule; the entry-wrapper
// just gates on the openai kind.
func isMiniMaxEntry(e *ProviderEntry) bool {
	return e != nil && e.Kind == "openai" && openai.IsMiniMax(e.BaseURL)
}

// isZhipuEntry reports whether the entry points at Zhipu's OpenAI-compatible
// endpoint for GLM models. See openai.IsZhipu for the host-matching rule; the
// entry-wrapper just gates on the openai kind.
func isZhipuEntry(e *ProviderEntry) bool {
	return e != nil && e.Kind == "openai" && openai.IsZhipu(e.BaseURL)
}

// isTokenRhythmGLMEntry upgrades older Token Rhythm configurations that predate
// per-model protocol overrides. Keep the rule scoped to the gateway and exact
// official model IDs so unrelated mixed-model providers retain their existing
// request shape.
func isTokenRhythmGLMEntry(e *ProviderEntry) bool {
	if e == nil || e.Kind != "openai" || !openai.IsTokenRhythm(e.BaseURL) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(e.Model)) {
	case "glm-5", "glm-5.1", "glm-5.2":
		return true
	default:
		return false
	}
}

// isLongCatEntry reports whether the entry points at LongCat's OpenAI-compatible
// endpoint. See openai.IsLongCat for the host-matching rule.
func isLongCatEntry(e *ProviderEntry) bool {
	return e != nil && e.Kind == "openai" && openai.IsLongCat(e.BaseURL)
}

// isOllamaCloudEntry reports whether the entry points at hosted Ollama Cloud,
// whose OpenAI-compatible endpoint accepts reasoning_effort=max. Local Ollama
// endpoints intentionally do not match.
func isOllamaCloudEntry(e *ProviderEntry) bool {
	return e != nil && e.Kind == "openai" && openai.IsOllamaCloud(e.BaseURL)
}

// isMimoEntry reports whether the entry points at Xiaomi MiMo's Responses API
// (api.xiaomimimo.com). Host matching mirrors provider/responses.DetectVendor
// but lives in the config layer to avoid an import cycle (control → config,
// not control → provider). Host-based exact/suffix matching (not full-URL
// substring) so unrelated or attacker-controlled URLs can't enable MiMo
// effort. The kind check is intentionally absent: MiMo is served through both
// kind="responses" and kind="openai" presets.
func isMimoEntry(e *ProviderEntry) bool {
	if e == nil {
		return false
	}
	host := officialProviderHost(e.BaseURL)
	return host == "api.xiaomimimo.com" || strings.HasSuffix(host, ".xiaomimimo.com")
}

// mimoEffortCapability mirrors MiMo's documented Chat Completions
// reasoning.effort vocabulary (8 depth levels, field required, server default
// "enabled"): none|minimal|low|medium|high|xhigh|max|ultra. The wire layer
// collapses the vendor's real depth spread onto the documented normalization
// (minimal→low, xhigh/max/ultra→high, none=off); "auto" means "don't send the
// field" so the server default (enabled) applies.
func mimoEffortCapability() EffortCapability {
	return EffortCapability{
		Supported: true,
		Levels:    []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"},
		Default:   "auto",
		// Task effortfix2: identity mark for display folding — only this
		// vocabulary (and never a shape sniff) is folded onto the four tiers.
		AliasFold: true,
	}
}

// normalizeMimoEffort is the single wire-normalization point for MiMo effort
// levels, shared by every path that stores a /effort value for a MiMo entry
// (the openai-protocol branch in effort_protocol.go and the isMimoEntry
// fallback in NormalizeEffort). It collapses the vendor's 8-level
// reasoning.effort scale onto the documented normalization: minimal→low,
// xhigh/max/ultra→high, none/disabled/off→none, low/medium/high verbatim.
// "auto" never reaches this function: NormalizeEffort resolves it to ""
// (field not sent) before dispatching.
func normalizeMimoEffort(level string) (string, error) {
	switch level {
	case "none", "disabled", "off":
		return "none", nil
	case "low", "medium", "high":
		return level, nil
	case "minimal":
		return "low", nil
	case "xhigh", "max", "ultra":
		return "high", nil
	default:
		return "", fmt.Errorf("usage: /effort auto|none|minimal|low|medium|high|xhigh|max|ultra")
	}
}

func resolvedModelReasoningCapability(e *ProviderEntry) (modelReasoningCapability, bool) {
	if e == nil || e.Kind != "openai" {
		return modelReasoningCapability{}, false
	}
	return modelReasoningCapabilityForEntry(e)
}

func modelReasoningCapabilityForEntry(e *ProviderEntry) (modelReasoningCapability, bool) {
	if e == nil {
		return modelReasoningCapability{}, false
	}
	cap, ok := modelReasoningCapabilities[strings.ToLower(strings.TrimSpace(e.Model))]
	return cap, ok
}

func normalizeBuiltInModelEffortAlias(e *ProviderEntry, supported []string, level string) string {
	cap, ok := modelReasoningCapabilityForEntry(e)
	if !ok || !slices.Equal(supported, normalizedEffortLevels(cap.Levels)) {
		return level
	}
	normalized, ok := cap.Aliases[level]
	if !ok || !containsString(supported, normalized) {
		return level
	}
	return normalized
}

func effortCapabilityFromModel(cap modelReasoningCapability) EffortCapability {
	levels := make([]string, 0, len(cap.Levels)+1)
	levels = append(levels, "auto")
	levels = append(levels, cap.Levels...)
	def := normalizeEffortLevel(cap.Default)
	if def == "" || !containsString(cap.Levels, def) {
		def = "auto"
	}
	return EffortCapability{Supported: true, Levels: levels, Default: def}
}

// deepSeekEffortCapability is the config-layer DeepSeek protocol default.
// Task effortfix2 (A-line P1): the full-depth SKU family — the v4 names,
// their bare compatibility aliases (deepseek-flash / deepseek-pro), and the
// pinned vision SKU — carries "low" too; everything else keeps the generic
// three depth levels. This mirrors the provider-layer gate in
// internal/provider/openai/reasoning_capability.go so the menu and the wire
// never disagree (the end-to-end test pins the bare-alias case: without this
// the 283-block deepseek-flash reached the generic table and lost "low" even
// after the provider fix).
func deepSeekEffortCapability(e *ProviderEntry) EffortCapability {
	if sku := deepSeekSKU(e); sku == "deepseek-v4-flash" || sku == "deepseek-v4-pro" ||
		sku == "deepseek-flash" || sku == "deepseek-pro" ||
		(e != nil && openai.IsOfficialDeepSeekVisionModel(e.Model)) {
		return EffortCapability{Supported: true, Levels: []string{"auto", "disabled", "low", "high", "max"}, Default: "high"}
	}
	return EffortCapability{Supported: true, Levels: []string{"auto", "disabled", "high", "max"}, Default: "high"}
}

// deepSeekSKU strips a leading "provider/" segment from the entry's model ref
// ("deepseek/deepseek-flash" → "deepseek-flash") so identity checks compare
// the bare SKU, matching the provider layer's prefixed-ref handling.
func deepSeekSKU(e *ProviderEntry) string {
	if e == nil {
		return ""
	}
	m := strings.TrimSpace(e.Model)
	if slash := strings.IndexByte(m, '/'); slash >= 0 {
		return strings.TrimSpace(m[slash+1:])
	}
	return m
}

func openAIEffortCapability() EffortCapability {
	return EffortCapability{Supported: true, Levels: []string{"auto", "low", "medium", "high"}, Default: "auto"}
}

func glmEffortCapability() EffortCapability {
	// GLM-5.2/5.3 support reasoning_effort (low/medium/high/max etc).
	// GLM-5.3/5.3-flash: thinking always enabled, cannot disable.
	// GLM-5.2: thinking can be disabled (none/minimal = give up thinking).
	// "auto" lets Reasonix pick the model default; strength levels map to reasoning_effort.
	return EffortCapability{Supported: true, Levels: []string{"auto", "disabled", "low", "medium", "high", "max"}, Default: "auto"}
}

func normalizeGLMEffort(level string) (string, error) {
	switch level {
	case "auto", "disabled", "low", "medium", "high", "max":
		return level, nil
	case "off":
		return "disabled", nil
	case "enabled":
		// Legacy: "enabled" mapped to thinking on with no strength → treat as "auto"
		return "auto", nil
	case "xhigh":
		return "max", nil
	default:
		return "", fmt.Errorf("usage: /effort auto|disabled|low|medium|high|max")
	}
}

// errEffortNotConfigurable marks the "this provider cannot configure effort
// at all" class (task 354): callers fall back to a full rebuild for it, while
// a plain usage error for one unsupported level must fail fast instead of
// paying a rebuild.
var errEffortNotConfigurable = errors.New("effort not configurable")

// IsEffortNotConfigurable reports whether err is the capability-class
// fallback (provider has no effort knob) as opposed to a usage error for a
// single unsupported level.
func IsEffortNotConfigurable(err error) bool {
	return errors.Is(err, errEffortNotConfigurable)
}

func effortNotConfigurableError(e *ProviderEntry) error {
	name := ""
	if e != nil {
		name = e.Name
	}
	if name == "" {
		name = "this model"
	}
	return fmt.Errorf("effort is not configurable for %s: %w", name, errEffortNotConfigurable)
}

func containsString(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

func normalizeEffortLevel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizedSupportedEfforts(e *ProviderEntry) []string {
	if e == nil || len(e.SupportedEfforts) == 0 {
		return nil
	}
	return normalizedEffortLevels(e.SupportedEfforts)
}

func normalizedEffortLevels(levels []string) []string {
	if len(levels) == 0 {
		return nil
	}
	out := make([]string, 0, len(levels))
	seen := map[string]bool{}
	for _, raw := range levels {
		level := normalizeEffortLevel(raw)
		if level == "" || level == "auto" || seen[level] {
			continue
		}
		seen[level] = true
		out = append(out, level)
	}
	return out
}

func normalizedProviderHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for rawName, rawValue := range headers {
		name := strings.TrimSpace(rawName)
		value := strings.TrimSpace(rawValue)
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizedModelOverrides(overrides map[string]ProviderModelOverride) map[string]ProviderModelOverride {
	if len(overrides) == 0 {
		return nil
	}
	out := make(map[string]ProviderModelOverride, len(overrides))
	for rawModel, ov := range overrides {
		model := strings.TrimSpace(rawModel)
		if model == "" {
			continue
		}
		ov.ReasoningProtocol = normalizeReasoningProtocol(ov.ReasoningProtocol)
		ov.SupportedEfforts = normalizedEffortLevels(ov.SupportedEfforts)
		ov.DefaultEffort = normalizeEffortLevel(ov.DefaultEffort)
		if ov.ContextWindow < 0 {
			ov.ContextWindow = 0
		}
		if ov.DefaultEffort != "" && !containsString(ov.SupportedEfforts, ov.DefaultEffort) {
			ov.DefaultEffort = ""
		}
		if ov.ReasoningProtocol == "" && len(ov.SupportedEfforts) == 0 && ov.DefaultEffort == "" && ov.Vision == nil && ov.ContextWindow == 0 {
			continue
		}
		out[model] = ov
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ReasoningCapabilityForEntry returns the provider-level reasoning capability
// for an entry. Upstream 1.38.3 moved this helper here; the fork keeps its own
// effort-capability layering, so the shared helper is restored for callers
// that expect the upstream contract (effort_legacy.go).
func ReasoningCapabilityForEntry(e *ProviderEntry) provider.ReasoningCapability {
	if e == nil {
		return provider.ReasoningOptions("")
	}
	// Resolver-backed entries carry the remote adapter declaration, not a local kind.
	if e.Kind == "" {
		return provider.ReasoningOptions(e.DefaultEffort, e.SupportedEfforts...)
	}
	cfg := provider.Config{Name: e.Name, BaseURL: e.BaseURL, Model: e.Model, Extra: map[string]any{
		"thinking": e.Thinking, "reasoning_protocol": ReasoningProtocolForEntry(e),
		"supported_efforts": normalizedSupportedEfforts(e), "default_effort": normalizeEffortLevel(e.DefaultEffort),
	}}
	rc := provider.ReasoningForConfig(e.Kind, cfg)
	// The provider layer carries the wire vocabulary (binary thinking for Zhipu
	// and LongCat). The effort layer knows the depth scale the vendor actually
	// accepts, so when the two disagree the richer table wins — otherwise the
	// composer only offers enabled/disabled and GLM strength levels are
	// unreachable, which regressed repeatedly (#glm-effort).
	//
	// Only the wire levels take part: "auto" is a composer-level alias for
	// "inherit the provider default", not a value any adapter can send, and
	// provider.ReasoningCapability.Validate rejects it outright. Mirroring the
	// table verbatim smuggled it into the provider contract, which failed every
	// official DeepSeek model at boot with INVALID_MODEL_REASONING (effort "auto").
	if levels := wireEffortLevels(EffortCapabilityForEntry(e).Levels); len(levels) > len(rc.Options) {
		rc.Options = reasoningOptionsFromLevels(levels)
		if rc.Default != "" && !slices.Contains(levels, rc.Default) {
			// The provider default belongs to the vocabulary being replaced
			// (Zhipu's binary "enabled"). Empty keeps provider-default behavior
			// instead of inventing a depth the vendor never declared.
			rc.Default = ""
		}
	}
	return rc
}

// wireEffortLevels drops the composer-level "auto" alias so the remaining
// entries describe values that can actually reach the wire.
func wireEffortLevels(levels []string) []string {
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		if normalizeEffortLevel(level) == "auto" {
			continue
		}
		out = append(out, level)
	}
	return out
}

// reasoningOptionsFromLevels mirrors an effort level table into the option
// shape the desktop composer renders.
func reasoningOptionsFromLevels(levels []string) []provider.ReasoningOption {
	out := make([]provider.ReasoningOption, 0, len(levels))
	for _, level := range levels {
		out = append(out, provider.ReasoningOption{ID: level, Name: level})
	}
	return out
}
