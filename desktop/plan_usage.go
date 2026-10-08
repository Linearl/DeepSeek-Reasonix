package main

// Task 287 — Provider plan usage query (coding plan / token plan quota).
//
// Reference implementation: cc-switch src-tauri/src/services/coding_plan.rs.
// One generic query surface for every plan-capable provider configured in the
// user's provider list; the first matching entry wins. Providers ported in
// this batch, with the contracts cc-switch taught:
//
//   智谱 GLM / Z.AI (BrandID "zai") — quota endpoint lives on the SAME host as
//     the coding endpoint: GET {base}/api/monitor/usage/quota/limit where base
//     is https://open.bigmodel.cn (CN presets) or https://api.z.ai (global).
//     Auth is the RAW key — `Authorization: <key>` with NO Bearer prefix
//     (cc-switch: "智谱不加 Bearer 前缀"). Both coding-plan and plain-API
//     presets hit the same account-level quota, so host alone decides.
//     Response: {success, msg, data:{level, limits:[{type, percentage,
//     nextResetTime(ms), unit}]}}; unit 3 = five-hour window, unit 6 = weekly.
//   Kimi For Coding (BrandID "kimi", base path /coding) — GET
//     https://api.kimi.com/coding/v1/usages, `Authorization: Bearer <key>`.
//     limits[].detail{limit,remaining,resetTime} → five-hour; usage{} → weekly.
//   MiniMax (BrandID "minimax") — GET
//     https://api.minimaxi.com (CN) / https://api.minimax.io (global)
//     /v1/api/openplatform/coding_plan/remains, Bearer. model_remains[] entry
//     with model_name "general" carries the coding plan; remaining percents
//     are INVERTED (remaining → used = 100-remaining); the weekly bucket only
//     exists when current_weekly_status == 1.
//
// Volcengine is deliberately NOT ported: its usage API is a control-plane
// OpenAPI requiring AccessKey/Secret signature V4 — a second credential
// system, not the provider key users already configured here.
//
// The host checks are structural allow-lists (same style as
// isOfficialOpenCodeGoBase): a custom bigmodel-compatible endpoint on a
// foreign host never reaches the network. The endpoint bases are package vars
// so tests can repoint them at httptest. The key is resolved through the
// entry's existing api_key_env (process env first, then the global credential
// resolver) and never reaches logs, notes, or the wire.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"reasonix/internal/config"
)

// Endpoint bases are vars only so the test suite can repoint them at an
// httptest server; production code never writes them.
var (
	zhipuQuotaPath    = "/api/monitor/usage/quota/limit"
	zhipuCnQuotaBase  = "https://open.bigmodel.cn"
	zhipuGlQuotaBase  = "https://api.z.ai"
	kimiUsageEndpoint = "https://api.kimi.com/coding/v1/usages"
	// MiniMax splits its coding-plan remains endpoint by region, like the
	// inference hosts.
	minimaxCnUsageEndpoint     = "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"
	minimaxGlobalUsageEndpoint = "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"
)

// planUsageTimeout bounds one usage query (the reference implementation uses
// 15s as well).
const planUsageTimeout = 15 * time.Second

// Plan usage window names shared with the frontend's display keys. "five_hour"
// is the window the status bar warns about when it exhausts (task 287 × 242).
const (
	planWindowFiveHour = "five_hour"
	planWindowWeekly   = "weekly"
	planWindowMonthly  = "monthly"
)

// PlanUsageWindow is one rolling window of a provider's plan quota. Percent is
// the USED share (0-100) — every family normalizes its wire shape (remaining
// percents, limit/remaining pairs) into this one semantic.
type PlanUsageWindow struct {
	// Window is "five_hour", "weekly", or "monthly".
	Window string `json:"window"`
	// Percent is the used percentage (0-100) of the window's allowance.
	Percent *float64 `json:"percent"`
	// ResetsAt is the ISO-8601 reset instant; empty when the wire carries no
	// usable reset time.
	ResetsAt string `json:"resetsAt"`
}

