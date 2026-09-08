package servepool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestVirtualProjectManifestAndSessions covers the task-16 protocol: a
// manifest-only virtual project ("global") appears in /manifest with state
// running, Open is a no-op success, and /p/<id>/sessions is served inline
// from the registered sessions source — no serve process involved.
func TestVirtualProjectManifestAndSessions(t *testing.T) {
	m, err := NewManager(Config{
		ReasonixBin: "definitely-not-a-real-binary",
		Virtual: []ProjectState{
			{ID: "global", Name: "Global", Root: "Global"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	if !m.IsVirtual("global") {
		t.Fatal("global should be virtual")
	}
	if m.IsVirtual("nope") {
		t.Fatal("unknown id must not be virtual")
	}
	// Open is a no-op success (no spawn attempt against the bogus binary).
	if err := m.Open("global"); err != nil {
		t.Fatalf("Open(global) = %v, want nil", err)
	}
	if m.Port("global") != 0 {
		t.Fatal("virtual project must not report a port")
	}

	g := NewGateway(m, "secret")
	g.SetSessionsSource("global", func() []SessionEntry {
		return []SessionEntry{
			{Name: "sess-1", Path: `C:\sessions\sess-1.jsonl`, Title: "First", Turns: 3, MtimeMilli: 1234},
		}
	})
	ts := httptest.NewServer(g)
	defer ts.Close()

	get := func(path string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		return resp, buf[:n]
	}

	// /manifest carries the virtual project.
	resp, body := get("/manifest")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d", resp.StatusCode)
	}
	var projects []ProjectState
	if err := json.Unmarshal(body, &projects); err != nil {
		t.Fatalf("manifest decode: %v (%s)", err, body)
	}
	found := false
	for _, p := range projects {
		if p.ID == "global" && p.State == "running" && p.Name == "Global" {
			found = true
		}
	}
	if !found {
		t.Fatalf("manifest missing virtual global: %s", body)
	}

	// /p/global/sessions is served inline from the registered source.
	resp, body = get("/p/global/sessions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("virtual sessions status = %d (%s)", resp.StatusCode, body)
	}
	var entries []SessionEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("sessions decode: %v (%s)", err, body)
	}
	if len(entries) != 1 || entries[0].Name != "sess-1" || entries[0].Turns != 3 {
		t.Fatalf("unexpected sessions payload: %s", body)
	}

	// Other virtual paths answer 501 so clients degrade gracefully.
	resp, _ = get("/p/global/history")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("virtual history status = %d, want 501", resp.StatusCode)
	}
}
