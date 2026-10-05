package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

const inlinePNGData = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAoAAAAKCAYAAACNMs+9AAAAFUlEQVR42mP8z8AARIQBEwMDAwMDAwAkBgMBjXK3EAAAAABJRU5ErkJggg=="

// Task 470: an opaque 400 on a request carrying data-URL images gains the
// inline-image hint naming the vision=false exit; the same 400 without images
// stays unannotated.
func TestStreamAnnotatesInlineImageRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"400","message":"Invalid request parameters","param":"","type":"BadRequestError"}}`))
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "mimo-api", BaseURL: srv.URL, Model: "mimo-v2.6-flash", APIKey: "k",
		Extra: map[string]any{"vision": true}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("with data-URL image the 400 names the exit", func(t *testing.T) {
		msgs := []provider.Message{{
			Role:    provider.RoleUser,
			Content: "看看这张图",
			Images:  []string{inlinePNGData},
		}}
		_, err := p.Stream(context.Background(), provider.Request{Messages: msgs})
		var apiErr *provider.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 400 {
			t.Fatalf("want *provider.APIError{Status:400}, got %T: %v", err, err)
		}
		if !strings.Contains(apiErr.Error(), "vision = false") {
			t.Errorf("error must carry the inline-image hint, got: %s", apiErr.Error())
		}
		if !strings.Contains(apiErr.Error(), "data: URL") {
			t.Errorf("hint must name the data-URL fact, got: %s", apiErr.Error())
		}
	})

	t.Run("text-only 400 stays unannotated", func(t *testing.T) {
		_, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
		var apiErr *provider.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 400 {
			t.Fatalf("want *provider.APIError{Status:400}, got %T: %v", err, err)
		}
		if apiErr.Hint != "" {
			t.Errorf("text-only 400 must not gain the inline-image hint, got: %s", apiErr.Hint)
		}
	})

	t.Run("http-URL image stays unannotated", func(t *testing.T) {
		msgs := []provider.Message{{
			Role:    provider.RoleUser,
			Content: "看图",
			Images:  []string{"https://example.com/x.png"},
		}}
		_, err := p.Stream(context.Background(), provider.Request{Messages: msgs})
		var apiErr *provider.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 400 {
			t.Fatalf("want *provider.APIError{Status:400}, got %T: %v", err, err)
		}
		if apiErr.Hint != "" {
			t.Errorf("http-URL images are not inline; hint must stay empty, got: %s", apiErr.Hint)
		}
	})
}

// Task 470: the wire-shape probe must see data-URL images through every
// content shape buildRequest can produce, and miss them on strings/nil.
func TestRequestHasInlineDataImages(t *testing.T) {
	withParts := chatRequest{Messages: []chatMessage{
		{Role: "user", Content: []chatContentPart{
			{Type: "text", Text: "看图"},
			{Type: "image_url", ImageURL: &chatImageURL{URL: inlinePNGData}},
		}},
	}}
	if !requestHasInlineDataImages(withParts) {
		t.Errorf("data-URL image part must be detected")
	}

	httpParts := chatRequest{Messages: []chatMessage{
		{Role: "user", Content: []chatContentPart{
			{Type: "image_url", ImageURL: &chatImageURL{URL: "https://example.com/x.png"}},
		}},
	}}
	if requestHasInlineDataImages(httpParts) {
		t.Errorf("http(s) image parts are not inline data")
	}

	if requestHasInlineDataImages(chatRequest{Messages: []chatMessage{{Role: "user", Content: "plain text"}}}) {
		t.Errorf("string content cannot carry images")
	}
	if requestHasInlineDataImages(chatRequest{Messages: []chatMessage{{Role: "assistant", Content: nil}}}) {
		t.Errorf("nil content cannot carry images")
	}
}
