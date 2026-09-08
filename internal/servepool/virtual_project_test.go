package servepool

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/serve"
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
	g.SetVirtualSource("global", VirtualSource{
		Sessions: func() []SessionEntry {
			return []SessionEntry{
				{Name: "sess-1", Path: `C:\sessions\sess-1.jsonl`, Title: "First", Turns: 3, MtimeMilli: 1234},
			}
		},
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
	resp, _ = get("/p/global/status")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("virtual status status = %d, want 501", resp.StatusCode)
	}
}

// TestVirtualProjectResumeHistory covers the inline history flow (task 16
// follow-up): resume binds a session inside AllowedDir, /history renders it;
// paths outside AllowedDir are rejected; history without a bound session
// answers 400.
func TestVirtualProjectResumeHistory(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "hist-1.jsonl")
	// Minimal native event-log-free transcript: one user line.
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.jsonl")

	m, err := NewManager(Config{
		ReasonixBin: "definitely-not-a-real-binary",
		Virtual:     []ProjectState{{ID: "global", Name: "Global", Root: "Global"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	g := NewGateway(m, "secret")
	g.SetVirtualSource("global", VirtualSource{
		Sessions:   func() []SessionEntry { return []SessionEntry{} },
		History:    func(path string) (any, error) { return serve.HistoryJSONForFile(path) },
		AllowedDir: dir,
	})
	ts := httptest.NewServer(g)
	defer ts.Close()

	do := func(method, path string, body string) (*http.Response, []byte) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 64*1024)
		n, _ := resp.Body.Read(buf)
		return resp, buf[:n]
	}

	// History before resume → 400.
	if resp, _ := do(http.MethodGet, "/p/global/history", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("history before resume = %d, want 400", resp.StatusCode)
	}

	// Resume with a path outside AllowedDir → 403.
	body := `{"path":"` + strings.ReplaceAll(outsidePath, `\`, `\\`) + `"}`
	if resp, _ := do(http.MethodPost, "/p/global/resume", body); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("resume outside scope = %d, want 403", resp.StatusCode)
	}

	// Resume inside scope → 204, then history → 200 with the user turn.
	body = `{"path":"` + strings.ReplaceAll(sessionPath, `\`, `\\`) + `"}`
	if resp, _ := do(http.MethodPost, "/p/global/resume", body); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("resume = %d, want 204", resp.StatusCode)
	}
	resp, raw := do(http.MethodGet, "/p/global/history", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history = %d (%s)", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"role":"user"`) || !strings.Contains(string(raw), `"hi"`) {
		t.Fatalf("history payload missing user turn: %s", raw)
	}
}
