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
	"strconv"
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
// cache-查证 candidates were previously analysis-only). tl1640 extends the
// table with the no-error TTL branch: a stale conversation stamp now reads
// prefix_ttl_expired (EST threshold), an unknown stamp stays with the shape
// family, and the error branches keep precedence regardless of turn data.
func TestCompactionMissAttributionAfterError(t *testing.T) {
	withUsage := CompactionTelemetry{InputTokens: 600_000, RequestCount: 1}
	cases := []struct {
		name     string
		tele     CompactionTelemetry
		gap      time.Duration
		hadError bool
		turnGap  time.Duration
		haveTurn bool
		want     string
	}{
		{"zero usage round", CompactionTelemetry{}, 0, false, 0, false, "zero_usage_no_request"},
		// Pre-tl1640 world: no conversation stamp at all — unknown gap must
		// not claim TTL, the shape family keeps it.
		{"no recent error", withUsage, 0, false, 0, false, "prefix_or_model_shape"},
		// tl1640 branch 1: stale conversation turn (the 13:04 case's 30.3min),
		// no provider error — the blind-spot label.
		{"no error, turn gap beyond ttl", withUsage, 0, false, 30*time.Minute + 21*time.Second, true, "prefix_ttl_expired"},
		// tl1640 branch 2: recent turn inside the window — the existing
		// attribution stands.
		{"no error, turn gap inside ttl", withUsage, 0, false, time.Minute, true, "prefix_or_model_shape"},
		// Error branches keep precedence: stale turn data on the side does not
		// reroute an error round.
		{"gap beyond ttl", withUsage, 10 * time.Minute, true, 30*time.Minute + 21*time.Second, true, "error_gap_over_ttl"},
		{"gap inside ttl", withUsage, 30 * time.Second, true, time.Minute, true, "error_gap_within_ttl"},
	}
	for _, tc := range cases {
		if got := compactionMissAttribution(tc.tele, tc.gap, tc.hadError, tc.turnGap, tc.haveTurn); got != tc.want {
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
	for _, field := range []string{"attribution=error_gap_over_ttl", "error_gap_ms=", "model_ref=",
		// tl1640: the same line carries the conversation gap; this scenario
		// never completed a conversation request, so the field reads unknown(-1).
		"last_turn_gap_min=-1"} {
		if !strings.Contains(line, field) {
			t.Fatalf("attribution line missing %s:\n%s", field, line)
		}
	}
}

// TestLastTurnGapMinutesFieldConvention (tl1640 O2): the conversation-gap
// value both "compaction done" lines carry follows the established unknown
// convention (-1, like error_gap_ms) and reads in MINUTES off the same stamp
// the prefix_ttl_expired attribution uses — the 13:04 case's 30.3min must
// surface as ~30.3, not seconds or a silently-zero unknown.
func TestLastTurnGapMinutesFieldConvention(t *testing.T) {
	prov := &fakeProvider{reply: "x", promptTokens: 10}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{ArchiveDir: t.TempDir()}, event.Discard)
	if got := a.lastTurnGapMinutes(); got != -1 {
		t.Fatalf("no conversation request yet: last_turn_gap_min = %v, want -1 (unknown)", got)
	}
	a.sess.lastConversationRequestAt.Store(time.Now().Add(-30*time.Minute - 21*time.Second).UnixMilli())
	if got := a.lastTurnGapMinutes(); got < 30.3 || got > 31.5 {
		t.Fatalf("30.3min-old stamp: last_turn_gap_min = %v, want ~30.3 (clock jitter tolerant)", got)
	}
}

// attributionLineFrom extracts the compaction cache miss attribution line
// from a captured slog trail (shared by the tl1640 replay pins below).
func attributionLineFrom(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "compaction cache miss attribution") {
			return l
		}
	}
	t.Fatalf("zero-hit compaction must emit the miss attribution line:\n%s", out)
	return ""
}

// attributionTurnGapMin parses last_turn_gap_min off an attribution line.
func attributionTurnGapMin(t *testing.T, line string) float64 {
	t.Helper()
	const key = "last_turn_gap_min="
	i := strings.Index(line, key)
	if i < 0 {
		t.Fatalf("attribution line missing %s:\n%s", key, line)
	}
	rest := line[i+len(key):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		rest = rest[:j]
	}
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		t.Fatalf("last_turn_gap_min = %q is not a number:\n%s", rest, line)
	}
	return v
}

