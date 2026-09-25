package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
