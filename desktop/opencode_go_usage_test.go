package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
