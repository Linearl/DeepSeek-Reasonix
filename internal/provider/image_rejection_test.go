package provider

import (
	"errors"
	"strings"
	"testing"
)

// Task 470: an opaque 400/422 on a request that carried data-URL images must
// gain the actionable hint; everything else must pass through untouched.
func TestAnnotateInlineImageRejection(t *testing.T) {
	const hintFragment = "vision = false"

	t.Run("400 with inline images gains hint", func(t *testing.T) {
		err := &APIError{Provider: "p", Status: 400, Body: `{"error":{"message":"Invalid request parameters"}}`}
		base := err.Error()
		got := AnnotateInlineImageRejection(err, true)
		// tl 1190: the ORIGINAL error object comes back — annotating must not
		// re-wrap, or the trusted ContextLimitError/OutputLimitError wrappers
		// above it would be stripped.
		if got != err {
			t.Fatalf("expected the original error in place, got %T", got)
		}
		var api *APIError
		if !errors.As(got, &api) || api.Hint == "" {
			t.Fatalf("expected annotated APIError with hint, got %v", got)
		}
		if !strings.Contains(api.Hint, hintFragment) {
			t.Errorf("hint must name the vision=false exit, got: %s", api.Hint)
		}
		if !strings.Contains(api.Hint, "data: URL") {
			t.Errorf("hint must name the data-URL fact, got: %s", api.Hint)
		}
		if api.Error() != base+"\n"+api.Hint {
			t.Errorf("Error() must append the hint as its own line exactly once")
		}
	})

	t.Run("422 with inline images gains hint", func(t *testing.T) {
		got := AnnotateInlineImageRejection(&APIError{Status: 422, Body: "bad"}, true)
		var api *APIError
		if !errors.As(got, &api) || api.Hint == "" {
			t.Fatalf("expected annotated 422, got %v", got)
		}
	})

	t.Run("no inline images passes through", func(t *testing.T) {
		err := &APIError{Status: 400, Body: "Invalid request parameters"}
		if got := AnnotateInlineImageRejection(err, false); got != err {
			t.Fatalf("expected the original error, got %v", got)
		}
	})

	t.Run("non-400 statuses pass through", func(t *testing.T) {
		for _, status := range []int{200, 429, 500, 503} {
			err := &APIError{Status: status, Body: "x"}
			if got := AnnotateInlineImageRejection(err, true); got != err {
				t.Fatalf("status %d: expected the original error, got %v", status, got)
			}
		}
	})

	t.Run("non-API errors pass through", func(t *testing.T) {
		err := errors.New("connection reset")
		if got := AnnotateInlineImageRejection(err, true); got != err {
			t.Fatalf("expected the original error, got %v", got)
		}
	})

	t.Run("existing hint is never overwritten", func(t *testing.T) {
		err := &APIError{Status: 400, Body: "x", Hint: "keep me"}
		got := AnnotateInlineImageRejection(err, true)
		var api *APIError
		if !errors.As(got, &api) || api.Hint != "keep me" {
			t.Fatalf("expected the existing hint to survive, got %v", got)
		}
	})

	t.Run("wrapped APIError is annotated without losing the chain", func(t *testing.T) {
		inner := &APIError{Status: 400, Body: "Invalid request parameters"}
		wrapped := errors.Join(inner)
		got := AnnotateInlineImageRejection(wrapped, true)
		var api *APIError
		if !errors.As(got, &api) || api.Hint == "" {
			t.Fatalf("expected wrapped APIError to gain hint, got %v", got)
		}
	})

	// tl 1190 (2026-10-27 deepseek incident): the old copy-and-return stripped
	// the trusted overflow wrapper SendWithRetry had just parsed, so the
	// agent's context recovery never saw the provider's four token numbers and
	// every image-carrying overflow turn died unrecovered. The wrapper must
	// survive the annotation.
	t.Run("context limit wrapper survives annotation", func(t *testing.T) {
		apiErr := &APIError{Provider: "deepseek", Status: 400, Body: "context"}
		wrapped := &ContextLimitError{APIError: apiErr, WindowTokens: 1_048_576, PromptTokens: 737_203}
		got := AnnotateInlineImageRejection(wrapped, true)
		if got != wrapped {
			t.Fatalf("expected the original wrapper, got %T", got)
		}
		limit := AsContextLimitError(got)
		if limit == nil || limit.WindowTokens != 1_048_576 || limit.PromptTokens != 737_203 {
			t.Fatalf("ContextLimitError numbers must survive, got %+v", limit)
		}
		if apiErr.Hint == "" || !strings.Contains(apiErr.Hint, hintFragment) {
			t.Fatalf("hint must be set on the shared APIError, got %q", apiErr.Hint)
		}
	})
}
