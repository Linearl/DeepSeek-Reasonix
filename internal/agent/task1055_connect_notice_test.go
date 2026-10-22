package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/provider/openai"
	"reasonix/internal/tool"
)

// 任务1055-A1/A3 acceptance, agent level: while the request is still fighting
// the network, the run strip must already say so. Driven end-to-end through
// the real OpenAI-compatible provider + SendWithRetry path (the same shape as
// TestAgentEmitsRetryingThenStreams): first request 503s, second streams.
//
//  ① 发起即见: a Connecting event (scope "connect", ordinal ≥ 1) is emitted
//     BEFORE the first Retrying event — the connecting status does not wait
//     for a failure.
//  ② ordinal increments: the second attempt's Connecting carries ordinal 2
//     (the request counter is shared across sampling attempts).
//  ③ A3 exclusivity: every in-flight Connecting frame is closed by the
//     zero-attempt clear frame the moment headers arrive (or the start
//     fails) — the connect label can never name the header-wait or prefill
//     silence.
func TestAgentEmitsConnectingBeforeRetryAndClearsOnStart(t *testing.T) {
	var reqs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs++
		if reqs == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"overloaded"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi there\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	prov, err := openai.New(provider.Config{Name: "deepseek", BaseURL: srv.URL, Model: "deepseek-v4", APIKey: "k"})
	if err != nil {
		t.Fatalf("New provider: %v", err)
	}

	sink := &recordSink{}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{}, sink)
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	connects := sink.kinds(event.Connecting)
	if len(connects) < 4 {
		t.Fatalf("Connecting events = %d, want >= 4 (attempt 1 + clear + attempt 2 + clear), got %+v", len(connects), connects)
	}
	if connects[0].RetryAttempt != 1 || connects[0].RetryScope != event.RetryScopeConnect {
		t.Fatalf("first Connecting = attempt %d scope %q, want attempt 1 scope %q", connects[0].RetryAttempt, connects[0].RetryScope, event.RetryScopeConnect)
	}
	// Ordinals increase across sampling attempts; every attempt frame is
	// followed by the zero-attempt clear.
	for i, e := range connects {
		if e.RetryScope != event.RetryScopeConnect {
			t.Errorf("Connecting[%d] scope = %q, want %q", i, e.RetryScope, event.RetryScopeConnect)
		}
		if i%2 == 1 && e.RetryAttempt != 0 {
			t.Errorf("Connecting[%d] = attempt %d, want the zero-attempt clear frame", i, e.RetryAttempt)
		}
	}
	if connects[1].RetryAttempt != 0 {
		t.Fatalf("Connecting[1] = attempt %d, want the clear after attempt 1", connects[1].RetryAttempt)
	}
	if connects[2].RetryAttempt != 2 {
		t.Fatalf("Connecting[2] = attempt %d, want ordinal 2 on the second attempt", connects[2].RetryAttempt)
	}

	// 发起即见: the first Connecting precedes the first Retrying in the
	// emitted order — visibility does not wait for the first failure.
	retries := sink.kinds(event.Retrying)
	if len(retries) == 0 {
		t.Fatal("want at least one Retrying event from the sampling recovery loop")
	}
	firstConnect, firstRetry := -1, -1
	for i, e := range sink.evs {
		if firstConnect < 0 && e.Kind == event.Connecting {
			firstConnect = i
		}
		if firstRetry < 0 && e.Kind == event.Retrying {
			firstRetry = i
		}
	}
	if firstConnect < 0 || firstRetry < 0 || firstConnect > firstRetry {
		t.Fatalf("event order: first Connecting at %d, first Retrying at %d — connecting must precede retrying", firstConnect, firstRetry)
	}

	var answer strings.Builder
	for _, e := range sink.kinds(event.Text) {
		answer.WriteString(e.Text)
	}
	if !strings.Contains(answer.String(), "hi there") {
		t.Errorf("streamed answer = %q, want it to contain %q", answer.String(), "hi there")
	}
}
