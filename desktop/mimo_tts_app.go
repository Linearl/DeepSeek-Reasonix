package main

// tl1817 — read-aloud MiMo TTS transport.
//
// When the lab switch (experimental_mimo_tts) is on and a MiMo credential is
// reachable from config providers, the selection read-aloud synthesizes
// through MiMo's chat/completions TTS shape (probed 2026-11-08/11, memory
// mem-bc1f140c):
//
//	POST {provider.base_url}/chat/completions
//	{"model":"mimo-v2.5-tts","messages":[{"role":"assistant","content":text}],"audio":{"format":"wav"}}
//	→ choices[0].message.audio.data (base64 wav)
//
// NOT the OpenAI /audio/speech endpoint — it does not exist on this platform
// (openresty 404). The assistant-role message is mandatory ("messages must
// contain an assistant role for TTS model" otherwise). Voices ride the
// server default (Chinese female); the optional voice parameter is a
// documented non-goal for this pass.
//
// Credential discipline: the key never crosses the bridge — the frontend
// receives base64 audio or a typed error. Detection matches a MiMo provider
// by endpoint host or legacy env-slot name, then requires the resolved key
// VALUE to carry a MiMo prefix (tp- token-plan / sk- api) — key slots are
// renamed to REASONIX_CONNECTION_* on settings saves (StageModelCredential-
// Locked), so env-name matching alone would miss UI-saved keys. Any failure
// (no credential, 401, network, timeout, malformed body) is a typed error;
// the frontend degrades to the system speechSynthesis chain with a visible
// notice. The system chain remains the standing fallback in every state.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
)

// mimoTtsModel is the TTS chat model on both the api and token-plan hosts
// (2026-11-11 probe; /audio/speech does not exist — see file comment).
const mimoTtsModel = "mimo-v2.5-tts"

// Credential slot names from the mimo provider presets. Hand-written configs
// may still carry these literally; UI-saved keys get renamed (see file
// comment) and are matched by host + key-shape instead.
const (
	mimoApiKeyEnv          = "MIMO_API_KEY"
	mimoTokenPlanApiKeyEnv = "MIMO_TOKEN_PLAN_API_KEY"
)

// MiMo key shapes per tl1817: api keys ride the sk- prefix, token-plan keys
// the tp- prefix — either counts as a MiMo credential.
const (
	mimoApiKeyShapePrefix          = "sk-"
	mimoTokenPlanKeyShapePrefix    = "tp-"
	mimoTokenPlanHostFragment      = "token-plan"
	mimoEndpointHostSuffix         = "xiaomimimo.com"
	mimoAnthropicPathSuffix        = "/anthropic"
	// TTS synthesis of a MAX_SPEAK_CHARS (5000) selection takes tens of
	// seconds; the reference script uses 120s — same ceiling here.
	mimoTtsRequestTimeout = 120 * time.Second
)

// MimoTtsStatusView reports whether the MiMo TTS transport is usable right
// now. Endpoint/keyEnv are non-secret metadata (a URL the config already
// holds and the slot name), never the key itself.
type MimoTtsStatusView struct {
	Available bool   `json:"available"`
	Endpoint  string `json:"endpoint"`
	KeyEnv    string `json:"keyEnv"`
}

// mimoTtsCandidate is one provider's qualification for the TTS transport.
type mimoTtsCandidate struct {
	endpoint string
	keyEnv   string
	score    int // family-matched (tp-↔token-plan, sk-↔api) beats bare qualification
}

// isMimoProviderEndpoint reports whether the provider's base URL is a MiMo
// host. Anthropic-compatible mirrors (…/anthropic) never qualify: the TTS
// model lives on the OpenAI-compatible chat path only.
func isMimoProviderEndpoint(baseURL string) bool {
	raw := strings.TrimSpace(baseURL)
	if raw == "" || strings.HasSuffix(strings.ToLower(raw), mimoAnthropicPathSuffix) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, mimoEndpointHostSuffix)
}

// mimoKeyShape reports whether a resolved credential value carries a MiMo
// key prefix (tp- token-plan / sk- api). Both count per tl1817.
func mimoKeyShape(value string) bool {
	v := strings.TrimSpace(value)
	return strings.HasPrefix(v, mimoApiKeyShapePrefix) || strings.HasPrefix(v, mimoTokenPlanKeyShapePrefix)
}

// mimoCandidateScore ranks a qualified candidate: +2 when the key prefix
// family agrees with the endpoint family (a tp- key on a token-plan host, an
// sk- key on the direct api host), +1 for a qualified mismatch. Higher wins;
// config order breaks ties in the caller.
func mimoCandidateScore(keyValue, baseURL string) int {
	value := strings.TrimSpace(keyValue)
	host := ""
	if u, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	tokenPlanHost := strings.Contains(host, mimoTokenPlanHostFragment)
	switch {
	case strings.HasPrefix(value, mimoTokenPlanKeyShapePrefix) && tokenPlanHost:
		return 2
	case strings.HasPrefix(value, mimoApiKeyShapePrefix) && !tokenPlanHost:
		return 2
	default:
		return 1
	}
}