// PlanUsageView is the generic plan-usage payload (task 287's getPlanUsage
// shape). Note carries the one non-success reason the UI may show; empty means
// Windows are real:
//
//	"no-key"      — the matched provider has no resolvable key (no request)
//	"unsupported" — no plan-capable provider configured (no request);
//	                Supported is false and display surfaces hide entirely
//	"auth-failed" — HTTP 401/403: invalid key
//	"api-error"   — HTTP 2xx with a provider business-level error body
//	"http-<code>" — any other non-2xx status
//	"network" / "parse" — transport or body failures
type PlanUsageView struct {
	Supported bool              `json:"supported"`
	Provider  string            `json:"provider,omitempty"`
	Region    string            `json:"region,omitempty"`
	Windows   []PlanUsageWindow `json:"windows"`
	Note      string            `json:"note"`
	QueriedAt int64             `json:"queriedAt"`
}

// planUsageView assembles a note-only payload with a non-nil empty Windows
// slice — a Go nil slice marshals to JSON null, and the frontend's window
// lookup must never meet null (the same wire contract as the task-163 card).
func planUsageView(supported bool, note string) PlanUsageView {
	return PlanUsageView{Supported: supported, Windows: []PlanUsageWindow{}, Note: note, QueriedAt: time.Now().UnixMilli()}
}

// planUsageTarget is one resolved, plan-capable provider entry.
type planUsageTarget struct {
	family   string // "zhipu" | "kimi" | "minimax"
	endpoint string
	bearer   bool // zhipu takes the raw key; kimi/minimax take Bearer
	provider string
	region   string
	keyEnv   string
}

// hostIs reports whether the entry's base URL belongs to the given host
// (exact-hostname match, scheme- and case-insensitive).
func hostIs(base, host string) bool {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), host)
}

// planUsageTargetForEntry resolves one provider entry into a plan-usage query
// target. Detection is dual-keyed like cc-switch: the preset catalog decides
// the FAMILY (BrandID) and the entry's own base URL decides the HOST — a
// hand-built bigmodel-compatible endpoint on a foreign host never queries
// zhipu's monitor API, and a kimi connection only counts on the /coding path.
func planUsageTargetForEntry(e *config.ProviderEntry) (planUsageTarget, bool) {
	if e == nil {
		return planUsageTarget{}, false
	}
	presetID, catalog, ok := config.CatalogForProviderEntry(e)
	if !ok {
		return planUsageTarget{}, false
	}
	base := strings.TrimSpace(e.BaseURL)
	env := strings.TrimSpace(e.APIKeyEnv)
	target := planUsageTarget{region: catalog.Region}
	switch catalog.BrandID {
	case "zai":
		switch {
		case hostIs(base, "open.bigmodel.cn"):
			target.endpoint = zhipuCnQuotaBase + zhipuQuotaPath
		case hostIs(base, "api.z.ai"):
			target.endpoint = zhipuGlQuotaBase + zhipuQuotaPath
		default:
			return planUsageTarget{}, false
		}
		target.family = "zhipu"
		target.provider = "GLM"
	case "kimi":
		// Only the coding-plan path carries a usage endpoint; the plain Kimi
		// API has none (cc-switch matches api.kimi.com/coding exactly).
		if !hostIs(base, "api.kimi.com") || !strings.Contains(strings.ToLower(base), "api.kimi.com/coding") {
			return planUsageTarget{}, false
		}
		if presetID != "kimi-coding-plan" {
			return planUsageTarget{}, false
		}
		target.family = "kimi"
		target.endpoint = kimiUsageEndpoint
		target.bearer = true
		target.provider = "Kimi For Coding"
	case "minimax":
		switch {
		case hostIs(base, "api.minimaxi.com"):
			target.endpoint = minimaxCnUsageEndpoint
		case hostIs(base, "api.minimax.io"):
			target.endpoint = minimaxGlobalUsageEndpoint
		default:
			return planUsageTarget{}, false
		}
		target.family = "minimax"
		target.bearer = true
		target.provider = "MiniMax"
	default:
		return planUsageTarget{}, false
	}
	if env == "" {
		return planUsageTarget{}, false
	}
	target.keyEnv = env
	return target, true
}

// resolvePlanUsageKey walks the provider list in config order and returns the
// first plan-capable entry with its resolved key. The resolve func maps an env
// name to its value; it is injected so the chain stays pure under test — the
// App wrapper checks process env first (explicit overrides) then the global
// credential resolver, and the value never reaches logs, notes, or the wire.
func resolvePlanUsageTarget(entries []config.ProviderEntry, resolve func(env string) (string, bool)) (planUsageTarget, string, bool) {
	for i := range entries {
		entry := &entries[i]
		target, ok := planUsageTargetForEntry(entry)
		if !ok {
			continue
		}
		if value, set := resolve(target.keyEnv); set {
			if key := strings.TrimSpace(value); key != "" {
				return target, key, true
			}
		}
	}
	return planUsageTarget{}, "", false
}

