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
		got := AnnotateInlineImageRejection(err, true)
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
		if api.Error() != err.Error()+"\n"+api.Hint {
			t.Errorf("Error() must append the hint as its own line")
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
}