// mimoTtsEndpointFromProviders picks the MiMo TTS endpoint from config
// providers. A provider qualifies when its endpoint is a MiMo host (or it
// still carries a literal MiMo env-slot name from hand-written config), AND
// its resolved credential value has a MiMo key shape. `resolve` is injected
// so tests can stub credential resolution; production wires
// config.RefreshProviderCredential (fresh read — a rotated key is seen on
// the next speak, no restart). Returns the OpenAI-compatible base URL and
// the credential slot name, never the key value.
func mimoTtsEndpointFromProviders(providers []config.ProviderEntry, resolve func(string) (string, bool)) (string, string, bool) {
	var best mimoTtsCandidate
	for i := range providers {
		p := providers[i]
		envName := strings.TrimSpace(p.APIKeyEnv)
		if !isMimoProviderEndpoint(p.BaseURL) && envName != mimoApiKeyEnv && envName != mimoTokenPlanApiKeyEnv {
			continue
		}
		value, found := resolve(envName)
		if !found || !mimoKeyShape(value) {
			continue
		}
		endpoint := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		if endpoint == "" {
			continue
		}
		score := mimoCandidateScore(value, p.BaseURL)
		// Strictly better wins; equal keeps the earlier (config-order) entry.
		if best.endpoint == "" || score > best.score {
			best = mimoTtsCandidate{endpoint: endpoint, keyEnv: envName, score: score}
		}
	}
	if best.endpoint == "" {
		return "", "", false
	}
	return best.endpoint, best.keyEnv, true
}

// mimoTtsEndpoint resolves the live endpoint for the speak path: fresh config
// read with credentials, fresh credential resolution — a flipped switch, a
// rotated key, or an edited connection are all seen on the next click.
func (a *App) mimoTtsEndpoint() (string, string, bool) {
	endpoint, keyEnv, _, ok := a.mimoTtsTransport()
	return endpoint, keyEnv, ok
}

// mimoTtsTransport resolves endpoint + credential slot + proxy spec from ONE
// config load. ok=false when the config is unreadable or no provider
// qualifies (see mimoTtsEndpointFromProviders).
func (a *App) mimoTtsTransport() (string, string, netclient.ProxySpec, bool) {
	cfg, _, err := a.loadDesktopUserConfigForViewWithCredentials()
	if err != nil || cfg == nil {
		return "", "", netclient.ProxySpec{}, false
	}
	endpoint, keyEnv, ok := mimoTtsEndpointFromProviders(cfg.Providers, config.RefreshProviderCredential)
	if !ok {
		return "", "", netclient.ProxySpec{}, false
	}
	return endpoint, keyEnv, cfg.NetworkProxySpec(), true
}

// MimoTtsStatus reports whether the MiMo TTS transport is currently usable.
// The settings lab card calls this for its 「未检测到 key」 hint; the speak
// path does NOT pre-flight it (the synthesize call is the authoritative
// check — status and speak race the same config window anyway).
func (a *App) MimoTtsStatus() MimoTtsStatusView {
	endpoint, keyEnv, _, ok := a.mimoTtsTransport()
	return MimoTtsStatusView{Available: ok, Endpoint: endpoint, KeyEnv: keyEnv}
}

// MimoTtsSynthesize synthesizes `text` through the MiMo TTS chat/completions
// transport and returns the raw wav bytes base64-encoded (24kHz mono wav per
// the 2026-11-11 probe). Errors are typed and key-free; every error maps to
// the same frontend behaviour: degrade to system TTS with a visible notice.
func (a *App) MimoTtsSynthesize(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("mimo tts: empty text")
	}
	endpoint, keyEnv, proxySpec, ok := a.mimoTtsTransport()
	if !ok {
		return "", errors.New("mimo tts: no MiMo provider credential in config")
	}
	key, found := config.RefreshProviderCredential(keyEnv)
	if !found || strings.TrimSpace(key) == "" {
		return "", errors.New("mimo tts: credential missing at synthesis time")
	}

	// The TTS shape (file comment): assistant message carries the text; the
	// audio object asks for wav. No voice parameter — server default voice
	// (documented non-goal for this pass).
	payload, err := json.Marshal(map[string]any{
		"model": mimoTtsModel,
		"messages": []map[string]string{
			{"role": "assistant", "content": text},
		},
		"audio": map[string]string{"format": "wav"},
	})
	if err != nil {
		return "", fmt.Errorf("mimo tts: request encode failed: %w", err)
	}

	// Proxy policy = the app-wide spec (same single load as detection). The
	// mimo presets ship no_proxy, so their hosts ride DirectHosts and stay
	// direct (domestic endpoint vs a GFW-circumvention auto proxy, #3635).
	client, clientErr := netclient.NewHTTPClient(proxySpec, netclient.TransportOptions{
		DialTimeout:           10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: mimoTtsRequestTimeout,
	})
	if clientErr != nil {
		return "", fmt.Errorf("mimo tts: http client build failed: %w", clientErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), mimoTtsRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return "", fmt.Errorf("mimo tts: request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Bearer is the probed-working auth (api-key header also accepted).
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mimo tts: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mimo tts: http %d", resp.StatusCode)
	}

	var body struct {
		Choices []struct {
			Message struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("mimo tts: response decode failed: %w", err)
	}
	if len(body.Choices) == 0 || body.Choices[0].Message.Audio.Data == "" {
		return "", errors.New("mimo tts: response carries no audio data")
	}
	// Validate before handing to the frontend: a malformed payload should
	// degrade at the transport boundary, not surface as a broken <audio>.
	if _, err := base64.StdEncoding.DecodeString(body.Choices[0].Message.Audio.Data); err != nil {
		return "", fmt.Errorf("mimo tts: audio payload is not valid base64: %w", err)
	}
	return body.Choices[0].Message.Audio.Data, nil
}
