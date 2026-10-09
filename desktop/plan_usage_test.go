package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

// Task 287 — plan usage query contracts: family detection (preset brand ×
// entry host), the per-family auth header shape (zhipu raw key, kimi/minimax
// Bearer), the window parses (zhipu unit 3/6 + heuristic, kimi limit/remaining,
// minimax inverted remaining), and the degradation notes. Zero skips — every
// branch is a constructed case.

// withPlanEndpoint points one endpoint var at a counting test server and
// restores it afterwards. hits records how many requests actually reached it.
func withPlanEndpoint(t *testing.T, endpoint *string, handler http.HandlerFunc) (hits *int, restore func()) {
	t.Helper()
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		handler(w, r)
	}))
	previous := *endpoint
	*endpoint = srv.URL
	return &n, func() {
		*endpoint = previous
		srv.Close()
	}
}

// isolatePlanUsageEnvironment points config and credential lookups at an empty
// temp Reasonix home so plan usage key resolution never consults the
// developer's real connections.
func isolatePlanUsageEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	t.Setenv("REASONIX_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("REASONIX_CACHE_HOME", filepath.Join(home, "cache"))
}

// seedPlanUsageConfig writes a user config with the given provider entries.
func seedPlanUsageConfig(t *testing.T, entries string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(entries), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// seedPlanUsageTab registers a local tab whose current model is the given ref
// so GetProviderPlanUsage can attribute the query to the provider actually in
// use (task 666 display condition: the surfaces follow the current model).
func seedPlanUsageTab(t *testing.T, app *App, modelRef string) string {
	t.Helper()
	const id = "tab-plan-usage"
	app.mu.Lock()
	if app.tabs == nil {
		app.tabs = map[string]*WorkspaceTab{}
	}
	app.tabs[id] = &WorkspaceTab{ID: id, model: modelRef}
	app.mu.Unlock()
	t.Cleanup(func() {
		app.mu.Lock()
		delete(app.tabs, id)
		app.mu.Unlock()
	})
	return id
}

func TestPlanUsageTargetForEntry(t *testing.T) {
	cases := []struct {
		name     string
		entry    config.ProviderEntry
		want     bool
		family   string
		bearer   bool
		contains string
	}{
		{"zhipu coding plan cn", config.ProviderEntry{Name: "glm-coding-plan-cn", PresetID: "glm-coding-plan-cn", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKeyEnv: "GLM_PLAN_API_KEY"}, true, "zhipu", false, "open.bigmodel.cn"},
		{"zhipu anthropic route", config.ProviderEntry{Name: "glm-coding-plan-cn-anthropic", PresetID: "glm-coding-plan-cn-anthropic", BaseURL: "https://open.bigmodel.cn/api/anthropic", APIKeyEnv: "GLM_PLAN_API_KEY"}, true, "zhipu", false, "open.bigmodel.cn"},
		{"zai global coding plan", config.ProviderEntry{Name: "zai-coding-plan-global", PresetID: "zai-coding-plan-global", BaseURL: "https://api.z.ai/api/coding/paas/v4", APIKeyEnv: "ZAI_CODING_API_KEY"}, true, "zhipu", false, "api.z.ai"},
		{"zhipu plain api presets hit the account quota too", config.ProviderEntry{Name: "glm-cn", PresetID: "glm-cn", BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKeyEnv: "GLM_API_KEY"}, true, "zhipu", false, "open.bigmodel.cn"},
		{"zhipu look-alike host never matches", config.ProviderEntry{Name: "glm-coding-plan-cn", PresetID: "glm-coding-plan-cn", BaseURL: "https://open.bigmodel.cn.evil.example/api/paas/v4", APIKeyEnv: "GLM_PLAN_API_KEY"}, false, "", false, ""},
		{"zhipu brand on a foreign host never matches", config.ProviderEntry{Name: "my-glm-relay", PresetID: "glm-coding-plan-cn", BaseURL: "https://relay.example.com/v1", APIKeyEnv: "GLM_PLAN_API_KEY"}, false, "", false, ""},
		{"kimi coding plan", config.ProviderEntry{Name: "kimi-coding-plan", PresetID: "kimi-coding-plan", BaseURL: "https://api.kimi.com/coding/", APIKeyEnv: "KIMI_CODING_API_KEY"}, true, "kimi", true, "api.kimi.com"},
		{"kimi plain api has no usage endpoint", config.ProviderEntry{Name: "kimi-cn", PresetID: "kimi-cn", BaseURL: "https://api.kimi.com/v1", APIKeyEnv: "KIMI_API_KEY"}, false, "", false, ""},
		{"minimax cn", config.ProviderEntry{Name: "minimax-cn-api", PresetID: "minimax-cn-api", BaseURL: "https://api.minimaxi.com/v1", APIKeyEnv: "MINIMAX_API_KEY"}, true, "minimax", true, "api.minimaxi.com"},
		{"minimax global anthropic route", config.ProviderEntry{Name: "minimax-global-anthropic", PresetID: "minimax-global-anthropic", BaseURL: "https://api.minimax.io/anthropic", APIKeyEnv: "MINIMAX_API_KEY"}, true, "minimax", true, "api.minimax.io"},
		{"non-plan brands never match", config.ProviderEntry{Name: "deepseek-chat", PresetID: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY"}, false, "", false, ""},
		{"uncataloged entries never match", config.ProviderEntry{Name: "my-relay", BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKeyEnv: "GLM_API_KEY"}, false, "", false, ""},
		{"entries without a key env never match", config.ProviderEntry{Name: "glm-coding-plan-cn", PresetID: "glm-coding-plan-cn", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4"}, false, "", false, ""},
	}
	for _, tc := range cases {
		target, ok := planUsageTargetForEntry(&tc.entry)
		if ok != tc.want {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.want)
			continue
		}
		if !ok {
			continue
		}
		if target.family != tc.family {
			t.Errorf("%s: family = %q, want %q", tc.name, target.family, tc.family)
		}
		if target.bearer != tc.bearer {
			t.Errorf("%s: bearer = %v, want %v", tc.name, target.bearer, tc.bearer)
		}
		if !strings.Contains(target.endpoint, tc.contains) {
			t.Errorf("%s: endpoint = %q, want it to contain %q", tc.name, target.endpoint, tc.contains)
		}
		if target.keyEnv == "" {
			t.Errorf("%s: keyEnv is empty", tc.name)
		}
	}
}

// TestPlanUsageTargetForCurrentModel locks the task-666 selection contract:
// the model ref picks the entry — two same-brand keys stay independent — and
// a model whose provider is not plan-capable never resolves to a target.
func TestPlanUsageTargetForCurrentModel(t *testing.T) {
	resolve := func(mapping map[string]string) func(string) (string, bool) {
		return func(env string) (string, bool) {
			v, ok := mapping[env]
			return v, ok
		}
	}
	glmA := config.ProviderEntry{Name: "glm-plan-a", PresetID: "glm-coding-plan-cn", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKeyEnv: "GLM_PLAN_A_KEY", Model: "glm-4.6"}
	glmB := config.ProviderEntry{Name: "glm-plan-b", PresetID: "zai-coding-plan-global", BaseURL: "https://api.z.ai/api/coding/paas/v4", APIKeyEnv: "GLM_PLAN_B_KEY", Model: "glm-4.7"}
	kimi := config.ProviderEntry{Name: "kimi-coding-plan", PresetID: "kimi-coding-plan", BaseURL: "https://api.kimi.com/coding/", APIKeyEnv: "KIMI_CODING_API_KEY", Model: "kimi-coding"}
	deepseek := config.ProviderEntry{Name: "deepseek-chat", PresetID: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY", Model: "deepseek-chat"}
	all := []config.ProviderEntry{deepseek, glmA, glmB, kimi}
	keys := resolve(map[string]string{"GLM_PLAN_A_KEY": "sk-a", "GLM_PLAN_B_KEY": "sk-b", "KIMI_CODING_API_KEY": "sk-kimi", "DEEPSEEK_API_KEY": "sk-ds"})

	t.Run("the ref's provider wins — second GLM key queries its own quota", func(t *testing.T) {
		target, key, ok := planUsageTargetForCurrentModel(all, "glm-plan-b/glm-4.7", keys)
		if !ok || key != "sk-b" || target.keyEnv != "GLM_PLAN_B_KEY" || target.provider != "GLM" {
			t.Fatalf("got %+v key %q ok %v, want the glm-plan-b entry with its own key", target, key, ok)
		}
		if !strings.Contains(target.endpoint, "api.z.ai") {
			t.Fatalf("endpoint = %q, want the glm-plan-b global route", target.endpoint)
		}
	})
	t.Run("the first configured entry is NOT a default fallback", func(t *testing.T) {
		// deepseek sits first in config order; only a deepseek ref may pick it.
		if _, _, ok := planUsageTargetForCurrentModel(all, "deepseek-chat/deepseek-chat", keys); ok {
			t.Fatalf("a deepseek current model must not resolve to a plan target")
		}
		target, key, ok := planUsageTargetForCurrentModel(all, "glm-plan-a/glm-4.6", keys)
		if !ok || key != "sk-a" || target.keyEnv != "GLM_PLAN_A_KEY" {
			t.Fatalf("got %+v key %q ok %v, want the glm-plan-a entry with its own key", target, key, ok)
		}
	})
	t.Run("kimi coding ref resolves its own family", func(t *testing.T) {
		target, key, ok := planUsageTargetForCurrentModel(all, "kimi-coding-plan/kimi-coding", keys)
		if !ok || key != "sk-kimi" || target.family != "kimi" {
			t.Fatalf("got %+v key %q ok %v, want the kimi entry", target, key, ok)
		}
	})
	t.Run("plan-capable entry without a key still resolves (setup note path)", func(t *testing.T) {
		target, key, ok := planUsageTargetForCurrentModel([]config.ProviderEntry{glmA}, "glm-plan-a/glm-4.6", resolve(nil))
		if !ok || key != "" || target.keyEnv != "GLM_PLAN_A_KEY" {
			t.Fatalf("got %+v key %q ok %v, want ok with an empty key", target, key, ok)
		}
	})
	t.Run("unknown ref and empty ref resolve nowhere", func(t *testing.T) {
		if _, _, ok := planUsageTargetForCurrentModel(all, "ghost/nope", keys); ok {
			t.Fatalf("an unknown ref must not resolve")
		}
		if _, _, ok := planUsageTargetForCurrentModel(all, "  ", keys); ok {
			t.Fatalf("an empty ref must not resolve")
		}
	})
}

// TestPlanUsageTargetForCurrentModelKimiMiniMaxMultiKey is task 666 point 3
// (the "same check for the Kimi/MiniMax channels" clause): those families go
// through the SAME provider-based resolution, so two same-brand entries are
// independent keys and only the entry behind the current model ref may answer
// — no cross-key quota bleed, and a deepseek ref never picks any of them.
func TestPlanUsageTargetForCurrentModelKimiMiniMaxMultiKey(t *testing.T) {
	resolve := func(mapping map[string]string) func(string) (string, bool) {
		return func(env string) (string, bool) {
			v, ok := mapping[env]
			return v, ok
		}
	}
	kimiA := config.ProviderEntry{Name: "kimi-a", PresetID: "kimi-coding-plan", BaseURL: "https://api.kimi.com/coding/", APIKeyEnv: "KIMI_A_KEY", Model: "kimi-for-coding"}
	kimiB := config.ProviderEntry{Name: "kimi-b", PresetID: "kimi-coding-plan", BaseURL: "https://api.kimi.com/coding/", APIKeyEnv: "KIMI_B_KEY", Model: "kimi-for-coding"}
	mmA := config.ProviderEntry{Name: "minimax-cn", PresetID: "minimax-cn-api", BaseURL: "https://api.minimaxi.com/v1", APIKeyEnv: "MM_CN_KEY", Model: "MiniMax-M2"}
	mmB := config.ProviderEntry{Name: "minimax-global", PresetID: "minimax-global-api", BaseURL: "https://api.minimax.io/v1", APIKeyEnv: "MM_GL_KEY", Model: "MiniMax-M2"}
	deepseek := config.ProviderEntry{Name: "deepseek", PresetID: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_KEY", Model: "deepseek-chat"}
	all := []config.ProviderEntry{deepseek, kimiA, kimiB, mmA, mmB}
	keys := resolve(map[string]string{
		"KIMI_A_KEY": "sk-kimi-a", "KIMI_B_KEY": "sk-kimi-b",
		"MM_CN_KEY": "sk-mm-cn", "MM_GL_KEY": "sk-mm-gl",
		"DEEPSEEK_KEY": "sk-ds",
	})

	t.Run("two kimi entries are independent keys", func(t *testing.T) {
		target, key, ok := planUsageTargetForCurrentModel(all, "kimi-b/kimi-for-coding", keys)
		if !ok || key != "sk-kimi-b" || target.keyEnv != "KIMI_B_KEY" || target.family != "kimi" {
			t.Fatalf("got %+v key %q ok %v, want the kimi-b entry with its own key", target, key, ok)
		}
		if _, _, ok := planUsageTargetForCurrentModel(all, "deepseek/deepseek-chat", keys); ok {
			t.Fatalf("a deepseek current model must not resolve to a kimi/minimax plan target")
		}
	})
	t.Run("two minimax regions are independent keys", func(t *testing.T) {
		target, key, ok := planUsageTargetForCurrentModel(all, "minimax-global/MiniMax-M2", keys)
		if !ok || key != "sk-mm-gl" || target.keyEnv != "MM_GL_KEY" || target.family != "minimax" {
			t.Fatalf("got %+v key %q ok %v, want the minimax-global entry with its own key", target, key, ok)
		}
		if !strings.Contains(target.endpoint, "api.minimax.io") {
			t.Fatalf("endpoint = %q, want the minimax-global route", target.endpoint)
		}
	})
}

func TestGetProviderPlanUsageUnsupportedMakesNoRequest(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com/v1"
api_key_env = "MY_DEEPSEEK_KEY"
`)
	t.Setenv("MY_DEEPSEEK_KEY", "sk")
	hits, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(http.ResponseWriter, *http.Request) {
		t.Error("a config without a plan provider must not reach the network")
	})
	defer restore()

	app := &App{}
	id := seedPlanUsageTab(t, app, "deepseek/deepseek-chat")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.Supported || view.Note != "unsupported" {
		t.Fatalf("view = %+v, want unsupported", view)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0 (unsupported must short-circuit before I/O)", *hits)
	}
}

// TestGetProviderPlanUsageNonPlanModelHides locks the task-666 display
// condition: the surfaces show only while the CURRENT model's provider is
// plan-capable — a GLM provider in the config must not leak its quota into a
// deepseek tab.
func TestGetProviderPlanUsageNonPlanModelHides(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-coding-plan-cn"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_API_KEY"
model = "glm-4.6"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com/v1"
api_key_env = "MY_DEEPSEEK_KEY"
model = "deepseek-chat"
`)
	t.Setenv("GLM_PLAN_API_KEY", "sk-glm")
	t.Setenv("MY_DEEPSEEK_KEY", "sk-ds")
	hits, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(http.ResponseWriter, *http.Request) {
		t.Error("a non-plan current model must not reach the network")
	})
	defer restore()

	app := &App{}
	id := seedPlanUsageTab(t, app, "deepseek/deepseek-chat")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.Supported || view.Note != "unsupported" {
		t.Fatalf("view = %+v, want unsupported while a deepseek model is current", view)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0", *hits)
	}
	// Switching the same tab back to the GLM model re-arms the surfaces —
	// the display follows the model, not the config order.
	restore()
	glHits, restoreGL := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","percentage":5,"unit":3}]}}`)
	})
	defer restoreGL()
	glID := seedPlanUsageTab(t, app, "glm-coding-plan-cn/glm-4.6")
	view, err = app.GetProviderPlanUsage(glID)
	if err != nil || !view.Supported || view.Note != "" {
		t.Fatalf("view = %+v err = %v, want a clean success for the GLM current model", view, err)
	}
	if *glHits != 1 {
		t.Fatalf("glHits = %d, want 1", *glHits)
	}
}

func TestGetProviderPlanUsageWithoutCurrentModelHides(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-coding-plan-cn"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_API_KEY"
model = "glm-4.6"
`)
	t.Setenv("GLM_PLAN_API_KEY", "sk-glm")
	hits, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(http.ResponseWriter, *http.Request) {
		t.Error("no current model must not reach the network")
	})
	defer restore()

	app := &App{}
	if view, err := app.GetProviderPlanUsage("tab-that-does-not-exist"); err != nil || view.Supported {
		t.Fatalf("view = %+v err = %v, want unsupported without a tab", view, err)
	}
	id := seedPlanUsageTab(t, app, "")
	if view, err := app.GetProviderPlanUsage(id); err != nil || view.Supported {
		t.Fatalf("view = %+v err = %v, want unsupported with an empty current model", view, err)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0", *hits)
	}
}

// TestGetProviderPlanUsageMultiKeyFollowsCurrentModel is the task-666
// multi-key acceptance: two GLM CP entries are independent keys/quotas, and
// the query must hit the entry the tab's current model names — with its key,
// on its own regional endpoint.
func TestGetProviderPlanUsageMultiKeyFollowsCurrentModel(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-plan-a"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_A_KEY"
model = "glm-4.6"

[[providers]]
name = "glm-plan-b"
kind = "openai"
preset_id = "zai-coding-plan-global"
base_url = "https://api.z.ai/api/coding/paas/v4"
api_key_env = "GLM_PLAN_B_KEY"
model = "glm-4.7"
`)
	t.Setenv("GLM_PLAN_A_KEY", "sk-key-A")
	t.Setenv("GLM_PLAN_B_KEY", "sk-key-B")

	cnHits, restoreCN := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the CN endpoint must not be queried for the glm-plan-b model, got key %q", r.Header.Get("Authorization"))
	})
	glHits, restoreGL := withPlanEndpoint(t, &zhipuGlQuotaBase, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "sk-key-B" {
			t.Errorf("Authorization = %q, want glm-plan-b's own key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","percentage":21,"unit":3}]}}`)
	})
	defer restoreCN()
	defer restoreGL()

	app := &App{}
	id := seedPlanUsageTab(t, app, "glm-plan-b/glm-4.7")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil || view.Note != "" {
		t.Fatalf("view = %+v err = %v, want a clean success for glm-plan-b", view, err)
	}
	if view.Provider != "GLM" {
		t.Fatalf("provider = %q, want GLM", view.Provider)
	}
	if *cnHits != 0 || *glHits != 1 {
		t.Fatalf("hits cn=%d gl=%d, want the query on the glm-plan-b endpoint only", *cnHits, *glHits)
	}
	byWindow := map[string]PlanUsageWindow{}
	for _, w := range view.Windows {
		byWindow[w.Window] = w
	}
	if five := byWindow["five_hour"]; five.Percent == nil || *five.Percent != 21 {
		t.Errorf("five_hour = %+v, want glm-plan-b's own 21%% window", five)
	}
}

func TestGetProviderPlanUsageWithoutKeyMakesNoRequest(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-coding-plan-cn"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_API_KEY"
model = "glm-4.6"
`)
	// No GLM_PLAN_API_KEY anywhere: env unset, credential store empty.
	hits, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(http.ResponseWriter, *http.Request) {
		t.Error("a missing key must not reach the network")
	})
	defer restore()

	app := &App{}
	id := seedPlanUsageTab(t, app, "glm-coding-plan-cn/glm-4.6")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !view.Supported || view.Note != "no-key" {
		t.Fatalf("view = %+v, want supported with no-key", view)
	}
	if view.Provider != "GLM" {
		t.Fatalf("provider = %q, want GLM", view.Provider)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0", *hits)
	}
}