// planUsageKeyResolver wires the pure chain to this app's config and global
// credential resolver: process env wins (explicit override, also the tests'
// zero-config path), then the credential store the user already configured
// the connection key in. Config load failures degrade to unsupported — the
// query is a nice-to-have and never worth surfacing an error for.
func (a *App) planUsageKeyResolver() ([]config.ProviderEntry, func(env string) (string, bool), bool) {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil || cfg == nil {
		return nil, nil, false
	}
	resolver := config.NewCredentialResolverForRoot(a.activeWorkspaceRoot())
	return cfg.Providers, func(env string) (string, bool) {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, true
		}
		res := resolver.ResolveGlobalFirst(env)
		return res.Value, res.Set
	}, true
}

// GetProviderPlanUsage queries the first plan-capable provider's quota for
// the status bar and the right-dock overview (task 287). No plan provider
// configured → unsupported with zero I/O; no key → no-key with zero I/O.
// Display surfaces hide on both instead of erroring.
func (a *App) GetProviderPlanUsage() (PlanUsageView, error) {
	entries, resolve, ok := a.planUsageKeyResolver()
	if !ok {
		return planUsageView(false, "unsupported"), nil
	}
	target, key, found := resolvePlanUsageTarget(entries, resolve)
	if !found {
		// A plan-family entry without any resolvable key still counts as
		// supported so the overview can show the setup note; no entry at all
		// means the feature is invisible.
		for i := range entries {
			if hinted, ok := planUsageTargetForEntry(&entries[i]); ok {
				return PlanUsageView{Supported: true, Provider: hinted.provider, Region: hinted.region, Windows: []PlanUsageWindow{}, Note: "no-key", QueriedAt: time.Now().UnixMilli()}, nil
			}
		}
		return planUsageView(false, "unsupported"), nil
	}
	windows, note, err := queryPlanUsage(target, key)
	if err != nil {
		return planUsageView(true, note), err
	}
	if note != "" {
		return planUsageView(true, note), nil
	}
	view := planUsageView(true, "")
	view.Provider = target.provider
	view.Region = target.region
	view.Windows = windows
	return view, nil
}

// queryPlanUsage fires one allow-listed endpoint request with the family's
// auth header shape and parses the body defensively. Deterministic provider
// errors come back as (nil, note, nil); transport failures as (nil, note, err).
func queryPlanUsage(target planUsageTarget, key string) ([]PlanUsageWindow, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), planUsageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.endpoint, nil)
	if err != nil {
		return nil, "network", fmt.Errorf("build plan usage request: %w", err)
	}
	// Zhipu takes the raw key (no Bearer); Kimi/MiniMax take Bearer — the two
	// header shapes are not interchangeable.
	if target.bearer {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("Authorization", key)
	}
	req.Header.Set("Accept", "application/json")
	// Zhipu localizes business messages; pin English so error notes stay
	// stable (cc-switch sends the same header).
	req.Header.Set("Accept-Language", "en-US,en")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "network", fmt.Errorf("query plan usage: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, "auth-failed", nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Sprintf("http-%d", resp.StatusCode), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "parse", fmt.Errorf("read plan usage body: %w", err)
	}
	switch target.family {
	case "zhipu":
		return parseZhipuPlanUsage(body)
	case "kimi":
		return parseKimiPlanUsage(body)
	case "minimax":
		return parseMinimaxPlanUsage(body)
	default:
		return nil, "unsupported", fmt.Errorf("unknown plan usage family %q", target.family)
	}
}

