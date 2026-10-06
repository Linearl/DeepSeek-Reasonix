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

	"reasonix/internal/config"
)

// Task 163 — usage query contracts: allow-list first, Bearer auth, the three
// window parse, and the entitlement/auth note split (403 vs 401 vs 401-key).

func TestIsOfficialOpenCodeGoBase(t *testing.T) {
	official := []string{
		"https://opencode.ai/zen/go/v1",
		"https://OpenCode.AI",
		"http://opencode.ai/zen/go",
		"  https://opencode.ai  ",
	}
	for _, base := range official {
		if !isOfficialOpenCodeGoBase(base) {
			t.Errorf("isOfficialOpenCodeGoBase(%q) = false, want true", base)
		}
	}
	lookAlikes := []string{
		"https://opencode.ai.evil.example/zen/go/v1", // suffix look-alike
		"https://evil.example/opencode.ai",
		"https://example.com",
		"",
		"not a url",
	}
	for _, base := range lookAlikes {
		if isOfficialOpenCodeGoBase(base) {
			t.Errorf("isOfficialOpenCodeGoBase(%q) = true, want false", base)
		}
	}
}

// withUsageEndpoint points the package var at a counting test server and
// restores it afterwards. hits records how many requests actually reached it.
func withUsageEndpoint(t *testing.T, handler http.HandlerFunc) (hits *int, restore func()) {
	t.Helper()
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		handler(w, r)
	}))
	previous := openCodeGoUsageEndpoint
	openCodeGoUsageEndpoint = srv.URL
	return &n, func() {
		openCodeGoUsageEndpoint = previous
		srv.Close()
	}
}

func TestGetOpenCodeGoUsageRejectsNonOfficialHostWithoutRequest(t *testing.T) {
	t.Setenv("OPENCODE_GO_API_KEY", "test-key")
	hits, restore := withUsageEndpoint(t, func(http.ResponseWriter, *http.Request) {
		t.Error("a non-official base must not reach the network")
	})
	defer restore()

	app := &App{}
	view, err := app.GetOpenCodeGoUsage("https://custom.example.com/zen/go/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.Note != "unsupported-endpoint" {
		t.Fatalf("note = %q, want unsupported-endpoint", view.Note)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0 (allow-list must short-circuit before I/O)", *hits)
	}
}

func TestGetOpenCodeGoUsageWithoutKeyMakesNoRequest(t *testing.T) {
	t.Setenv("OPENCODE_GO_API_KEY", "")
	// Config is a key source since task 337 — isolate so a developer machine
	// with a real OpenCode Go connection cannot turn this into a live request.
	isolateUsageKeyEnvironment(t)
	hits, restore := withUsageEndpoint(t, func(http.ResponseWriter, *http.Request) {
		t.Error("a missing key must not reach the network")
	})
	defer restore()

	app := &App{}
	view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.Note != "no-key" {
		t.Fatalf("note = %q, want no-key", view.Note)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0", *hits)
	}
}