// tl1640ReplaySession builds the same quiet-session shape the existing
// attribution pin uses — a compactable transcript whose summary round
// measures zero cache hit on the fake provider.
func tl1640ReplaySession() *Session {
	large := strings.Repeat("old tool output ", 160)
	return &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "system stays"},
		{Role: provider.RoleUser, Content: "old request alpha"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("analysis ", 160)},
		{Role: provider.RoleTool, ToolCallID: "read-1", Name: "read_file", Content: large},
		{Role: provider.RoleUser, Content: "unique boundary request"},
		{Role: provider.RoleAssistant, Content: "tail stays byte-for-byte"},
	}}
}

// TestCompactionMissAttributionPrefixTTLExpiredReplay (tl1640 案例回放): the
// 20261116 13:04 incident — a quiet session, NO provider error, the last
// conversation turn 30.3 minutes old (13:04:59 − 12:34:38), compaction lands
// hit=0. The old labels had nowhere to put this: no error meant the shape
// family, which read as "TTL ruled out" and was misjudged unreasonable. The
// replay must mark prefix_ttl_expired and surface the 30.3min gap on the
// line. EST semantics: the 5min threshold is the industry lower bound — the
// label is the leading inference, not a measured provider verdict.
func TestCompactionMissAttributionPrefixTTLExpiredReplay(t *testing.T) {
	prov := &fakeProvider{reply: "old work summarized", promptTokens: 5000}
	a := New(prov, tool.NewRegistry(), tl1640ReplaySession(), Options{ArchiveDir: t.TempDir()}, event.Discard)
	// No provider error ever recorded this session — the O1 branch's lane.
	a.sess.lastConversationRequestAt.Store(time.Now().Add(-30*time.Minute - 21*time.Second).UnixMilli())
	trail := captureSlog(t)
	if _, err := a.CompressContext(context.Background(), tool.CompressRequest{
		Direction: "before", Anchor: "unique boundary", Focus: "keep decisions",
	}); err != nil {
		t.Fatalf("CompressContext: %v", err)
	}
	line := attributionLineFrom(t, trail.String())
	if !strings.Contains(line, "attribution=prefix_ttl_expired") {
		t.Fatalf("30.3min-quiet no-error compaction must read prefix_ttl_expired:\n%s", line)
	}
	if got := attributionTurnGapMin(t, line); got < 30.3 || got > 31.5 {
		t.Fatalf("last_turn_gap_min = %v, want ~30.3 (clock jitter tolerant):\n%s", got, line)
	}
}

// TestCompactionMissAttributionShortGapKeepsShapeFamily (tl1640 对照案例):
// the same quiet-session compaction with the last conversation turn INSIDE
// the TTL window keeps the existing prefix_or_model_shape attribution — the
// new branch only claims TTL expiry when the gap actually exceeds the
// estimate, so short-interval misses still point at the shape family.
func TestCompactionMissAttributionShortGapKeepsShapeFamily(t *testing.T) {
	prov := &fakeProvider{reply: "old work summarized", promptTokens: 5000}
	a := New(prov, tool.NewRegistry(), tl1640ReplaySession(), Options{ArchiveDir: t.TempDir()}, event.Discard)
	a.sess.lastConversationRequestAt.Store(time.Now().Add(-1 * time.Minute).UnixMilli())
	trail := captureSlog(t)
	if _, err := a.CompressContext(context.Background(), tool.CompressRequest{
		Direction: "before", Anchor: "unique boundary", Focus: "keep decisions",
	}); err != nil {
		t.Fatalf("CompressContext: %v", err)
	}
	line := attributionLineFrom(t, trail.String())
	if !strings.Contains(line, "attribution=prefix_or_model_shape") {
		t.Fatalf("short-gap compaction must keep prefix_or_model_shape:\n%s", line)
	}
	if got := attributionTurnGapMin(t, line); got < 1.0 || got > 2.0 {
		t.Fatalf("last_turn_gap_min = %v, want ~1.0 (clock jitter tolerant):\n%s", got, line)
	}
}
