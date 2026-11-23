package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tl1774 (issue #105): a 404 on the chat POST must carry the base_url
// guidance so a bare-base misconfiguration names its fix at the first
// failure, not after the user cross-references the settings page.
func TestSendWithRetryNotFoundHintPerProtocol(t *testing.T) {
	cases := []struct {
		protocol     string
		wantContains string
	}{
		{protocol: "openai", wantContains: "/chat/completions"},
		{protocol: "anthropic", wantContains: "/v1/messages"},
		{protocol: "responses", wantContains: "/responses"},
		{protocol: "custom-adapter", wantContains: "/chat/completions"},
	}
	for _, tc := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		_, err := SendWithRetry(context.Background(), server.Client(), SendOptions{
			Provider: "p", ProviderDisplayName: "P", Protocol: tc.protocol,
		}, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/chat", nil)
		})
		server.Close()
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: error = %T %v", tc.protocol, err, err)
		}
		if apiErr.Status != http.StatusNotFound {
			t.Fatalf("%s: status = %d", tc.protocol, apiErr.Status)
		}
		if !strings.Contains(apiErr.Hint, "base_url") || !strings.Contains(apiErr.Hint, tc.wantContains) {
			t.Fatalf("%s: hint %q misses base_url/%s", tc.protocol, apiErr.Hint, tc.wantContains)
		}
	}
}

// Zero-noise guard: non-404 endpoints keep their body as the only diagnostic,
// the endpoint hint never attaches.
func TestSendWithRetryNotFoundHintStays404Scoped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad payload shape`))
	}))
	defer server.Close()
	_, err := SendWithRetry(context.Background(), server.Client(), SendOptions{
		Provider: "p", ProviderDisplayName: "P", Protocol: "openai",
	}, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/chat", nil)
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if apiErr.Hint != "" {
		t.Fatalf("400 should not carry the endpoint hint, got %q", apiErr.Hint)
	}
	if !strings.Contains(apiErr.Error(), "bad payload shape") {
		t.Fatalf("400 body lost: %q", apiErr.Error())
	}
}
