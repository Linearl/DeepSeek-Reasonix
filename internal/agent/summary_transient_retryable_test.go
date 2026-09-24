package agent

import (
	"errors"
	"testing"
)

// TestSummaryTransientRetryable pins the task 303 stream-error classifier:
// provider stream/transport failures (the mimo-api INTERNAL_ERROR sample) are
// retryable; semantic rejections are not.
func TestSummaryTransientRetryable(t *testing.T) {
	retryable := []string{
		"mimo-api: read stream: stream error: stream ID 5; INTERNAL_ERROR; received from peer",
		"provider: connection reset by peer",
		"unexpected EOF while reading response body",
		"server disconnected before completion",
		"HTTP 429 too many requests",
		"bad gateway: 502",
		"service unavailable: 503",
		"gateway timeout: 504",
		"context deadline exceeded",
	}
	for _, msg := range retryable {
		if !summaryTransientRetryable(errors.New(msg)) {
			t.Errorf("summaryTransientRetryable(%q) = false, want true", msg)
		}
	}
	notRetryable := []string{
		"",
		"context limit exceeded: fold too large",
		"model refused: content policy",
		"invalid api key",
	}
	for _, msg := range notRetryable {
		if summaryTransientRetryable(errors.New(msg)) {
			t.Errorf("summaryTransientRetryable(%q) = true, want false", msg)
		}
	}
	if summaryTransientRetryable(nil) {
		t.Error("summaryTransientRetryable(nil) must be false")
	}
}