func TestGetProviderPlanUsageZhipuEndToEnd(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-coding-plan-cn"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_API_KEY"
model = "glm-4.6"
`)
	t.Setenv("GLM_PLAN_API_KEY", "test-key")
	const glmRef = "glm-coding-plan-cn/glm-4.6"

	t.Run("raw key header and unit windows", func(t *testing.T) {
		hits, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "test-key" {
				t.Errorf("Authorization = %q, want the raw key (zhipu takes no Bearer prefix)", got)
			}
			if r.URL.Path != "/api/monitor/usage/quota/limit" {
				t.Errorf("path = %q, want the quota limit route", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			// unit 3 = five-hour, unit 6 = weekly; a CREDIT_LIMIT row and an
			// unknown type ride along; one entry lacks unit entirely.
			fmt.Fprint(w, `{"success":true,"data":{"level":"glm-coding-pro","limits":[
				{"type":"TOKENS_LIMIT","percentage":42.5,"nextResetTime":1767225600000,"unit":3,"number":5},
				{"type":"TOKENS_LIMIT","percentage":7,"nextResetTime":1767830400000,"unit":6,"number":1},
				{"type":"CREDIT_LIMIT","percentage":11,"unit":3},
				{"type":"OTHER_LIMIT","percentage":99,"unit":3},
				{"type":"TOKENS_LIMIT","percentage":1}
			]}}`)
		})
		defer restore()

		app := &App{}
		id := seedPlanUsageTab(t, app, glmRef)
		view, err := app.GetProviderPlanUsage(id)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if view.Note != "" || !view.Supported {
			t.Fatalf("view = %+v, want a clean success", view)
		}
		if len(view.Windows) != 2 {
			t.Fatalf("windows = %d, want 2 (five_hour + weekly)", len(view.Windows))
		}
		byWindow := map[string]PlanUsageWindow{}
		for _, w := range view.Windows {
			byWindow[w.Window] = w
		}
		five, ok := byWindow["five_hour"]
		if !ok || five.Percent == nil || *five.Percent != 42.5 {
			t.Errorf("five_hour = %+v, want percent 42.5 (unit 3 wins over the unclassified row)", five)
		}
		weekly := byWindow["weekly"]
		if weekly.Percent == nil || *weekly.Percent != 7 {
			t.Errorf("weekly = %+v, want percent 7 (unit 6)", weekly)
		}
		if five.ResetsAt == "" {
			t.Errorf("five_hour resetsAt = empty, want the millis instant converted")
		}
		if _, _, err := parseAt(five.ResetsAt); err != nil {
			t.Errorf("resetsAt %q is not RFC3339: %v", five.ResetsAt, err)
		}
		if *hits != 1 {
			t.Fatalf("hits = %d, want 1", *hits)
		}
	})

	t.Run("missing unit falls back to the reset-time heuristic", func(t *testing.T) {
		_, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, _ *http.Request) {
			// Two unitless rows: the one without a reset time is the five-hour
			// bucket; the later reset fills the weekly slot.
			fmt.Fprint(w, `{"success":true,"data":{"limits":[
				{"type":"TOKENS_LIMIT","percentage":9,"nextResetTime":1767830400000},
				{"type":"TOKENS_LIMIT","percentage":55}
			]}}`)
		})
		defer restore()
		app := &App{}
		id := seedPlanUsageTab(t, app, glmRef)
		view, _ := app.GetProviderPlanUsage(id)
		byWindow := map[string]PlanUsageWindow{}
		for _, w := range view.Windows {
			byWindow[w.Window] = w
		}
		if five := byWindow["five_hour"]; five.Percent == nil || *five.Percent != 55 {
			t.Errorf("five_hour = %+v, want percent 55 (no-reset row first)", five)
		}
		if weekly := byWindow["weekly"]; weekly.Percent == nil || *weekly.Percent != 9 {
			t.Errorf("weekly = %+v, want percent 9", weekly)
		}
	})

	t.Run("business error is a note not a crash", func(t *testing.T) {
		_, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"success":false,"msg":"invalid scope"}`)
		})
		defer restore()
		app := &App{}
		id := seedPlanUsageTab(t, app, glmRef)
		view, err := app.GetProviderPlanUsage(id)
		if err != nil {
			t.Fatalf("business errors must not surface as Go errors: %v", err)
		}
		if view.Note != "api-error" {
			t.Fatalf("note = %q, want api-error", view.Note)
		}
	})

	t.Run("401 stays auth", func(t *testing.T) {
		_, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		defer restore()
		app := &App{}
		id := seedPlanUsageTab(t, app, glmRef)
		view, _ := app.GetProviderPlanUsage(id)
		if view.Note != "auth-failed" {
			t.Fatalf("note = %q, want auth-failed", view.Note)
		}
	})
}

