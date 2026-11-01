package main

import (
	"errors"
	"testing"
)

// tl1317 P3: Go-internal error texts (the double-enveloped RawMessage marshal
// failure among them) must not reach the chat notice verbatim; the user sees
// a short classified reason while slog keeps the full error.
func TestSnapshotUserNoticeReasonSanitizesInternals(t *testing.T) {
	raw := errors.New("json: error calling MarshalJSON for type json.RawMessage: invalid character '{' looking for beginning of object key string")
	if got := snapshotUserNoticeReason(raw); got != "internal serialization error (details in logs)" {
		t.Fatalf("internal error text leaked: %q", got)
	}

	panicking := errors.New("panic: runtime error: invalid memory address")
	if got := snapshotUserNoticeReason(panicking); got != "internal serialization error (details in logs)" {
		t.Fatalf("panic text leaked: %q", got)
	}

	// Curated errors pass through: the damaged-store error names the offending
	// event index and kind, which is exactly the locator the user should see.
	curated := errors.New("damaged session store: invalid JSON payload at events[1] (kind=\"diagnostic\")")
	if got := snapshotUserNoticeReason(curated); got != curated.Error() {
		t.Fatalf("curated damaged-store locator was altered: %q", got)
	}

	plain := errors.New("disk full")
	if got := snapshotUserNoticeReason(plain); got != "disk full" {
		t.Fatalf("plain actionable error was altered: %q", got)
	}
}
