package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreviewMediaKindHTML asserts the backend preview type classification
// picks up .html/.htm as a distinct "html" kind (tl 1110). Non-HTML paths
// (txt, unknown ext) must still fall back to the empty kind (text path).
func TestPreviewMediaKindHTML(t *testing.T) {
	cases := []struct {
		path     string
		wantKind string
		wantMime string
	}{
		{"index.html", "html", "text/html"},
		{"INDEX.HTML", "html", "text/html"},
		{"page.htm", "html", "text/html"},
		{"report.txt", "", ""},
		{"unknown.zzz", "", ""},
	}
	for _, c := range cases {
		kind, mime := previewMediaKind(c.path)
		if kind != c.wantKind || mime != c.wantMime {
			t.Errorf("previewMediaKind(%q) = (%q,%q), want (%q,%q)", c.path, kind, mime, c.wantKind, c.wantMime)
		}
	}
}

// TestReadFileHTMLPreview asserts ReadFile classifies an HTML file as kind=html
// with a token-backed media URL and no text body (tl 1110).
func TestReadFileHTMLPreview(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile("index.html", []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	var app App
	p := app.ReadFile("index.html")
	if p.Err != "" {
		t.Fatalf("ReadFile html err = %q", p.Err)
	}
	if p.Kind != "html" || p.Mime != "text/html" {
		t.Fatalf("ReadFile html = %+v, want html kind + text/html mime", p)
	}
	if p.Body != "" {
		t.Fatalf("media preview should have empty body, got %q", p.Body)
	}
	if !strings.HasPrefix(p.URL, "/__reasonix_workspace_media/") || !strings.HasSuffix(p.URL, "/index.html") {
		t.Fatalf("unexpected media URL: %q", p.URL)
	}
}

// TestMediaTokenHandlerHTMLSecurityHeaders asserts the HTML-serving branch of
// the workspace media middleware stamps a CSP sandbox header (defense in depth
// against the same-origin media route) plus CORP and Referrer-Policy (tl 1110).
// The image branch must keep its own CSP and NOT be affected.
func TestMediaTokenHandlerHTMLSecurityHeaders(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile("page.html", []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("shot.png", []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	mw := app.workspaceMediaMiddleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback handler should not be called for media URLs")
	}))

	htmlPreview := app.ReadFile("page.html")
	req := httptest.NewRequest(http.MethodGet, htmlPreview.URL, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("html expected 200, got %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") {
		t.Fatalf("html media response missing CSP sandbox directive, got %q", csp)
	}
	if rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("html media response missing CORP same-origin")
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("html media response missing Referrer-Policy no-referrer")
	}

	imgPreview := app.ReadFile("shot.png")
	req2 := httptest.NewRequest(http.MethodGet, imgPreview.URL, nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("image expected 200, got %d", rec2.Code)
	}
	if imgCSP := rec2.Header().Get("Content-Security-Policy"); imgCSP != "default-src 'none'; sandbox" {
		t.Fatalf("image CSP changed unexpectedly, got %q", imgCSP)
	}
}

// TestPreviewMediaKindIgnoresHTMLOnlyForKnownExt guards against accidentally
// classifying arbitrary files as html. A ".txt" file holding html content
// must stay on the text path.
func TestPreviewMediaKindIgnoresHTMLOnlyForKnownExt(t *testing.T) {
	kind, mime := previewMediaKind(filepath.Join("docs", "notes.txt"))
	if kind != "" || mime != "" {
		t.Fatalf("notes.txt should stay on text path, got (%q,%q)", kind, mime)
	}
}
