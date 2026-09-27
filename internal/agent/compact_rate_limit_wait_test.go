package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captureSlogForRateLimit redirects the default logger into a buffer for the duration of
// the test (these tests are not parallel) and returns the buffer.
func captureSlogForRateLimit(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// shrinkRateLimitWait makes the 429 lane's wait measurable in a test run.
func shrinkRateLimitWait(t *testing.T) {
	t.Helper()
	old := summaryRateLimitWaitDefault
	summaryRateLimitWaitDefault = 20 * time.Millisecond
	t.Cleanup(func() { summaryRateLimitWaitDefault = old })
}

// Task 330: the rate-limit classifier and wait picker are the two judgment
// surfaces of the 429 lane - shape recognition (status text vs message) and
// Retry-After parsing with a default and a cap.
func TestSummaryRateLimitedShapesAndWaits(t *testing.T) {
	if !summaryRateLimited(errors.New("provider returned status 429 too many requests")) {
		t.Fatal("status-429 text not classified as rate limited")
	}
	if !summaryRateLimited(errors.New("Error: rate limit exceeded, retry later")) {
		t.Fatal("rate-limit message not classified as rate limited")
	}
	if summaryRateLimited(errors.New("stream error: connection reset by peer")) {
		t.Fatal("transport error misclassified as rate limited")
	}
	if summaryRateLimited(nil) {
		t.Fatal("nil error classified as rate limited")
	}

	if got := summaryRateLimitWait(errors.New("status 429, retry-after: 42")); got != 42*time.Second {
		t.Fatalf("Retry-After 42 => %v, want 42s", got)
	}
	if got := summaryRateLimitWait(errors.New("status 429, retry after 9999")); got != summaryRateLimitWaitCap {
		t.Fatalf("huge Retry-After => %v, want the %v cap", got, summaryRateLimitWaitCap)
	}
	if got := summaryRateLimitWait(errors.New("status 429 too many")); got != summaryRateLimitWaitDefault {
		t.Fatalf("no Retry-After => %v, want the default %v", got, summaryRateLimitWaitDefault)
	}
}

// The main path: the first summary attempt is rate limited, the lane waits
// (logged with its duration) and resumes the identical request once; the
// success log names the rate-limit resume, not the generic transient one.
// A 429 must not consume the 303 short backoff: it has its own lane.
func TestSummarizeFoldWaitsOutRateLimitAndResumes(t *testing.T) {
	buf := captureSlogForRateLimit(t)
	shrinkRateLimitWait(t)

	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{
		reply:      "consolidated summary",
		streamErrs: []error{errors.New("provider returned status 429 rate limit exceeded")},
	}, sess)

	res, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err != nil {
		t.Fatalf("summarizeFold = %v, want the resume after the wait to succeed", err)
	}
	if !strings.Contains(res.Text, "consolidated summary") {
		t.Fatalf("summary text = %q, want the resumed reply", res.Text)
	}
	out := buf.String()
	waitLine := strings.Count(out, "summary rate limited — waiting to resume")
	resumeLine := strings.Count(out, "summary request resumed after rate-limit wait")
	if waitLine != 1 {
		t.Fatalf("waiting-to-resume lines = %d, want exactly 1; slog:%s", waitLine, out)
	}
	if resumeLine != 1 {
		t.Fatalf("resumed-after-wait lines = %d, want exactly 1; slog:%s", resumeLine, out)
	}
	if strings.Contains(out, "succeeded after transient retry") {
		t.Fatalf("429 took the generic transient lane; slog:%s", out)
	}
}

// A persistent limit (both attempts 429) must wait once, then fail through -
// no unbounded waiting, and the failure line still ships the rate_limited
// flag for the acceptance readout.
func TestSummarizeFoldRateLimitGivesUpAfterOneWait(t *testing.T) {
	buf := captureSlogForRateLimit(t)
	shrinkRateLimitWait(t)

	sess := foldableSessionOverForce(3)
	rl := errors.New("provider returned status 429 rate limit exceeded")
	a := agentOverForce(t, &fakeProvider{
		streamErrs: []error{rl, rl},
	}, sess)

	_, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err == nil {
		t.Fatal("summarizeFold = nil, want the persistent 429 to surface")
	}
	out := buf.String()
	if got := strings.Count(out, "summary rate limited — waiting to resume"); got != 1 {
		t.Fatalf("waiting lines = %d, want exactly 1 (one wait, no loop); slog:%s", got, out)
	}
	if !strings.Contains(out, "summary request failed") {
		t.Fatalf("no terminal failure line; slog:%s", out)
	}
}

