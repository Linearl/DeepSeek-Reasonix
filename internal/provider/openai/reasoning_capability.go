package openai

import (
	"strings"

	"reasonix/internal/provider"
)

// ReasoningForConfig is pure: capability discovery never reads credentials or
// performs I/O. It shares the adapter's endpoint and protocol predicates.
func ReasoningForConfig(cfg provider.Config) provider.ReasoningCapability {
	protocol, _ := cfg.Extra["reasoning_protocol"].(string)
	protocol = normalizeReasoningProtocol(protocol)
	if protocol == "none" {
		return provider.ReasoningOptions("")
	}
	var cap provider.ReasoningCapability
	switch {
	case usesKimiK3Contract(protocol, cfg.BaseURL, cfg.Model):
		return provider.ReasoningOptions("max", "low", "high", "max")
	case protocol == "glm" || (protocol == "" && (IsZhipu(cfg.BaseURL) || IsLongCat(cfg.BaseURL))):
		cap = provider.ReasoningOptions("enabled", "enabled", "disabled")
	case protocol == "" && IsMiniMax(cfg.BaseURL):
		cap = provider.ReasoningOptions("adaptive", "adaptive", "disabled")
	case protocol == "deepseek" || (protocol == "" && IsDeepSeek(cfg.BaseURL)):
		cap = provider.ReasoningOptions("high", "disabled", "high", "max")
		// The full-depth SKU family: the v4 names, their bare compatibility
		// aliases (deepseek-flash / deepseek-pro — the refs users and
		// ResolveModel actually hand out, see the default-ref alias tests), and
		// the pinned vision SKU. Task effortfix2 (A-line P1): the aliases were
		// missing here, so a bare deepseek-flash landed on the generic three
		// levels and silently lost "low". Matching runs on the ref with any
		// "provider/" prefix stripped — users grant "deepseek/deepseek-flash"
		// exactly like the vision SKU's documented prefixed form.
		sku := modelWithoutProviderRef(cfg.Model)
		if sku == "deepseek-v4-flash" || sku == "deepseek-v4-pro" ||
			sku == "deepseek-flash" || sku == "deepseek-pro" ||
			IsOfficialDeepSeekVisionModel(cfg.Model) {
			cap = provider.ReasoningOptions("high", "disabled", "low", "high", "max")
		}
	case protocol == "" && IsOllamaCloud(cfg.BaseURL):
		cap = provider.ReasoningOptions("", "none", "low", "medium", "high", "max")
	case protocol == "" && IsMiMo(cfg.BaseURL):
		// Task 606: MiMo's reasoning_effort wire domain includes "none"
		// (thinking off) — internal/config's normalizeMimoEffort folds
		// none/disabled/off onto none and the value passes through to the wire
		// verbatim. The capability used to omit it, so a stock entry armed a
		// none override at the probe gate and had the very next request
		// rejected by reasoning.Validate below (and a persisted effort=none
		// failed New outright at configuredEffort). Listing none keeps the
		// capability equal to the probe vocabulary (mimoRequestEffortVocabulary),
		// so both gates accept it naturally instead of needing a per-gate fork
		// exception like GLM's binary thinking clip. An explicit
		// supported_efforts list still replaces this vocabulary via
		// DeclaredReasoning — a declaration that excludes none stays authoritative.
		cap = provider.ReasoningOptions("", "none", "low", "medium", "high")
	case protocol == "openai":
		// Generic OpenAI-compatible protocol with no known vendor effort
		// scale: keep the three canonical levels. "none" stays out — a
		// reasoning_effort of none is a MiMo vocabulary value, not a generic
		// OpenAI-compatible one.
		cap = provider.ReasoningOptions("", "low", "medium", "high")
	default:
		cap = provider.ReasoningOptions("")
	}
	cap = provider.DeclaredReasoning(cfg, cap)
	if protocol == "glm" || (protocol == "" && (IsZhipu(cfg.BaseURL) || IsLongCat(cfg.BaseURL))) {
		cap = provider.RestrictReasoning(cap, "enabled", "disabled")
	}
	if protocol == "" && IsMiniMax(cfg.BaseURL) {
		cap = provider.RestrictReasoning(cap, "adaptive", "disabled")
	}
	if configuredThinkingType(cfg) == "disabled" {
		return provider.ReasoningOptions("disabled", "disabled")
	}
	return cap
}

// modelWithoutProviderRef strips a leading "provider/" segment from a model
// ref ("deepseek/deepseek-flash" → "deepseek-flash"), mirroring the vision
// SKU's documented prefixed-ref handling. Identity checks compare the bare
// SKU so granted refs behave exactly like bare model names (task effortfix2).
func modelWithoutProviderRef(model string) string {
	m := strings.TrimSpace(model)
	if slash := strings.IndexByte(m, '/'); slash >= 0 {
		return strings.TrimSpace(m[slash+1:])
	}
	return m
}