// clampPercent keeps every family's arithmetic inside the 0-100 display range.
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// millisToISO converts a millisecond epoch to RFC3339; non-positive values
// (volcano-style "no active window" sentinels) have no reset instant.
func millisToISO(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// flexibleMillis accepts epoch seconds or milliseconds (values below 1e12 are
// seconds) and ISO strings; anything else has no reset instant.
func flexibleMillis(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var asInt int64
	if err := json.Unmarshal(v, &asInt); err == nil {
		if asInt > 0 && asInt < 1_000_000_000_000 {
			asInt *= 1000
		}
		return millisToISO(asInt)
	}
	var asString string
	if err := json.Unmarshal(v, &asString); err == nil {
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(asString)); err == nil {
			return at.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// flexibleNumber accepts a JSON number or numeric string ("100" and 100 both
// appear in the wild).
func flexibleNumber(v json.RawMessage) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	var asFloat float64
	if err := json.Unmarshal(v, &asFloat); err == nil {
		return asFloat, true
	}
	var asString string
	if err := json.Unmarshal(v, &asString); err == nil {
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(asString), "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// parseZhipuPlanUsage decodes 智谱's account quota:
//
//	{"success":true,"data":{"level":"...","limits":[
//	  {"type":"TOKENS_LIMIT","percentage":42.0,"nextResetTime":1735...,"unit":3|6}]}}
//
// unit 3 → five-hour, unit 6 → weekly (cc-switch anchors unit only — both
// number 7 and number 1 weekly shapes have been observed). Entries with a
// missing/unknown unit fall back to cc-switch's heuristic: the entry without
// a reset time is the five-hour bucket (it can sit at 0% with no reset
// scheduled), the rest fill remaining slots in reset-time order. Type must be
// TOKENS_LIMIT or CREDIT_LIMIT (case-insensitive). A success:false body is a
// business-level error, not a parse failure.
func parseZhipuPlanUsage(body []byte) ([]PlanUsageWindow, string, error) {
	var payload struct {
		Success *bool  `json:"success"`
		Msg     string `json:"msg"`
		Data    *struct {
			Limits []struct {
				Type          string          `json:"type"`
				Percentage    json.RawMessage `json:"percentage"`
				NextResetTime json.RawMessage `json:"nextResetTime"`
				Unit          *int64          `json:"unit"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "parse", fmt.Errorf("decode zhipu plan usage: %w", err)
	}
	if payload.Success != nil && !*payload.Success {
		// Business-level answer (deterministic) — a note for the UI, never a
		// Go error: the frontend keeps showing the note instead of a rejected
		// promise, mirroring cc-switch's success:false quota shape.
		return nil, "api-error", nil
	}
	if payload.Data == nil {
		return nil, "parse", fmt.Errorf("zhipu plan usage: missing data field")
	}
	type entry struct {
		percent float64
		reset   string
	}
	var fiveHour, weekly *entry
	var unclassified []entry
	for _, limit := range payload.Data.Limits {
		t := strings.ToUpper(strings.TrimSpace(limit.Type))
		if t != "TOKENS_LIMIT" && t != "CREDIT_LIMIT" {
			continue
		}
		percent, ok := flexibleNumber(limit.Percentage)
		if !ok {
			continue
		}
		e := &entry{percent: clampPercent(percent), reset: flexibleMillis(limit.NextResetTime)}
		switch {
		case limit.Unit == nil:
			unclassified = append(unclassified, *e)
		case *limit.Unit == 3:
			if fiveHour == nil {
				fiveHour = e
			} else {
				unclassified = append(unclassified, *e)
			}
		case *limit.Unit == 6:
			if weekly == nil {
				weekly = e
			} else {
				unclassified = append(unclassified, *e)
			}
		default:
			unclassified = append(unclassified, *e)
		}
	}
	// Heuristic fallback (unit missing or unrecognized): no-reset entries
	// first (five-hour bucket at 0%), then reset-time ascending.
	sort.SliceStable(unclassified, func(i, j int) bool {
		if (unclassified[i].reset == "") != (unclassified[j].reset == "") {
			return unclassified[i].reset == ""
		}
		return unclassified[i].reset < unclassified[j].reset
	})
	for i := range unclassified {
		e := unclassified[i]
		switch {
		case fiveHour == nil:
			fiveHour = &e
		case weekly == nil:
			weekly = &e
		}
	}
	windows := []PlanUsageWindow{}
	for name, e := range map[string]*entry{planWindowFiveHour: fiveHour, planWindowWeekly: weekly} {
		if e == nil {
			continue
		}
		windows = append(windows, PlanUsageWindow{Window: name, Percent: &e.percent, ResetsAt: e.reset})
	}
	sort.SliceStable(windows, func(i, j int) bool { return windows[i].Window < windows[j].Window })
	return windows, "", nil
}

// parseKimiPlanUsage decodes Kimi For Coding:
//
//	{"limits":[{"detail":{"limit":N,"remaining":M,"resetTime":...}}],
//	 "usage":{"limit":N,"remaining":M,"resetTime":...}}
//
// limits → five-hour (used = limit-remaining), usage → weekly. resetTime may
// be an ISO string or an epoch number.
func parseKimiPlanUsage(body []byte) ([]PlanUsageWindow, string, error) {
	var payload struct {
		Limits []struct {
			Detail *struct {
				Limit     json.RawMessage `json:"limit"`
				Remaining json.RawMessage `json:"remaining"`
				ResetTime json.RawMessage `json:"resetTime"`
			} `json:"detail"`
		} `json:"limits"`
		Usage *struct {
			Limit     json.RawMessage `json:"limit"`
			Remaining json.RawMessage `json:"remaining"`
			ResetTime json.RawMessage `json:"resetTime"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "parse", fmt.Errorf("decode kimi plan usage: %w", err)
	}
	usedPercent := func(limitRaw, remainingRaw json.RawMessage) (float64, bool) {
		limit, ok1 := flexibleNumber(limitRaw)
		remaining, ok2 := flexibleNumber(remainingRaw)
		if !ok1 || !ok2 || limit <= 0 {
			return 0, false
		}
		return clampPercent((limit - remaining) / limit * 100), true
	}
	windows := []PlanUsageWindow{}
	for _, limit := range payload.Limits {
		if limit.Detail == nil {
			continue
		}
		if percent, ok := usedPercent(limit.Detail.Limit, limit.Detail.Remaining); ok {
			windows = append(windows, PlanUsageWindow{Window: planWindowFiveHour, Percent: &percent, ResetsAt: flexibleMillis(limit.Detail.ResetTime)})
			break
		}
	}
	if payload.Usage != nil {
		if percent, ok := usedPercent(payload.Usage.Limit, payload.Usage.Remaining); ok {
			windows = append(windows, PlanUsageWindow{Window: planWindowWeekly, Percent: &percent, ResetsAt: flexibleMillis(payload.Usage.ResetTime)})
		}
	}
	return windows, "", nil
}

// parseMinimaxPlanUsage decodes MiniMax coding-plan remains:
//
//	{"base_resp":{"status_code":0,"status_msg":"success"},
//	 "model_remains":[{"model_name":"general",
//	   "current_interval_remaining_percent":72.0,"end_time":1735...,
//	   "current_weekly_status":1,"current_weekly_remaining_percent":88.0,
//	   "weekly_end_time":1735...}]}
//
// Remaining percents invert to used percents. The weekly bucket only exists
// when current_weekly_status == 1 (status 3 = no weekly allowance — its
// remaining is pinned at 100 and must not show as a 0%-used bar). Other
// model_name entries (video) are not the coding plan.
func parseMinimaxPlanUsage(body []byte) ([]PlanUsageWindow, string, error) {
	var payload struct {
		BaseResp *struct {
			StatusCode *int64 `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
		ModelRemains []struct {
			ModelName         string          `json:"model_name"`
			IntervalRemaining json.RawMessage `json:"current_interval_remaining_percent"`
			IntervalEnd       json.RawMessage `json:"end_time"`
			WeeklyStatus      *int64          `json:"current_weekly_status"`
			WeeklyRemaining   json.RawMessage `json:"current_weekly_remaining_percent"`
			WeeklyEnd         json.RawMessage `json:"weekly_end_time"`
		} `json:"model_remains"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "parse", fmt.Errorf("decode minimax plan usage: %w", err)
	}
	if payload.BaseResp != nil && payload.BaseResp.StatusCode != nil && *payload.BaseResp.StatusCode != 0 {
		// Business-level answer (deterministic) — a note for the UI, never a
		// Go error.
		return nil, "api-error", nil
	}
	remainingToUsed := func(raw json.RawMessage) (float64, bool) {
		if remaining, ok := flexibleNumber(raw); ok {
			return clampPercent(100 - remaining), true
		}
		return 0, false
	}
	windows := []PlanUsageWindow{}
	for i := range payload.ModelRemains {
		item := &payload.ModelRemains[i]
		if item.ModelName != "general" {
			continue
		}
		if percent, ok := remainingToUsed(item.IntervalRemaining); ok {
			windows = append(windows, PlanUsageWindow{Window: planWindowFiveHour, Percent: &percent, ResetsAt: flexibleMillis(item.IntervalEnd)})
		}
		if item.WeeklyStatus != nil && *item.WeeklyStatus == 1 {
			if percent, ok := remainingToUsed(item.WeeklyRemaining); ok {
				windows = append(windows, PlanUsageWindow{Window: planWindowWeekly, Percent: &percent, ResetsAt: flexibleMillis(item.WeeklyEnd)})
			}
		}
		break
	}
	return windows, "", nil
}