func TestGetOpenCodeGoUsageEndToEnd(t *testing.T) {
	t.Setenv("OPENCODE_GO_API_KEY", "test-key")
	// Task 564: the switch gate reads the saved config — isolate and turn the
	// lab switch on so the end-to-end paths stay hermetic on any machine.
	enableOpenCodeGoUsageSwitch(t)

	t.Run("bearer and three windows", func(t *testing.T) {
		hits, restore := withUsageEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("Authorization = %q, want Bearer test-key (never x-api-key)", got)
			}
			w.Header().Set("Content-Type", "application/json")
			// rolling=0%% keeps its placeholder resetsAt out; weekly is partial;
			// monthly carries a real countdown; one window is missing entirely.
			fmt.Fprint(w, `{"usage":{
				"rolling":{"status":"ok","percent":0,"resetsAt":"2026-09-25T20:00:00Z"},
				"weekly":{"status":"rate-limited","percent":100,"resetsAt":"2026-09-26T00:00:00Z"},
				"monthly":{"status":"ok","percent":41.5,"resetsAt":"2026-10-01T00:00:00Z"}
			}}`)
		})
		defer restore()

		app := &App{}
		view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if view.Note != "" {
			t.Fatalf("note = %q, want empty", view.Note)
		}
		if len(view.Tiers) != 3 {
			t.Fatalf("tiers = %d, want 3", len(view.Tiers))
		}
		byWindow := map[string]int{}
		for i, tier := range view.Tiers {
			byWindow[tier.Window] = i
		}
		if rolling := view.Tiers[byWindow["rolling"]]; rolling.ResetsAt != "" {
			t.Errorf("percent=0 must drop the placeholder resetsAt, got %q", rolling.ResetsAt)
		}
		if weekly := view.Tiers[byWindow["weekly"]]; weekly.Percent == nil || *weekly.Percent != 100 || weekly.ResetsAt == "" {
			t.Errorf("weekly = %+v, want percent=100 with resetsAt", weekly)
		}
		if monthly := view.Tiers[byWindow["monthly"]]; monthly.Percent == nil || *monthly.Percent != 41.5 {
			t.Errorf("monthly = %+v, want percent=41.5", monthly)
		}
		if *hits != 1 {
			t.Fatalf("hits = %d, want 1", *hits)
		}
	})

	t.Run("403 is entitlement not auth", func(t *testing.T) {
		hits, restore := withUsageEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		defer restore()
		app := &App{}
		view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if err != nil {
			t.Fatalf("403 must not be an error: %v", err)
		}
		if view.Note != "no-subscription" {
			t.Fatalf("note = %q, want no-subscription (valid key, no Go plan)", view.Note)
		}
		if *hits != 1 {
			t.Fatalf("hits = %d, want 1", *hits)
		}
	})

	t.Run("401 stays auth", func(t *testing.T) {
		_, restore := withUsageEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		defer restore()
		app := &App{}
		view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if err != nil {
			t.Fatalf("401 must not be an error: %v", err)
		}
		if view.Note != "auth-failed" {
			t.Fatalf("note = %q, want auth-failed", view.Note)
		}
	})

	t.Run("other statuses and bad bodies degrade", func(t *testing.T) {
		_, restore := withUsageEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		app := &App{}
		view, _ := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if view.Note != "http-500" {
			t.Fatalf("note = %q, want http-500", view.Note)
		}
		restore()

		_, restore2 := withUsageEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `not-json`)
		})
		defer restore2()
		view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if err == nil || view.Note != "parse" {
			t.Fatalf("bad body = note %q err %v, want parse+error", view.Note, err)
		}
	})
}