func (c *client) ReasoningCapability() provider.ReasoningCapability { return c.reasoning.Clone() }

func configuredEffort(cfg provider.Config) (string, error) {
	effort, _ := cfg.Extra["effort"].(string)
	protocol, _ := cfg.Extra["reasoning_protocol"].(string)
	cap := ReasoningForConfig(cfg)
	if effort == "auto" || effort == "off" || protocol == "none" || configuredThinkingType(cfg) == "disabled" {
		return effort, nil
	}
	// Fork: GLM carries a depth knob through reasoning_effort on top of the
	// binary thinking.type. Its resolved capability only lists enabled/disabled,
	// so validating against it would reject the fork's low..max strengths.
	if protocol == "glm" || (protocol == "" && IsZhipu(cfg.BaseURL)) {
		return effort, nil
	}
	return effort, cap.Validate(cfg.Model, effort)
}

// reasoningState is the immutable reasoning contract resolved at construction.
type reasoningState struct {
	ollamaCloud    bool
	thinkingLocked bool
	reasoning      provider.ReasoningCapability
}

func (c *client) applyReasoning(out *chatRequest, req provider.Request) {
	maxOutputTokens := out.MaxTokens
	switch {
	case c.kimiK3:
		// K3 fixes its sampling values and recommends omitting them. It also
		// names the output budget max_completion_tokens rather than max_tokens.
		out.Temperature = nil
		out.MaxTokens = 0
		out.MaxCompletionTokens = maxOutputTokens
		out.ExtraBody = omitExtraBodyFields(out.ExtraBody,
			"temperature", "top_p", "n", "presence_penalty", "frequency_penalty", "max_completion_tokens")
	case IsOpenAI(c.baseURL):
		// OpenAI's current Chat Completions contract replaces max_tokens with
		// max_completion_tokens, which includes visible and reasoning tokens and
		// is required by o-series models. Compatible gateways retain max_tokens.
		out.MaxTokens = 0
		out.MaxCompletionTokens = maxOutputTokens
	case c.deepseek:
		// DeepSeek's CoT is controlled by `thinking` plus `reasoning_effort` for
		// depth. Thinking is on by default but can be turned off for one
		// stateless request through EffortOverride=disabled.
		out.Thinking = &thinkingMode{Type: c.deepSeekRequestThinking(req)}
		if out.Thinking.Type == "disabled" {
			out.ReasoningEffort = ""
		}
	case c.minimax:
		// M3 uses a single `thinking.type` field with two valid values:
		// "adaptive" (default, thinking on) and "disabled" (off). Reasoning
		// depth is not a knob on M3, so reasoning_effort is omitted entirely.
		t := c.requestEffort(req)
		if t == "" {
			t = "adaptive" // /effort auto == the M3 model default
		}
		out.Thinking = &thinkingMode{Type: t}
		out.ReasoningEffort = ""
	case c.zhipu:
		// Zhipu GLM's binary thinking knob ("enabled"/"disabled") plus, for the
		// fork, a thinking-strength knob carried by reasoning_effort. Upstream
		// dropped the strength mapping and only drove thinking.type, which lost
		// GLM low/medium/high/max; it is restored here on top of the per-request
		// effort resolution (c.requestEffort) so per-request overrides still win.
		t := c.requestEffort(req)
		strength := ""
		switch t {
		case "disabled":
			// thinking off — no reasoning_effort
		case "low", "medium", "high", "max":
			strength = t
			t = "enabled"
		default:
			// auto, enabled, or empty → thinking on, no explicit strength
			t = "enabled"
		}
		if c.thinkingType != "" && req.EffortOverride == "" {
			t = c.thinkingType // explicit `thinking` config overrides the effort knob
		}
		out.Thinking = &thinkingMode{Type: t}
		out.ReasoningEffort = strength
	case c.longcat:
		// LongCat's binary thinking knob: "enabled" (default, thinking on) or
		// "disabled". The API documents reasoning_content in OpenAI responses but
		// not reasoning_effort, so keep depth out of the request.
		t := c.requestEffort(req)
		if t == "" {
			t = c.thinkingType
		}
		if t == "" {
			t = "enabled"
		}
		out.Thinking = &thinkingMode{Type: t}
		out.ReasoningEffort = ""
	case c.ollamaCloud:
		if out.ReasoningEffort == "none" {
			out.ReasoningEffort = ""
		}
	case c.thinkingType != "":
		// Generic OpenAI-compatible provider with an explicit `thinking` config
		// field (e.g. opencode.ai) — emit thinking.type; reasoning_effort, if any,
		// is left untouched for backends that also honour it.
		out.Thinking = &thinkingMode{Type: c.thinkingType}
	}
}
