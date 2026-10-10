package servepool

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Task 539: POST /p/<id>/inbox/items with a session selector is gated — the
// desktop-held session's guided messages are answered by the gate; requests
// without a selector forward untouched.

func TestGatewayInboxGateHandlesSessionScopedEnqueue(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	var got InboxGateRequest
	g := NewGateway(m, "s")
	g.SetInboxGate(func(req InboxGateRequest) (bool, int, string, []byte) {
		got = req
		return true, http.StatusAccepted, "application/json",
			[]byte(`{"itemID":"i-1","disposition":"steered"}`)
	})
	ts := httptest.NewServer(g)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/inbox/items?session=sess-a",
		strings.NewReader(`{"input":"先跑测试再修 lint","intent":"steer"}`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 from the gate", resp.StatusCode)
	}
	var receipt map[string]any
	if err := json.Unmarshal(body, &receipt); err != nil || receipt["itemID"] != "i-1" {
		t.Fatalf("gate payload = %q (err %v)", string(body), err)
	}
	if got.SessionName != "sess-a" || got.Intent != "steer" || !strings.Contains(string(got.Body), "先跑测试再修 lint") {
		t.Fatalf("gate request = %+v, want session/intent/body", got)
	}
}

func TestGatewayInboxGateBodySessionSelector(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	var got InboxGateRequest
	g := NewGateway(m, "s")
	g.SetInboxGate(func(req InboxGateRequest) (bool, int, string, []byte) {
		got = req
		return true, http.StatusAccepted, "application/json", []byte(`{}`)
	})
	ts := httptest.NewServer(g)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/inbox/items",
		strings.NewReader(`{"input":"x","session":"sess-b"}`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if got.SessionName != "sess-b" {
		t.Fatalf("gate session = %q, want sess-b from the body", got.SessionName)
	}
}

func TestGatewayInboxGateForwardsWithoutSelector(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	g := NewGateway(m, "s")
	called := false
	g.SetInboxGate(func(InboxGateRequest) (bool, int, string, []byte) {
		called = true
		return true, http.StatusAccepted, "application/json", []byte(`{}`)
	})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inbox/items" {
			t.Errorf("forwarded path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"forwarded":true}`))
	}))
	defer backend.Close()
	takeoverGateForceRunning(t, m, id, backendPort(t, backend))

	ts := httptest.NewServer(g)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/inbox/items",
		strings.NewReader(`{"input":"plain followup"}`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if called {
		t.Fatal("gate answered a request without a session selector")
	}
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), "forwarded") {
		t.Fatalf("forwarded response = %d %q, want the backend's answer", resp.StatusCode, string(body))
	}
}