func TestGetProviderPlanUsageKimiEndToEnd(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "kimi-coding-plan"
kind = "anthropic"
preset_id = "kimi-coding-plan"
base_url = "https://api.kimi.com/coding/"
api_key_env = "KIMI_CODING_API_KEY"
model = "kimi-for-coding"
`)
	t.Setenv("KIMI_CODING_API_KEY", "test-kimi")

	hits, restore := withPlanEndpoint(t, &kimiUsageEndpoint, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-kimi" {
			t.Errorf("Authorization = %q, want Bearer test-kimi", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// five-hour: limit 100 remaining 30 → 70% used; weekly: 200/50 → 75%.
		fmt.Fprint(w, `{"limits":[{"detail":{"limit":100,"remaining":30,"resetTime":"2026-10-09T00:00:00Z"}}],
			"usage":{"limit":200,"remaining":50,"resetTime":1767830400}}`)
	})
	defer restore()

	app := &App{}
	id := seedPlanUsageTab(t, app, "kimi-coding-plan/kimi-for-coding")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil || view.Note != "" {
		t.Fatalf("view = %+v err = %v, want a clean success", view, err)
	}
	if view.Provider != "Kimi For Coding" {
		t.Fatalf("provider = %q, want Kimi For Coding", view.Provider)
	}
	byWindow := map[string]PlanUsageWindow{}
	for _, w := range view.Windows {
		byWindow[w.Window] = w
	}
	if five := byWindow["five_hour"]; five.Percent == nil || *five.Percent != 70 {
		t.Errorf("five_hour = %+v, want 70 (limit-remaining over limit)", five)
	}
	if weekly := byWindow["weekly"]; weekly.Percent == nil || *weekly.Percent != 75 {
		t.Errorf("weekly = %+v, want 75", weekly)
	} else if weekly.ResetsAt == "" {
		// The weekly resetTime arrived as epoch SECONDS — must still convert.
		t.Errorf("weekly resetsAt = empty, want the seconds epoch converted")
	}
	if *hits != 1 {
		t.Fatalf("hits = %d, want 1", *hits)
	}
}

func TestGetProviderPlanUsageMinimaxEndToEnd(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "minimax-cn-api"
kind = "openai"
preset_id = "minimax-cn-api"
base_url = "https://api.minimaxi.com/v1"
api_key_env = "MINIMAX_API_KEY"
model = "MiniMax-M2"
`)
	t.Setenv("MINIMAX_API_KEY", "test-mm")

	hits, restore := withPlanEndpoint(t, &minimaxCnUsageEndpoint, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-mm" {
			t.Errorf("Authorization = %q, want Bearer test-mm", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// general interval 72% remaining → 28% used; weekly status 1 with 88%
		// remaining → 12% used; a video row must never leak in.
		fmt.Fprint(w, `{"base_resp":{"status_code":0,"status_msg":"success"},"model_remains":[
			{"model_name":"video","current_interval_remaining_percent":10},
			{"model_name":"general","current_interval_remaining_percent":72,"end_time":1767225600000,
			 "current_weekly_status":1,"current_weekly_remaining_percent":88,"weekly_end_time":1767830400000}
		]}`)
	})
	defer restore()

	app := &App{}
	id := seedPlanUsageTab(t, app, "minimax-cn-api/MiniMax-M2")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil || view.Note != "" {
		t.Fatalf("view = %+v err = %v, want a clean success", view, err)
	}
	if view.Provider != "MiniMax" || view.Region != "cn" {
		t.Fatalf("provider/region = %q/%q, want MiniMax/cn", view.Provider, view.Region)
	}
	byWindow := map[string]PlanUsageWindow{}
	for _, w := range view.Windows {
		byWindow[w.Window] = w
	}
	if five := byWindow["five_hour"]; five.Percent == nil || *five.Percent != 28 {
		t.Errorf("five_hour = %+v, want 28 (100-remaining)", five)
	}
	if weekly := byWindow["weekly"]; weekly.Percent == nil || *weekly.Percent != 12 {
		t.Errorf("weekly = %+v, want 12", weekly)
	}
	if *hits != 1 {
		t.Fatalf("hits = %d, want 1", *hits)
	}

	t.Run("weekly status 3 means no weekly bucket", func(t *testing.T) {
		_, restore := withPlanEndpoint(t, &minimaxCnUsageEndpoint, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"base_resp":{"status_code":0},"model_remains":[
				{"model_name":"general","current_interval_remaining_percent":50,
				 "current_weekly_status":3,"current_weekly_remaining_percent":100}
			]}`)
		})
		defer restore()
		app := &App{}
		id := seedPlanUsageTab(t, app, "minimax-cn-api/MiniMax-M2")
		view, _ := app.GetProviderPlanUsage(id)
		if len(view.Windows) != 1 || view.Windows[0].Window != "five_hour" {
			t.Fatalf("windows = %+v, want five_hour only", view.Windows)
		}
	})

	t.Run("base_resp error is a note", func(t *testing.T) {
		_, restore := withPlanEndpoint(t, &minimaxCnUsageEndpoint, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"base_resp":{"status_code":1004,"status_msg":"invalid"}}`)
		})
		defer restore()
		app := &App{}
		id := seedPlanUsageTab(t, app, "minimax-cn-api/MiniMax-M2")
		view, err := app.GetProviderPlanUsage(id)
		if err != nil {
			t.Fatalf("business errors must not surface as Go errors: %v", err)
		}
		if view.Note != "api-error" {
			t.Fatalf("note = %q, want api-error", view.Note)
		}
	})
}

