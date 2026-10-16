package agent

// 任务912 P0-a: compaction must survive the failure shapes a fast retry cannot
// fix (upstream cancel / expired key / rate limit) by walking a slow backoff
// ladder instead of parking the pass on its first appearance — and, once the
// ladder is spent, close the pass with the task-867 failed card.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// captureSlogFor912 mirrors captureSlogForRateLimit for the backoff tests.
func captureSlogFor912(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// 验收①: the production schedule is exactly 30s → 2m → 5m, three steps. Pinned
// against the default (not the test-shrunk var), so a silent schedule change
// fails here rather than only in wall-clock behaviour.
func TestTask912BackoffScheduleIsThirtySecondsTwoMinutesFiveMinutes(t *testing.T) {
	got := defaultSummaryBackoffSteps()
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute}
	if len(got) != len(want) {
		t.Fatalf("backoff schedule = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff step %d = %v, want %v (full schedule %v)", i, got[i], want[i], got)
		}
	}
	// The package var is the production schedule until a test shrinks it.
	if len(summaryBackoffSteps) != 3 {
		t.Fatalf("live summaryBackoffSteps = %v, want the 3-step production ladder", summaryBackoffSteps)
	}
}

// The classifier is exactly the three resilient shapes: an expired key, an
// upstream/inner cancel, and a rate limit. Semantic rejections stay terminal —
// a backoff never fixes a bad input.
func TestTask912ResilientClassifierShapes(t *testing.T) {
	resilient := []string{
		`authentication failed for provider "MiMo TP · Chat Completions" (HTTP 401): REASONIX_CONNECTION_ABC_KEY from Reasonix credentials (.env) is invalid or expired — update it (in .env or your environment) and retry, or run ` + "`reasonix setup`",
		"context canceled",
		"upstream cancelled the stream",
		"mimo-token-plan: Request rate limited",
	}
	for _, msg := range resilient {
		if !summaryResilientRetryable(errors.New(msg)) {
			t.Errorf("summaryResilientRetryable(%q) = false, want true", msg)
		}
	}
	terminal := []string{
		"context limit exceeded: fold too large",
		"model refused: content policy",
		"status 400: {\"model\":\"deepseek-v4-flash\"}",
		"mimo-api: read stream: stream error: stream ID 5; INTERNAL_ERROR",
	}
	for _, msg := range terminal {
		if summaryResilientRetryable(errors.New(msg)) {
			t.Errorf("summaryResilientRetryable(%q) = true, want false", msg)
		}
	}
	if summaryResilientRetryable(nil) {
		t.Error("summaryResilientRetryable(nil) must be false")
	}
	// A typed 401 is the key face; a 403 is not (a refusal never heals by
	// waiting).
	if !summaryAuthExpired(&provider.AuthError{Status: 401}) {
		t.Error("a typed 401 AuthError must classify as an expired key")
	}
	if summaryAuthExpired(&provider.AuthError{Status: 403}) {
		t.Error("a typed 403 must stay terminal — waiting cannot grant entitlement")
	}
}

// 验收①: an expired key is retried on the ladder, and the retry SUCCEEDS — the
// pass no longer dies on the first 401 (the 14:55:05 log sample).
func TestTask912ExpiredKeyBacksOffThenSucceeds(t *testing.T) {
	buf := captureSlogFor912(t)
	shrinkSummaryBackoff(t)

	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{
		reply: "recovered digest",
		streamErrs: []error{errors.New(
			`authentication failed for provider "MiMo TP · Chat Completions" (HTTP 401): REASONIX_CONNECTION_ABC_KEY from Reasonix credentials (.env) is invalid or expired`)},
	}, sess)

	res, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err != nil {
		t.Fatalf("summarizeFold = %v, want the ladder to carry the 401 to a success", err)
	}
	if !strings.Contains(res.Text, "recovered digest") {
		t.Fatalf("summary text = %q, want the post-backoff reply", res.Text)
	}
	out := buf.String()
	if got := strings.Count(out, "summary request backing off before retry"); got != 1 {
		t.Fatalf("backoff lines = %d, want exactly 1; slog:%s", got, out)
	}
	if !strings.Contains(out, "summary request succeeded after backoff retry") {
		t.Fatalf("no backoff-success line; slog:%s", out)
	}
	if !strings.Contains(out, "auth_expired=true") {
		t.Fatalf("backoff line does not name the expired key; slog:%s", out)
	}
}

// 验收②: the ladder logs its three steps in order and then hands the failure
// to the task-867 failed card (CompactionDone outcome=failed) instead of a
// silent park.
func TestTask912ExhaustedBackoffEmitsFailedCardWithStepSequence(t *testing.T) {
	buf := captureSlogFor912(t)
	shrinkSummaryBackoff(t)

	sess := foldableSessionOverForce(6)
	a := agentOverForce(t, &fakeProvider{streamErrs: []error{
		errors.New("context canceled"),
		errors.New("context canceled"),
		errors.New("context canceled"),
		errors.New("context canceled"),
	}}, sess)
	var failed []event.Compaction
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.CompactionDone && e.Compaction.Outcome == "failed" {
			failed = append(failed, e.Compaction)
		}
	})

	_ = prepareContext(context.Background(), a, CompactionTriggerPressure)

	if len(failed) != 1 {
		t.Fatalf("failed outcomes = %d, want exactly one visible failure card", len(failed))
	}
	if failed[0].Attempt != 1 {
		t.Fatalf("failed card attempt = %d, want 1", failed[0].Attempt)
	}
	out := buf.String()
	for _, want := range []string{"backoff_step=1", "backoff_step=2", "backoff_step=3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in the backoff sequence; slog:%s", want, out)
		}
	}
	if got := strings.Count(out, "summary request backing off before retry"); got != len(summaryBackoffSteps) {
		t.Fatalf("backoff lines = %d, want exactly %d (ladder length); slog:%s", got, len(summaryBackoffSteps), out)
	}
	if !strings.Contains(out, "summary request failed") {
		t.Fatalf("no terminal failure line after the ladder; slog:%s", out)
	}
}

// A cancel of THIS request's context is the turn ending, never a retry: the
// ladder must not sleep, and the error must come straight back.
func TestTask912OwnContextCancelSkipsTheLadder(t *testing.T) {
	buf := captureSlogFor912(t)
	shrinkSummaryBackoff(t)

	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{hang: true}, sess)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := a.summarizeFold(ctx, CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("summarizeFold err = %v, want context.Canceled handed straight back", err)
	}
	if got := strings.Count(buf.String(), "summary request backing off before retry"); got != 0 {
		t.Fatalf("backoff lines = %d, want 0 — a dead context must never be waited on", got)
	}
}
