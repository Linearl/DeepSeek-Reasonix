package main

// Task 163 — OpenCode Go subscription usage query.
//
// The official-but-undocumented endpoint GET https://opencode.ai/zen/go/v1/usage
// (found via cc-switch #6433) reports three rolling windows as percentages.
// Two contracts matter and differ from the inference side:
//   - auth: this endpoint only accepts `Authorization: Bearer <key>` while
//     /v1/messages only accepts `x-api-key` — never interchangeable;
//   - entitlement: HTTP 403 means the key is valid but the workspace has no
//     Go subscription — surfaced as its own note, never as a generic auth
//     failure. HTTP 401 remains the auth failure.
//
// The domain allow-list is structural: only the exact opencode.ai host may be
// queried (custom OpenCode-compatible endpoints never reach the network), and
// the endpoint itself is a package var so tests can point it at httptest.
// OpenCode Zen (pay-as-you-go) has no usage API — out of scope by design.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// openCodeGoUsageEndpoint is a var only so the test suite can repoint it at an
// httptest server; production code never writes it.
var openCodeGoUsageEndpoint = "https://opencode.ai/zen/go/v1/usage"

// openCodeGoUsageTimeout bounds one usage query (the reference implementation
// uses 15s as well).
const openCodeGoUsageTimeout = 15 * time.Second

// OpenCodeGoUsageTier is one rolling window of the subscription usage report.
// Percent is nil when the window is missing or unparseable (the endpoint has
// changed shape once already, so parsing is per-window defensive).
type OpenCodeGoUsageTier struct {
	// Window is the wire key: "rolling" (5 hours), "weekly" (7 days), or
	// "monthly" (subscription month).
	Window string `json:"window"`
	// Percent is the used percentage (0-100) of the window's allowance.
	Percent *float64 `json:"percent"`
	// ResetsAt is the ISO-8601 reset instant; empty when percent is 0 (the
	// upstream placeholder is meaningless then) or the field is absent.
	ResetsAt string `json:"resetsAt"`
}

// OpenCodeGoUsageView is the settings-card payload. Note carries the one
// non-success reason the UI may show; empty means Tiers are real.
//
//	"no-key"             — OPENCODE_GO_API_KEY is not configured (no request)
//	"unsupported-endpoint" — baseUrl is not opencode.ai (no request)
//	"no-subscription"    — HTTP 403: valid key, no Go subscription
//	"auth-failed"        — HTTP 401: invalid key
//	"http-<code>"        — any other non-2xx status
//	"network" / "parse"  — transport or body failures
type OpenCodeGoUsageView struct {
	Tiers []OpenCodeGoUsageTier `json:"tiers"`
	Note  string                `json:"note"`
}

// isOfficialOpenCodeGoBase reports whether the caller's endpoint belongs to
// the official opencode.ai host — the same exact-host allow-list style as
// ApplyOpenCodeGoHeaders. A look-alike host (opencode.ai.evil.example) fails.
func isOfficialOpenCodeGoBase(base string) bool {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "opencode.ai")
}

// usageView assembles a note-only payload with a non-nil empty Tiers slice.
// A nil slice marshals to JSON null, and the settings card's tier lookup
// crashed on it (TypeError: null.find) — the wire carries [] for every
// degradation note so the card falls to its note/empty branch instead.
func usageView(note string) OpenCodeGoUsageView {
	return OpenCodeGoUsageView{Tiers: []OpenCodeGoUsageTier{}, Note: note}
}

// GetOpenCodeGoUsage queries the subscription usage endpoint for the settings
// card (task 163). baseUrl is the provider the card belongs to; anything off
// the official host returns before any network I/O.
func (a *App) GetOpenCodeGoUsage(baseUrl string) (OpenCodeGoUsageView, error) {
	if !isOfficialOpenCodeGoBase(baseUrl) {
		return usageView("unsupported-endpoint"), nil
	}
	key := strings.TrimSpace(os.Getenv("OPENCODE_GO_API_KEY"))
	if key == "" {
		return usageView("no-key"), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), openCodeGoUsageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openCodeGoUsageEndpoint, nil)
	if err != nil {
		return usageView("network"), fmt.Errorf("build usage request: %w", err)
	}
	// Bearer here, x-api-key on the inference side — never interchangeable.
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return usageView("network"), fmt.Errorf("query usage: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden:
		// Valid key, no Go subscription — distinct from an auth failure.
		return usageView("no-subscription"), nil
	case resp.StatusCode == http.StatusUnauthorized:
		return usageView("auth-failed"), nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return usageView(fmt.Sprintf("http-%d", resp.StatusCode)), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return usageView("parse"), fmt.Errorf("read usage body: %w", err)
	}
	tiers, err := parseOpenCodeGoUsage(body)
	if err != nil {
		return usageView("parse"), err
	}
	return OpenCodeGoUsageView{Tiers: tiers}, nil
}

// parseOpenCodeGoUsage decodes the endpoint's three windows defensively:
//
//	{"usage":{"rolling|weekly|monthly":{"status","percent","resetsAt"}}}
//
// A missing window or an unparseable percent skips that tier instead of
// failing the whole card (the endpoint already changed shape once). When
// percent is 0 the upstream resetsAt is a placeholder — drop it.
func parseOpenCodeGoUsage(body []byte) ([]OpenCodeGoUsageTier, error) {
	var payload struct {
		Usage map[string]struct {
			Status   string   `json:"status"`
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resetsAt"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}
	windows := []string{"rolling", "weekly", "monthly"}
	tiers := make([]OpenCodeGoUsageTier, 0, len(windows))
	for _, window := range windows {
		entry, ok := payload.Usage[window]
		if !ok || entry.Percent == nil {
			continue
		}
		tier := OpenCodeGoUsageTier{Window: window, Percent: entry.Percent}
		if *entry.Percent > 0 {
			tier.ResetsAt = entry.ResetsAt
		}
		tiers = append(tiers, tier)
	}
	return tiers, nil
}
