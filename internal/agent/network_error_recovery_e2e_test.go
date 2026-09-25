package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/provider/anthropic"
	"reasonix/internal/tool"
)

// Task 317 (batch 7 F2): network-error recovery v2. These tests pin the
// acceptance contract that task 273's stream-interrupt classification left
// open for the header/connection phase forms: a 5xx or connection failure at
// request start must (a) enter the sampling-recovery loop and complete the
// turn without a manual second compaction, (b) leave a searchable slog trail
// carrying the per-attempt cache surface, and (c) end honestly — bounded,
// with an exhaustion line — when the outage persists. The slog assertions are
// the "recovery trail" and "error-turn cache surface" acceptances (V1/V3);
// the retry path previously emitted events only, never slog.

// syncBuffer makes a slog destination safe for concurrent writers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureSlog redirects the default logger for one test and restores it.
func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(before) })
	return buf
}

// requireRecoveryTrail pins the shared V1/V3 shape: every logged failed
// attempt must carry the cache surface (hit/miss tokens plus an explicit
// usage_present flag so the zero-usage error round — cache-查证 candidate A —
// is distinguishable from a measured hit=0).
func requireRecoveryTrail(t *testing.T, trail, wantPhase string, minLines int) {
	t.Helper()
	var matched []string
	for _, line := range strings.Split(trail, "\n") {
		if strings.Contains(line, "sampling attempt failed") {
			matched = append(matched, line)
		}
	}
	if len(matched) < minLines {
		t.Fatalf("recovery trail lines = %d (want >= %d)\ntrail:\n%s", len(matched), minLines, trail)
	}
	for _, line := range matched {
		for _, field := range []string{"cache_hit_tokens=", "cache_miss_tokens=", "usage_present="} {
			if !strings.Contains(line, field) {
				t.Fatalf("recovery trail line missing %s:\n%s", field, line)
			}
		}
		if wantPhase != "" && !strings.Contains(line, "phase="+wantPhase) {
			t.Fatalf("recovery trail line missing phase=%s:\n%s", wantPhase, line)
		}
	}
}

// TestNetwork5xxAutoRecoversWithSlogTrail (V1a): the observed mimo shape —
// HTTP 500 at request start — must recover inside the sampling loop once the
// provider recovers, with a slog trail per failed attempt: no manual second
// compaction, no turn failure.
func TestNetwork5xxAutoRecoversWithSlogTrail(t *testing.T) {
	var hit5xx atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if hit5xx.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "Internal Server Error")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, finalAnswerSSE)
	}))
	defer srv.Close()

	p, err := anthropic.New(provider.Config{
		Name: "custom-anthropic", BaseURL: srv.URL,
		Model: "deepseek-v4-flash", APIKey: "fake-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	trail := captureSlog(t)
	sink := &recordSink{}
	a := New(p, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: t.TempDir()}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("5xx start must recover automatically, got: %v", err)
	}
	if len(sink.kinds(event.Retrying)) == 0 {
		t.Fatal("header-phase 5xx must enter sampling recovery (EventKind=Retrying)")
	}
	requireRecoveryTrail(t, trail.String(), "headers", 1)
}

// TestConnectFailureAutoRecoversWithSlogTrail (V1b): connection-establishment
// failures (dial refused — ClassifyRecovery phase "connect") are the second
// boundary-outside form named by the task; they must recover the same way,
// with the same trail.
func TestConnectFailureAutoRecoversWithSlogTrail(t *testing.T) {
	mp := testutil.NewMock("netflaky",
		testutil.Turn{StreamError: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}},
		testutil.Turn{StreamError: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}},
		testutil.Turn{Text: "recovered", Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, CacheHitTokens: 7, CacheMissTokens: 3}},
	)
	trail := captureSlog(t)
	sink := &recordSink{}
	a := New(mp, echoRegistry(), NewSession(""), Options{}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("connect failure must recover automatically, got: %v", err)
	}
	if got := mp.CallCount(); got != 3 {
		t.Fatalf("calls=%d, want 3 (2 failures + 1 success)", got)
	}
	if len(sink.kinds(event.Retrying)) == 0 {
		t.Fatal("connect failure must enter sampling recovery (EventKind=Retrying)")
	}
	requireRecoveryTrail(t, trail.String(), "connect", 2)
}