// TestGetProviderPlanUsageWireNeverNullsWindows guards the frontend contract:
// every degradation path marshals "windows" as [] — a Go nil slice emits JSON
// null and a null .windows crashed the task-163 card once already.
func TestGetProviderPlanUsageWireNeverNullsWindows(t *testing.T) {
	isolatePlanUsageEnvironment(t)
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-coding-plan-cn"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_API_KEY"
model = "glm-4.6"
`)
	variants := []struct {
		name    string
		key     string
		handler http.HandlerFunc
		want    string
	}{
		{"no-key", "", nil, "no-key"},
		{"auth-failed", "k", status(http.StatusUnauthorized), "auth-failed"},
		{"http-500", "k", status(http.StatusInternalServerError), "http-500"},
		{"parse", "k", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `not-json`) }, "parse"},
		{"api-error", "k", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"success":false,"msg":"no"}`) }, "api-error"},
	}
	app := &App{}
	planTab := seedPlanUsageTab(t, app, "glm-coding-plan-cn/glm-4.6")
	checked := 0
	for _, v := range variants {
		t.Setenv("GLM_PLAN_API_KEY", v.key)
		var restore func()
		if v.handler != nil {
			_, restore = withPlanEndpoint(t, &zhipuCnQuotaBase, v.handler)
		} else {
			_, restore = withPlanEndpoint(t, &zhipuCnQuotaBase, func(http.ResponseWriter, *http.Request) {
				t.Errorf("%s must not reach the network", v.name)
			})
		}
		view, _ := app.GetProviderPlanUsage(planTab)
		restore()
		if view.Note != v.want {
			t.Fatalf("%s: note = %q, want %q", v.name, view.Note, v.want)
		}
		payload, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("%s: marshal: %v", v.name, err)
		}
		if !strings.Contains(string(payload), `"windows":[]`) || strings.Contains(string(payload), `"windows":null`) {
			t.Errorf("%s: wire = %s, want \"windows\":[]", v.name, payload)
		}
		checked++
	}
	// The network-error path returns an error alongside the note view; assert
	// the same non-null contract there. Point the endpoint at a refuse-to-
	// connect port so the request fails in transport, not with a real status.
	t.Setenv("GLM_PLAN_API_KEY", "k")
	previous := zhipuCnQuotaBase
	zhipuCnQuotaBase = "http://127.0.0.1:1"
	view, err := app.GetProviderPlanUsage(planTab)
	zhipuCnQuotaBase = previous
	if err == nil || view.Note != "network" {
		t.Fatalf("network: note = %q err %v, want network+error", view.Note, err)
	}
	payload, _ := json.Marshal(view)
	if !strings.Contains(string(payload), `"windows":[]`) || strings.Contains(string(payload), `"windows":null`) {
		t.Errorf("network: wire = %s, want \"windows\":[]", payload)
	}
	checked++
	if checked != 6 {
		t.Fatalf("checked = %d note paths, want 6 (enumeration guard)", checked)
	}
}

