package agent

import (
	"errors"
	"testing"
	"time"
)

// 任务912 P0-b（采样侧）: the status-less gateway rate limit ("mimo-token-plan:
// Request rate limited") used to classify KindUnknown and spend the 8/15min
// storm budget; task 845 made a 429 a budget-exempt queue and the text face is
// the same shape.
func TestTask912TextRateLimitIsKindRateLimitAndBudgetExempt(t *testing.T) {
	const msg = "mimo-token-plan: Request rate limited"
	if got := classifyRetryKind(msg); got != KindRateLimit {
		t.Fatalf("classifyRetryKind(%q) = %q, want %q", msg, got, KindRateLimit)
	}
	b := NewRetryBudget()
	now := time.Now()
	ok, kind, why := b.Allow("unknown", errors.New(msg), now)
	if !ok {
		t.Fatalf("Allow for a text rate limit refused: kind=%q reason=%q", kind, why)
	}
	if kind != KindRateLimit {
		t.Fatalf("Allow kind = %q, want %q", kind, KindRateLimit)
	}
	if b.Count("unknown", now) != 0 {
		t.Fatalf("rate limit consumed the storm budget: count=%d, want 0 (budget-exempt)", b.Count("unknown", now))
	}
}

// 任务912 P0-b（summary 侧）: summaryTransientRetryable only knew the status
// token "429", so a limit named in words classified transient=false and the
// summary failed after its single dedicated wait.
func TestTask912TextRateLimitIsSummaryTransient(t *testing.T) {
	retryable := []string{
		"mimo-token-plan: Request rate limited",
		"gateway: rate_limit_exceeded for this key",
		"upstream: Too Many Requests",
	}
	for _, msg := range retryable {
		if !summaryTransientRetryable(errors.New(msg)) {
			t.Errorf("summaryTransientRetryable(%q) = false, want true", msg)
		}
		if !summaryRateLimited(errors.New(msg)) {
			t.Errorf("summaryRateLimited(%q) = false, want true", msg)
		}
	}
	// The classifier stays narrow: semantic rejections are still terminal.
	notRetryable := []string{
		"context limit exceeded: fold too large",
		"model refused: content policy",
		"invalid api key",
	}
	for _, msg := range notRetryable {
		if summaryTransientRetryable(errors.New(msg)) {
			t.Errorf("summaryTransientRetryable(%q) = true, want false", msg)
		}
	}
}