// TestPersistent5xxExhaustsHonestlyWithSlogTrail (V1c): an outage that never
// clears must still end the turn honestly — RecoveryWaitExhaustedError, a
// bounded number of provider calls, and a slog exhaustion line — never loop.
func TestPersistent5xxExhaustsHonestlyWithSlogTrail(t *testing.T) {
	p := &transientHeaderProvider{}
	oldBudget := recoveryWaitBudget
	defer func() { recoveryWaitBudget = oldBudget }()
	recoveryWaitBudget = 3 * time.Minute
	trail := captureSlog(t)
	sink := &recordSink{}
	a := New(p, echoRegistry(), NewSession(""), Options{}, sink)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	var exhausted *provider.RecoveryWaitExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("persistent 5xx must end honestly with RecoveryWaitExhaustedError, got: %v", err)
	}
	if p.calls == 0 || p.calls > maxSamplingAttempts+2 {
		t.Fatalf("calls=%d must be bounded (maxSamplingAttempts=%d)", p.calls, maxSamplingAttempts)
	}
	out := trail.String()
	if !strings.Contains(out, "recovery wait budget exhausted") {
		t.Fatalf("exhaustion must be slogged:\n%s", out)
	}
	requireRecoveryTrail(t, out, "headers", 1)
}

// TestCompactionMissAttributionAfterError (V2): a zero-hit compaction round
// must name its cause instead of leaving a bare hit=0 — the error→compact gap
// against the TTL estimate, the zero-usage write gap, or the prefix/model
// shape family — and the attribution line must actually reach slog (the
// cache-查证 candidates were previously analysis-only).
func TestCompactionMissAttributionAfterError(t *testing.T) {
	withUsage := CompactionTelemetry{InputTokens: 600_000, RequestCount: 1}
	cases := []struct {
		name     string
		tele     CompactionTelemetry
		gap      time.Duration
		hadError bool
		want     string
	}{
		{"zero usage round", CompactionTelemetry{}, 0, false, "zero_usage_no_request"},
		{"no recent error", withUsage, 0, false, "prefix_or_model_shape"},
		{"gap beyond ttl", withUsage, 10 * time.Minute, true, "error_gap_over_ttl"},
		{"gap inside ttl", withUsage, 30 * time.Second, true, "error_gap_within_ttl"},
	}
	for _, tc := range cases {
		if got := compactionMissAttribution(tc.tele, tc.gap, tc.hadError); got != tc.want {
			t.Errorf("%s: attribution = %q, want %q", tc.name, got, tc.want)
		}
	}

	// End-to-end: a real compaction round measuring hit=0, with a provider
	// error 10 minutes old, must emit the attribution line to slog.
	large := strings.Repeat("old tool output ", 160)
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "system stays"},
		{Role: provider.RoleUser, Content: "old request alpha"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("analysis ", 160)},
		{Role: provider.RoleTool, ToolCallID: "read-1", Name: "read_file", Content: large},
		{Role: provider.RoleUser, Content: "unique boundary request"},
		{Role: provider.RoleAssistant, Content: "tail stays byte-for-byte"},
	}}
	prov := &fakeProvider{reply: "old work summarized", promptTokens: 5000}
	a := New(prov, tool.NewRegistry(), sess, Options{ArchiveDir: t.TempDir()}, event.Discard)
	a.sess.lastProviderErrorAt.Store(time.Now().Add(-10 * time.Minute).UnixMilli())
	trail := captureSlog(t)
	if _, err := a.CompressContext(context.Background(), tool.CompressRequest{
		Direction: "before", Anchor: "unique boundary", Focus: "keep decisions",
	}); err != nil {
		t.Fatalf("CompressContext: %v", err)
	}
	out := trail.String()
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "compaction cache miss attribution") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("zero-hit compaction must emit the miss attribution line:\n%s", out)
	}
	for _, field := range []string{"attribution=error_gap_over_ttl", "error_gap_ms=", "model_ref="} {
		if !strings.Contains(line, field) {
			t.Fatalf("attribution line missing %s:\n%s", field, line)
		}
	}
}
