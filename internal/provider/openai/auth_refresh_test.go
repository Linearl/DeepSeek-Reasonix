package openai

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

// TestStream401ReauthSelfHeals is the 任务1308 acceptance chain, end to end on
// the wire: a client authenticates once (authed), the credential source rotates
// behind it (the runtime still holds the old key), the gateway starts 401ing —
// and the client must recover WITHOUT any runtime rebuild: SendWithRetry forces
// one credential re-read inside the existing auth budget, and the very next
// attempt carries the fresh key.
func TestStream401ReauthSelfHeals(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	var auths []string
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		switch {
		case n == 1: // first send authenticates — arms RetryAuth for the client
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		case n <= 3: // the rotated-in key rejects the stale cached one (twice: the retry right after the forced re-read and the last budget attempt would both land here in a live rotation)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"token expired"}}`)
		default: // fresh key accepted
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		}
	}))
	defer srv.Close()

	rotated := false
	sourceReads := 0
	p, err := New(provider.Config{
		Name: "mimo", BaseURL: srv.URL, Model: "mimo-v2.6-flash", APIKey: "stale-token",
		Extra: map[string]any{
			"request_url": srv.URL,
			"reauth": func() (string, bool) {
				mu.Lock()
				defer mu.Unlock()
				sourceReads++
				if !rotated {
					return "stale-token", true // source unchanged until the rotation
				}
				return "fresh-token", true // the source rotated behind the runtime
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)

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

	// The credential source rotates behind the runtime; the cached key goes stale.
	mu.Lock()
	rotated = true
	mu.Unlock()

	// Second send: stale key → 401 → forced re-read → fresh key → success.
	drain("after rotation")

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"Bearer stale-token", // first send, authenticates
		"Bearer stale-token", // rotation: 401 on the cached key
		"Bearer fresh-token", // retry after the forced re-read already carries the new key
		"Bearer fresh-token", // accepted
	}
	if len(auths) != len(want) {
		t.Fatalf("wire requests = %d (%v), want %d", len(auths), auths, len(want))
	}
	for i := range want {
		if auths[i] != want[i] {
			t.Fatalf("request %d Authorization = %q, want %q (full: %v)", i+1, auths[i], want[i], auths)
		}
	}
	if sourceReads != 2 {
		t.Fatalf("credential source reads = %d, want 2 (stream-1 proactive check + the forced re-auth)", sourceReads)
	}
}

// TestProactiveCredentialRefreshThrottled pins 任务1308 ③: the proactive check
// runs at most once per CredentialRefreshInterval — a long-lived session picks
// up a rotated credential on its next send after the interval, without waiting
// for a 401, and does not touch the credential file on every send.
func TestProactiveCredentialRefreshThrottled(t *testing.T) {
	old := provider.CredentialRefreshInterval
	provider.CredentialRefreshInterval = time.Hour
	t.Cleanup(func() { provider.CredentialRefreshInterval = old })

	var mu sync.Mutex
	sourceReads := 0
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()

	p, err := New(provider.Config{
		Name: "mimo", BaseURL: srv.URL, Model: "mimo-v2.6-flash", APIKey: "k",
		Extra: map[string]any{
			"request_url": srv.URL,
			"reauth": func() (string, bool) {
				mu.Lock()
				defer mu.Unlock()
				sourceReads++
				return "k", true // unchanged — only the read is counted
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c := p.(*client)

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ch, err := c.Stream(ctx, provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		for range ch {
		}
		cancel()
	}
	mu.Lock()
	defer mu.Unlock()
	if sourceReads != 1 {
		t.Fatalf("source reads across two back-to-back sends = %d, want 1 (interval throttle)", sourceReads)
	}
}
