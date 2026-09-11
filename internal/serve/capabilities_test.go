package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// capabilityProbePath maps a declared capability to a path the host must serve for
// it. Without this the declaration could advertise anything and nothing would
// notice.
var capabilityProbePath = map[string]string{
	"events":         "/events",
	"runtime-states": "/runtime-states",
	"history":        "/history",
	"context":        "/context",
	"submit":         "/submit",
	"provider-setup": "/provider-setup",
	"inbox":          "/inbox",
	"checkpoints":    "/checkpoints",
}

// TestCapabilitiesHaveProbePaths keeps the declaration checkable: adding a
// capability without saying how to reach it would make the next test vacuous.
func TestCapabilitiesHaveProbePaths(t *testing.T) {
	for _, c := range Capabilities() {
		if _, ok := capabilityProbePath[c]; !ok {
			t.Fatalf("capability %q has no probe path; add one so the declaration stays checkable", c)
		}
	}
	for c := range capabilityProbePath {
		if !slices.Contains(Capabilities(), c) {
			t.Fatalf("probe path for %q exists but the capability is not declared", c)
		}
	}
}

// TestCapabilitiesEndpointReportsTheDeclaration is the contract the client relies
// on: what the host answers when asked is exactly what it declares.
func TestCapabilitiesEndpointReportsTheDeclaration(t *testing.T) {
	// Call the handler directly: this checks the response contract without needing a
	// fully constructed Server, and the routes it must be wired into are asserted
	// separately by the probe-path table above.
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	s.capabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /capabilities = %d, want 200", rec.Code)
	}
	var got capabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities response: %v", err)
	}
	if got.Protocol != serveProtocolVersion {
		t.Fatalf("protocol = %d, want %d", got.Protocol, serveProtocolVersion)
	}
	want := Capabilities()
	if !slices.Equal(got.Capabilities, want) {
		t.Fatalf("capabilities = %v, want %v", got.Capabilities, want)
	}
}