// TestGetOpenCodeGoUsageWireNeverNullsTiers guards the settings-card wire
// contract: every degradation note must marshal "tiers" as []. A Go nil
// slice used to emit JSON null and the card's tier lookup crashed with
// TypeError: null.find (opencodefix). All seven note paths run end to end —
// no skipped cases, and the lower bound proves the enumeration itself worked.
func TestGetOpenCodeGoUsageWireNeverNullsTiers(t *testing.T) {
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	variants := []struct {
		name    string
		base    string
		key     string
		handler http.HandlerFunc
		want    string
	}{
		{"unsupported-endpoint", "https://custom.example.com/zen/go/v1", "k", nil, "unsupported-endpoint"},
		{"no-key", "https://opencode.ai/zen/go/v1", "", nil, "no-key"},
		{"no-subscription", "https://opencode.ai/zen/go/v1", "k", status(http.StatusForbidden), "no-subscription"},
		{"auth-failed", "https://opencode.ai/zen/go/v1", "k", status(http.StatusUnauthorized), "auth-failed"},
		{"http-500", "https://opencode.ai/zen/go/v1", "k", status(http.StatusInternalServerError), "http-500"},
		{"parse", "https://opencode.ai/zen/go/v1", "k", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `not-json`) }, "parse"},
	}
	app := &App{}
	checked := 0
	for _, v := range variants {
		t.Setenv("OPENCODE_GO_API_KEY", v.key)
		// Task 337: config is a key source — keep the empty-explicit variants
		// hermetic so a configured dev machine cannot flip them live.
		isolateUsageKeyEnvironment(t)
		// Task 564: the on-path variants must pin the lab switch on — the gate
		// reads the saved config and the no-key/unsupported variants below
		// short-circuit before it, so enabling here is harmless for them.
		seedOpenCodeGoUsageSwitchConfig(t)
		var restore func()
		if v.handler != nil {
			_, restore = withUsageEndpoint(t, v.handler)
		} else {
			hits, r := withUsageEndpoint(t, func(http.ResponseWriter, *http.Request) {
				t.Errorf("%s must not reach the network", v.name)
			})
			_ = hits
			restore = r
		}
		view, _ := app.GetOpenCodeGoUsage(v.base)
		restore()
		if view.Note != v.want {
			t.Fatalf("%s: note = %q, want %q", v.name, view.Note, v.want)
		}
		payload, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("%s: marshal: %v", v.name, err)
		}
		if !strings.Contains(string(payload), `"tiers":[]`) || strings.Contains(string(payload), `"tiers":null`) {
			t.Errorf("%s: wire = %s, want \"tiers\":[] (a null crashed the settings card)", v.name, payload)
		}
		checked++
	}
	// The network-error path returns an error alongside the note view; assert
	// the same non-null tiers contract on that seventh path. Point the
	// endpoint at a refuse-to-connect localhost port instead of closing an
	// httptest server: a restore there would repoint the var back at the real
	// opencode.ai host and the request would succeed with a 401.
	t.Setenv("OPENCODE_GO_API_KEY", "k")
	seedOpenCodeGoUsageSwitchConfig(t) // task 564: keep the request-firing path past the switch gate
	previousEndpoint := openCodeGoUsageEndpoint
	openCodeGoUsageEndpoint = "http://127.0.0.1:1/usage"
	view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
	openCodeGoUsageEndpoint = previousEndpoint
	if err == nil || view.Note != "network" {
		t.Fatalf("network: note = %q err %v, want network+error", view.Note, err)
	}
	payload, _ := json.Marshal(view)
	if !strings.Contains(string(payload), `"tiers":[]`) || strings.Contains(string(payload), `"tiers":null`) {
		t.Errorf("network: wire = %s, want \"tiers\":[]", payload)
	}
	checked++
	if checked != 7 {
		t.Fatalf("checked = %d note paths, want 7 (enumeration guard)", checked)
	}
}

// isolateUsageKeyEnvironment points config and credential lookups at an empty
// temp Reasonix home so openCodeGoUsageKey never consults the developer's
// real OpenCode Go connection (task 337: config is now a key source).
func isolateUsageKeyEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	t.Setenv("REASONIX_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("REASONIX_CACHE_HOME", filepath.Join(home, "cache"))
}

