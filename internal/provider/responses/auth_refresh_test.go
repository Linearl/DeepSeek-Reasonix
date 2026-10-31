package responses

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// TestStream401ReauthSelfHeals is the 任务1308 acceptance chain on the
// Responses wire: authenticate once, the credential source rotates behind the
// runtime, the gateway 401s the stale key — and the client recovers without a
// rebuild: one forced re-read inside the existing auth budget, and the next
// attempt carries the fresh key.
func TestStream401ReauthSelfHeals(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	var auths []string
	sse := writeEventsBody(
		`{"type":"response.completed","response":{"id":"resp_1","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		switch {
		case n == 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		case n <= 3:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"token expired"}}`)
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		}
	}))
	defer srv.Close()

	rotated := false
	p := New(Config{
		Name: "ds-responses", BaseURL: srv.URL, Model: "deepseek-chat", APIKey: "stale-token",
		Protocol: "responses", KeyEnv: "DEEPSEEK_API_KEY",
		Extra: map[string]any{
			"request_url": srv.URL,
			"reauth": func() (string, bool) {
				mu.Lock()
				defer mu.Unlock()
				if !rotated {
					return "stale-token", true
				}
				return "fresh-token", true
			},
		},
	})
	c := p

	drain := func(stage string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ch, err := c.Stream(ctx, provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
		if err != nil {
			t.Fatalf("%s: Stream: %v", stage, err)
		}
		for range ch {
		}
	}

	drain("first send") // 200 — authed

	mu.Lock()
	rotated = true
	mu.Unlock()

	drain("after rotation") // stale 401 → forced re-read → fresh key

	mu.Lock()
	defer mu.Unlock()
	want := []string{"Bearer stale-token", "Bearer stale-token", "Bearer fresh-token", "Bearer fresh-token"}
	if len(auths) != len(want) {
		t.Fatalf("wire requests = %d (%v), want %d", len(auths), auths, len(want))
	}
	for i := range want {
		if auths[i] != want[i] {
			t.Fatalf("request %d Authorization = %q, want %q (full: %v)", i+1, auths[i], want[i], auths)
		}
	}
}

// writeEventsBody mirrors the shared writeEvents helper in responses_test.go as
// a standalone string builder so this file stays self-contained.
func writeEventsBody(events ...string) string {
	out := ""
	for _, event := range events {
		out += "event: ignored\n"
		out += "data: " + event + "\n\n"
	}
	return out
}