// TestPlanUsageKeyAndWireNeverLeakSecrets pins the credential contract: the
// resolved key may only reach the Authorization header — never the JSON view,
// the returned error, or any logging call (task 287 acceptance).
func TestPlanUsageKeyAndWireNeverLeakSecrets(t *testing.T) {
	const sentinel = "sk-SENTINEL-task287-DO-NOT-LEAK"
	isolatePlanUsageEnvironment(t)
	seedPlanUsageConfig(t, `
[[providers]]
name = "glm-coding-plan-cn"
kind = "openai"
preset_id = "glm-coding-plan-cn"
base_url = "https://open.bigmodel.cn/api/coding/paas/v4"
api_key_env = "GLM_PLAN_API_KEY"
model = "glm-4.6"
`)
	t.Setenv("GLM_PLAN_API_KEY", sentinel)

	hits, restore := withPlanEndpoint(t, &zhipuCnQuotaBase, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != sentinel {
			t.Errorf("Authorization = %q, want the resolved raw key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","percentage":10,"unit":3}]}}`)
	})
	defer restore()

	app := &App{}
	id := seedPlanUsageTab(t, app, "glm-coding-plan-cn/glm-4.6")
	view, err := app.GetProviderPlanUsage(id)
	if err != nil || view.Note != "" {
		t.Fatalf("view = %+v err = %v, want a clean success", view, err)
	}
	if *hits != 1 {
		t.Fatalf("hits = %d, want 1", *hits)
	}
	payload, marshalErr := json.Marshal(view)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	if strings.Contains(string(payload), sentinel) {
		t.Fatalf("wire leaked the key: %s", payload)
	}
	src, readErr := os.ReadFile("plan_usage.go")
	if readErr != nil {
		t.Fatalf("read source: %v", readErr)
	}
	for _, forbidden := range []string{"fmt.Print", "slog.", "log.Print", "log.Printf"} {
		if strings.Contains(string(src), forbidden) {
			t.Errorf("source contains %q — the plan usage key must never be logged", forbidden)
		}
	}
}

// TestPlanUsageFlexibleHelpers cover the two wire-shape adapters directly:
// seconds-vs-millis epoch detection and numeric strings.
func TestPlanUsageFlexibleHelpers(t *testing.T) {
	if got := flexibleMillis(json.RawMessage(`1767225600`)); got == "" {
		t.Errorf("seconds epoch did not convert")
	}
	if got := flexibleMillis(json.RawMessage(`1767225600000`)); got == "" {
		t.Errorf("millis epoch did not convert")
	}
	if got := flexibleMillis(json.RawMessage(`0`)); got != "" {
		t.Errorf("zero epoch must have no reset instant, got %q", got)
	}
	if got := flexibleMillis(json.RawMessage(`"2026-10-09T00:00:00Z"`)); got != "2026-10-09T00:00:00Z" {
		t.Errorf("ISO string = %q, want passthrough (normalized)", got)
	}
	if got := flexibleMillis(json.RawMessage(`"garbage"`)); got != "" {
		t.Errorf("garbage = %q, want empty", got)
	}
	if v, ok := flexibleNumber(json.RawMessage(`"42.5"`)); !ok || v != 42.5 {
		t.Errorf("numeric string = %v %v, want 42.5 true", v, ok)
	}
	if v, ok := flexibleNumber(json.RawMessage(`7`)); !ok || v != 7 {
		t.Errorf("number = %v %v, want 7 true", v, ok)
	}
	if _, ok := flexibleNumber(json.RawMessage(`"abc"`)); ok {
		t.Errorf("garbage must not parse as a number")
	}
}

// parseAt asserts a reset instant is a real RFC3339 timestamp.
func parseAt(iso string) (bool, string, error) {
	_, err := time.Parse(time.RFC3339, iso)
	return err == nil, iso, err
}
