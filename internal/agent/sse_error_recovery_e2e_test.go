package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/provider/anthropic"
)

// TestSSEErrorEntersStreamingRecovery pins task 273's acceptance 1 at the
// agent level: an SSE error frame (mimo INTERNAL_ERROR shape) must surface as
// a stream interruption so the first response enters the sampling-recovery
// loop (EventKind=Retrying) and the turn completes on the next attempt —
// instead of terminating the turn and waiting for a manual resend.
func TestSSEErrorEntersStreamingRecovery(t *testing.T) {
	errorSSE := `event: message_start
data: {"type":"message_start","message":{"id":"msg_err","usage":{"input_tokens":5}}}

event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}
`
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		response := finalAnswerSSE
		if n == 1 {
			response = errorSSE
		}
		_, _ = io.WriteString(w, response)
	}))
	defer srv.Close()

	p, err := anthropic.New(provider.Config{
		Name: "custom-anthropic", BaseURL: srv.URL,
		Model: "deepseek-v4-flash", APIKey: "fake-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	a := New(p, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: t.TempDir()}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("run must recover from the SSE error, got: %v", err)
	}

	mu.Lock()
	requests := len(bodies)
	mu.Unlock()
	if requests < 2 {
		t.Fatalf("HTTP=%d, want at least one recovery attempt after the error frame", requests)
	}
	if len(sink.kinds(event.Retrying)) == 0 {
		t.Fatal("first response must enter sampling recovery (EventKind=Retrying)")
	}
}