// Mid-chain (segment level): a 429 on one fragment waits that fragment out
// and resumes it - the sibling fragments already summarized are not redone,
// which is what distinguishes this from restarting the whole chain.
func TestSegmentRateLimitWaitsAndResumes(t *testing.T) {
	buf := captureSlogForRateLimit(t)
	shrinkRateLimitWait(t)

	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{
		reply:      "fragment digest",
		streamErrs: []error{errors.New("Error: rate limit hit, retry after 1")},
	}, sess)

	run := newChunkedSummaryRun(a)
	got, err := run.summarize(context.Background(), sess.Messages, "", 0)
	if err != nil {
		t.Fatalf("run.summarize = %v, want the fragment to resume after the wait", err)
	}
	if !strings.Contains(got.Text, "fragment digest") {
		t.Fatalf("fragment text = %q, want the resumed reply", got.Text)
	}
	out := buf.String()
	if !strings.Contains(out, "summary fragment rate limited — waiting to resume") {
		t.Fatalf("no fragment waiting line; slog:%s", out)
	}
	if !strings.Contains(out, "summary fragment resumed after rate-limit wait") {
		t.Fatalf("no fragment resumed line; slog:%s", out)
	}
	if run.calls != 2 {
		t.Fatalf("run.calls = %d, want 2 (initial + resumed, budget-accounted)", run.calls)
	}
}

// Retry-After on the segment lane is honored from the error text too.
func TestSegmentRateLimitRetryAfterParsing(t *testing.T) {
	shrinkRateLimitWait(t)
	if got := summaryRateLimitWait(errors.New("429: retry-after: 7")); got != 7*time.Second {
		t.Fatalf("segment Retry-After => %v, want 7s", got)
	}
}

// boundary note (task 330 acceptance 4): the binary split lives inside
// summarizeFold's own request; when it converges the function returns err
// == nil, so the task-307 ceiling branch in summaryFailed never sees it.
// The 429 lane above likewise returns (res, nil) on resume - both "good
// candidate" paths walk the normal return, and only a real error reaches
// summaryFailed. This test pins the resume path's nil error as that boundary.
func TestRateLimitResumeReturnsNilErrorForNormalPath(t *testing.T) {
	shrinkRateLimitWait(t)
	sess := foldableSessionOverForce(3)
	a := agentOverForce(t, &fakeProvider{
		reply:      "ok",
		streamErrs: []error{errors.New("status 429")},
	}, sess)
	_, _, err := a.summarizeFold(context.Background(), CompactionTriggerPressure,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err != nil {
		t.Fatalf("resumed summarizeFold err = %v, want nil (normal path to the caller)", err)
	}
}

// Task 196fix note (2): the reuse guard must see one physical file behind
// the two raw case forms the logs show (C-Users vs c-users).
func TestSameSessionLogPath(t *testing.T) {
	upper := "C:/Users/yinji/AppData/Roaming/reasonix/projects/C--Users-x/sessions/a.jsonl"
	lower := "c:/users/yinji/appdata/roaming/reasonix/projects/c--users-x/sessions/a.jsonl"
	if !sameSessionLogPath(upper, upper) {
		t.Fatal("identical paths must match")
	}
	if !sameSessionLogPath(upper, lower) {
		t.Fatal("the two logged case forms of one physical file must match")
	}
	if sameSessionLogPath(upper, upper+"-other.jsonl") {
		t.Fatal("different files must not match")
	}
	if sameSessionLogPath("", "") {
		t.Fatal("two empty paths must not claim a physical file")
	}
	if canonicalSessionSavePath(upper) == "" {
		t.Fatal("canonical form of a real path is empty")
	}
	_ = lower
}
