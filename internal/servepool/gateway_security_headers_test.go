package servepool

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGatewaySecurityHeadersAndReflectedEscaping pins the task-415
// code-scanning reflected-xss triage on gateway.go: every response carries
// X-Content-Type-Options: nosniff, and reflected path ids are HTML-escaped by
// the JSON encoder, so a script payload can never survive into the output as
// executable markup.
func TestGatewaySecurityHeadersAndReflectedEscaping(t *testing.T) {
	m, err := NewManager(Config{
		ReasonixBin: "definitely-not-a-real-binary",
		Virtual:     []ProjectState{{ID: "global", Name: "Global", Root: "Global"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	g := NewGateway(m, "secret")
	ts := httptest.NewServer(g)
	defer ts.Close()

	get := func(path string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(body)
	}

	// JSON, virtual-inline and not-found paths all carry the sniff guard.
	for _, path := range []string{"/status", "/p/global/sessions", "/definitely-missing"} {
		resp, _ := get(path)
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: X-Content-Type-Options = %q, want nosniff", path, got)
		}
	}

	// Injection attempt: a script payload as the project id must come back
	// escaped inside a JSON error, never as raw markup.
	injected := "/p/<script>alert(1)</script>/sessions"
	resp, body := get(injected)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("injected id status = %d, want 503; body = %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("reflected id was not escaped: %s", body)
	}
	if !strings.Contains(body, `\u003cscript\u003e`) {
		t.Fatalf("expected HTML-escaped script payload in JSON body, got: %s", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