// seedOpenCodeGoUsageSwitchConfig writes a user config with the task-163 lab
// switch turned on into the CURRENT config home. The task-564 switch gate in
// GetOpenCodeGoUsage reads the saved config on every call, so the on-path
// tests must pin the switch instead of leaning on the developer machine's
// real config. Call after isolateUsageKeyEnvironment.
func seedOpenCodeGoUsageSwitchConfig(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte("[agent]\nexperimental_opencode_go_usage = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// enableOpenCodeGoUsageSwitch isolates the config home AND seeds the lab
// switch on — one call for tests that need both.
func enableOpenCodeGoUsageSwitch(t *testing.T) {
	t.Helper()
	isolateUsageKeyEnvironment(t)
	seedOpenCodeGoUsageSwitchConfig(t)
}

// TestGetOpenCodeGoUsageSwitchGatesRequest pins the task-564 wiring: with the
// lab switch off the query returns before any network I/O from every caller
// (the task-442 context-ring popup included) — empty note, non-nil empty
// tiers (the settings-card wire contract forbids null), zero hits. With the
// switch on the same setup reaches the endpoint exactly once.
func TestGetOpenCodeGoUsageSwitchGatesRequest(t *testing.T) {
	t.Setenv("OPENCODE_GO_API_KEY", "test-key")

	t.Run("off makes no request", func(t *testing.T) {
		isolateUsageKeyEnvironment(t) // empty home: switch defaults to off
		hits, restore := withUsageEndpoint(t, func(http.ResponseWriter, *http.Request) {
			t.Error("a switched-off usage query must not reach the network")
		})
		defer restore()

		app := &App{}
		view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if view.Note != "" || len(view.Tiers) != 0 {
			t.Fatalf("view = %+v, want empty note and zero tiers", view)
		}
		payload, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(payload), `"tiers":[]`) || strings.Contains(string(payload), `"tiers":null`) {
			t.Errorf("wire = %s, want \"tiers\":[] (a null crashed the settings card)", payload)
		}
		if *hits != 0 {
			t.Fatalf("hits = %d, want 0 (switch gate must short-circuit before I/O)", *hits)
		}
	})

	t.Run("on reaches the endpoint", func(t *testing.T) {
		enableOpenCodeGoUsageSwitch(t)
		hits, restore := withUsageEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		defer restore()

		app := &App{}
		view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if view.Note != "auth-failed" {
			t.Fatalf("note = %q, want auth-failed (request must have fired)", view.Note)
		}
		if *hits != 1 {
			t.Fatalf("hits = %d, want 1", *hits)
		}
	})
}

// TestResolveOpenCodeGoUsageKeyPriorityChain locks task 337's resolution
// order as a pure function: explicit env first, then the OpenCode Go
// (Recommended) bundle's shared api_key_env, never a foreign connection.
// Zero skips — every branch is a constructed case.
func TestResolveOpenCodeGoUsageKeyPriorityChain(t *testing.T) {
	bundle := func(name, env string) config.ProviderEntry {
		return config.ProviderEntry{Name: name, PresetID: "opencode-go-recommended", APIKeyEnv: env}
	}
	newResolve := func(mapping map[string]string, calls *int) func(string) (string, bool) {
		return func(env string) (string, bool) {
			if calls != nil {
				*calls++
			}
			value, ok := mapping[env]
			return value, ok
		}
	}

	t.Run("explicit env wins without touching the connection", func(t *testing.T) {
		calls := 0
		got := resolveOpenCodeGoUsageKey("sk-explicit",
			[]config.ProviderEntry{bundle("opencode-go", "REASONIX_CONNECTION_OC_KEY")},
			newResolve(map[string]string{"REASONIX_CONNECTION_OC_KEY": "sk-connection"}, &calls))
		if got != "sk-explicit" {
			t.Fatalf("got %q, want sk-explicit (explicit priority)", got)
		}
		if calls != 0 {
			t.Fatalf("fallback resolve called %d times, want 0 while explicit is set", calls)
		}
	})

	t.Run("connection key is reused through the preset bundle", func(t *testing.T) {
		got := resolveOpenCodeGoUsageKey("",
			[]config.ProviderEntry{bundle("opencode-go", "REASONIX_CONNECTION_OC_KEY")},
			newResolve(map[string]string{"REASONIX_CONNECTION_OC_KEY": "sk-connection"}, nil))
		if got != "sk-connection" {
			t.Fatalf("got %q, want sk-connection (the configured connection key)", got)
		}
	})

	t.Run("hand-built non-preset connections are never reused", func(t *testing.T) {
		got := resolveOpenCodeGoUsageKey("",
			[]config.ProviderEntry{{Name: "my-opencode", APIKeyEnv: "CUSTOM_OPENCODE_KEY"}},
			newResolve(map[string]string{"CUSTOM_OPENCODE_KEY": "sk-foreign"}, nil))
		if got != "" {
			t.Fatalf("got %q, want empty — only the opencode-go-recommended bundle counts (337 ③)", got)
		}
	})

	t.Run("bundle entries without an env name are skipped", func(t *testing.T) {
		got := resolveOpenCodeGoUsageKey("", []config.ProviderEntry{bundle("opencode-go", "  ")}, newResolve(nil, nil))
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("unset or empty resolution degrades to no-key", func(t *testing.T) {
		entries := []config.ProviderEntry{bundle("opencode-go", "MISSING_KEY")}
		if got := resolveOpenCodeGoUsageKey("", entries, newResolve(map[string]string{}, nil)); got != "" {
			t.Fatalf("unset: got %q, want empty", got)
		}
		if got := resolveOpenCodeGoUsageKey("", entries, newResolve(map[string]string{"MISSING_KEY": "   "}, nil)); got != "" {
			t.Fatalf("blank value: got %q, want empty", got)
		}
	})

	t.Run("a bundle whose first route resolves nothing falls to the next", func(t *testing.T) {
		got := resolveOpenCodeGoUsageKey("",
			[]config.ProviderEntry{
				bundle("opencode-go-anthropic", "ROUTE_A_KEY"),
				bundle("opencode-go-responses", "ROUTE_B_KEY"),
			},
			newResolve(map[string]string{"ROUTE_B_KEY": "sk-b"}, nil))
		if got != "sk-b" {
			t.Fatalf("got %q, want sk-b (walk the bundle until one resolves)", got)
		}
	})

	t.Run("whitespace explicit counts as unset", func(t *testing.T) {
		got := resolveOpenCodeGoUsageKey("   ",
			[]config.ProviderEntry{bundle("opencode-go", "CONNECTION_KEY")},
			newResolve(map[string]string{"CONNECTION_KEY": "sk-connection"}, nil))
		if got != "sk-connection" {
			t.Fatalf("got %q, want sk-connection", got)
		}
	})
}

// TestOpenCodeGoUsageKeyShellDegradesWithoutConfig: the App wrapper on an
// empty home (no config.toml) must degrade to no-key instead of erroring.
func TestOpenCodeGoUsageKeyShellDegradesWithoutConfig(t *testing.T) {
	isolateUsageKeyEnvironment(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	app := &App{}
	if key := app.openCodeGoUsageKey(); key != "" {
		t.Fatalf("key = %q on an empty home, want empty (no-key degradation)", key)
	}
}

// TestOpenCodeGoUsageKeyAndWireNeverLeakSecrets: the resolved key may only
// reach the Authorization header. It must never appear in the JSON view, the
// returned error, or any logging call in the source (task 337 acceptance:
// credentials stay out of logs and reports; .env is read, never written).
func TestOpenCodeGoUsageKeyAndWireNeverLeakSecrets(t *testing.T) {
	const sentinel = "sk-SENTINEL-task337-DO-NOT-LEAK"
	t.Setenv("OPENCODE_GO_API_KEY", sentinel)

	hits, restore := withUsageEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+sentinel {
			t.Errorf("Authorization = %q, want the resolved key as Bearer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"usage":{"rolling":{"status":"ok","percent":10,"resetsAt":"2026-10-01T00:00:00Z"}}}`)
	})
	defer restore()

	app := &App{}
	view, err := app.GetOpenCodeGoUsage("https://opencode.ai/zen/go/v1")
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
	src, readErr := os.ReadFile("opencode_go_usage.go")
	if readErr != nil {
		t.Fatalf("read source: %v", readErr)
	}
	for _, forbidden := range []string{"fmt.Print", "slog.", "log.Print", "log.Printf"} {
		if strings.Contains(string(src), forbidden) {
			t.Errorf("source contains %q — the usage key must never be logged", forbidden)
		}
	}
}
