package serve

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGzipMiddleware(t *testing.T) {
	big := []byte(`{"data":"` + strings.Repeat("x", 8192) + `"}`)
	handler := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {}\n\n"))
			return
		}
		w.Header().Set("ETag", `"abc"`)
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(big)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/history", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := resp.Header.Get("ETag"); got != `"abc"` {
		t.Fatalf("ETag = %q, want preserved", got)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, big) {
		t.Fatal("decompressed body mismatch")
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/history", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("If-None-Match", `"abc"`)
	resp304, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp304.Body.Close()
	if resp304.StatusCode != http.StatusNotModified || resp304.Header.Get("Content-Encoding") != "" {
		t.Fatalf("304 response = status %d encoding %q", resp304.StatusCode, resp304.Header.Get("Content-Encoding"))
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	events, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	if events.Header.Get("Content-Encoding") != "" {
		t.Fatal("SSE must bypass gzip")
	}
}

func TestGzipMiddlewareKeepsSmallResponsesPlain(t *testing.T) {
	h := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("small"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("small response encoding = %q", got)
	}
	if rr.Body.String() != "small" {
		t.Fatalf("small body = %q", rr.Body.String())
	}
}

func TestGzipMiddlewareHonorsDisabledEncoding(t *testing.T) {
	h := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", gzipThreshold+1)))
	}))
	for _, encoding := range []string{"gzip;q=0", "br, *;q=1, gzip;q=0"} {
		req := httptest.NewRequest(http.MethodGet, "/history", nil)
		req.Header.Set("Accept-Encoding", encoding)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if got := rr.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Accept-Encoding %q produced %q", encoding, got)
		}
	}
}

// TestSecurityHeadersGuardGzipPassthrough pins the task-415 code-scanning
// reflected-xss triage on gzip.go: the chain-wide sniff guard must reach the
// response even on the sub-threshold plain-write passthrough (the flagged
// ResponseWriter.Write path), including when the handler echoes request data,
// and gzip compression must keep working above the threshold.
func TestSecurityHeadersGuardGzipPassthrough(t *testing.T) {
	handler := securityHeaders(gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"echo":"` + r.URL.Query().Get("q") + `"}`))
	})))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Small reflected body: plain passthrough, still guarded.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/echo?q="+url.QueryEscape("<script>alert(1)</script>"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	plain, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("passthrough X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("small response encoding = %q, want plain", got)
	}
	if len(plain) >= gzipThreshold {
		t.Fatalf("passthrough body unexpectedly large: %d bytes", len(plain))
	}

	// Large body: still compressed, still guarded.
	big := strings.Repeat("x", gzipThreshold+1)
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/echo?q="+url.QueryEscape(big), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("compressed X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("large response encoding = %q, want gzip", got)
	}
}
